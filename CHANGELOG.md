# Changelog

All notable changes will be documented here.

## [v0.1.0-alpha.9] - 2026-09-06

### Added

- Project-owned listener relays that implement `listener/port -> selected managed Xray outbound -> fixed destination` for TCP, UDP, or both.
- VLESS REALITY/XHTTP URI import for the Xray relay adapter, with credentials encrypted at rest and kept out of browser/API plan responses.
- SQLite-backed relay CRUD with revisions, outbound foreign-key protection, optional source-CIDR allow lists, protected management/SSH-port checks, and foreign-listener collision checks.
- A dedicated `egress-manager-xray-relay.service` using the bundled pinned Xray runtime without modifying standalone Xray, Marzban, or 3x-ui services.
- Atomic relay plan/apply, native Xray candidate validation, authenticated rollback journals, startup recovery, and manual rollback.
- Production English/Persian Relay console and live Dashboard relay status, including create/edit/enable/disable/delete and review/apply flows.
- CI coverage against pinned Xray 26.7.28 plus browser CRUD, deterministic planning, fail-closed routing, rollback/recovery, and Ubuntu installation lifecycle checks.

### Safety

- Listener relay plans fail closed: if the selected outbound fails, traffic is not sent directly to the fixed destination.
- Relay listeners cannot claim the panel port, detected SSH ports, or conflicting foreign listeners.
- Existing Egress Manager configuration, administrator database, TLS material, IPC key, and runtime state remain preserved on repeat secure installs.
- Alpha.6 package-preservation protections and Alpha.7 HTTPS installer diagnostics remain in force.

## [v0.1.0-alpha.8] - 2026-09-06

### Added

- Persian/English language switching with persistent preference and RTL Persian layout.
- Live Dashboard refresh, partial-data warnings, and real configuration/runtime summaries.
- Browser regression coverage for live Dashboard data, working navigation/actions, Persian RTL, Network refresh/retry, Xray rescans, and explicit administrator login.

### Changed

- Replaced the Dashboard's hard-coded demonstration routes, traffic totals, service health, latency, uptime, and availability values with data returned by authenticated server APIs and the SQLite-backed configuration endpoints.
- Replaced the fake 24-hour traffic chart with actual Egress Manager NAT counters; no traffic history is invented when the backend does not provide it.
- Removed unfinished Firewall, Logs, and Settings entries from production navigation until backed by real browser APIs, instead of exposing non-functional or misleading controls.
- Simplified operator-facing wording and added clearer Persian copy for the production Dashboard and login flow.
- The login form no longer pre-fills a demonstration `operator` username; administrators enter the account that actually exists on the server.
- Dashboard shortcuts now open the real Routes and Network consoles, and create actions open the same production editors used by their owning pages.
- Network inventory has an explicit live Refresh action in addition to a real retry path.
- The design-system fixture is available only in development builds and is not exposed as a production panel page.
- Xray empty-state wording and rescan behavior distinguish the bundled validation runtime from a compatible existing Xray-family service managed through the native routing console.

### Safety

- Alpha.7 observable installer diagnostics and persistent install logs remain unchanged.
- Alpha.6 offline package-preservation protections remain unchanged: local APT uses `--no-upgrade --no-remove` and CI rejects removal of any pre-existing Ubuntu package.

## [Unreleased]

### Added

- Initial repository, governance, backend, frontend, and CI scaffolding.
- Validated SQLite storage, Argon2id authentication, secure sessions, CSRF, and login throttling.
- Authenticated typed Unix-socket IPC between the web process and privileged daemon.
- Hardened API runtime with TLS policy, health checks, operation IDs, redacted structured logs, and graceful shutdown.
- Dark-first responsive application shell, reusable UI primitives, and Playwright coverage.
- Read-only Linux inventory for interfaces, routes, listeners, DNS, network services, and binding conflicts.
- Deterministic nftables NAT planning with protected management ports, native validation, operation journals, verification, and rollback recovery.
