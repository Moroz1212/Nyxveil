import Foundation

// Calls cross queues intentionally: the Go engine locks its state; writes use queue.
public final class CoreBridge: @unchecked Sendable {
    private let core: NVCore
    private let queue = DispatchQueue(label: "nyxveil.nvp.send")
    public init(packet: @escaping (Data) -> Void, failure: @escaping (String) -> Void) {
        core = NVCore(packet: packet, failure: failure)
    }
    public static func verify(_ catalog: Data, keys: Data) throws { try NVCore.verify(catalog, keys: keys) }
    public func begin(_ json: String) async throws -> TunnelConfig {
        try await withCheckedThrowingContinuation { continuation in
            queue.async {
                do {
                    let raw = try self.core.begin(json)
                    let cfg = try JSONDecoder().decode(TunnelConfig.self, from: Data(raw.utf8))
                    try cfg.validate()
                    continuation.resume(returning: cfg)
                } catch {
                    let allowed: Set<String> = ["quic_tls_or_connect_failed", "nvp_handshake_failed", "nvp_auth_failed",
                        "typeconfig_session_ended", "typeconfig_invalid", "mtu_probe_failed", "quic_handshake_auth_or_config_failed",
                        "catalog_signature_or_time_invalid", "catalog_key_invalid", "catalog_invalid", "ticket_invalid_or_expired",
                        "connect_cancelled_or_timeout", "connect_parameters_invalid"]
                    let code = (error as NSError).localizedDescription
                    continuation.resume(throwing: ConnectionFailure(allowed.contains(code) ? code : "nvp_bridge_failed"))
                }
            }
        }
    }
    // Await a whole packetFlow batch before reading another: bounded memory/backpressure.
    public func send(_ packets: [Data], completion: @escaping () -> Void) {
        queue.async {
            for packet in packets { do { try self.core.send(packet) } catch { break } }
            completion()
        }
    }
    public func close() { core.close() }
    public func counters() -> (UInt64, UInt64) {
        struct Counts: Decodable { let rxBytes: UInt64; let txBytes: UInt64 }
        guard let c = try? JSONDecoder().decode(Counts.self, from: Data(core.stats().utf8)) else { return (0, 0) }
        return (c.rxBytes, c.txBytes)
    }
}
