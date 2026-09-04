# Nyxveil Protocol Core (NVP/1)

Commercial VPN protocol foundation for Nyxveil platform.

**Requires Go 1.23+** (TLS Encrypted Client Hello APIs).

## Structure

```
Nyxveil/
├── cmd/
│   ├── nvp-test-client/    # Test client
│   ├── nvp-test-server/    # Test server node
│   ├── nvp-controlplane/   # Control Plane (stub + production modes)
│   ├── nvp-bench/          # Load/handshake benchmark
│   └── nvp-diag/           # Network diagnostic CLI (DNS/TCP/UDP/TLS/QUIC/ECH)
├── protocol/               # Version constants
├── session/                # NVP session state machine + fresh ECDH rekey
├── packet/                 # Wire framing and parsers
├── replay/                 # Replay protection window
├── control/                # Control message types
├── keys/                   # X25519, HKDF, ChaCha20-Poly1305
├── transport/
│   ├── quic/               # QUIC/UDP:443 primary
│   ├── tlsstream/          # TLS/TCP:443 fallback
│   ├── ech/                # ECH policy (preferred/required)
│   ├── masque/             # MASQUE stub (future)
│   ├── memory/             # In-memory transport for tests
│   └── racing.go           # QUIC → TLS transport racing
├── auth/
│   ├── ticket/             # JWT Ed25519 access tickets
│   └── device/             # Device identity
├── client/
│   ├── windows/            # Wintun integration foundation
│   └── android/            # Android VpnService SDK foundation
├── node/                   # Node descriptors
├── controlplane/
│   ├── api/
│   ├── catalog/            # Signed catalog
│   ├── model/
│   ├── store/              # SQLite persistence
│   └── server/             # Stub + production HTTP server
├── failover/               # Node/transport failover
├── server/                 # Node auth handler
├── tunnel/                 # MTU configuration
├── integration/            # End-to-end tests
├── internal/               # Metrics, test utilities
└── docs/                   # Protocol documentation (14+ files)
```

## Build & Test

```bash
go test ./...
go vet ./...
go test -bench=. -benchmem ./auth/ticket/...
go test -fuzz=FuzzDecodeWireRecord -fuzztime=10s ./packet/...
go run ./cmd/nvp-controlplane/ -listen :8443
go run ./cmd/nvp-bench/ -sessions 200
go run ./cmd/nvp-diag/ -host example.com -port 443 -all
```

## Documentation

| Document | Description |
|----------|-------------|
| [docs/PROTOCOL.md](docs/PROTOCOL.md) | Full NVP/1 specification |
| [docs/CRYPTOGRAPHY.md](docs/CRYPTOGRAPHY.md) | Crypto primitives and key schedule |
| [docs/SECURITY-AUDIT-PREP.md](docs/SECURITY-AUDIT-PREP.md) | Audit preparation checklist |
| [docs/COMMERCIAL-READINESS-REPORT.md](docs/COMMERCIAL-READINESS-REPORT.md) | Honest production readiness status |
| [docs/CLIENT-ARCHITECTURE.md](docs/CLIENT-ARCHITECTURE.md) | Windows/Android client foundations |

## Status

**Commercial readiness: NOT READY** — see [docs/COMMERCIAL-READINESS-REPORT.md](docs/COMMERCIAL-READINESS-REPORT.md).

Independent security/cryptographic audit: **NOT PERFORMED**
