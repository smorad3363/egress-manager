---
title: Phase 5 NAT and Port Forward Engine
tags:
  - egress-manager
  - specification
  - phase/5
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 5 NAT and Port Forward Engine

## Goal

Implement safe, transactional TCP and UDP port forwarding with nftables as the primary engine and a compatible iptables adapter.

## Capabilities

- Single ports, multiple ports, ranges, TCP, UDP, and combined TCP and UDP.
- Same-port forwarding and explicit port remapping.
- Canonical source CIDR restrictions.
- All-ports-except policies with mandatory management-port exclusions.
- Enable, disable, clone, delete, and traffic-counter inspection.
- Dry-run plans that expose structured intent and generated native changes before apply.

## Safety Invariants

- Protect SSH, the panel port, loopback, and required local management routes.
- Own every generated table, chain, and rule with the `egm_` namespace and stable comments.
- Never flush or replace foreign firewall state.
- Reject duplicate rules, conflicting listeners, invalid CIDRs, invalid ranges, and protected-port capture.
- Every apply follows inspect, plan, snapshot, native validation, apply, verify, commit, and rollback.
- Persist operation journals and recovery metadata before mutation.

## Required Failure Tests

- Remote destination unreachable.
- Duplicate rule and conflicting local port.
- Invalid CIDR and invalid port range.
- SSH or panel port included in all-ports forwarding.
- Service restart and simulated interrupted operation.
- Verification failure and rollback.

## Acceptance Criteria

- Unit tests cover normalization, ownership, rendering, conflict checks, and rollback transitions.
- TCP and UDP namespace integration tests exercise generated rules.
- Foreign firewall-preservation tests remain green.
- No product code performs a global firewall flush.
