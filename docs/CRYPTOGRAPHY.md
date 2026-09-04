# Cryptography

## Approved Primitives

| Purpose | Algorithm | Notes |
|---------|-----------|-------|
| Transport encryption | TLS 1.3 | QUIC embeds TLS 1.3 |
| Session key agreement | X25519 | Ephemeral per session |
| Key derivation | HKDF-SHA256 | Domain-separated labels |
| Session AEAD | ChaCha20-Poly1305 | 256-bit key, 96-bit nonce |
| Access ticket signatures | Ed25519 | via JWT (JWS) |
| Random | crypto/rand | OS CSPRNG |

## Prohibited

- Custom ciphers, MACs, hash functions, KDFs, curves
- TLS 1.0/1.1
- `alg=none` JWT
- Hardcoded private keys
- Encrypt-without-authentication

## Key Hierarchy

```
TLS 1.3 handshake
  └── X25519 ECDH (ephemeral)
        └── HKDF-SHA256
              ├── nvp/1/c2s → ChaCha20-Poly1305 (client→server)
              └── nvp/1/s2c → ChaCha20-Poly1305 (server→client)
```

Domain separation labels prevent cross-direction key reuse.

## Forward Secrecy

Each session uses fresh X25519 ephemeral keys. Compromise of long-term server TLS certificate does not decrypt past sessions that used ephemeral ECDH.

Rekey derives new epoch keys from shared secret + epoch counter.

## Nonce Construction

Nonce = epoch (4 bytes BE) || sequence (8 bytes BE)

Never reuse nonce with same AEAD key.

## AEAD Additional Data

AAD = epoch (4) || sequence (8)

Message type is inside encrypted plaintext.

## Control Plane Keys

- Signing: Ed25519 (JWT `EdDSA`)
- Nodes store verification public keys only
- Support `current` + `next` key_id for rotation

Independent security/cryptographic audit: **NOT PERFORMED**
