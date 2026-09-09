import NetworkExtension
import Network
import NyxveilShared

final class PacketTunnelProvider: NEPacketTunnelProvider {
    // All state belongs to MainActor. Go callbacks hop here before touching it.
    @MainActor private var engine: CoreBridge?
    @MainActor private var task: Task<Void, Never>?
    @MainActor private var telemetry = Telemetry()
    @MainActor private var generation = UUID()
    @MainActor private var lifecycle = UUID()
    @MainActor private var running = false
    @MainActor private var monitor: NWPathMonitor?
    @MainActor private var lastPath: String?
    @MainActor private var wake: CheckedContinuation<Void, Never>?
    @MainActor private var startCompletion: ((Error?) -> Void)?
    @MainActor private var readerStarted = false
    @MainActor private var inbox: PacketInbox?

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        Task { @MainActor in
            guard !running, let proto = protocolConfiguration as? NETunnelProviderProtocol,
                  let location = proto.providerConfiguration?["location_id"] as? String, !location.isEmpty else {
                completionHandler(PrototypeError.configuration); return
            }
            running = true
            lifecycle = UUID()
            generation = UUID()
            startCompletion = completionHandler
            telemetry = Telemetry()
            telemetry.state = "Connecting"
            let pathMonitor = NWPathMonitor()
            pathMonitor.pathUpdateHandler = { [weak self] path in
                let key = "\(path.status)-\(path.usesInterfaceType(.wifi))-\(path.usesInterfaceType(.cellular))-\(path.usesInterfaceType(.wiredEthernet))"
                Task { @MainActor in
                    guard let self, self.running else { return }
                    defer { self.lastPath = key }
                    if let previous = self.lastPath, previous != key {
                        self.engine?.close()
                        self.signalReconnect()
                    }
                }
            }
            monitor = pathMonitor
            pathMonitor.start(queue: DispatchQueue(label: "nyxveil.path"))
            task = Task { await self.run(location: location) }
            let startID = lifecycle
            Task { @MainActor [weak self] in
                try? await Task.sleep(nanoseconds: 55_000_000_000)
                guard let self, self.lifecycle == startID, self.running, self.startCompletion != nil else { return }
                self.running = false
                self.task?.cancel()
                self.signalReconnect()
                self.monitor?.cancel()
                self.telemetry.state = "Error"
                self.telemetry.lastError = "connection_start_timeout"
                self.startCompletion?(PrototypeError.response); self.startCompletion = nil
                self.cancelTunnelWithError(PrototypeError.response)
            }
        }
    }

    @MainActor private func run(location: String) async {
        let cp = ControlPlane()
        var failures = 0
        while running && !Task.isCancelled {
            let attempt = UUID()
            generation = attempt
            telemetry.state = readerStarted ? "Reconnecting" : "Connecting"
            telemetry.connectedSince = nil
            reasserting = readerStarted
            do {
                let (json, _) = try await cp.prepare(location: location)
                try Task.checkCancellation()
                guard running, generation == attempt else { throw PrototypeError.cancelled }
                let packetInbox = PacketInbox { [weak self] packets in
                        guard let self, self.running, self.generation == attempt, self.telemetry.state == "Connected" else { return }
                        if !self.packetFlow.writePackets(packets, withProtocols: packets.map { _ in NSNumber(value: AF_INET) }) {
                            self.telemetry.lastError = "packet_flow_write_failed"
                            self.signalReconnect()
                        }
                }
                inbox = packetInbox
                let bridge = CoreBridge(packet: { packetInbox.enqueue($0) }, failure: { [weak self] code in
                    Task { @MainActor in
                        guard let self, self.running, self.generation == attempt else { return }
                        self.telemetry.lastError = code
                        self.signalReconnect()
                    }
                })
                engine = bridge
                let config = try await bridge.begin(json)
                try Task.checkCancellation()
                guard running, generation == attempt else { throw PrototypeError.cancelled }
                try await apply(config)
                try Task.checkCancellation()
                guard running, generation == attempt else { throw PrototypeError.cancelled }
                telemetry.state = "Connected"
                telemetry.vpnIP = config.vpn_ip
                telemetry.lastError = nil
                telemetry.connectedSince = Date()
                reasserting = false
                if !readerStarted { readerStarted = true; readPackets() }
                startCompletion?(nil); startCompletion = nil
                let establishedAt = Date()
                await withCheckedContinuation { wake = $0 }
                if Date().timeIntervalSince(establishedAt) >= 30 { failures = 0 }
            } catch {
                if !running || Task.isCancelled { break }
                // Fixed local descriptions only; don't publish remote body / secrets.
                telemetry.lastError = connectionErrorText(error)
            }
            engine?.close(); engine = nil
            inbox?.close(); inbox = nil
            guard running, !Task.isCancelled else { break }
            failures += 1
            if failures >= 5 {
                telemetry.state = "Error"
                running = false
                monitor?.cancel(); monitor = nil
                startCompletion?(PrototypeError.response); startCompletion = nil
                cancelTunnelWithError(PrototypeError.response)
                return
            }
            telemetry.state = "Reconnecting"
            do { try await Task.sleep(nanoseconds: UInt64(min(1 << (failures - 1), 8)) * 1_000_000_000) }
            catch { break }
        }
    }

    @MainActor private func apply(_ config: TunnelConfig) async throws {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: config.remote_address)
        let ipv4 = NEIPv4Settings(addresses: [config.vpn_ip], subnetMasks: [config.subnetMask])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        settings.ipv4Settings = ipv4
        // NVP's current TypeConfig is IPv4-only. includeAllNetworks on the manager
        // captures unsupported traffic too; Go drops non-IPv4. Never invent IPv6/DNS.
        settings.dnsSettings = NEDNSSettings(servers: config.dns_servers)
        settings.dnsSettings?.matchDomains = [""]
        settings.mtu = NSNumber(value: config.mtu)
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            // One-shot gate also bounds the OS callback; late completion cannot resume twice.
            let gate = SettingsGate(continuation)
            setTunnelNetworkSettings(settings) { error in gate.finish(error) }
            DispatchQueue.global().asyncAfter(deadline: .now() + 10) { gate.finish(PrototypeError.settings) }
        }
    }

    @MainActor private func signalReconnect() {
        generation = UUID() // Suppress all late callbacks from the previous session.
        inbox?.close(); inbox = nil
        if readerStarted { telemetry.state = "Reconnecting"; reasserting = true }
        engine?.close()
        let continuation = wake; wake = nil; continuation?.resume()
    }

    @MainActor private func readPackets() {
        guard running else { return }
        packetFlow.readPackets { [weak self] packets, _ in
            Task { @MainActor in
                guard let self, self.running else { return }
                if let engine = self.engine, self.telemetry.state == "Connected" {
                    engine.send(packets) { [weak self] in Task { @MainActor in self?.readPackets() } }
                } else { self.readPackets() } // Drain/drop while reconnecting; routes stay installed.
            }
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        Task { @MainActor in
            running = false
            lifecycle = UUID()
            task?.cancel(); task = nil
            signalReconnect()
            engine = nil
            monitor?.cancel(); monitor = nil; lastPath = nil
            startCompletion?(PrototypeError.cancelled); startCompletion = nil
            telemetry = Telemetry()
            readerStarted = false
            reasserting = false
            completionHandler()
        }
    }
    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        Task { @MainActor in
            if let engine { let (rx, tx) = engine.counters(); telemetry.rxBytes = rx; telemetry.txBytes = tx }
            completionHandler?(try? JSONEncoder().encode(telemetry))
        }
    }
}

// Bound Go -> MainActor buffering to 128 KiB with a single scheduled delivery.
// Congestion drops DATA, as permitted by the QUIC DATAGRAM transport.
private final class PacketInbox {
    private let lock = NSLock()
    private var packets: [Data] = []
    private var bytes = 0
    private var scheduled = false
    private var closed = false
    private let deliver: @MainActor ([Data]) -> Void
    init(deliver: @escaping @MainActor ([Data]) -> Void) { self.deliver = deliver }
    func enqueue(_ packet: Data) {
        lock.lock()
        guard !closed, bytes + packet.count <= 128 * 1024 else { lock.unlock(); return }
        packets.append(packet); bytes += packet.count
        let start = !scheduled; scheduled = true
        lock.unlock()
        if start { DispatchQueue.main.async { self.flush() } }
    }
    @MainActor private func flush() {
        lock.lock()
        let batch = packets; packets = []; bytes = 0; scheduled = false
        let allowed = !closed
        lock.unlock()
        if allowed { deliver(batch) }
    }
    func close() { lock.lock(); closed = true; packets = []; bytes = 0; lock.unlock() }
}

private final class SettingsGate: @unchecked Sendable {
    private let lock = NSLock()
    private var continuation: CheckedContinuation<Void, Error>?
    init(_ continuation: CheckedContinuation<Void, Error>) { self.continuation = continuation }
    func finish(_ error: Error?) {
        lock.lock(); let pending = continuation; continuation = nil; lock.unlock()
        if let error { pending?.resume(throwing: error) } else { pending?.resume() }
    }
}
