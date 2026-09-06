# State Machine

## States

```
NEW
  ↓ transport connect
TRANSPORT_CONNECTED
  ↓ X25519 handshake complete
SECURE_CHANNEL
  ↓ enter auth phase
AUTHENTICATING
  ↓ AUTH_OK
ESTABLISHED
  ↓ rekey trigger
REKEYING
  ↓ rekey complete
ESTABLISHED
  ↓ close
CLOSING → CLOSED
```

## Forbidden Transitions

- DATA before AUTH_OK (ESTABLISHED required)
- AUTH after ESTABLISHED
- REKEY before ESTABLISHED
- CLOSED → any active state
- TLS downgrade acceptance

## State Guards

| Action | Required State |
|--------|---------------|
| Send AUTH | AUTHENTICATING |
| Send DATA | ESTABLISHED or REKEYING |
| Initiate REKEY | ESTABLISHED |
| Send HANDSHAKE | TRANSPORT_CONNECTED |

## Server Pre-Auth Resource Limits

- Handshake timeout: 30s
- Max pending handshakes: 256
- Max frame size: 65536
- Auth rate limiting hooks available
