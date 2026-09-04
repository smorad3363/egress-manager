---
title: Network Namespace Lab
tags:
  - egress-manager
  - testing/network
  - phase/1
status: active
---

# Network Namespace Lab

Lab creates three disposable Linux network namespaces inside a privileged test environment:

```text
client (10.203.1.2)
  -> router (10.203.1.1 / 10.203.2.1)
  -> server (10.203.2.2)
```

It verifies TCP and UDP DNAT, masquerade, source-CIDR filtering, routing, nftables counters, owned-rule rollback, foreign-rule preservation, and two consecutive clean runs.

## Docker

```sh
docker build -t egress-manager-network-lab tests/network
docker run --rm --privileged egress-manager-network-lab
```

The nftables rules live only inside the disposable router namespace. Cleanup targets only namespace names created by the current process.

## Host Linux

```sh
sudo tests/network/run.sh
```

Requires `iproute2`, `nftables`, `socat`, `procps`, and `coreutils`.
