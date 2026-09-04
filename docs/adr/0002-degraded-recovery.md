---
title: Keep Emergency Recovery Available During Degraded Startup
tags:
  - egress-manager
  - architecture/adr
status: accepted
---

# ADR 0002: Keep Emergency Recovery Available During Degraded Startup

- Status: accepted
- Date: 2026-09-05

## Context

Exiting the daemon after a subsystem recovery failure also removes the authenticated IPC endpoint used by `egressctl recover`. A broken component must not prevent operators from inspecting or retrying recovery.

## Decision

After database, authentication, configuration, and global-lock initialization succeed, a coordinator failure leaves the daemon serving authenticated read-only and recovery operations. Health reports degraded status and recovery status includes the sanitized latest coordinator report. Ordinary host apply operations acquire the global lock and require both a successful recovery outcome and an empty unfinished journal before executing.

Recovery errors are not returned or logged verbatim. Only component identity and sanitized report fields are exposed. Fatal initialization errors still prevent startup.

## Consequences

An operator can inspect and retry recovery without changing services or bypassing authentication. Failed recovery cannot silently enable ordinary network mutation. The latest outcome is held in process memory; after restart a fresh startup recovery report replaces it, while durable transaction recovery still depends on the journal rather than memory.

## Alternatives Considered

- Exiting on every recovery error makes emergency IPC unavailable.
- Allowing normal applies during failed recovery risks overlapping unresolved transactions.
- Exposing raw recovery errors can disclose private configuration or credentials.
