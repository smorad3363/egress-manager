# Alpha.10: project-owned Xray only

## Goal

Egress Manager must manage only its own Xray runtime and must remain operationally independent from every other proxy panel installed on the host.

## Required behavior

1. Production Xray UI shows only Egress Manager managed outbounds, listener relays, owned runtime paths, and owned service state.
2. Foreign standalone Xray, Marzban, 3x-ui/Sanaei, and other proxy-panel configuration is not discovered during normal operation.
3. Egress Manager never writes foreign Xray configuration directories and never restarts or stops foreign services or containers.
4. Host listener inventory may reject an occupied port without modifying the process that owns it.
5. Managed listener relays continue to use `/usr/local/lib/egress-manager/bin/xray`, `/var/lib/egress-manager/private/xray-relay.json`, and `egress-manager-xray-relay.service` only.
6. Relay apply remains atomic, recoverable, and fail-closed.
7. Secure reinstall preserves Egress Manager state and leaves foreign proxy-panel state untouched.
8. `/usr/local/lib/egress-manager/VERSION` is present after a normal installation and accurately identifies the installed release.

## Acceptance

- Go tests prove production discovery performs no foreign file or service probes.
- Browser tests prove the Xray page contains project-owned runtime data and contains no foreign-panel discovery controls or names.
- Ubuntu install CI verifies the installed version marker and the owned relay systemd unit.
- Release assets pass the existing package-preservation and repeat-install checks.
