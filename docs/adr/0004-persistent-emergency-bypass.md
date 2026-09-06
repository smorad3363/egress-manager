---
title: Persistent Emergency Routing Bypass
tags:
  - egress-manager
  - architecture/adr
  - recovery
status: accepted
---

# ADR 0004: Persistent Emergency Routing Bypass

- Status: accepted
- Date: 2026-09-06

## Context

An operator needs a local emergency path that disables Egress Manager traffic interception without flushing unrelated firewall or route state. A volatile-only removal would be unsafe operationally because ordinary daemon restart reconciliation would silently reactivate the last applied routing policy. Removing NAT or stopping outbound services would exceed the specified interception/routing boundary.

## Decision

`egressctl bypass` uses authenticated IPC, requires local administrator privilege, and acquires the global mutation lock. The privileged route executor snapshots authenticated owned state, validates the nftables deletion, removes only exact applied policy rules and routes marked with protocol `242`, removes only `inet egm_egress`, verifies absence, and records the transaction. It does not alter NAT tables, sing-box/native services, HAProxy, Xray, panel configuration, SQLite desired state, or applied configuration files.

Persist a strict secret-free bypass marker at `bypass_state_path`. Startup recovery completes an interrupted bypass but does not reactivate routing while the marker is active. Repeated bypass first verifies the disabled runtime and then returns without a new mutation or journal entry. Ordinary route Apply is rejected with `bypass_active`.

`egressctl recover` is the deliberate resume operation. After journal recovery and prerequisite reconciliation, it removes the marker and restores the last applied routing/sing-box state rather than pending database edits. A normal bypass failure restores the pre-command owned runtime; crash recovery deterministically completes an operation that had entered its applying phase.

## Consequences

Bypass survives daemon and host restart and remains visible through status and dependency health. Operators have one explicit resume action. The bypass boundary is narrow: port-forward NAT remains active because it is not egress route interception. An invalid marker fails recovery closed and is reported as invalid instead of being ignored.
