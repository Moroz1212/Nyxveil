import XCTest
@testable import NyxveilShared

final class ContractTests: XCTestCase {
    func testControlPlaneJSON() throws {
        XCTAssertTrue(try JSONDecoder().decode(LicenseValidation.self, from: Data(#"{"valid":true,"max_devices":3}"#.utf8)).valid)
        let raw = Data(#"{"catalog":{"locations":[{"location_id":"fi","display_name":"Helsinki","enabled":true}],"nodes":[]},"key_id":"test"}"#.utf8)
        let locations = try JSONDecoder().decode(CatalogEnvelope.self, from: raw).catalog.locations
        XCTAssertEqual(locations.first?.id, "fi")
        let activation = try JSONDecoder().decode(Activation.self, from: Data(#"{"activated":true,"device_id":"abc"}"#.utf8))
        XCTAssertEqual(activation.device_id, "abc")
    }
    func testTicket() throws {
        let ticket = try JSONDecoder().decode(AccessTicket.self, from: Data(#"{"access_ticket":"test","expires_at":1,"node_id":"fi-02"}"#.utf8))
        XCTAssertEqual(ticket.node_id, "fi-02")
        XCTAssertThrowsError(try ticket.validate())
    }
    func testTypeConfigAndMTU() throws {
        let raw = #"{"vpn_ip":"10.8.0.2","vpn_prefix":24,"gateway":"10.8.0.1","dns_servers":["10.8.0.1"],"mtu":1135,"typeconfig_mtu":1400,"remote_address":"192.0.2.1"}"#
        let config = try JSONDecoder().decode(TunnelConfig.self, from: Data(raw.utf8))
        try config.validate()
        XCTAssertEqual(config.subnetMask, "255.255.255.0")
        XCTAssertEqual(config.mtu, 1135)
        let invalid = raw.replacingOccurrences(of: #""mtu":1135"#, with: #""mtu":1600"#)
        XCTAssertThrowsError(try JSONDecoder().decode(TunnelConfig.self, from: Data(invalid.utf8)).validate())
    }
}
