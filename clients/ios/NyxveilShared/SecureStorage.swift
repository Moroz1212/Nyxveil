import Foundation
import Security
import CryptoKit

public enum SecureStorage {
    private static func query(_ account: String) throws -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: "Nyxveil.iOS.prototype",
         kSecAttrAccount as String: account,
         kSecAttrAccessGroup as String: try Settings.value("KeychainAccessGroup")]
    }
    public static func load(_ account: String) throws -> Data? {
        var q = try query(account)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data else { throw PrototypeError.credential }
        return data
    }
    public static func save(_ data: Data, account: String) throws {
        let q = try query(account)
        let attributes: [String: Any] = [kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly]
        var status = SecItemUpdate(q as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            status = SecItemAdd(q.merging(attributes) { _, new in new } as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw PrototypeError.credential }
    }
    public static func license() throws -> String {
        guard let data = try load("license"), let token = String(data: data, encoding: .utf8), !token.isEmpty else {
            throw PrototypeError.credential
        }
        return token
    }
}

public struct DeviceIdentity: Codable {
    public let id: String
    public let seed: Data
    public var publicKey: Data { get throws { try Curve25519.Signing.PrivateKey(rawRepresentation: seed).publicKey.rawRepresentation } }
    public static func load(create: Bool = false) throws -> DeviceIdentity {
        if let saved = try SecureStorage.load("identity") {
            let identity = try JSONDecoder().decode(DeviceIdentity.self, from: saved)
            _ = try identity.publicKey
            return identity
        }
        guard create else { throw PrototypeError.credential }
        let identity = DeviceIdentity(id: UUID().uuidString.lowercased(), seed: Curve25519.Signing.PrivateKey().rawRepresentation)
        try SecureStorage.save(JSONEncoder().encode(identity), account: "identity")
        return identity
    }
}
