import Foundation

public struct ConnectionFailure: Error, LocalizedError {
    public let code: String
    public var errorDescription: String? { code }
    init(_ code: String) { self.code = code }
}

public func connectionErrorText(_ error: Error) -> String {
    if let failure = error as? ConnectionFailure { return failure.code }
    return (error as? PrototypeError)?.rawValue ?? "Operation failed. Check signing, connectivity and Control Plane access."
}

public enum PrototypeError: String, Error, LocalizedError {
    case configuration = "Check Config/Local.xcconfig and signing identifiers."
    case credential = "License or device identity is unavailable."
    case license = "License validation or device activation failed."
    case response = "Invalid Control Plane response."
    case location = "Selected location is unavailable."
    case ticket = "Access ticket is missing or expired."
    case settings = "Invalid tunnel network settings."
    case cancelled = "Connection cancelled."
    public var errorDescription: String? { rawValue }
}

public enum Settings {
    public static func value(_ key: String) throws -> String {
        guard let value = Bundle.main.object(forInfoDictionaryKey: key) as? String,
              !value.isEmpty, !value.contains("$("), !value.contains("REPLACE") else { throw PrototypeError.configuration }
        return value
    }
    public static var cpURL: URL {
        // Matches Windows ClientSettings and Android CONTROL_PLANE_BASE_URL.
        URL(string: "https://cp.nyxveil.ru:18443")!
    }
}

public struct LicenseValidation: Decodable { public let valid: Bool }
public struct Activation: Decodable { public let activated: Bool; public let device_id: String }
public struct CatalogKeys: Decodable { public let keys: [String: String] }
public struct Location: Decodable, Identifiable {
    public let location_id: String
    public let display_name: String
    public let enabled: Bool
    public var id: String { location_id }
}
public struct CatalogEnvelope: Decodable {
    public struct Catalog: Decodable { public let locations: [Location] }
    public let catalog: Catalog
}
public struct AccessTicket: Codable {
    public let access_ticket: String
    public let expires_at: Int64
    public let node_id: String?
    public func validate() throws {
        guard !access_ticket.isEmpty, expires_at > Int64(Date().timeIntervalSince1970) + 15 else { throw PrototypeError.ticket }
    }
}
public struct ConnectInput: Encodable {
    public let location_id: String
    public let access_ticket: String
    public let device_seed: Data
    public let catalog: Data
    public let keys: [String: String]
}
public struct TunnelConfig: Decodable {
    public let vpn_ip: String
    public let vpn_prefix: Int
    public let gateway: String
    public let dns_servers: [String]
    public let mtu: Int
    public let typeconfig_mtu: Int
    public let remote_address: String
    public var subnetMask: String {
        let mask: UInt32 = vpn_prefix == 0 ? 0 : UInt32.max << (32 - vpn_prefix)
        return [24, 16, 8, 0].map { String((mask >> $0) & 255) }.joined(separator: ".")
    }
    public func validate() throws {
        guard (0...32).contains(vpn_prefix), (576...65535).contains(mtu),
              mtu <= typeconfig_mtu, !vpn_ip.isEmpty, !gateway.isEmpty,
              !remote_address.isEmpty, !dns_servers.isEmpty else { throw PrototypeError.settings }
    }
}
public struct Telemetry: Codable {
    public var state = "Disconnected"
    public var connectedSince: Date?
    public var vpnIP: String?
    public var rxBytes: UInt64 = 0
    public var txBytes: UInt64 = 0
    public var lastError: String?
    public init() {}
}
