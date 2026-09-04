---
title: Phase 1 Architecture and Network Test Lab
tags:
  - egress-manager
  - specification
  - phase/1
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 1 Architecture and Network Test Lab

## Goal

Define stable domain models and a repeatable Linux namespace lab before any host networking mutation exists.

## Domain Models

- `Route`
- `Outbound`
- `PortForward`
- `FirewallRule`
- `HAProxyFrontend`
- `HAProxyBackend`
- `Transaction`
- `HealthStatus`

Models use validated types, deterministic identifiers, explicit enablement, timestamps, and secret-safe string/JSON forms.

## Lab Requirements

- Run only on Linux with explicit capability/root preflight.
- Create uniquely named disposable network namespaces and veth pairs.
- Provide isolated TCP and UDP echo endpoints.
- Exercise DNAT, SNAT/MASQUERADE, source CIDR restrictions, routing, counters, and rollback.
- Snapshot relevant state and clean only objects owned by the current test run.
- Skip with an exact reason when kernel capabilities are unavailable; never report a skip as pass.
- Repeat consecutive setup/test/cleanup cycles without residue.

## Acceptance Criteria

- Domain validation unit tests pass.
- Lab preflight is read-only.
- Linux integration test proves repeatable setup and cleanup.
- Developer host firewall is never flushed or replaced.
- Network operations are behind injectable command and filesystem boundaries.
