---
title: Phase 0 Bootstrap Specification
tags:
  - egress-manager
  - specification
  - phase/0
status: active
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 0 Bootstrap

## Goal

Create a reproducible, checkpointed repository baseline for Go services and a React/TypeScript frontend.

## Requirements

- Initialize Git on `main` and preserve the supplied roadmap documents.
- Copy the authoritative English roadmap to `docs/PROJECT_ROADMAP.md`.
- Add concise agent rules, project state, constitution, ADR process, and focused project skills.
- Scaffold `egressd`, `egress-web`, shared internal packages, tests, migrations, and scripts.
- Scaffold Vite, React, TypeScript, Tailwind CSS, linting, and production build.
- Pin CI toolchains and commit dependency lockfiles.
- Keep optional AI helpers non-blocking.

## Acceptance Criteria

- `go test ./...` passes.
- `go vet ./...` passes.
- `pnpm --dir web typecheck` passes.
- `pnpm --dir web lint` passes.
- `pnpm --dir web build` passes.
- Git contains a green baseline checkpoint.

## Out of Scope

Product networking mutations, database persistence, authentication, and production UI pages begin in later phases.
