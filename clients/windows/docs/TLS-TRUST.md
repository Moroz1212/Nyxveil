# Node TLS trust (Windows client)

## Gate result

**BLOCKED BY NODE TLS TRUST** for nodes that present a certificate **not** trusted by the Windows SystemTrust store — even when the catalog `spki_pin` is correct.

### Mandatory tests (`engine/internal/tlsgate`)

| Test | Result |
|------|--------|
| `TestFrozenConnectorSelfSignedWithCorrectSPKIPin` | SystemTrust-only + correct pin → **FAIL** (`x509` unknown authority) before SPKI check |
| Same dial with private CA pool + correct pin | **PASS** |
| Wrong pin with trusted CA | **FAIL** (pin mismatch) |

Frozen Core (`tlsstream` / `quic`) performs a normal `crypto/tls` handshake first, then `transport.VerifySPKIPin`. SPKI pin is **not** a substitute for chain trust. There is no `InsecureSkipVerify` and we will not add TrustAll.

## Production fix (no Frozen Core change)

Ubuntu **server-v1.0.2** (unpublished rebuild includes TLS provisioning):

- Operator: `--tls-cert` / `--tls-key` → `tls_cert_file` / `tls_key_file` (never overwritten on repair unless `--tls-replace`)
- Optional ACME: `--tls-domain <fqdn>` (+ DNS A/AAAA, port 80) with stable leaf key for SPKI stability
- Gate tests in `server/internal/nodetls`: trusted+hostname+pin PASS; trusted+wrong pin FAIL; self-signed+correct pin FAIL

See `server/docs/WINDOWS-CLIENT-TLS.md`.

## Client policy

- `RequirePin=true` always.
- Never `InsecureSkipVerify`.
- Optional `RootCAs`: only an operator-installed private CA pool under ProgramData — never TrustAll.
