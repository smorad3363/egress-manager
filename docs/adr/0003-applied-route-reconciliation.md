---
title: Reconcile Applied Routing Without Activating Pending Edits
tags:
  - egress-manager
  - architecture/adr
status: accepted
---

# ADR 0003: Reconcile Applied Routing Without Activating Pending Edits

- Status: accepted
- Date: 2026-09-05

## Context

Routing CRUD and privileged Apply are separate operations. Rebuilding a startup plan directly from database rows could activate saved but unreviewed edits after reboot. Kernel routing and nftables are volatile, whereas applied routing and sing-box configuration files survive.

## Decision

After interrupted transactions and native interfaces recover, reconstruct the route transaction from validated applied routing and sing-box files. Preserve the exact candidate bytes and allocated resources. Require complete dual-family inventory, valid current ingress and management protection, and no foreign route or policy-rule collision. Reuse the authenticated journal, native validators, global lock, rollback, and exact runtime verification. Healthy repeated recovery creates no new transaction.

Never replace persisted applied route state with pending database edits during recovery. If the applied files are missing, malformed, or inconsistent, report degraded recovery rather than guessing. First-time route activation still requires explicit Apply.

## Consequences

Restart restores the last applied configuration without silently publishing pending changes. Foreign collisions require operator intervention rather than automatic resource reallocation. When a reboot has erased the old nftables snapshot, rollback validates and reinstalls leak protection from persistent applied routing before restoring policy routes instead of deleting the new kill switch.

Native-interface lifecycle retains its existing encrypted desired-state reconciliation; this decision governs coordinated route and sing-box replay. A real VM reboot remains an acceptance requirement, separate from simulated runtime loss tests.
