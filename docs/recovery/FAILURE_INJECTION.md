---
title: Phase 11 Failure Injection
tags:
  - egress-manager
  - recovery
  - testing
status: active
---

# Phase 11 Failure Injection

This runbook maps the Phase 11 exit gate to repeatable evidence. All native networking runs only inside the disposable privileged container and its network namespaces.

## Automated matrix

| Injection | Expected result | Evidence |
| --- | --- | --- |
| Interrupted apply | Authenticated snapshot restores only owned state | `TestExecutorRecoversInterruptedApply`, `TestExecutorRecoversInterruptedInitialApply`, `TestExecutorRecoversInterruptedNativeApply`, `TestFragmentExecutorRecoversInterruptedApply` |
| Partial native mutation | Reverse rollback preserves leak protection | `TestExecutorRollsBackPartialIPApply`, `TestReconcileAppliedRestoresLostRuntimeAndSecondRunDoesNotApply` |
| Corrupt authenticated journal | No mutation; transaction remains unfinished and daemon remains degraded | `TestExecutorRecoveryLeavesTamperedAuthenticatedSnapshotUnfinished`, `TestRecoveryLeavesTamperedAuthenticatedNATJournalUnfinished`, `TestFailedStartupRecoveryKeepsEmergencyIPCAndBlocksApply` |
| Invalid candidate | Native validation rejects before install | sing-box, HAProxy, NAT, Xray, route-engine executor regression tests |
| HAProxy/sing-box restart failure | Previous owned configuration restored | `TestExecutorVerificationFailureRestoresPreviousConfig`, `TestExecutorRestartFailureRollsBackInitialApply`, `TestFragmentExecutorRestartFailureRestoresPreviousAbsence` |
| Lost outbound runtime | Traffic stays fail-closed; manual rollback does not overwrite stale state | privileged lab plus `TestRollbackCommittedRejectsLostHAProxyRuntimeBeforeTransition` and `TestRollbackCommittedRejectsMissingNFTStateBeforeTransition` |
| Dependency failure | Two-sample degraded/unhealthy transition; no automatic mutation | `TestDependencyMonitorReportsDisabledComponentsAndDebouncesRuntimeFailure`, `TestMonitorBoundsProbeTimeout` |
| Concurrent/stale lock | One mutation owner; unsafe metadata fails closed | Linux reliability and app mutation-lock tests |
| Volatile reboot-like loss | Remove owned TUN, nftables table, IPv4/IPv6 rules and routes; reconcile persistent applied files once; second run makes no transaction | privileged network lab routing `--reconcile` checkpoint |
| Emergency bypass | Exact owned routing/interception removed; foreign and persistent state preserved; second call is a no-op | privileged lab plus route-engine bypass tests |

## Verification commands

```sh
go test ./...
go vet ./...
go test -race ./internal/app ./internal/reliability ./internal/routeengine ./internal/ipc ./internal/haproxy ./internal/nat ./internal/singbox ./internal/interfaceoutbound ./internal/xray ./cmd/egressctl
docker build -t egress-manager-network-lab -f tests/network/Dockerfile .
docker run --rm --privileged egress-manager-network-lab
```

> [!important]
> A fresh-container network namespace is strong evidence for loss of volatile kernel state, but it is not evidence of a real init-system boot. Before closing Phase 11, run the same release artifact across an actual reboot of a disposable Linux VM and record the result here.

## Disposable VM reboot gate

1. Apply one sing-box route and one native-interface route with kill switches.
2. Add a foreign nftables table, foreign iptables chain, and foreign policy route marker.
3. Reboot the VM without issuing bypass.
4. Confirm `egressd` completes recovery before accepting mutation.
5. Confirm owned services, interfaces, nftables expressions, and IPv4/IPv6 policy state exactly match persisted applied state.
6. Confirm foreign state is unchanged and client traffic cannot escape directly while an outbound is unavailable.
7. Run `egressctl recover` twice; the second run must create no transaction or native mutation.
8. Save only sanitized status and test results; never archive journal ciphertext, credentials, or raw configuration.
