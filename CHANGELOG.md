# Changelog

All notable changes will be documented here.

## [Unreleased]

### Added

- Initial repository, governance, backend, frontend, and CI scaffolding.
- Validated SQLite storage, Argon2id authentication, secure sessions, CSRF, and login throttling.
- Authenticated typed Unix-socket IPC between the web process and privileged daemon.
- Hardened API runtime with TLS policy, health checks, operation IDs, redacted structured logs, and graceful shutdown.
