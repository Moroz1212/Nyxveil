# Third-party Protocol Core (Frozen)

The Nyxveil server embeds **Nyxveil Protocol Core 1.0.0 (NVP/1)** as a Go module replace:

```
replace github.com/nyxveil/nvp => ./third_party/nvp
```

## Frozen Core identity

| Field | Value |
|-------|-------|
| Release | Nyxveil-Protocol-Core-v1.0.0-FROZEN |
| SHA256 | `7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` |
| Vendored path | `server/third_party/nvp` |

Verify an authoritative zip before replacing the tree:

```bash
sha256sum Nyxveil-Protocol-Core-v1.0.0-FROZEN.zip
# expect 7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b
```

## Policy

- **Do not edit** protocol code under `third_party/nvp`.
- To upgrade: extract a new frozen release zip over `third_party/nvp` and update this document’s SHA256.
- Server-owned packages live under `internal/` and `cmd/` only.
- Licensing for the core remains under the repository’s licensing materials; this file is operational provenance only.

## Compatibility shim: `go:linkname` → `Session.sendControl`

Frozen Core 1.0.0 exposes `TypeConfig` on the wire but has **no public** `SendConfig` / push-control API.
The node therefore uses an explicit, documented compatibility shim in `internal/netcfg/send.go`:

```go
//go:linkname sessionSendControl github.com/nyxveil/nvp/core/session.(*Session).sendControl
```

This is **not** a public Core API. It must not be treated as one.

Build/package gates (`scripts/assert-frozen-core.sh`, `scripts/build-release.sh`,
`scripts/verify-release.sh`, `scripts/package-release.sh`) assert the Frozen Core SHA above
and that `sendControl` / `TypeConfig` still exist. If the Core hash differs, packaging fails.
Do not modify Frozen Core to “fix” the shim — replace the vendored zip only via a new frozen release.
