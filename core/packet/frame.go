package packet

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/nyxveil/nvp/control"
	"github.com/nyxveil/nvp/protocol"
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
// Format: length(4 BE) || ciphertext
type WireRecord struct {
	Ciphertext []byte
}

// EncodeWireRecord encodes length-prefixed ciphertext for transport.
func EncodeWireRecord(ciphertext []byte) ([]byte, error) {
	total := 4 + len(ciphertext)
	if total > protocol.MaxFrameSize+4 {
		return nil, ErrFrameTooLarge
	}
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[0:4], uint32(len(ciphertext)))
	copy(out[4:], ciphertext)
	return out, nil
}

// DecodeWireRecord decodes length-prefixed ciphertext from transport.
func DecodeWireRecord(data []byte) (WireRecord, []byte, error) {
	if len(data) < 4 {
		return WireRecord{}, data, ErrFrameTooShort
	}
	length := binary.BigEndian.Uint32(data[0:4])
	if length > protocol.MaxFrameSize {
		return WireRecord{}, data, fmt.Errorf("%w: %d", ErrFrameTooLarge, length)
	}
	if len(data) < 4+int(length) {
		return WireRecord{}, data, ErrFrameTooShort
	}
	record := WireRecord{Ciphertext: data[4 : 4+length]}
	remaining := data[4+length:]
	return record, remaining, nil
}

// HandshakeInitPayload is sent inside TLS-protected channel before inner AEAD.
type HandshakeInitPayload struct {
	Version      uint16
	ClientPubKey [32]byte
}

// HandshakeRespPayload is the server handshake response.
type HandshakeRespPayload struct {
	Version      uint16
	ServerPubKey [32]byte
	Epoch        uint32
}

const handshakeInitSize = 2 + 32
const handshakeRespSize = 2 + 32 + 4

// EncodeHandshakeInit encodes client handshake init (no magic bytes).
func EncodeHandshakeInit(p HandshakeInitPayload) ([]byte, error) {
	out := make([]byte, handshakeInitSize)
	binary.BigEndian.PutUint16(out[0:2], p.Version)
	copy(out[2:], p.ClientPubKey[:])
	return out, nil
}

// DecodeHandshakeInit decodes client handshake init.
func DecodeHandshakeInit(data []byte) (HandshakeInitPayload, error) {
	if len(data) < handshakeInitSize {
		return HandshakeInitPayload{}, ErrFrameTooShort
	}
	var p HandshakeInitPayload
	p.Version = binary.BigEndian.Uint16(data[0:2])
	copy(p.ClientPubKey[:], data[2:handshakeInitSize])
	return p, nil
}

// EncodeHandshakeResp encodes server handshake response.
func EncodeHandshakeResp(p HandshakeRespPayload) ([]byte, error) {
	out := make([]byte, handshakeRespSize)
	binary.BigEndian.PutUint16(out[0:2], p.Version)
	copy(out[2:34], p.ServerPubKey[:])
	binary.BigEndian.PutUint32(out[34:38], p.Epoch)
	return out, nil
}

// DecodeHandshakeResp decodes server handshake response.
func DecodeHandshakeResp(data []byte) (HandshakeRespPayload, error) {
	if len(data) < handshakeRespSize {
		return HandshakeRespPayload{}, ErrFrameTooShort
	}
	var p HandshakeRespPayload
	p.Version = binary.BigEndian.Uint16(data[0:2])
	copy(p.ServerPubKey[:], data[2:34])
	p.Epoch = binary.BigEndian.Uint32(data[34:38])
	return p, nil
}
