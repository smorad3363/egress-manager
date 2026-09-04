---
title: Phase 6 HAProxy Engine
tags:
  - egress-manager
  - specification
  - phase/6
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 6 HAProxy Engine

## Goal

Implement HAProxy as an independent transactional TCP proxy and load-balancer module without opening a separate statistics web port.

## Capabilities

- TCP frontends with single backends or backend pools.
- Round-robin and least-connections balancing.
- Server weights, backup servers, enable and disable state, and bounded health-check settings.
- Connection and traffic statistics through the HAProxy Runtime API over a Unix socket.
- Outcome-focused UI with generated HAProxy configuration available only as advanced, read-only detail.

## Transaction Workflow

1. Inspect the currently owned HAProxy configuration and Runtime API state.
2. Normalize desired intent and generate a deterministic candidate.
3. Persist the operation journal and previous owned snapshot.
4. Validate with the installed HAProxy binary.
5. Atomically install the owned candidate.
6. Perform a graceful reload while preserving active connections.
7. Verify frontends, backend health, Runtime API availability, and expected process state.
8. Commit, or restore the previous owned snapshot and reload on failure.

## Safety Constraints

- Use fixed executable names and argument arrays without a shell.
- Own a dedicated configuration fragment and Runtime API socket path; never overwrite unrelated HAProxy configuration.
- Never expose the HAProxy statistics web interface.
- Bound configuration size, Runtime API reads, health-check timing, and returned statistics.
- Serialize mutations and reject stale plans when owned state changes after inspection.
- Keep secrets out of generated configuration, logs, journals, API responses, and UI state.

## Required Failure Tests

- Invalid candidate configuration.
- Occupied frontend port.
- Backend failure and backup failover.
- Backend recovery.
- Graceful reload failure.
- Verification failure and rollback.
- Interrupted operation recovery after service restart.

## Acceptance Criteria

- Unit tests cover validation, normalization, deterministic rendering, owned-state hashing, statistics parsing, and rollback transitions.
- Disposable integration tests prove single-backend proxying, multi-backend distribution, failure, failover, recovery, config rejection, and graceful reload.
- API and IPC tests prove authentication, CSRF protection, strict typed payloads, and bounded output.
- UI supports complete desired-state management, dry-run review, apply state, health, and statistics without making raw syntax the default view.
- Existing NAT and foreign-firewall preservation suites remain green.
