# Wire Format

## Pre-Authentication Traffic

**No NVP-specific plaintext identifiers.** First bytes visible to observer are standard TLS/QUIC handshake messages.

## Transport Framing

All NVP messages over transport use length-prefix framing:

```
┌──────────────┬─────────────────────┐
│ length (4BE) │ payload (variable)  │
└──────────────┴─────────────────────┘
```

Maximum frame size: 65536 bytes.

## Handshake (inside TLS-protected channel)

### Client Init

```
version:     uint16 BE (1)
client_pubkey: 32 bytes (X25519)
```

Total: 34 bytes. No magic bytes.

### Server Response

```
version:      uint16 BE (1)
server_pubkey: 32 bytes (X25519)
epoch:        uint32 BE (initial epoch, typically 1)
```

Total: 38 bytes.

## Encrypted Records

After handshake, all messages are AEAD-encrypted:

```
Wire: length(4) || ciphertext

Ciphertext = AEAD.Seal(nonce, inner_plaintext, AAD)

AAD = epoch(4) || sequence(8)

Inner plaintext:
  msg_type:   uint8
  flags:      uint8
  padding_len: uint16 BE
  payload:    variable
  padding:    optional (authenticated, discarded)
```

## Message Types

| Type | Value | Direction |
|------|-------|-----------|
| AUTH | 0x01 | Client→Server |
| AUTH_OK | 0x02 | Server→Client |
| AUTH_FAIL | 0x03 | Server→Client |
| CONFIG | 0x04 | Server→Client |
| PING | 0x05 | Both |
| PONG | 0x06 | Both |
| REKEY | 0x07 | Both |
| REKEY_ACK | 0x08 | Both |
| CLOSE | 0x09 | Both |
| DATA | 0x10 | Both |

## Padding

Optional authenticated padding inside encrypted inner frame. Configurable policy; not fixed length; not mandatory per packet.
