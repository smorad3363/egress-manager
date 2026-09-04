---
title: Phase 10 Interface Outbounds
tags:
  - egress-manager
  - specification
  - phase/10
  - wireguard
  - openvpn
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
  - "[[008-interface-subnet-egress-routing/spec|Phase 8 Interface and Subnet Egress Routing]]"
---

# Phase 10 Interface Outbounds

## Goal

Extend the generic outbound model with project-owned kernel WireGuard and OpenVPN client interfaces. Route bindings must use those native interfaces directly instead of forcing the VPN through sing-box.

## Adapter Boundary

- Add a native interface adapter alongside the existing sing-box adapter.
- Import one bounded WireGuard or OpenVPN client profile into typed public metadata plus an encrypted credential document.
- Allocate deterministic project-owned Linux interface and service names from the immutable outbound ID.
- Keep VPN endpoint bypass, lifecycle, health, routing, cleanup, and reboot recovery behind the privileged daemon boundary.
- Never mutate foreign WireGuard, OpenVPN, NetworkManager, firewall, route, or systemd state.

## Import Safety

- Accept WireGuard INI and OpenVPN 2.x client profiles only; reject archives, URLs, shell interpolation, and unbounded input.
- Require exactly one usable remote endpoint for the first implementation and retain only its host, port, and transport as public metadata.
- WireGuard must contain one interface, one peer, a private key, a peer public key, interface addresses, and allowed IPs. Reject `PreUp`, `PostUp`, `PreDown`, `PostDown`, `SaveConfig`, `DNS`, and externally managed routing directives.
- OpenVPN must be a TUN client profile. Reject TAP, server mode, scripts/plugins, management listeners, external file references, arbitrary logging/status paths, route commands, and directives that can escape the owned lifecycle boundary.
- Normalize profiles into deterministic private credential documents. Secret values, embedded certificates, and complete profiles never appear in public APIs, logs, plans, or UI state.

## Native Lifecycle

### Kernel WireGuard

- Create only a deterministic project-owned WireGuard interface.
- Apply keys and peer state with fixed `ip` and `wg` commands without a shell.
- Disable automatic route ownership; Egress Manager remains the only policy-route and leak-control owner.
- Verify link kind, address state, peer endpoint, handshake/transfer evidence, and intended interface identity.

### OpenVPN Client

- Materialize the normalized profile only in a private runtime directory on tmpfs.
- Use a project-owned systemd client unit and deterministic TUN device name.
- Prevent server-pushed routes and DNS from changing host policy; Egress Manager owns both.
- Bound startup/reload/stop waits and verify the service plus TUN device before routing traffic.

## Routing Integration

- Interface outbounds expose a concrete tunnel interface to the existing route planner.
- Routes targeting sing-box continue to receive dedicated project-owned TUNs rendered by sing-box.
- Routes targeting an interface outbound use its verified native interface and exclude that VPN endpoint from tunnel policy.
- Failover is permitted only between compatible adapters whose lifecycle is healthy and already prepared.
- The coordinated transaction prepares interface lifecycles before policy routing, installs kill switches before moving traffic, and rolls back owned resources in reverse order.
- Route apply must reject stale outbound definitions, native interface state, routing state, or candidate hashes.

## Persistence and Cleanup

- Store public metadata through revisioned outbound rows and keep normalized profiles in the existing authenticated encryption boundary.
- Persist only desired state. Recreate required runtime files, services, interfaces, and policies during daemon startup recovery.
- Deleting or disabling an outbound referenced by a route remains blocked by foreign-key and route validation boundaries.
- Cleanup removes only positively identified project-owned interfaces, runtime files, unit instances, and owned policy state.

## Health

- Report configuration validation, lifecycle/service state, interface presence, endpoint reachability evidence where safe, latest WireGuard handshake when available, and bounded diagnostic detail.
- Do not classify a configured interface as internet-reachable solely because the link exists.
- Health probes must not alter the host default route or leak traffic outside the reviewed route policy.

## Representative Tests

- Valid WireGuard client config with IPv4/IPv6 addresses, one peer, endpoint, allowed IPs, keepalive, and optional preshared key.
- Valid inline OpenVPN TUN client profile with UDP and TCP variants, CA/certificate/key material, and optional inline user/password authentication.
- Malformed, oversized, multi-peer/multi-remote, unsafe hooks/plugins/scripts, external secret references, pushed-route acceptance, TAP/server, duplicate directives, and unsafe endpoint cases.
- Native validation failure, interface collision, service failure, stale state, partial apply, rollback, cleanup, and interrupted-operation recovery.
- Disposable-host integration proving WireGuard and OpenVPN traffic uses the intended interface and remains blocked when the tunnel fails.

## Acceptance Criteria

- WireGuard and OpenVPN imports are bounded, typed, deterministic, and secret-safe.
- Native interface lifecycle is project-owned, validated, reversible, recoverable, and reboot-persistent.
- Existing route bindings can select either sing-box or interface outbounds without double tunneling or foreign state mutation.
- Public plans contain owned actions and hashes only.
- OpenVPN and WireGuard fixtures plus disposable privileged-host integration tests pass.

## Research References

- [WireGuard `wg-quick` behavior and configuration](https://git.zx2c4.com/wireguard-tools/about/src/man/wg-quick.8)
- [OpenVPN 2.6 reference manual](https://build.openvpn.net/man/openvpn-2.6/openvpn.8.html)
- [Upstream OpenVPN systemd integration](https://github.com/OpenVPN/openvpn/blob/master/distro/systemd/README.systemd)

## Progress

- Phase specification created from the authoritative English roadmap after Phase 9 acceptance passed.
- Graphify confirmed that the generic outbound abstraction connects to interface/subnet routing through the safe network mutation transaction and native validation boundaries.
- Extended the domain with the native interface adapter and OpenVPN type while preserving sing-box WireGuard compatibility and enforcing adapter/type combinations.
- Added deterministic, bounded, secret-safe WireGuard and OpenVPN imports. WireGuard rejects hooks, automatic routing/DNS, multiple peers, and unsafe directives; OpenVPN accepts only TUN clients with one explicit remote, inline credentials, and a strict non-executable directive allowlist.
- Normalized OpenVPN profiles force an owned deterministic TUN name, `nobind`, and `route-nopull`; normalized credentials and complete profiles remain in the existing encrypted credential boundary.
- Reused the existing revisioned outbound table and `(enabled, adapter, id)` covering lookup index; no migration or additional write amplification is required for native adapter persistence.
