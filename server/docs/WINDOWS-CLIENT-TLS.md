# Node TLS for Windows clients (server-v1.0.2)

## Trust model

Frozen Core dials require:

1. Normal x509 chain trust (Windows SystemTrust **or** explicit private CA in client RootCAs)
2. Catalog `spki_pin` match (`RequirePin=true`)

Self-signed installer leaves fail step 1 even with a correct pin. Do **not** use InsecureSkipVerify.

## Operator-provided certificate (recommended)

```bash
sudo bash install.sh ... \
  --tls-cert /path/to/fullchain.pem \
  --tls-key /path/to/privkey.pem \
  --dns-servers 203.0.113.53
```

- Files are copied to `/var/lib/nyxveil/tls.crt` + `tls.key` (key mode `0600`).
- Existing material is **not** overwritten on repair/update unless `--tls-replace`.
- `server.json` keeps `tls_cert_file` / `tls_key_file` paths.

## ACME (Let's Encrypt HTTP-01)

```bash
sudo bash install.sh ... \
  --tls-domain vpn.example.com \
  --tls-email ops@example.com \
  --public-host vpn.example.com \
  --dns-servers 203.0.113.53
```

Requirements:

- DNS A/AAAA for `--tls-domain` points at this node
- TCP port **80** free during issuance/renewal
- Node process issues/renews via `acme_domain` in `server.json`

Behaviour:

- Reuses a **stable ECDSA** leaf private key so SPKI stays constant across renewals when possible
- If the existing leaf key is incompatible (e.g. legacy Ed25519 self-signed), generates a new **ECDSA P-256** key in staging; SPKI changes once, then stays stable on renewals
- Atomic cert/key replace (`.tmp` → rename); key never logged
- If SPKI changes, same-node Control Plane re-register keeps catalog pin consistent
- Existing operator certs are not overwritten by ACME unless `--tls-replace` / `Replace: true`

## Gate tests (`go test ./internal/nodetls/`)

| Case | Result |
|------|--------|
| Trusted CA (models SystemTrust) + hostname + correct SPKI | PASS |
| Trusted CA + wrong SPKI | FAIL |
| Self-signed + correct SPKI + SystemTrust (RootCAs=nil) | FAIL |

## After cert change

Re-register (PoP) so catalog `spki_pin` matches the live leaf, then restart `nyxveil-server` if listeners were started with the old cert.
