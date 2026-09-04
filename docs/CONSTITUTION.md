---
title: Egress Manager Constitution
tags:
  - egress-manager
  - architecture
  - governance
status: active
---

# Egress Manager Constitution

## Principles

1. Safety before reachability: failed egress blocks by default; it never silently leaks to direct routing.
2. Ownership boundaries: mutate only Egress Manager-owned firewall, routing, proxy, and service objects.
3. Least privilege: `egress-web` is unprivileged; `egressd` alone owns typed privileged operations.
4. Transactional change: inspect, plan, snapshot, validate, protect, apply, verify, commit; rollback on failure.
5. Evidence over assumption: completion requires executable tests and recorded results.
6. Recovery by construction: Git plus `.project/STATE.yaml` must allow session-independent continuation.
7. Small dependable core: prefer Go standard library and narrow dependencies; V2 stays deferred.
8. Secrets stay secret: redact sensitive values from logs, APIs, fixtures, and diagnostics.
9. Accessibility and clarity: operational UI stays responsive, keyboard accessible, dark-first, and low-motion.
10. Foreign state survives: installation, operation, recovery, and uninstall preserve unrelated system configuration.

## Authority

[[PROJECT_ROADMAP]] defines scope and phase gates. Active specs refine implementation. ADRs document durable deviations or architectural choices.
