import Foundation

// Ephemeral HTTPS session: no credential cache, no response bodies in errors, no redirects.
private final class NoRedirect: NSObject, URLSessionTaskDelegate {
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                    newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
}
public final class ControlPlane {
    private let session: URLSession
    public init() {
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 15
        config.timeoutIntervalForResource = 25
        config.waitsForConnectivity = false
        config.urlCache = nil
        config.httpCookieStorage = nil
        config.urlCredentialStorage = nil
        session = URLSession(configuration: config, delegate: NoRedirect(), delegateQueue: nil)
    }
    deinit { session.invalidateAndCancel() }
    private func request(_ path: String, token: String? = nil, body: [String: String]? = nil) async throws -> Data {
        var req = URLRequest(url: Settings.cpURL.appendingPathComponent("api/v1/" + path))
        req.setValue("application/json", forHTTPHeaderField: "Accept")
        if let token { req.setValue("Bearer " + token, forHTTPHeaderField: "Authorization") }
        if let body {
            req.httpMethod = "POST"
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONEncoder().encode(body)
        }
        let (data, response) = try await session.data(for: req)
        guard let http = response as? HTTPURLResponse, data.count <= 4 * 1024 * 1024 else {
            throw PrototypeError.response
        }
        guard (200..<300).contains(http.statusCode) else { throw ConnectionFailure("control_plane_http_\(http.statusCode)") }
        return data
    }
    public func activate(_ token: String) async throws {
        let validated = try JSONDecoder().decode(LicenseValidation.self,
            from: await request("license/validate", body: ["license_token": token]))
        guard validated.valid else { throw PrototypeError.license }
        let identity = try DeviceIdentity.load(create: true)
        let result = try JSONDecoder().decode(Activation.self, from: await request("device/activate", body: [
            "license_token": token, "device_id": identity.id, "public_key": try identity.publicKey.base64EncodedString(),
            "platform": "ios", "device_name": "Nyxveil iOS"]))
        guard result.activated, result.device_id == identity.id else { throw PrototypeError.license }
        try SecureStorage.save(Data(token.utf8), account: "license")
    }
    private func catalog(_ token: String) async throws -> (Data, [String: String], [Location]) {
        let keys = try JSONDecoder().decode(CatalogKeys.self, from: await request("catalog-keys", token: token)).keys
        let raw = try await request("catalog", token: token)
        try CoreBridge.verify(raw, keys: JSONEncoder().encode(keys))
        let locations = try JSONDecoder().decode(CatalogEnvelope.self, from: raw).catalog.locations.filter(\.enabled)
        return (raw, keys, locations)
    }
    public func locations() async throws -> [Location] {
        let (_, _, locations) = try await catalog(SecureStorage.license())
        return locations
    }
    // A new location-scoped ticket per reconnect lets CP choose another same-location Node.
    public func prepare(location: String) async throws -> (String, AccessTicket) {
        let token = try SecureStorage.license()
        let identity = try DeviceIdentity.load()
        let (raw, keys, locations) = try await catalog(token)
        guard locations.contains(where: { $0.id == location }) else { throw PrototypeError.location }
        var ticket = try JSONDecoder().decode(AccessTicket.self, from: await request("ticket/issue", body: [
            "license_token": token, "device_id": identity.id, "location_id": location]))
        try ticket.validate()
        // Refresh only when needed before AUTH. Established NVP sessions don't have
        // a ticket-replacement control message; reconnect obtains fresh location scope.
        if ticket.expires_at < Int64(Date().timeIntervalSince1970) + 60 { ticket = try await refresh(ticket) }
        let input = ConnectInput(location_id: location, access_ticket: ticket.access_ticket, device_seed: identity.seed, catalog: raw, keys: keys)
        return (String(decoding: try JSONEncoder().encode(input), as: UTF8.self), ticket)
    }
    public func refresh(_ old: AccessTicket) async throws -> AccessTicket {
        let fresh = try JSONDecoder().decode(AccessTicket.self, from: await request("ticket/refresh", body: [
            "license_token": SecureStorage.license(), "device_id": DeviceIdentity.load().id, "access_ticket": old.access_ticket]))
        try fresh.validate()
        return fresh
    }
}
