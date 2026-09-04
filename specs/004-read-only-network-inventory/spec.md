---
title: Phase 4 Read-Only Network Inventory
tags:
  - egress-manager
  - specification
  - phase/4
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 4 Read-Only Network Inventory

## Goal

Build a trustworthy, read-only view of the host network and relevant services before any production network mutation is introduced.

## Requirements

- Detect interfaces, addresses, routes, default gateways, and DNS state.
- Detect listening TCP and UDP sockets with process attribution when available.
- Detect nftables availability and the active iptables backend.
- Detect HAProxy, sing-box, Xray, OpenVPN, WireGuard, Marzban, and 3x-ui indicators without changing their state.
- Return typed inventory data over authenticated IPC and expose it only through an authenticated API endpoint.
- Display process, port, protocol, interface, subnet, service, and conflict warnings.
- Treat absent commands, restricted process metadata, and partially available data as explicit capability states rather than fatal errors.

## Safety Constraints

- Every probe is read-only and uses the injectable command runner with fixed executable names and arguments.
- Do not invoke a shell or interpolate host data into commands.
- Do not reload, restart, enable, disable, or write configuration for any detected service.
- Bound command output, execution time, and returned collection sizes.

## Acceptance Criteria

- Parsers have fixtures for representative and malformed Linux output.
- Inventory integration runs in the disposable network namespace lab.
- API tests prove authentication is required and no mutation endpoint exists.
- Existing foreign-state preservation tests remain green.

## Deferred

Snapshots, planners, network writes, firewall mutation, and service configuration remain in later phases.
