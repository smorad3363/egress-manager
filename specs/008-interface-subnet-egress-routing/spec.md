---
title: Phase 8 Interface and Subnet Egress Routing
tags:
  - egress-manager
  - specification
  - phase/8
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
  - "[[007-sing-box-outbound-engine/spec|Phase 7 sing-box Outbound Engine]]"
---

# Phase 8 Interface and Subnet Egress Routing

## Goal

Route traffic selected by ingress interface or source subnet through an enabled outbound without direct, DNS, IPv6, or recursive transport leaks.

## Desired State

- Stable route ID and display name.
- Exactly one source selector: interface or canonical IPv4/IPv6 subnet.
- Referenced outbound ID with protected deletion semantics.
- Explicit DNS policy and IPv4/IPv6 behavior.
- Failure policy: `block` by default; `failover` only with an explicit fallback; `direct` only when explicitly selected.
- Kill-switch, MTU, and TCP MSS settings where required.
- Enabled state and optimistic revision.

## Transaction Workflow

1. Inspect interfaces, addresses, routes, policy rules, DNS state, and project-owned TUN/firewall state.
2. Resolve enabled desired routes and referenced enabled outbounds.
3. Detect selector overlap, routing recursion, protected-management impact, and outbound-server paths.
4. Build deterministic TUN, policy-routing, DNS, IPv4/IPv6, bypass, kill-switch, and MTU/MSS candidates.
5. Persist a journal and owned snapshots.
6. Validate candidates natively without mutating host state.
7. Atomically apply in an order that keeps management and outbound transport paths reachable.
8. Verify selected egress, DNS behavior, IPv4/IPv6 policy, and kill switch; commit or roll back all owned state.

## Safety Constraints

- Never modify foreign routes, rules, DNS configuration, firewall tables, interfaces, or service instances.
- Always bypass selected outbound server endpoints from their own tunnel path.
- Protect loopback, SSH, panel listeners, required management routes, Docker/Kubernetes networks, and discovered VPN control paths.
- Direct fallback is never implicit. Missing or unhealthy selected egress blocks client traffic by default.
- IPv6 must be explicitly tunneled or blocked whenever an IPv4-only egress policy is active.
- DNS must follow the selected policy and must not silently fall back to host-direct resolution.
- Serialize mutation, reject stale reviewed plans, bound all inputs and command output, and use fixed executables with argument arrays.

## Required Failure and Leak Tests

- Missing interface, invalid or overlapping subnet, missing or disabled outbound, and recursive outbound-server route.
- Occupied TUN resources, native validation failure, partial apply failure, verification failure, rollback, and interrupted recovery.
- Kill the selected outbound and prove client traffic cannot use direct egress.
- Prove DNS cannot escape the selected route policy.
- Prove IPv6 cannot bypass a blocked IPv4 route.
- Prove outbound transport endpoints and management traffic remain reachable.
- Run packet capture only inside disposable network namespaces and assert the expected path.

## Acceptance Criteria

- Adapter-neutral revisioned route desired state supports interface and subnet sources.
- Planning covers TUN, policy routing, loop prevention, outbound-server bypass, DNS, IPv4, IPv6, kill switch, and MTU/MSS.
- Apply is journaled, validated, atomic, verifiable, reversible, and recoverable.
- Authenticated API and UI support CRUD, explicit failure policy, dry-run review, apply, and outcome visibility.
- Disposable no-leak tests pass twice while existing NAT, HAProxy, sing-box, and foreign-state suites remain green.

## Progress

- Adapter-neutral route model with explicit primary and fallback outbounds, failure, DNS, IPv4, IPv6, kill-switch, MTU, and TCP MSS policy.
- Revisioned SQLite desired state with keyset pagination, exact enabled-selector uniqueness, and indexed restrictive foreign keys for both primary and fallback outbounds.
- Read-only inventory now includes bounded, typed Linux policy rules for foreign-priority preservation.
- Deterministic safety planner resolves subnet ingress interfaces, rejects selector overlap and protected-management capture, requires enabled compatible outbounds, bounds hostname resolution, reserves foreign route tables and rule priorities, and emits explicit endpoint bypasses.
- Project-owned routing candidates and public plans use stable hashes, dedicated TUN names, bounded policy slots, and no secret material.
- Follow-outbound DNS requires explicit bounded resolvers; unsafe, mapped, link-local, duplicate, or implicit resolver state is rejected.
- sing-box composition now emits dedicated non-auto-route TUN inbounds, detoured DNS servers, explicit IPv4/IPv6 actions, and direct output only for explicitly selected policies; the pinned native binary validates the generated configuration.
- Native routing plans render only the `inet egm_egress` nftables table and protocol-`242` IPv4/IPv6 policy batches, including endpoint blocking inside selected tables, MTU/MSS controls, DNS input protection, and family-specific kill switches.
- Replanning preserves verified project-owned tables, priorities, and TUN prefixes while continuing to reject foreign collisions; disposable native tests validate nftables syntax and execute both iproute2 batches twice.
- A single coordinated route-engine transaction now binds the reviewed sing-box, routing-state, nftables, and IPv4/IPv6 candidates; authenticated snapshots cover all owned resources without placing credentials in public plans or plaintext journal fields.
- Apply installs leak controls before restarting the dedicated sing-box service, verifies every generated TUN, replaces only protocol-`242` policy state, commits the owned state atomically, and rolls back partial or interrupted operations in a safety-preserving order.
- Authenticated IPC and CSRF-protected HTTP now expose revisioned route CRUD plus plan/apply; apply rebuilds desired state inside `egressd` and requires exact sing-box, routing, native, and combined review hashes.
- The responsive route operations console supports interface/subnet sources, primary/fallback selection, explicit failure/DNS/IPv4/IPv6 policy, kill switch, MTU/MSS, enable/disable, editing, deletion, and public-only atomic review.
