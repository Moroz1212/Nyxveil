package packet

import (
	"encoding/binary"
)

// RekeyInitPayload starts fresh ECDH rekey (inside encrypted channel).
// Format: epoch(4 BE) || ephemeral_pubkey(32)
type RekeyInitPayload struct {
	Epoch        uint32
	EphemeralPub [32]byte
}

const rekeyInitSize = 4 + 32

// RekeyAckPayload completes rekey exchange.
type RekeyAckPayload struct {
	Epoch        uint32
	EphemeralPub [32]byte
}

const rekeyAckSize = 4 + 32

// EncodeRekeyInit encodes rekey init payload.
func EncodeRekeyInit(p RekeyInitPayload) ([]byte, error) {
	out := make([]byte, rekeyInitSize)
	binary.BigEndian.PutUint32(out[0:4], p.Epoch)
	copy(out[4:], p.EphemeralPub[:])
	return out, nil
}

// DecodeRekeyInit decodes rekey init payload.
func DecodeRekeyInit(data []byte) (RekeyInitPayload, error) {
	if len(data) < rekeyInitSize {
		return RekeyInitPayload{}, ErrFrameTooShort
	}
	var p RekeyInitPayload
	p.Epoch = binary.BigEndian.Uint32(data[0:4])
	copy(p.EphemeralPub[:], data[4:rekeyInitSize])
	return p, nil
}

// EncodeRekeyAck encodes rekey ack payload.
func EncodeRekeyAck(p RekeyAckPayload) ([]byte, error) {
	out := make([]byte, rekeyAckSize)
	binary.BigEndian.PutUint32(out[0:4], p.Epoch)
	copy(out[4:], p.EphemeralPub[:])
	return out, nil
}

// DecodeRekeyAck decodes rekey ack payload.
func DecodeRekeyAck(data []byte) (RekeyAckPayload, error) {
	if len(data) < rekeyAckSize {
		return RekeyAckPayload{}, ErrFrameTooShort
	}
	var p RekeyAckPayload
	p.Epoch = binary.BigEndian.Uint32(data[0:4])
	copy(p.EphemeralPub[:], data[4:rekeyAckSize])
	return p, nil
}
