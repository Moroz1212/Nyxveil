# Node authentication

## Node key

- Path: `/var/lib/nyxveil/node.key`
- Format: PEM `NYXVEIL NODE PRIVATE KEY` (Ed25519)
- Mode: `0600`, owner `nyxveil`
- Created on first register / first run (`identity.LoadOrCreate`)
- **Never** leave the host; never put in `server.json` or git

Repair installs **must** preserve `node.key` and the configured `node_id`. Losing either forces re-registration with Control Plane.

## Management auth (Control Plane HTTP)

Prefix: `nvp-node-req-v2`

Canonical message:

```
nvp-node-req-v2|{node_id}|{ts}|{nonce}|{METHOD}|{pathAndQuery}|{sha256_hex(body)}
```

Headers:

- `X-Node-Id`
- `X-Node-Timestamp`
- `X-Node-Nonce`
- `X-Node-Signature` (base64url Ed25519)

Implementation: `internal/nodeauth`.

## Bootstrap vs PoP

| Credential | Use | Lifetime |
|------------|-----|----------|
| Bootstrap token | First register only | One-shot from Control Plane |
| Node private key | All signed management APIs | Lifetime of node identity |
| Core `nvp-node-v1` token | Existing-node retry helpers | Short-lived PoP string |

## Client session auth

Client tickets are verified by Frozen Protocol Core (`third_party/nvp`) using issuer audience configured in runtime (not the node management key).
