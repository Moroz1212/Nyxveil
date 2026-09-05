# Node registration

1. Admin creates a bootstrap token, optionally restricted to a location, use count and expiry.
2. A new node generates its identity/keypair and sends bootstrap + public identity + credential public key to `POST /api/v1/nodes/register`.
3. Control Plane atomically consumes the bootstrap use and creates registry, credentials, health and NodeConfig in one transaction.
4. The response includes authoritative config, canonical `location_id` and `config_version`.
5. All normal heartbeat/config/revocation requests use **nvp-node-req-v2**.

An existing NodeId requires proof of the registered private key: Frozen Core
`nvp-node-v1` in body `node_token`. Both public identity and public key must remain
unchanged. No new credential is returned. Bootstrap cannot reset an identity or
replace a key. Key rotation is not a registration retry and is not implemented here.

TestOnly nodes are accessible only to active, valid **master-role** licenses.
The test role and master Plan.Code do not grant this privilege.

See [NODE-API.md](NODE-API.md) for exact request-signing bytes, replay rules and
the heartbeat → config → atomic apply flow for the future VPN server.
