---
title: Manual Snapshot Rollback Semantics
tags:
  - egress-manager
  - architecture/adr
  - recovery
status: accepted
---

# ADR 0005: Manual Snapshot Rollback Semantics

- Status: accepted
- Date: 2026-09-06

## Context

Committed transactions already contain the snapshot needed to reverse their mutation. Copying that snapshot into a second journal entry would duplicate protected material and create two competing recovery records. Blindly restoring an older record could overwrite later owned or foreign changes.

## Decision

Permit only the explicit transition `COMMITTED → ROLLING_BACK → ROLLED_BACK`. `egressctl rollback` selects the latest committed operation in durable insertion order and dispatches only formats with implemented authentication and current-state preconditions. It never skips a newer unsupported committed operation to reach an older one.

For route-engine apply and bypass, authenticate the stored envelopes, require exact current applied files and live runtime (or exact active bypass), validate complete current dual-family inventory, management boundaries, and foreign slot collisions, then restore through the existing dependency-safe recovery path. Leave a failed restore in `ROLLING_BACK` so startup/manual recovery can retry it rather than hiding an unfinished mutation.

## Consequences

The original transaction is the audit record and cannot be rolled back twice. Repeated rollback proceeds to the next latest committed transaction only after the first becomes `ROLLED_BACK`. Unsupported or stale snapshots fail before mutation. Other subsystem formats remain ineligible until equivalent authentication and ownership preconditions are added.
