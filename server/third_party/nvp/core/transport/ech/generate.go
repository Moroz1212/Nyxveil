package ech

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/tls"
	"fmt"

	"golang.org/x/crypto/cryptobyte"
)

// ECHConfigVersion is the RFC 9849 ECHConfig version (0xfe0d).
const ECHConfigVersion uint16 = 0xfe0d

// GeneratedKey is a server ECH private key plus the marshalled config clients need.
type GeneratedKey struct {
	Key        tls.EncryptedClientHelloKey
	Config     []byte // single ECHConfig (for EncryptedClientHelloKey.Config)
	ConfigList []byte // ECHConfigList (for client EncryptedClientHelloConfigList)
}

// GenerateKey creates a real X25519 ECH keypair and marshalled ECHConfig/ECHConfigList
// suitable for crypto/tls EncryptedClientHelloKeys / EncryptedClientHelloConfigList.
// publicName is the outer SNI / public_name in the ECH config.
func GenerateKey(publicName string, configID uint8) (*GeneratedKey, error) {
	if publicName == "" {
		return nil, fmt.Errorf("ech: public name required")
	}
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	config := marshalECHConfig(ECHConfigVersion, configID, priv.PublicKey().Bytes(), publicName, 64)
	list := marshalECHConfigList(config)
	return &GeneratedKey{
		Key: tls.EncryptedClientHelloKey{
			Config:      config,
			PrivateKey:  priv.Bytes(),
			SendAsRetry: true,
		},
		Config:     config,
		ConfigList: list,
	}, nil
}

// ConfigListFromKeys builds an ECHConfigList from one or more server keys (for DNS / clients).
func ConfigListFromKeys(keys []tls.EncryptedClientHelloKey) []byte {
	builder := cryptobyte.NewBuilder(nil)
	builder.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		for _, k := range keys {
			b.AddBytes(k.Config)
		}
	})
	return builder.BytesOrPanic()
}

func marshalECHConfig(version uint16, id uint8, pubKey []byte, publicName string, maxNameLen uint8) []byte {
	builder := cryptobyte.NewBuilder(nil)
	builder.AddUint16(version)
	builder.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(id)
		b.AddUint16(0x0020) // DHKEM(X25519, HKDF-SHA256)
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes(pubKey)
		})
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddUint16(0x0001) // HKDF-SHA256
			b.AddUint16(0x0001) // AES-128-GCM
		})
		b.AddUint8(maxNameLen)
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddBytes([]byte(publicName))
		})
		b.AddUint16(0) // extensions
	})
	return builder.BytesOrPanic()
}

func marshalECHConfigList(configs ...[]byte) []byte {
	builder := cryptobyte.NewBuilder(nil)
	builder.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
		for _, c := range configs {
			b.AddBytes(c)
		}
	})
	return builder.BytesOrPanic()
}
