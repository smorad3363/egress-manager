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

It verifies TCP and UDP DNAT, masquerade, source-CIDR filtering, routing, nftables counters, owned-rule rollback, foreign-rule preservation, typed read-only inventory, native interface-outbound imports, WireGuard lifecycle and routed traffic, OpenVPN TLS client traffic, tunnel-loss kill switches, and two consecutive clean runs.

## Docker

```sh
docker build -t egress-manager-network-lab -f tests/network/Dockerfile .
docker run --rm --privileged egress-manager-network-lab
```

The nftables rules, VPN interfaces, generated test certificates, and credentials live only inside the disposable container and namespaces. Cleanup targets only namespace names and temporary paths created by the current process.

> [!note] WSL transport fallback
> Linux WireGuard uses the actual kernel tunnel. On the Microsoft WSL2 kernel, where kernel-created WireGuard UDP sockets are affected by the [known mirrored-network limitation](https://github.com/microsoft/WSL/issues/10842), the lab still validates kernel interface creation, peer configuration, and identity, then uses a namespace-local point-to-point transport for the routing and fail-closed assertions. OpenVPN remains end-to-end on both environments.

## Host Linux

```sh
go build -o /tmp/egress-inventory-probe ./tests/network/inventoryprobe
go build -o /tmp/egress-nat-probe ./tests/network/natprobe
sudo EGRESS_INVENTORY_PROBE=/tmp/egress-inventory-probe EGRESS_NAT_PROBE=/tmp/egress-nat-probe tests/network/run.sh
```

Requires Go 1.27.1, `iproute2`, `iputils-ping`, `iptables`, `nftables`, `socat`, `procps`, `coreutils`, `tcpdump`, `wireguard-tools`, OpenVPN 2.6, and OpenSSL. The Docker image provides the pinned test environment.
