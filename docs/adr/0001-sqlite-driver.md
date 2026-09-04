---
title: "ADR 0001: Use go-sqlite3 for SQLite"
tags:
  - egress-manager
  - architecture/adr
  - database/sqlite
status: accepted
---

# ADR 0001: Use go-sqlite3 for SQLite

- Status: accepted
- Date: 2026-09-04

## Context

Egress Manager needs embedded SQLite, transactional migrations, predictable WAL behavior, and bounded dependencies. The pure-Go `modernc.org/sqlite` module could not be fetched in the development environment because its module archive returned HTTP 403. It also adds a larger generated-code dependency surface.

## Decision

Use `github.com/mattn/go-sqlite3` with CGO. Pin module and CI toolchain versions. Open databases with foreign keys, WAL, busy timeout, and bounded connections.

## Consequences

- Builds require a C compiler and CGO.
- SQLite is embedded in the binary build; target releases must be compiled per platform.
- Driver behavior is mature and direct SQLite diagnostics such as `EXPLAIN QUERY PLAN` remain available.
- Release CI must test Linux builds with CGO enabled.

## Alternatives Considered

- `modernc.org/sqlite`: pure Go and easier cross-compilation, rejected for V1 after repeated module-download failure and larger dependency footprint.
- External SQLite CLI: rejected because process boundaries and parsing would weaken transactional storage guarantees.
