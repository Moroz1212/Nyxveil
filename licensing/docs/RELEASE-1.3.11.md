# Nyxveil Control Plane 1.3.11

Purity-proof patch over **1.3.10** for artifact-pure button self-update verification.

## Purpose

Provides a published target for **1.3.10 → 1.3.11** self-update by button after
hosts have been bootstrapped onto **1.3.10** (required because immutable 1.3.8/1.3.9
cannot load fixed apply scripts without modifying InstallDir).

## Changes

- Version metadata **1.3.11**
- Includes the **1.3.10** self-update apply / locked-updater fixes

## Version

- Tag: `control-plane-v1.3.11`
- Schema: **5**
