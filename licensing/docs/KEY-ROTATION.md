# Key rotation

## Signing keys

- Active Ed25519 signing key signs catalogs and access tickets
- Verification set may include previous keys during overlap
- Rotate via Admin → Signing Keys (SuperAdmin) or `ISigningKeyService.RotateAsync`
- Action is written to AuditLog

## License KEK

`Security:LicenseKekHex` protects license verifiers. Rotating KEK requires a planned re-hash/migration of verifiers — treat as a controlled security operation, not a routine click.

## Node tokens / bootstrap tokens

- Bootstrap tokens expire / exhaust uses
- Compromised node credentials: disable node, revoke related access, re-bootstrap with a new token

## Operational tips

1. Rotate signing keys during a maintenance window
2. Keep previous verification keys until tickets/catalogs signed by them expire
3. Never copy private key material into tickets, logs, or backups without encryption
