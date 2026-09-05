package packet

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/nyxveil/nvp/core/control"
	"github.com/nyxveil/nvp/core/protocol"
)

var (
	ErrFrameTooLarge  = errors.New("frame exceeds maximum size")
	ErrFrameTooShort  = errors.New("frame too short")
	ErrInvalidType    = errors.New("invalid message type")
	ErrInvalidPadding = errors.New("invalid padding length")
)

// FrameHeader is the authenticated inner frame metadata (inside AEAD plaintext).
type FrameHeader struct {
	MsgType    byte
	Flags      byte
	PaddingLen uint16
}

const (
	FlagPadding byte = 1 << 0
)

// MaxInnerPayload is maximum payload before AEAD overhead.
const MaxInnerPayload = protocol.MaxFrameSize - 64

// EncodeInner encodes inner plaintext: header + payload + optional padding.
func EncodeInner(msgType byte, payload []byte, padding []byte) ([]byte, error) {
	if len(payload)+len(padding)+4 > MaxInnerPayload {
		return nil, ErrFrameTooLarge
	}
	if !control.IsValidType(msgType) {
		return nil, ErrInvalidType
	}
	var flags byte
	if len(padding) > 0 {
		flags |= FlagPadding
	}
	if len(padding) > 0xFFFF {
		return nil, ErrInvalidPadding
	}

	out := make([]byte, 4+len(payload)+len(padding))
	out[0] = msgType
	out[1] = flags
	binary.BigEndian.PutUint16(out[2:4], uint16(len(padding)))
	copy(out[4:], payload)
	if len(padding) > 0 {
		copy(out[4+len(payload):], padding)
	}
	return out, nil
}

// DecodeInner decodes inner plaintext into header, payload, and padding.
func DecodeInner(data []byte) (FrameHeader, []byte, []byte, error) {
	if len(data) < 4 {
		return FrameHeader{}, nil, nil, ErrFrameTooShort
	}
	hdr := FrameHeader{
		MsgType:    data[0],
		Flags:      data[1],
		PaddingLen: binary.BigEndian.Uint16(data[2:4]),
	}
	if hdr.Flags&^FlagPadding != 0 {
		return FrameHeader{}, nil, nil, ErrInvalidPadding
	}
	hasPad := hdr.Flags&FlagPadding != 0
	if hasPad != (hdr.PaddingLen > 0) {
		return FrameHeader{}, nil, nil, ErrInvalidPadding
	}
	if !control.IsValidType(hdr.MsgType) {
		return FrameHeader{}, nil, nil, ErrInvalidType
	}
	bodyStart := 4
	bodyEnd := len(data) - int(hdr.PaddingLen)
	if bodyEnd < bodyStart {
		return FrameHeader{}, nil, nil, ErrInvalidPadding
	}
	payload := data[bodyStart:bodyEnd]
	var padding []byte
	if hdr.PaddingLen > 0 {
		padding = data[bodyEnd:]
	}
	return hdr, payload, padding, nil
}

// WireRecord wraps a complete encrypted record for transport framing.
// Format: length(4 BE) || epoch(4 BE) || sequence(8 BE) || ciphertext
type WireRecord struct {
	Epoch      uint32
	Sequence   uint64
	Ciphertext []byte
}

const wireHeaderSize = 4 + 8 // epoch + sequence

// EncodeWireRecord encodes length-prefixed epoch/seq header and ciphertext.
func EncodeWireRecord(epoch uint32, sequence uint64, ciphertext []byte) ([]byte, error) {
	inner := wireHeaderSize + len(ciphertext)
	total := 4 + inner
	if total > protocol.MaxFrameSize+4 {
		return nil, ErrFrameTooLarge
	}
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[0:4], uint32(inner))
	binary.BigEndian.PutUint32(out[4:8], epoch)
	binary.BigEndian.PutUint64(out[8:16], sequence)
	copy(out[16:], ciphertext)
	return out, nil
}

// DecodeWireRecord decodes length-prefixed epoch/seq/ciphertext from transport.
func DecodeWireRecord(data []byte) (WireRecord, []byte, error) {
	if len(data) < 4 {
		return WireRecord{}, data, ErrFrameTooShort
	}
	length := binary.BigEndian.Uint32(data[0:4])
	if length > protocol.MaxFrameSize {
		return WireRecord{}, data, fmt.Errorf("%w: %d", ErrFrameTooLarge, length)
	}
	if length < uint32(wireHeaderSize) {
		return WireRecord{}, data, ErrFrameTooShort
	}
	if len(data) < 4+int(length) {
		return WireRecord{}, data, ErrFrameTooShort
	}
	body := data[4 : 4+length]
	record := WireRecord{
		Epoch:      binary.BigEndian.Uint32(body[0:4]),
		Sequence:   binary.BigEndian.Uint64(body[4:12]),
		Ciphertext: body[12:],
	}
	remaining := data[4+length:]
	return record, remaining, nil
}

// HandshakeInitPayload is sent inside TLS-protected channel before inner AEAD.
type HandshakeInitPayload struct {
	Version      uint16
	ClientPubKey [32]byte
	// Padding is optional random padding selected via crypto/rand (length-prefixed on wire).
	Padding []byte
}

// HandshakeRespPayload is the server handshake response.
type HandshakeRespPayload struct {
	Version      uint16
	ServerPubKey [32]byte
	Epoch        uint32
	// Padding is optional random padding selected via crypto/rand (length-prefixed on wire).
	Padding []byte
}

const handshakeInitFixed = 2 + 32
const handshakeRespFixed = 2 + 32 + 4

// EncodeHandshakeInit encodes client handshake init (no magic bytes).
// Wire: version(2) || client_pubkey(32) || pad_len(2 BE) || padding
func EncodeHandshakeInit(p HandshakeInitPayload) ([]byte, error) {
	if len(p.Padding) > 0xFFFF {
		return nil, ErrInvalidPadding
	}
	total := handshakeInitFixed + 2 + len(p.Padding)
	if total > protocol.MaxHandshakeSize {
		return nil, ErrFrameTooLarge
	}
	out := make([]byte, total)
	binary.BigEndian.PutUint16(out[0:2], p.Version)
	copy(out[2:handshakeInitFixed], p.ClientPubKey[:])
	binary.BigEndian.PutUint16(out[handshakeInitFixed:handshakeInitFixed+2], uint16(len(p.Padding)))
	copy(out[handshakeInitFixed+2:], p.Padding)
	return out, nil
}

// DecodeHandshakeInit decodes client handshake init.
// Accepts legacy fixed-size (34-byte) messages and padded messages with pad_len header.
func DecodeHandshakeInit(data []byte) (HandshakeInitPayload, error) {
	if len(data) < handshakeInitFixed {
		return HandshakeInitPayload{}, ErrFrameTooShort
	}
	if len(data) > protocol.MaxHandshakeSize {
		return HandshakeInitPayload{}, ErrFrameTooLarge
	}
	var p HandshakeInitPayload
	p.Version = binary.BigEndian.Uint16(data[0:2])
	copy(p.ClientPubKey[:], data[2:handshakeInitFixed])
	if len(data) == handshakeInitFixed {
		return p, nil
	}
	if len(data) < handshakeInitFixed+2 {
		return HandshakeInitPayload{}, ErrFrameTooShort
	}
	padLen := int(binary.BigEndian.Uint16(data[handshakeInitFixed : handshakeInitFixed+2]))
	if handshakeInitFixed+2+padLen != len(data) {
		return HandshakeInitPayload{}, ErrInvalidPadding
	}
	if padLen > 0 {
		p.Padding = data[handshakeInitFixed+2:]
	}
	return p, nil
}

// EncodeHandshakeResp encodes server handshake response.
// Wire: version(2) || server_pubkey(32) || epoch(4) || pad_len(2 BE) || padding
func EncodeHandshakeResp(p HandshakeRespPayload) ([]byte, error) {
	if len(p.Padding) > 0xFFFF {
		return nil, ErrInvalidPadding
	}
	total := handshakeRespFixed + 2 + len(p.Padding)
	if total > protocol.MaxHandshakeSize {
		return nil, ErrFrameTooLarge
	}
	out := make([]byte, total)
	binary.BigEndian.PutUint16(out[0:2], p.Version)
	copy(out[2:34], p.ServerPubKey[:])
	binary.BigEndian.PutUint32(out[34:38], p.Epoch)
	binary.BigEndian.PutUint16(out[handshakeRespFixed:handshakeRespFixed+2], uint16(len(p.Padding)))
	copy(out[handshakeRespFixed+2:], p.Padding)
	return out, nil
}

// DecodeHandshakeResp decodes server handshake response.
// Accepts legacy fixed-size (38-byte) messages and padded messages with pad_len header.
func DecodeHandshakeResp(data []byte) (HandshakeRespPayload, error) {
	if len(data) < handshakeRespFixed {
		return HandshakeRespPayload{}, ErrFrameTooShort
	}
	if len(data) > protocol.MaxHandshakeSize {
		return HandshakeRespPayload{}, ErrFrameTooLarge
	}
	var p HandshakeRespPayload
	p.Version = binary.BigEndian.Uint16(data[0:2])
	copy(p.ServerPubKey[:], data[2:34])
	p.Epoch = binary.BigEndian.Uint32(data[34:38])
	if len(data) == handshakeRespFixed {
		return p, nil
	}
	if len(data) < handshakeRespFixed+2 {
		return HandshakeRespPayload{}, ErrFrameTooShort
	}
	padLen := int(binary.BigEndian.Uint16(data[handshakeRespFixed : handshakeRespFixed+2]))
	if handshakeRespFixed+2+padLen != len(data) {
		return HandshakeRespPayload{}, ErrInvalidPadding
	}
	if padLen > 0 {
		p.Padding = data[handshakeRespFixed+2:]
	}
	return p, nil
}
