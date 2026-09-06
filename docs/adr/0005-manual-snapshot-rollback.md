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

For every eligible format, authenticate both stored snapshot and candidate and require exact current owned files plus live runtime before restore. Route-engine apply and bypass additionally require complete current dual-family inventory, management boundaries, and foreign slot collision checks. Writable Xray additionally requires the exact foreign confdir hash. Native interfaces positively verify owned link kinds and services. HAProxy requires exact owned configuration, healthy Runtime API evidence, and native validation of the prior configuration. nftables compares a counter-independent canonical owned table; iptables compares its parsed owned chains, anchors, and rules. Both NAT adapters natively validate the rollback before transition. Leave a failed restore in `ROLLING_BACK` so startup/manual recovery can retry it rather than hiding an unfinished mutation.

## Consequences

The original transaction is the audit record and cannot be rolled back twice. Repeated rollback proceeds to the next latest committed transaction only after the first becomes `ROLLED_BACK`. Unsupported or stale snapshots fail before mutation. Route-engine, sing-box, native interface, writable Xray, HAProxy, nftables NAT, and iptables NAT formats created with authenticated envelopes are eligible. Legacy plaintext HAProxy and NAT records remain usable only for interrupted-operation recovery and are never manually eligible.
