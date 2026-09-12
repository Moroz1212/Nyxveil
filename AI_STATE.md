# AI_STATE.md — Nyxveil current project state

> Updated 2026-09-12 after releases **control-plane-v1.3.9** and **server-v1.1.17**.  
> Schema **5**. Frozen Core unchanged.  
> LIVE three operator clicks: **PENDING**.  
> Preserved dirty: `licensing/tests/CoreInterop/verify-signed/go.mod`.

## Releases

| Component | Version | Tag | Product SHA |
|---|---|---|---|
| Control Plane | 1.3.9 | `control-plane-v1.3.9` | `3f3e750d9eeab575e5edaaa40c7e54cffa51b1a4` |
| Server | 1.1.17 | `server-v1.1.17` | `3f3e750d9eeab575e5edaaa40c7e54cffa51b1a4` |

- CP CI: https://github.com/Moroz1212/Nyxveil/actions/runs/34690952992 (480 unit / 130 integration / Browser E2E / SCM / FULL_OPERATOR lab PASS)
- Server CI: https://github.com/Moroz1212/Nyxveil/actions/runs/34690953034 + Server Release `34691224528`
- CP ZIP SHA256: `C206E77B101BB061E1B550D1B7549BC8AACEEFDCD999B3B2B841B83BFE014C93` (download-back matched)
- CP release: https://github.com/Moroz1212/Nyxveil/releases/tag/control-plane-v1.3.9
- Server release: https://github.com/Moroz1212/Nyxveil/releases/tag/server-v1.1.17

## LIVE operator acceptance (user only)

1. Control Plane button: **1.3.8 → 1.3.9**
2. Node update button: **1.1.15 or 1.1.16 → 1.1.17**
3. Renew certificate button

No PowerShell/SSH/chmod/sc manual repair.

## Frozen Core

`7b13097da410c79e4ad3292642f4a7bc03e576489edb058597cc538468e63b4b`
