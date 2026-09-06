---
title: Listener and Port Egress Relay
tags:
  - egress-manager
  - specification
  - v1
  - routing
  - xray
status: ready
related:
  - "[[../../docs/PROJECT_ROADMAP|PROJECT_ROADMAP]]"
  - "[[../007-sing-box-outbound-engine/spec|Phase 7 sing-box Outbound Engine]]"
  - "[[../008-interface-subnet-egress-routing/spec|Phase 8 Interface and Subnet Egress Routing]]"
---

# Listener and Port Egress Relay

## Goal

Close the V1 routing-source gap explicitly listed in the roadmap by supporting a fixed listener/port whose accepted traffic reaches a fixed destination through a selected managed outbound.

```text
Client -> Egress Manager listener -> Selected outbound -> Fixed destination
```

Primary examples:

- `0.0.0.0:6111/TCP -> VLESS/REALITY/XHTTP outbound -> 91.107.220.12:6111` for an existing SSH-VPN/user-management service.
- A local listener on the gateway -> selected managed outbound -> a remote Telegram proxy service.

This feature does not manage VPN users, Telegram proxy users, billing, or the remote service. It only owns the relay listener and its egress path.

## Desired State

A relay has:

- stable ID and display name;
- listen IP and port;
- network: TCP, UDP, or TCP+UDP when the selected outbound supports it;
- fixed destination host and port;
- selected outbound ID;
- optional source CIDR allow-list;
- enabled state and optimistic revision.

Failure policy is fail-closed. There is no implicit direct fallback.

## Xray Outbound Adapter

The generic outbound model gains a project-owned `xray` adapter. V1 initially accepts Xray VLESS URI imports when Xray is technically required, including REALITY + XHTTP share links that cannot be represented faithfully by the pinned sing-box adapter.

For VLESS REALITY/XHTTP, map bounded share-link fields into an encrypted Xray outbound credential document, including:

- UUID;
- server and port;
- SNI/server name;
- REALITY public key (`pbk`);
- short ID (`sid`);
- client fingerprint (`fp`);
- XHTTP path, mode, host and bounded JSON `extra`.

Credentials and transport secrets never appear in public API responses, plans, logs, fixtures, or diagnostics.

## Runtime Architecture

Use one dedicated project-owned Xray instance for all relay listeners:

```text
relay A listener --\
relay B listener ----> egress-manager-xray-relay.service -> selected Xray outbounds
relay C listener --/
```

The dedicated service is separate from foreign `xray.service`, Marzban, and 3x-ui. Existing foreign-Xray discovery and managed-fragment rules remain unchanged.

Each enabled relay renders a namespaced `dokodemo-door` inbound with a fixed destination and an exact inbound-tag routing rule to the selected outbound. Optional source CIDRs are enforced before the selected-outbound rule; non-allowed sources are blackholed.

## Transaction Workflow

Every apply follows:

1. Inspect the project-owned relay configuration and live listeners.
2. Load desired relays and referenced enabled outbounds from SQLite.
3. Reject protected management ports, listener overlap, missing/disabled/incompatible outbounds, unsupported network capability, malformed source CIDRs, and stale state.
4. Build a deterministic project-owned Xray candidate.
5. Persist authenticated snapshot and encrypted candidate in the operation journal.
6. Validate with the bundled Xray binary using native `xray run -test -config`.
7. Atomically replace or remove only the project-owned relay configuration.
8. Restart/stop only `egress-manager-xray-relay.service`.
9. Verify native configuration and service/listener state.
10. Commit; otherwise restore the authenticated snapshot and verify rollback.

All mutation paths use the existing global host-network mutation lock.

## Safety Constraints

- Never alter foreign Xray, Marzban, 3x-ui, firewall, route, Docker, Kubernetes, WireGuard, OpenVPN, or HAProxy state.
- Protect the Egress Manager panel and discovered/configured SSH ports from relay listeners.
- Reject foreign listener conflicts. A currently applied project-owned relay listener may be replaced by its own next candidate.
- A selected outbound must exist, be enabled, use the Xray adapter for the project-owned Xray relay runtime, and support the requested transport.
- Do not silently route direct when the outbound is unavailable.
- Do not expose candidate configuration or Xray credentials in public plans.
- Bound relay count, import size, XHTTP extra JSON, source CIDR count, generated candidate size, command output, and validation time.

## Recovery

`xray_relay_apply` participates in Phase 11 recovery and manual rollback:

- prepared/validated interrupted before mutation -> fail safely;
- applying/verifying interruption -> restore authenticated prior config and runtime state;
- rolling-back interruption -> complete restore;
- startup recovery occurs before normal mutations become ready;
- manual rollback requires exact current project-owned state and authenticated snapshot/candidate.

## Required Tests

- VLESS REALITY/XHTTP URI normalization with synthetic credentials only.
- malformed UUID, endpoint, REALITY key/SID/SNI/fingerprint, XHTTP mode/path/extra, oversized import.
- no secret values in public metadata or plan output.
- relay domain validation and SQLite CRUD/revision/FK deletion protection.
- protected panel/SSH port rejection.
- foreign listener conflict and overlapping desired listener rejection.
- missing, disabled, wrong-adapter, or capability-mismatched outbound rejection.
- deterministic candidate and stale-hash rejection.
- native Xray validation of representative generated candidates with pinned Xray v26.7.28.
- transactional apply, service restart, failure rollback, interrupted recovery, and manual rollback.
- TCP listener integration through a disposable fixed backend.
- browser CRUD/review/apply flow in English and Persian.
- existing NAT, HAProxy, sing-box, interface/subnet routing, foreign Xray preservation, recovery, and installer gates stay green.

## Acceptance Criteria

- A user can import a supported VLESS REALITY/XHTTP outbound without hand-writing Xray JSON.
- A user can create `listen -> selected outbound -> destination` from the web panel.
- The public plan shows only safe actions and hashes.
- Applying the plan produces a working project-owned listener and never mutates foreign Xray state.
- Killing or invalidating the selected outbound fails closed rather than connecting directly.
- Restart/reboot recovery converges without duplicate listeners or orphaned runtime state.
- Upgrade preserves existing SQLite/admin/configuration data.
