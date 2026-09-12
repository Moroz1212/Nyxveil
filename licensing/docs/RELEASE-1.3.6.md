# Nyxveil Control Plane 1.3.6

Patch release: UTF-8 Attention encoding hardening + certificate operation UX,
paired with Server **1.1.15** for RenewCertificate diagnostics/ownership.

## Highlights

- Fix Release-build mojibake risk for Dashboard Attention Russian strings
  (`Directory.Build.props` CodePage 65001 + Unicode-escape `AttentionCopy`)
- Attention no longer treats `rolled_back_healthy` / plain `expired` as current incidents
- UI ResultCode labels for renew_* failures; Operations shows node ResultMessage
- Schema **5** unchanged

## Companion Server

- Server **1.1.15** required for useful renew diagnostics and ACME state ownership repair
- Server **1.1.14** remains immutable

## Package

- Artifact: `Nyxveil-ControlPlane-v1.3.6-release.zip`
- Tag: `control-plane-v1.3.6`

## Not in this release

- Canary 25/50/100%
- Frozen Core / NVP changes
