---
title: Recovery Notes
tags:
  - egress-manager
  - recovery
status: active
---

# Recovery Notes

Session recovery uses `.project/STATE.yaml` plus Git. Runtime recovery uses the durable transaction journal specified in [[PROJECT_ROADMAP#18. RUNTIME TRANSACTION JOURNAL]].

## Dependency monitoring

`egressd` runs read-only dependency probes every 30 seconds with a five-second bound per probe. Recovery status reports separate executable, configuration, process, transport, and internet evidence for system tools, HAProxy, sing-box, native interfaces, routing, and writable Xray.

Raw command errors are never returned through IPC. One failed sample publishes `degraded`; two consecutive failures publish `unhealthy`. An unhealthy dependency requires two successful samples to return to `healthy`. Monitoring never calls reconciliation or another mutation path.

## Emergency bypass

`egressctl bypass` uses authenticated IPC and the global host-mutation lock. It snapshots authenticated route-engine state, validates the nftables deletion, removes only exact protocol-`242` routes/rules from recorded tables and the `inet egm_egress` table, verifies absence, and commits an auditable journal entry. Failure restores the previous owned runtime in leak-safe order.

The persistent `bypass_state_path` marker prevents daemon startup from silently restoring interception. An active bypass reports routing health as `disabled`. Repeating bypass performs no mutation. `egressctl recover` is the deliberate resume action: it removes the marker and reconciles the last applied files, never pending database edits. NAT, HAProxy, sing-box/native services, Xray, panel configuration, and foreign firewall/routing state are outside bypass scope. See [[../adr/0004-persistent-emergency-bypass|ADR 0004]].
