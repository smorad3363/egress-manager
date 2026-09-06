# Listener relays

Alpha.9 adds a project-owned listener relay path for traffic that must enter this server on a fixed local port, leave through a selected managed Xray outbound, and reach one fixed destination.

The data path is:

```text
client -> Egress Manager listener -> selected Xray outbound -> fixed destination
```

## Current Xray outbound support

The Alpha.9 Xray relay adapter accepts VLESS share links using REALITY security with XHTTP transport. Imported credentials are encrypted at rest and are not returned by the public API or plan responses.

The project-owned relay runtime is separate from any standalone Xray, Marzban, or 3x-ui installation. Egress Manager does not rewrite or restart those foreign services for listener relays.

## Creating a relay

In the browser console, open **Relays** and create a relay with:

- a stable relay ID and display name;
- a listen IP address and port;
- TCP, UDP, or TCP+UDP as required by the selected outbound;
- one fixed destination host and port;
- one enabled Xray-adapter outbound;
- an optional source-CIDR allow-list.

Save changes first, then use **Review & apply**. The review contains public actions and authenticated state/candidate hashes only. The generated Xray candidate and credentials stay inside `egressd`.

## Safety behavior

Listener relays are fail-closed. If the selected Xray outbound is unavailable, traffic is not sent directly to the destination. The planner also rejects protected management ports, overlapping desired listeners, and collisions with foreign listeners already present on the host.

Runtime changes are applied through the dedicated `egress-manager-xray-relay.service` and the project-owned configuration at `/var/lib/egress-manager/private/xray-relay.json`. Apply operations are journaled and support rollback/recovery.

Existing Egress Manager database, administrator accounts, TLS material, and panel configuration are retained by repeat secure installs.
