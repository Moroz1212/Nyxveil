import SwiftUI
import NetworkExtension
import NyxveilShared

@main struct NyxveilApp: App {
    @StateObject private var model = ConnectionModel()
    var body: some Scene { WindowGroup { ContentView(model: model) } }
}

struct ContentView: View {
    @ObservedObject var model: ConnectionModel
    var body: some View {
        Form {
            Section("Nyxveil") {
                SecureField("License / token", text: $model.license)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .disabled(model.busy || model.active)
                Button("Activate / Load locations") { model.activate() }.disabled(model.busy || model.active)
                Picker("Location", selection: $model.location) {
                    Text("Select location").tag("")
                    ForEach(model.locations) { Text($0.display_name).tag($0.id) }
                }.disabled(model.busy || model.active)
                Button(model.active || model.busy ? "Disconnect" : "Connect") {
                    if model.active || model.busy { model.disconnect() } else { model.connect() }
                }.disabled(!model.active && !model.busy && model.location.isEmpty)
            }
            Section("Connection") {
                LabeledContent("Status", value: model.status.state)
                LabeledContent("VPN IP", value: model.status.vpnIP ?? "—")
                LabeledContent("RX / TX", value: "\(model.status.rxBytes) / \(model.status.txBytes) bytes")
                if let error = model.status.lastError { Text(error).foregroundStyle(.red) }
            }
        }.task { await model.restore() }
    }
}

@MainActor final class ConnectionModel: ObservableObject {
    @Published var license = ""
    @Published var location = ""
    @Published var locations: [Location] = []
    @Published var status = Telemetry()
    @Published var busy = false
    @Published var active = false
    private var manager: NETunnelProviderManager?
    private var operation: Task<Void, Never>?
    private var poll: Task<Void, Never>?
    private var operationID = UUID()

    func restore() async {
        do {
            try await loadManager()
            if let saved = try SecureStorage.load("license") { license = String(decoding: saved, as: UTF8.self) }
            if !license.isEmpty { locations = try await ControlPlane().locations() }
            if location.isEmpty { location = locations.first?.id ?? "" }
        } catch { status.lastError = safeError(error) }
        startPolling()
    }
    private func loadManager() async throws {
        let bundle = try Settings.value("TunnelBundleIdentifier")
        let managers: [NETunnelProviderManager] = try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { values, error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume(returning: values ?? []) }
            }
        }
        manager = managers.first { ($0.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == bundle }
        if let existing = manager {
            location = (existing.protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration?["location_id"] as? String ?? ""
            active = ![NEVPNStatus.disconnected, .invalid].contains(existing.connection.status)
        }
    }
    func activate() {
        let token = license.trimmingCharacters(in: .whitespacesAndNewlines)
        busy = true
        operation = Task {
            defer { busy = false }
            do {
                try await ControlPlane().activate(token)
                try Task.checkCancellation()
                locations = try await ControlPlane().locations()
                if !locations.contains(where: { $0.id == location }) { location = locations.first?.id ?? "" }
                status.lastError = nil
            } catch { if !Task.isCancelled { status.lastError = safeError(error) } }
        }
    }
    func connect() {
        busy = true; status.state = "Connecting"; status.lastError = nil
        let id = UUID(); operationID = id
        operation = Task {
            defer { if operationID == id { busy = false } }
            do {
                if manager == nil { try await loadManager() }
                let m = manager ?? NETunnelProviderManager()
                manager = m
                let proto = NETunnelProviderProtocol()
                proto.providerBundleIdentifier = try Settings.value("TunnelBundleIdentifier")
                proto.serverAddress = "Nyxveil"
                proto.providerConfiguration = ["location_id": location]
                proto.includeAllNetworks = true
                proto.excludeLocalNetworks = false
                proto.disconnectOnSleep = false
                m.protocolConfiguration = proto
                m.localizedDescription = "Nyxveil"
                m.isEnabled = true
                try Task.checkCancellation()
                try await m.saveToPreferences()
                try await m.loadFromPreferences()
                try Task.checkCancellation()
                guard operationID == id else { return }
                try m.connection.startVPNTunnel()
                active = true
                startPolling()
            } catch { if operationID == id && !Task.isCancelled { status.state = "Error"; status.lastError = safeError(error) } }
        }
    }
    func disconnect() {
        operationID = UUID(); operation?.cancel(); operation = nil
        manager?.connection.stopVPNTunnel()
        active = false; busy = false; status = Telemetry()
    }
    private func startPolling() {
        poll?.cancel()
        poll = Task {
            while !Task.isCancelled {
                if let manager {
                    let os = manager.connection.status
                    active = ![.disconnected, .invalid].contains(os)
                    if active, let session = manager.connection as? NETunnelProviderSession {
                        // No awaiting an unbounded provider message; stale replies are discarded.
                        let id = operationID
                        try? session.sendProviderMessage(Data("status".utf8)) { [weak self] data in
                            Task { @MainActor in
                                guard let self, self.operationID == id, self.active, let data,
                                      let decoded = try? JSONDecoder().decode(Telemetry.self, from: data) else { return }
                                self.status = decoded
                            }
                        }
                        if os == .reasserting { status.state = "Reconnecting" }
                        else if os == .connecting { status.state = "Connecting" }
                    } else if !busy {
                        if status.state != "Error", status.state != "Disconnected" {
                            // Retrieve the extension's terminal error after it has stopped.
                            if #available(iOS 16.0, *) {
                                manager.connection.fetchLastDisconnectError { [weak self] error in
                                    Task { @MainActor in
                                        guard let self, !self.active, let error else { return }
                                        self.status.state = "Error"; self.status.lastError = self.safeError(error)
                                    }
                                }
                            }
                            status = Telemetry()
                        }
                    }
                }
                do { try await Task.sleep(nanoseconds: 1_000_000_000) } catch { break }
            }
        }
    }
    private func safeError(_ error: Error) -> String {
        connectionErrorText(error)
    }
}
