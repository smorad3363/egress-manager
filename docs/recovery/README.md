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
