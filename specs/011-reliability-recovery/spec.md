---
title: Phase 11 Reliability and Recovery
tags:
  - egress-manager
  - specification
  - phase/11
  - reliability
  - recovery
status: ready
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[CONSTITUTION]]"
  - "[[010-interface-outbounds/spec|Phase 10 Interface Outbounds]]"
---

# Phase 11 Reliability and Recovery

## Goal

Make every project-owned runtime mutation predictable across validation failure, daemon or dependency crashes, service restarts, and host reboot. Recovery must compose the existing subsystem journals and ownership boundaries instead of adding a second mutation path.

## Recovery Model

- Define one typed recovery inventory for NAT, HAProxy, sing-box, routing, native interface outbounds, and writable Xray fragments.
- Preserve each subsystem's authenticated journal and rollback semantics; a coordinator orders them by dependency and reports their state without copying secret snapshots into public models.
- Treat desired state in SQLite plus authenticated project-owned state and journals as authoritative. Treat live kernel, process, service, and foreign configuration state as evidence that must be rediscovered.
- Recovery order follows dependencies: recover interrupted component mutations, restore required outbound runtimes, restore proxy/runtime configuration, then reconcile routing and interception last.
- Recovery must be idempotent. Repeating it against the same desired and observed state produces no additional mutations.
- Any ambiguous ownership, corrupt journal, stale reviewed hash, or changed foreign resource fails closed and remains visible to the operator.

## Configuration Snapshots and Restore

- Create bounded metadata for recoverable snapshots: component, operation ID, schema version, creation time, authenticated content hash, previous-state presence, and restore status.
- Keep secret or native candidate bytes inside the existing encrypted journal boundary; public status exposes metadata and hashes only.
- Verify authentication, schema, size, ownership, and current-state preconditions before restore.
- Restore only project-owned files, rules, routes, interfaces, services, and reserved fragments. Never overwrite concurrently changed foreign state.
- Retain a bounded, documented snapshot history and remove superseded material without deleting the active recovery point.

## Runtime Journal and Crash Recovery

- Standardize operation phases across subsystem journals: prepared, applying, verifying, committed, rolling_back, rolled_back, and failed.
- Persist the recovery record before the first mutation and advance it durably after each dependency-safe checkpoint.
- On daemon startup, inspect every journal before accepting mutating requests. Resolve recoverable interrupted work or enter an explicit degraded state.
- A corrupt or unauthenticated journal must never be ignored, executed, or silently replaced.
- Crash recovery records a bounded public outcome while keeping candidates, credentials, and foreign configuration bytes private.

## Operation Locking

- Allow only one host-network mutation transaction at a time across HTTP, IPC, startup recovery, and emergency CLI paths.
- Use an OS-visible project-owned lock with bounded acquisition and typed busy diagnostics.
- Record owner PID, process start identity, operation ID, component, and acquisition time without secrets.
- Reclaim a stale lock only after positively proving the recorded process identity no longer exists; PID reuse or unreadable evidence must fail safe.
- Read-only inventory, health, and status operations remain available while a mutation lock is held.

## Restart and Reboot Persistence

- Daemon startup completes journal recovery before normal mutation service becomes ready.
- Reconcile desired enabled outbounds, HAProxy runtime, native interfaces, routing, and interception in dependency order after reboot.
- Missing optional dependencies produce explicit degraded health. Missing required dependencies block only affected managed traffic and never enable direct fallback implicitly.
- Repeated daemon or dependency restart must converge without duplicate rules, routes, processes, transient units, or state files.

## Dependency Health Monitoring

- Poll fixed, bounded probes for HAProxy, sing-box, native WireGuard/OpenVPN lifecycle, writable Xray, nftables/iproute2 ownership, and required executables.
- Track configuration validity, process/service state, transport evidence, internet reachability when safely testable, last success, consecutive failures, and bounded diagnostic detail separately.
- Apply debounced state transitions so one transient probe does not trigger mutation loops.
- Monitoring is observational by default. Automated reconciliation must acquire the global mutation lock and use the same journaled apply path as operator actions.
- Never change the host default route or bypass reviewed policy to perform a health probe.

## Emergency CLI

Provide a privileged local CLI with stable machine-readable and human-readable output:

```text
egressctl status
egressctl recover
egressctl bypass
egressctl rollback
```

- `status` is read-only and reports daemon readiness, lock ownership, journal state, component health, and the last recovery outcome.
- `recover` runs the same ordered, idempotent recovery coordinator used at daemon startup.
- `bypass` disables only Egress Manager-managed interception and routing, preserving desired state and all unrelated firewall, route, service, and panel configuration.
- `rollback` restores the latest authenticated eligible project-owned snapshot after checking current-state and foreign-state preconditions.
- Mutating commands require local privilege, acquire the global operation lock, emit no secret material, and return distinct stable exit codes.
- The CLI must still provide emergency recovery when the HTTP service is unavailable; it communicates only through the privileged daemon boundary or an explicitly offline-safe recovery boundary.

## Failure Injection

- Kill `egressd` after journal prepare and after each major mutation checkpoint.
- Kill HAProxy, sing-box, WireGuard/OpenVPN runtime, and writable Xray during or after apply.
- Corrupt candidate input, truncate or alter a journal, disconnect an outbound, and simulate DNS failure.
- Restart dependencies and the daemon repeatedly, then reboot a disposable Linux host or equivalent isolated VM fixture.
- Assert deterministic recovery result, bounded time, idempotent second run, fail-closed traffic, preserved foreign state, no secret exposure, and no orphaned project-owned resources.

## Acceptance Criteria

- Configuration snapshots are authenticated, bounded, restorable, and ownership-safe.
- Every mutating entry point shares one cross-component operation lock with safe stale-lock handling.
- Startup, manual recovery, rollback, and bypass are deterministic, idempotent, and auditable.
- Dependency failures remain visible and do not silently weaken routing or leak policy traffic.
- `bypass` removes only Egress Manager-managed interception/routing and preserves unrelated firewall configuration.
- All roadmap failure injections recover predictably in disposable integration environments.
- Go test/vet/race, frontend typecheck/lint/build, Playwright, and the repeatable privileged network lab remain green.

## Progress

- Phase specification created from the authoritative English roadmap after the Phase 10 native WireGuard/OpenVPN exit gate passed.
- Graphify connected Phase 11 to the existing runtime transaction journal, safe network mutation transaction, native validation, interface/subnet routing, and checkpointed interruption recovery boundaries.
- Inventory found six independently serialized host-mutation paths and ordered startup recovery, but no cross-component lock; concurrent NAT, HAProxy, sing-box, interface, routing, and Xray applies could overlap.
- Added a Linux kernel-backed global mutation lock with nonblocking acquisition, a private mode-`0600` metadata file, boot/process-start identity, bounded strict metadata parsing, symlink rejection, automatic crash release, stale metadata replacement, and secret-free inspection.
- Startup recovery/reconciliation and all six privileged apply paths now share the same lock while read-only operations remain available.
- Added a secret-free recovery status contract over authenticated IPC. It reports readiness, active or last lock ownership, and sanitized unfinished journal summaries without snapshot, candidate, credential, or raw failure content.
- Added `egressctl status` with human and JSON output plus stable usage, transport-failure, and recovery-required exit codes.
- Updated CI to run the same pinned Docker network-lab image used locally, eliminating drift from the expanded WireGuard/OpenVPN probe and dependency set.
- Checkpoint acceptance passed: full Go test/vet/race and two consecutive privileged network-lab runs are green.
- Replaced the daemon's ad hoc startup calls with one validated ordered coordinator: native interfaces, sing-box, HAProxy, conditional writable Xray, routing, NAT, then a final unfinished-journal assertion.
- The coordinator stops at the first unsafe dependency failure, returns only component/timing outcomes publicly, and can be rerun idempotently without exposing the underlying error.
- Added authenticated `recovery.run` IPC and `egressctl recover`; manual recovery uses the same coordinator and global lock as startup recovery.
- IPC now returns a stable `busy` code for lock contention, `egressctl` maps it to exit code `4`, and HTTP apply endpoints return an accurate `409` instead of claiming rollback ran before mutation began.
- Coordinator checkpoint passed full Go test/vet/race; the previously green network lab and frontend gates remain valid because this checkpoint changes no network candidates or frontend assets.
- Startup and manual recovery now share native-interface reconciliation after all interrupted journals have recovered, followed by another unfinished-journal check.
- Runtime verification checks live interfaces and services even when private configuration hashes match. Missing owned WireGuard links and OpenVPN transient services are recreated through the existing journaled executor.
- Regression coverage simulates loss of both native runtimes while private files survive, verifies convergence and repeated healthy checks, and rejects a foreign WireGuard link kind. This is not evidence of a real host reboot; route reconciliation and disposable reboot integration remain pending.
- Native-runtime checkpoint passed full Go test/vet, race tests for interfaceoutbound/app/reliability, and two consecutive rebuilt privileged network-lab runs. Frontend checks were not rerun because frontend assets are unchanged; their prior green result is retained. WSL WireGuard traffic coverage retains the documented transport-emulation limitation.
- Route-runtime reconciliation review found that existing rule/route and link checks used JSON substring matching. Replaced those checks with structural decoding: protocol ownership accepts numeric and string representations, whitespace does not cause false absence, nested fields cannot establish ownership, and malformed successful inventory blocks removal verification.
- Protocol presence and nftables table presence still do not prove exact desired-state convergence. Automatic route reconciliation remains deferred until exact runtime comparison is implemented. Existing absent-FIB-table error handling is retained and needs explicit error classification before automatic recovery.
- Inventory-parsing checkpoint passed full Go test/vet, final routeengine race/vet tests, and two rebuilt privileged network-lab runs. Added 16 parser/link regression cases. Frontend is unchanged and its previous verification is retained; no real VM reboot was performed.
- Added exact policy-runtime comparison for rule priority, source/interface selector, table, protocol, route destination/type/interface, endpoint throw routes, modifiers, duplicates, and foreign entries. Apply verification now uses this comparison instead of protocol-marker presence.
- Added complete owned nftables expression comparison including chain hook/priority/policy, rule order, DNS controls, MSS, acceptance, and kill switches. Only kernel handles and anonymous counter values are excluded. A read-only coordinated runtime verifier also checks persisted hashes and services.
- Native lab verification exercises exact IPv4/IPv6 and nftables comparisons for sing-box, WireGuard, and OpenVPN. Native output identified split src/srclen rule fields and IPv6 blackhole dev lo, both now covered by regression tests.
- Exact-runtime checkpoint passed full Go test/vet, routing/routeengine race tests, and two native network-lab runs. Frontend remains unchanged; real reboot and automatic reconciliation remain pending.
- Added degraded startup availability, sanitized latest recovery reports, and a global-lock-protected ordinary mutation gate. Regression coverage starts a real daemon with an unknown unfinished transaction, verifies emergency IPC remains available, retries recovery, and confirms apply is rejected. Decision: [[../../docs/adr/0002-degraded-recovery|ADR 0002]].
- Corrected coordinated runtime hash comparison to compare parsed state hashes, not content-only candidate digests; a regression test confirms unchanged persisted state reaches live nftables inspection.
- Degraded recovery checkpoint passed full Go test/vet and app/reliability/ipc/routeengine race tests. The prior native network gate remains applicable because this checkpoint changes no generated network candidates; frontend assets are unchanged.
- Added IPv6 route/rule inventory, numeric protocol parsing, split source-prefix parsing, combined inventory bounds, and fail-closed planning on incomplete routing inventory. Empty tables referenced by foreign policy rules are reserved as well as tables containing routes.
- Startup and manual recovery now replay validated applied routing/sing-box bytes after native lifecycle recovery. They preserve allocations, reject foreign collisions and changed management boundaries, and do not activate pending database edits. Decision: [[../../docs/adr/0003-applied-route-reconciliation|ADR 0003]].
- Exact nftables verification now runs before the route transaction commits. Regression tests cover missing runtime restoration, a no-op second recovery, and failure during replay when the old volatile NFT snapshot is absent. Rollback rebuilds leak protection from persistent policy in that case.
- Rollback validates and reinstalls leak protection before restoring policy routes, including when the initial nftables replay fails after reboot. This prevents a transient direct-egress window during recovery.
- Reboot-reconciliation checkpoint passed full Go test/vet, race tests for inventory/routing/sing-box/routeengine/app/reliability, and the rebuilt two-run privileged network lab. Frontend assets are unchanged; the prior frontend gate remains applicable. Real VM reboot remains for the Phase 11 failure-injection gate.
- Added a bounded observational dependency-monitor core with concurrent per-probe timeouts, sanitized typed results, deterministic snapshots, consecutive-failure tracking, and two-sample failure/recovery debounce. The monitor has no mutation or reconciliation callback. Core checkpoint passed full Go test/vet and reliability race tests.
