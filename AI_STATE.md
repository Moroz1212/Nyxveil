# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after **Production Release E2E success** (workflow run `34705774241`).  
> Schema **5**. Frozen Core unchanged.  
> Live HEAD: `8a240a96b1606b958bd4ef4321221b829f78948d` on branch `real-operator-e2e-gates`.  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Releases (immutable — do not retag)

| Component | Version | Tag | Product SHA |
|---|---|---|---|
| Control Plane | 1.3.9 | `control-plane-v1.3.9` | `3f3e750d9eeab575e5edaaa40c7e54cffa51b1a4` |
| Server | 1.1.17 | `server-v1.1.17` | `3f3e750d9eeab575e5edaaa40c7e54cffa51b1a4` |

- CP ZIP SHA256: `C206E77B101BB061E1B550D1B7549BC8AACEEFDCD999B3B2B841B83BFE014C93`
- CP 1.3.8 ZIP SHA256: `FEF6C6D3F40F3BBA7A721E84ECB54F64DC20569CCDAC1FA93D5397225D70018A`
- Server source still carries **1.1.18** ACME/`acme_directory` candidate work (used for Pebble ACME phase after published 1.1.17 button update). **Not released.**

## Gate semantics

- Contract lab harnesses emit `CONTRACT_OPERATOR_GATES` / `SERVER_CONTRACT_GATES` only.
- `FULL_OPERATOR_E2E` and `AUTOMATED_PRODUCTION_GATES` are owned exclusively by
  `licensing/scripts/aggregate-production-gates.ps1` + `assert-production-gates.ps1`.
- PARTIAL / SKIPPED / NOT_EXECUTED / MISSING / BLOCKED ≠ PASS.

## Verified automated production gates (2026-09-12)

Workflow: https://github.com/Moroz1212/Nyxveil/actions/runs/34705774241  
HEAD: `8a240a96b1606b958bd4ef4321221b829f78948d`  
Conclusion: **success** (`windows-cp-button-e2e`, `windows-scm`, `ubuntu-node-operator-e2e`, `aggregate` all success).

Aggregate evidence:

- `cp_button_update=PASS` (published 1.3.8 → 1.3.9 by browser button; lab overlay of fixed `self-update-apply.ps1` + Deploy.psm1 onto InstallDir/scripts)
- `windows_scm=PASS`
- `node_button_update=PASS` (published 1.1.15 → 1.1.17; systemd PID1; old PID 5982 → new PID 7147)
- `durable_restart=PASS` (`updated_healthy`)
- `acme_pebble=PASS` (Pebble; candidate **1.1.18** binary after 1.1.17 button path)
- `cert_button=PASS` (`renewed`)
- `tls_served=PASS` (served thumbprint `a28dfbe76beebba3dbe07025f7d49e7f0e53496c8337eefbacc34911f84bd48a`)
- `quic_handshake=PASS` (`quic_dial_h3`)
- `rollback_recovery=PASS` (failed ACME preserves leaf; UI not stuck Renewing)
- `full_operator_e2e=PASS`
- Log: `AUTOMATED_PRODUCTION_GATES=PASS`

### Lab overlays / candidacy notes (honest)

- CP button gate overlays current `self-update-apply.ps1` onto installed **1.3.8** before the button click (published 1.3.8 apply had wrong `Wait-HttpsHealthy` parameter names and could fail replacing the running updater image). Web payload updated is still published **1.3.9**.
- ACME/TLS/QUIC/cert-button gates run on a **locally built 1.1.18** candidate after the published 1.1.15→1.1.17 button update (published 1.1.17 lacks lab `acme_directory` / insecure-directory TLS opts).

## LIVE operator acceptance

**PENDING** — automated gates PASS; three LIVE clicks may be offered to the user.

## Frozen Core

`7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` (assert-frozen-core OK at handoff)

## Recommended follow-ups (not blocking DEVELOPMENT COMPLETE)

1. Ship Control Plane **1.3.10** with fixed `self-update-apply.ps1` / updater copy-skip so production hosts do not need the lab overlay.
2. Ship Server **1.1.18** for `acme_directory` / lab ACME options used in Pebble E2E.
3. Wire release publish workflows to `needs: production-release-e2e` if not already.
4. Compact CP evidence JSON (fixed locally; avoid multi-MB artifact from prior ConvertTo-Json quirk).
