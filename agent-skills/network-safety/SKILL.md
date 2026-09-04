---
name: network-safety
description: Guard every Egress Manager firewall, route, proxy, and interface mutation.
---

# Network Safety

1. Inspect current owned and foreign state.
2. Build a typed plan and durable snapshot.
3. Validate candidates with native validators.
4. Protect SSH, panel port, loopback, and management routes.
5. Apply only namespaced Egress Manager objects.
6. Verify desired behavior and foreign-state preservation.
7. Commit journal state or rollback.

Never run `nft flush ruleset`, `iptables -F`, or mutate foreign objects.
