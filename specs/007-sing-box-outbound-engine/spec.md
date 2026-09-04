---
title: Phase 7 sing-box Outbound Engine
tags:
  - egress-manager
  - specification
  - phase/7
status: complete
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 7 sing-box Outbound Engine

## Goal

Create a generic outbound model and implement sing-box as its first transactional adapter without making the domain model depend on sing-box.

## Initial Protocols

- VLESS.
- Trojan.
- Shadowsocks.
- VMess when supported by the installed sing-box version.
- Hysteria2.
- TUIC when supported by the installed sing-box version.
- SOCKS5.
- WireGuard.

Support URI and structured-configuration import where practical, with fixture-based parser tests for every accepted format.

## Outbound State

- Stable ID, display name, adapter type, protocol type, and remote server endpoint.
- Capabilities for TCP and UDP.
- Enabled state and latest typed health result.
- Configuration validity, transport reachability, internet reachability, external IP, TCP status, UDP status when testable, and latency.
- Secret metadata that identifies secret presence or source without returning secret values.

## Operations

- Test an unsaved candidate without persisting it.
- Save revisioned desired state.
- Enable and disable.
- Delete with reference protection.
- Clone with a new identity and preserved secret semantics.
- Generate, review, validate, and transactionally apply owned sing-box configuration.

## Safety Constraints

- Never expose credentials, private keys, UUIDs, tokens, or transport secrets in logs, journals, API responses, plan output, UI state, or error text.
- Keep encrypted secret material separate from public outbound metadata and use stable secret references in desired state.
- Use fixed executable names and argument arrays without a shell.
- Bound imports, generated configuration, command output, probes, health-check concurrency, and returned results.
- Own a dedicated configuration and runtime paths; never replace unrelated sing-box configuration.
- Serialize mutations and reject stale plans when owned state changes after inspection.
- Every apply follows inspect, plan, snapshot, native validation, atomic install, restart or reload, verify, commit, and rollback.

## Required Failure Tests

- Malformed or oversized URI and configuration imports.
- Unsupported protocol or unsupported installed-version capability.
- Invalid candidate configuration.
- DNS failure, transport timeout, authentication failure, and internet-reachability failure.
- TCP succeeds while UDP is unsupported or untestable.
- External-IP probe failure does not leak secrets.
- Process start or verification failure triggers rollback.
- Interrupted operation recovers after service restart.

## Acceptance Criteria

- The domain model and repository remain adapter-neutral.
- Fixture tests cover every supported parser and prove canonical normalization plus redaction.
- Integration tests exercise each protocol where feasible and explicitly report capability-based skips.
- API and IPC tests prove authentication, CSRF protection, strict typed payloads, bounded output, optimistic concurrency, and secret redaction.
- UI supports test, save, enable, disable, delete, clone, health outcomes, latency, external IP, capability display, dry-run review, and apply.
- Existing NAT, HAProxy, and foreign-state preservation suites remain green.

## Progress

- Adapter-neutral public domain model with typed capabilities and granular health outcomes.
- AES-256-GCM credential documents with a domain-separated key, outbound-ID-bound authenticated context, bounded envelopes, and tamper detection.
- Revisioned SQLite persistence with keyset pagination, encrypted credential isolation, atomic clone/delete semantics, and indexes for enabled adapter scans and protocol filtering.
- Fixture-based URI import for VLESS, Trojan, Shadowsocks, VMess, Hysteria2, TUIC, SOCKS5, and WireGuard, plus structured sing-box JSON import.
- Public import results and structured logging expose secret metadata only; credential values remain private and redacted.
- Deterministic secret-safe planning where public output contains only actions and hashes while candidate and recovery journal payloads remain encrypted.
- Native validation, atomic install, dedicated service restart, verification, rollback, and interrupted-operation recovery.
- Current-version WireGuard endpoint conversion at the adapter boundary while preserving the generic WireGuard outbound domain type.
- Layered health testing for configuration validity, direct transport reachability, proxied internet reachability, external IP, TCP, UDP testability, and latency.
- Repeatable integration against the official pinned sing-box `v1.13.20` binary for all parser fixtures and an isolated chained SOCKS5 connectivity probe.
- Authenticated IPC and CSRF-protected HTTP APIs for import, unsaved and stored tests, revisioned updates, clone, delete, public dry-run plans, and transactional apply.
- Reviewed state and candidate hashes are required at apply time, so a changed desired or owned state rejects stale UI plans.
- Responsive operations UI for protected import, granular health, latency, external IP, capabilities, enable/disable, clone, delete, dry-run review, and apply.
- API, database, and browser tests prove atomic batch import, optimistic concurrency, strict payloads, CSRF enforcement, and secret-free responses.
