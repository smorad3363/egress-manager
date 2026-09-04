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
