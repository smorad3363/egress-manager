# Changelog

All notable changes will be documented here.

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
