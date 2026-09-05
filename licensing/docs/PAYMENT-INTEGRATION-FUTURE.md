# Payment integration (future)

## Intent

Payment providers must **not** write licenses directly. They call an application service boundary:

`ILicenseProvisioningService`

- `CreateLicenseAsync`
- `ExtendLicenseAsync`
- `DisableLicenseAsync` / `EnableLicenseAsync`
- `RevokeLicenseAsync`

## PaymentEvents table

Stores provider event idempotency metadata:

- Provider
- ExternalPaymentId (unique with Provider)
- Status / Amount / Currency
- PayloadHash
- ReceivedAt / ProcessedAt

No card PANs or provider secrets belong here.

## Suggested flow

1. Provider webhook → verify signature
2. Insert/upsert `PaymentEvents` by ExternalPaymentId (idempotent)
3. Map SKU → Plan
4. Call provisioning service
5. Mark event processed; audit without secrets

## Out of scope for v1.0.0

No live Stripe/PayPal/etc. connectors ship in this release.
