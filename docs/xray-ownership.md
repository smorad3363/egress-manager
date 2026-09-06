# Project-owned Xray boundary

Egress Manager is self-contained. Its managed Xray runtime belongs only to Egress Manager and is intentionally isolated from every other proxy panel or standalone Xray installation on the same host.

## Owned resources

Egress Manager may use only these Xray resources:

- binary: `/usr/local/lib/egress-manager/bin/xray`
- private relay configuration: `/var/lib/egress-manager/private/xray-relay.json`
- service: `egress-manager-xray-relay.service`
- Egress Manager database records for managed Xray outbounds and listener relays

The owned service starts only when an Egress Manager relay candidate exists.

## Explicit non-goals

Production code must not discover, read, rewrite, restart, stop, or otherwise integrate with:

- `/etc/xray` or `/usr/local/etc/xray`
- Marzban files, services, or containers
- 3x-ui / Sanaei files, services, databases, or containers
- any other proxy panel or standalone Xray runtime

The host listener inventory may observe that a port is already occupied. This is used only to reject unsafe listener collisions; Egress Manager must not mutate the process that owns the socket.

## Data path

Managed listener relays use only the project-owned runtime:

```text
client
  -> Egress Manager listener
  -> selected Egress Manager Xray outbound
  -> fixed destination
```

Relay apply is atomic and fail-closed. If the selected outbound cannot be used, Egress Manager does not silently bypass it with a direct connection.

## Upgrade rule

Repeat secure installs preserve Egress Manager's own database, administrator accounts, TLS material, IPC key, and managed runtime state. Foreign proxy-panel files and services are outside the upgrade boundary and must remain untouched.
