---
title: Phase 2 Backend Foundation
tags:
  - egress-manager
  - specification
  - phase/2
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 2 Backend Foundation

## Goal

Build the unprivileged web/API foundation, privileged typed IPC boundary, persistent SQLite storage, and secure operator authentication.

## Requirements

- Load validated configuration including `listen_address` and `listen_port`.
- Select and persist a random free panel port while excluding SSH and known listeners.
- Use SQLite with embedded, ordered, transactional migrations and a repository boundary.
- Emit structured logs with operation IDs and redact secrets.
- Return stable typed API errors and expose a read-only health endpoint.
- Authenticate `egress-web` to `egressd` over a permission-restricted Unix socket using typed messages.
- Store passwords with a memory-hard password hash.
- Use opaque, rotated, server-side sessions with Secure, HttpOnly, SameSite cookies.
- Enforce CSRF on state-changing browser requests and throttle failed logins.
- Keep network mutation unavailable until later engine phases.

## Acceptance Criteria

- Configuration, migrations, storage, auth, sessions, CSRF, throttling, redaction, API errors, health, and IPC have unit/integration coverage.
- Privilege-boundary tests prove unauthenticated and malformed IPC requests are rejected.
- API tests prove unauthenticated control endpoints are rejected.
- `go test ./...`, race detector, and `go vet ./...` pass.
- No secret appears in API error or structured log fixtures.

## Deferred

Firewall, HAProxy, outbound, and routing mutations remain unavailable until their dedicated phases.
