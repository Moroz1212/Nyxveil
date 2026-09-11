# Server 1.1.13: drained remote update

Local candidate. No GitHub tag, release, or production deployment is authorized by this change.

## Cause and behavior

Control Plane deliberately drains a node before dispatching an update. Its restarted
process does not start listeners until the saved lifecycle permits accepting sessions.
The old updater required accepting, TLS, and QUIC unconditionally, then misclassified
the same deliberate listener shutdown as a rollback regression.

Update/rollback now use explicit pre-update Drain/Maintenance evidence. They require
running, identity, TUN, bridge, ticket keys, acceptable revocation, no version block,
preserved node/config version, and restored preexisting CP connectivity. Ordinary
active-node DataplaneOK remains strict. TLS material, SAN, validity, key pairing,
ownership and modes are checked independently of listener readiness.

The installed gate has an explicit `updater` mode with no license prompt or stop test.
Its initialization supports ctl's normalized stdin invocation, where BASH_SOURCE is
unset and the installed path is supplied as the first argument.
The updater does not undrain. Control Plane's existing `updated_healthy` completion
restores the prior admin state, increments configuration version, and the runtime
starts listeners when that configuration arrives. A prior manual drain remains set.

## Handoff and compatibility

Legacy journals lacking lifecycle metadata are enriched only from a matching
authoritative applied-config and runtime status. Missing or inconsistent evidence
fails closed. A legacy missing previous-version field uses the original CLI version;
rollback still verifies installed and running versions before reporting health.

The 1.1.9 parent recognizes only `rolling_back`/`rolled_back` as child-owned rollback.
For legacy parents these wire phases are retained, with a separate terminal_outcome
carrying the exact healthy/failed result. Updated readers preserve that distinction.
Successful journals are retained so the daemon can durably adopt the terminal result;
transactions predating the current command are rejected. Failure reasons survive the
journal-to-runtime bridge. Failed TLS enforcement/restart is never reported as healthy.

An old server restored by rollback retains its old reporting implementation. Therefore
an actual 1.1.9 artifact must be used for the disposable upgrade/rollback gate; changing
a new build's version string does not prove that compatibility.

## Local coverage and required next gate

Regression tests cover drained 1.1.9-style state to 1.1.13, maintenance, active listener
failure, runtime prerequisites, legacy journal ownership/outcomes, TLS key corruption,
TLS enforcement failure, stale journals, terminal success while drained, and actual
local TCP/QUIC listeners starting after undrain. Shell tests execute the installed
gate's health block. Existing CP tests cover admin restoration and location safety.

Linux systemd, service ownership, real TUN/nftables, real NodeAuth/CP, ACME, reboot,
crash recovery and the exact old release binary require disposable Ubuntu 24.04
validation. Windows fixtures and cross-compilation do not establish LIVE readiness.
Frozen Core 1.0.0 and NVP/1 are unchanged.
