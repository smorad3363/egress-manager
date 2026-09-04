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

It verifies TCP and UDP DNAT, masquerade, source-CIDR filtering, routing, nftables counters, owned-rule rollback, foreign-rule preservation, typed read-only inventory, and two consecutive clean runs.

## Docker

```sh
docker build -t egress-manager-network-lab -f tests/network/Dockerfile .
docker run --rm --privileged egress-manager-network-lab
```

The nftables rules live only inside the disposable router namespace. Cleanup targets only namespace names created by the current process.

## Host Linux

```sh
go build -o /tmp/egress-inventory-probe ./tests/network/inventoryprobe
go build -o /tmp/egress-nat-probe ./tests/network/natprobe
sudo EGRESS_INVENTORY_PROBE=/tmp/egress-inventory-probe EGRESS_NAT_PROBE=/tmp/egress-nat-probe tests/network/run.sh
```

Requires Go 1.27.1, `iproute2`, `iptables`, `nftables`, `socat`, `procps`, and `coreutils`.
