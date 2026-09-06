package control

// Message types for NVP/1 control plane (inside encrypted channel only).
const (
	TypeAuth     byte = 0x01
	TypeAuthOK   byte = 0x02
	TypeAuthFail byte = 0x03
	TypeConfig   byte = 0x04
	TypePing     byte = 0x05
	TypePong     byte = 0x06
	TypeRekey    byte = 0x07
	TypeRekeyAck byte = 0x08
	TypeClose    byte = 0x09
)

// Data message type for IP packets tunneled through NVP.
const (
	TypeData byte = 0x10
)

// Handshake message types (protected by TLS, before inner AEAD established).
const (
	TypeHandshakeInit byte = 0x20
	TypeHandshakeResp byte = 0x21
)

// IsControl returns true if message type is a control message.
func IsControl(t byte) bool {
	switch t {
	case TypeAuth, TypeAuthOK, TypeAuthFail, TypeConfig,
		TypePing, TypePong, TypeRekey, TypeRekeyAck, TypeClose:
		return true
	default:
		return false
	}
}

// IsValidType checks whether a message type is recognized.
func IsValidType(t byte) bool {
	switch t {
	case TypeAuth, TypeAuthOK, TypeAuthFail, TypeConfig,
		TypePing, TypePong, TypeRekey, TypeRekeyAck, TypeClose,
		TypeData, TypeHandshakeInit, TypeHandshakeResp:
		return true
	default:
		return false
	}
}

// AuthFailReason codes for TypeAuthFail payloads.
const (
	AuthFailInvalidTicket byte = 0x01
	AuthFailExpired       byte = 0x02
	AuthFailWrongAudience byte = 0x03
	AuthFailWrongDevice   byte = 0x04
	AuthFailRevoked       byte = 0x05
	AuthFailWrongScope    byte = 0x06
	AuthFailRateLimited   byte = 0x07
	AuthFailInternal      byte = 0xFF
)

// CloseReason codes for TypeClose payloads.
const (
	CloseNormal      byte = 0x00
	CloseRekeyFailed byte = 0x01
	CloseAuthTimeout byte = 0x02
	CloseAdminDrain  byte = 0x03
	CloseIdleTimeout byte = 0x04
)
