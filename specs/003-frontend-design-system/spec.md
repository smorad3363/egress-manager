---
title: Phase 3 Frontend Design System
tags:
  - egress-manager
  - specification
  - phase/3
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
---

# Phase 3 Frontend Design System

## Goal

Create the dark-first, responsive application shell and reusable UI primitives before feature pages are implemented.

## Requirements

- Use React, TypeScript, Tailwind CSS, shadcn-compatible primitives, Motion, and ECharts.
- Define typography, spacing, surface, border, focus, and status tokens.
- Provide cards, forms, dialogs, tables, badges, toasts, skeletons, and loading, empty, and error states.
- Provide responsive navigation for Dashboard, Outbounds, Routes, Port Forward, HAProxy, Firewall, Network, Logs, and Settings.
- Keep animation subtle, fast, functional, and disabled or reduced under `prefers-reduced-motion`.
- Preserve keyboard navigation, visible focus, semantic landmarks, and readable contrast.
- Keep the initial bundle and dependency surface small.

## Acceptance Criteria

- Component and state fixtures render in a dedicated design-system showcase.
- Desktop and mobile layouts have automated browser coverage.
- Keyboard focus and reduced-motion behavior are tested.
- Type checking, linting, production build, and browser tests pass.

## Deferred

Live feature data, network mutation forms, and final workflow pages remain in their dedicated phases.
