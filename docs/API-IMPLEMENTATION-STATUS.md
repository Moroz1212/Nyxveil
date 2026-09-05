# API Implementation Status — NVP/1 Control Plane

| Endpoint | Stub | Production | Notes |
|----------|------|------------|-------|
| POST /license/validate | ✅ | ✅ | |
| POST /device/activate | ✅ | ✅ | max_devices enforced |
| POST /device/remove | ✅ | ✅ | triggers revocation |
| POST /ticket/issue | ✅ | ✅ | Ed25519 JWT |
| POST /ticket/refresh | ✅ | ✅ | separate refresh handler |
| GET /catalog | ✅ | ✅ | signed, role filter |
| GET /locations | ✅ | ✅ | from catalog config |
| GET /nodes | ✅ | ✅ | role filter |
| GET /revocation | ✅ | ✅ | store + memory merge |
| GET /version | ✅ | ✅ | |
| POST /master/access | ✅ | ✅ | master plan check |
| POST /nodes/{id}/health | ❌ | ✅ | Ed25519 node_token |
| POST /nodes/{id}/drain | ❌ | ✅ | |
| POST /nodes/{id}/maintenance | ❌ | ✅ | |

## Security Features

| Feature | Status |
|---------|--------|
| HTTPS/TLS listen | ✅ (`-cert`/`-key`) |
| Rate limiting | ✅ per-IP middleware |
| Persistent signing keys | ✅ `-keys` directory |
| Node token auth | ✅ `auth/nodeauth` |
| Revocation push (pull sync) | ✅ `node/revocation` |
| SQLite persistence | ✅ |
| HA / PostgreSQL | ❌ planned |

Independent security/cryptographic audit: **NOT PERFORMED**
