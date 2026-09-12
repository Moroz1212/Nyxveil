# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-13 after **development close-out** under revised COMPLETE criteria  
> (lab/CI sufficient; LIVE acceptance is user-only and does **not** block DEVELOPMENT COMPLETE).  
> Schema **5**. Frozen Core unchanged.  
> Live HEAD: `3ad1894591d14166dddb3f61d38d05b39a013b8e` on branch `real-operator-e2e-gates`.  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Releases (immutable — do not retag)

| Component | Version | Tag | Notes |
|---|---|---|---|
| Control Plane | **1.3.11** | `control-plane-v1.3.11` | Current published CP; includes 1.3.10 self-update fixes |
| Control Plane | 1.3.10 | `control-plane-v1.3.10` | First productized self-update apply / locked-updater fix |
| Control Plane | 1.3.9 | `control-plane-v1.3.9` | Prior; do not retag |
| Server | **1.1.18** | `server-v1.1.18` | ACME directory / cert path productized; do not retag |
| Server | 1.1.17 | `server-v1.1.17` | Prior; do not retag |

- CP 1.3.10 ZIP SHA256: `099507D8E9D76F273B314A70BEFB962B5152016FD42D3C77858249D159EF4AD3`
- CP 1.3.11 ZIP SHA256: `BBB2EC621480133A435E588667E6E4DBD7ADF0A6E2555369C917BF198464A309`
- Server 1.1.18 `nyxveil-server-linux-amd64` SHA256 (release SHA256SUMS): `18b05cbb9b0f2ffced73a78271bcd5ec5976e0c2877bca5f8f29c1a4cfa561a9`

## Gate policy (revised 2026-09-13)

- **FAST DEVELOPMENT GATES**: Control Plane CI / Server CI / root CI (build, unit, targeted integration). Default loop.
- **FULL RELEASE E2E** (`production-release-e2e.yml`): `workflow_dispatch` + `workflow_call` only — **not** on every push.
- LIVE production clicks are **user-only** and always reported as **PENDING** until the user runs them. They do **not** block DEVELOPMENT COMPLETE.

## Lab evidence baseline

### Run `34705774241` (contracts — accepted baseline)

https://github.com/Moroz1212/Nyxveil/actions/runs/34705774241  
HEAD: `8a240a96b1606b958bd4ef4321221b829f78948d` — conclusion **success**.

Proved on disposable GHA runners: Windows SCM; CP browser update flow; Ubuntu 24.04 systemd PID1; published server 1.1.15 → 1.1.17 by button; PID 5982→7147; `updated_healthy`; ACME/Pebble; cert button; TLS; QUIC; failure preservation.

**Honesty (not final artifact-pure):** that run used a CP InstallDir overlay of fixed apply/Deploy scripts and a local server **1.1.18** candidate for ACME/TLS/QUIC after the published 1.1.17 button path. Therefore it is **real OS lab E2E**, not byte-exact final purity proof for those overlays/candidate.

### Run `34708791199` (server artifact-pure on published 1.1.18)

Server Release / node job: **PASS** for published `server-v1.1.15` → published `server-v1.1.18` by button; `server_artifact_purity=PASS`; `server_no_local_candidate=PASS`; ACME migration root:root→nyxveil; Pebble; cert; TLS; QUIC; failure preservation on the **same** published 1.1.18 binary (live SHA `18b05cbb…`).

CP jobs in that same parent run did **not** aggregate PASS (GitHub API unauthenticated 403 rate-limit on Windows runners during CP Latest discovery). That is a **lab harness / GHA shared-IP** issue, not a known reproducible defect in published 1.3.10/1.3.11 packages.

## Control Plane bootstrap (product fact)

Immutable **1.3.8 / 1.3.9** installed apply scripts cannot load newer package fixes without modifying InstallDir (wrong `Wait-HttpsHealthy` args; locked updater copy).  

**LIVE hosts still on 1.3.8:** one-time elevated `production-deploy` to **≥1.3.10** (prefer **1.3.11**), then future CP updates by button. Do **not** label 1.3.8→newer as button PASS.

## DEVELOPMENT COMPLETE

**YES** — product fixes shipped; server final release lab-proven artifact-pure; CP fixes published; Frozen Core unchanged; no known reproducible product defects blocking operator use after the one-time CP bootstrap if needed.

LIVE three-click acceptance remains **PENDING** (user).

## Frozen Core

`7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b` (assert-frozen-core OK)

## User LIVE steps (only)

1. Control Plane → Update (to published ≥1.3.10 if still on 1.3.8: one-time production-deploy first).  
2. Node → Update (to published **1.1.18**).  
3. Certificate → Renew.

No SSH / chmod / sc / SQL / ACL / binary swap instructions from the agent.
