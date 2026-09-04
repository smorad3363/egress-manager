---
title: Phase 9 Xray, Marzban, and 3x-ui Adapter
tags:
  - egress-manager
  - specification
  - phase/9
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
  - "[[008-interface-subnet-egress-routing/spec|Phase 8 Interface and Subnet Egress Routing]]"
---

# Phase 9 Xray, Marzban, and 3x-ui Adapter

## Goal

Discover compatible Xray installations and safely bind existing inbound tags to outbound tags with native Xray routing, without taking ownership of arbitrary panel-managed configuration.

## Integration Boundary

- Detect Xray, Marzban, and 3x-ui installations from bounded read-only system and configuration evidence.
- Represent installation kind, service boundary, version, configuration roots, ownership status, and supported mutation strategy as typed discovery state.
- Prefer native `inboundTag` to `outboundTag` rules whenever supported.
- Do not transparently intercept Xray traffic into sing-box when native Xray routing is sufficient.
- Never rewrite an entire external panel configuration or assume ownership from a path or process name alone.

## Managed Fragment Strategy

- Use a dedicated Egress Manager fragment only where the discovered loader and panel lifecycle safely support it.
- Record the exact source state hash and reject stale plans after any external modification.
- Validate a complete effective candidate with the discovered native Xray binary before mutation.
- Snapshot every owned fragment and the minimal required external reference before apply.
- Use atomic replacement, bounded service control, post-reload verification, rollback, and interrupted-operation recovery.
- If a safe fragment or supported API boundary is unavailable, report the installation as read-only and explain the limitation.

## Safety Constraints

- Treat all discovered external files, services, sockets, and panel databases as foreign unless ownership is explicitly proven.
- Reject symlinks, unbounded files, ambiguous duplicate installations, unsupported versions, missing tags, duplicate routing rules, and unsafe service boundaries.
- Never return credentials or complete foreign configurations through logs, IPC, HTTP, or the UI.
- Preserve rule order and existing panel behavior; only add or replace the exact project-owned reference.
- Detect external changes before validate, apply, verification, and rollback.
- Avoid direct panel database writes unless an explicitly supported, documented adapter makes them transactional and reversible.

## Representative Tests

- Standalone Xray JSON with native routing and a supported managed include boundary.
- Representative Marzban and 3x-ui layouts with read-only discovery and explicit compatibility results.
- Missing or renamed inbound/outbound tags, duplicate tags, malformed JSON, oversized input, symlinked paths, and unsupported versions.
- External modification between plan and apply, native validation failure, reload failure, verification failure, rollback, and restart recovery.
- Preservation of unrelated routing rules, outbounds, panel files, services, and database state.

## Acceptance Criteria

- Discovery is read-only, bounded, typed, deterministic, and distinguishes supported mutation from read-only compatibility.
- Public plans expose only installation metadata, owned actions, limitations, and hashes.
- Supported native Xray route binding is validated, journaled, reversible, recoverable, and stale-safe.
- Representative standalone Xray, Marzban, and 3x-ui fixtures pass without blind external configuration overwrite.
- Integration limitations are documented in the UI and operator runbook.

## Progress

- Phase specification created from the authoritative English roadmap after Phase 8 no-leak acceptance passed.
- Added deterministic, bounded, read-only discovery for fixed standalone Xray, Marzban, and 3x-ui layouts.
- Discovery records configuration roots and hashes, explicit tags, fixed executable/version evidence, systemd state, foreign ownership, read-only strategy, and limitations without returning foreign configuration contents.
- Unsafe files, symlinks, malformed input, duplicate or unsafe tags, unstable reads, and ambiguous candidates for one service are rejected.
- Added a conservative managed-fragment boundary: only a loaded standalone `xray.service` with one absolute `-confdir` matching the discovered root and no explicit `-config` input is writable. Marzban and 3x-ui remain read-only.
- Added bounded all-JSON confdir snapshots that hash every foreign filename and byte, collect effective tags and routing, reject unsafe entries and unsupported merge layouts, and authenticate the owned fragment separately.
- Added private candidate composition that prepends deterministic project-owned native `inboundTag` to `outboundTag` rules while preserving the effective foreign routing object. Public review exposes only actions, counts, paths, and state/candidate hashes.
- Added native validation in a private temporary confdir, encrypted transaction snapshots, repeated foreign/owned change detection, atomic fragment replacement or removal, bounded `xray.service` restart and verification, rollback, and restart recovery.
- Added revisioned native binding persistence with keyset pagination and a partial unique index enforcing one enabled binding per inbound tag.

## Integration References

- [Xray multiple configuration files](https://xtls.github.io/en/config/features/multiple.html)
- [Xray `run` command and `-confdir` implementation](https://github.com/XTLS/Xray-core/blob/main/main/run.go)
- [Marzban Xray configuration defaults](https://github.com/Gozargah/Marzban/blob/master/config.py)
- [3x-ui configuration paths](https://github.com/MHSanaei/3x-ui/blob/main/CONTRIBUTING.md)
