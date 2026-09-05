package keys

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// AEADContext wraps ChaCha20-Poly1305 with epoch/sequence nonce construction.
type AEADContext struct {
	aead     cipherAEAD
	isClient bool
}

type cipherAEAD interface {
	Overhead() int
	NonceSize() int
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
	Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
}

// NewClientAEAD creates client-to-server encryption context.
func NewClientAEAD(key []byte) (*AEADContext, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return &AEADContext{aead: a, isClient: true}, nil
}

// NewServerAEAD creates server-to-client encryption context.
func NewServerAEAD(key []byte) (*AEADContext, error) {
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	return &AEADContext{aead: a, isClient: false}, nil
}

// Seal encrypts plaintext with authenticated additional data.
// AAD format: epoch(4) || sequence(8). Message type is inside plaintext.
func (c *AEADContext) Seal(epoch uint32, sequence uint64, plaintext []byte) ([]byte, error) {
	aad := makeAAD(epoch, sequence)
	nonce := makeNonce(epoch, sequence)
	return c.aead.Seal(nil, nonce, plaintext, aad), nil
}

// Open decrypts and authenticates ciphertext.
func (c *AEADContext) Open(epoch uint32, sequence uint64, ciphertext []byte) ([]byte, error) {
	aad := makeAAD(epoch, sequence)
	nonce := makeNonce(epoch, sequence)
	pt, err := c.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("aead open: %w", err)
	}
	return pt, nil
}

func makeAAD(epoch uint32, sequence uint64) []byte {
	aad := make([]byte, 12)
	binary.BigEndian.PutUint32(aad[0:4], epoch)
	binary.BigEndian.PutUint64(aad[4:12], sequence)
	return aad
}

// makeNonce constructs a 12-byte nonce from epoch and sequence (never repeats per key).
func makeNonce(epoch uint32, sequence uint64) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint32(nonce[0:4], epoch)
	binary.BigEndian.PutUint64(nonce[4:12], sequence)
	return nonce
}
