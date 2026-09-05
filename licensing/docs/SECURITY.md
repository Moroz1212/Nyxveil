# Security

## HTTPS

Production: HTTPS only. If `Https:RequireHttpsInProduction` is `true` and no HTTPS URL/certificate/Kestrel endpoint is configured, the host **fails closed** at startup.

Development / localhost may use HTTP via explicit Development settings.

## Secrets

Never commit:

- `Security:LicenseKekHex` (64 hex chars / 32-byte HMAC key)
- SQL passwords
- Signing private keys
- Raw license or bootstrap tokens

Use `appsettings.Example.json` as a template with empty placeholders.

## License storage

- Raw license token: shown **once**
- Database: verifier only (`hmac1:...` style), never plaintext

## Signing keys

Ed25519 material stored via `ISigningKeyService` with protected private key bytes. Rotation is SuperAdmin-only and audited.

## Admin auth

Identity password hashing (no custom hashers). Cookie auth: HttpOnly, Secure, SameSite=Lax.

## Rate limiting

Applied to license / device / ticket / login / bootstrap-sensitive paths. Heartbeat is excluded from the sensitive limiter partition.

## Logging hygiene

Do not log: raw license tokens, bootstrap tokens, node tokens, LicenseKekHex, DB passwords, private keys.
Safe to log: LicenseId, NodeId, DeviceId, TicketId (jti), operation results.

## NVP/1 reminders

- Tickets are location-scoped
- Refresh never widens scope
- Failover is same-location only
- Master is a role, not a backdoor
