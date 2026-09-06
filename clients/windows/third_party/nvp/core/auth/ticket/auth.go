package ticket

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// EncodeAuthPayload builds AUTH body: jwt_len(2 BE) || jwt || Ed25519(sig over transcript||jwt).
func EncodeAuthPayload(jwt string, transcript []byte, devicePriv ed25519.PrivateKey) ([]byte, error) {
	if len(devicePriv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("device private key required")
	}
	jwtb := []byte(jwt)
	if len(jwtb) > 0xFFFF {
		return nil, fmt.Errorf("ticket too large")
	}
	sig := ed25519.Sign(devicePriv, authBindMessage(transcript, jwt))
	out := make([]byte, 2+len(jwtb)+ed25519.SignatureSize)
	binary.BigEndian.PutUint16(out[0:2], uint16(len(jwtb)))
	copy(out[2:], jwtb)
	copy(out[2+len(jwtb):], sig)
	return out, nil
}

// SplitAuthPayload extracts JWT and device signature from AUTH body.
func SplitAuthPayload(payload []byte) (jwt string, sig []byte, err error) {
	if len(payload) < 2+ed25519.SignatureSize {
		return "", nil, ErrInvalidToken
	}
	n := int(binary.BigEndian.Uint16(payload[0:2]))
	if 2+n+ed25519.SignatureSize != len(payload) {
		return "", nil, ErrInvalidToken
	}
	return string(payload[2 : 2+n]), payload[2+n:], nil
}

// VerifySessionBinding checks device signature over this session transcript.
func VerifySessionBinding(claims *Claims, jwt string, sig, transcript []byte) error {
	if claims == nil || len(claims.DevicePub) != ed25519.PublicKeySize {
		return ErrSessionBinding
	}
	if !ed25519.Verify(ed25519.PublicKey(claims.DevicePub), authBindMessage(transcript, jwt), sig) {
		return ErrSessionBinding
	}
	return nil
}

func authBindMessage(transcript []byte, jwt string) []byte {
	h := sha256.New()
	h.Write([]byte("nvp/1/auth"))
	h.Write(transcript)
	h.Write([]byte(jwt))
	return h.Sum(nil)
}
