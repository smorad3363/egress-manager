---
title: Xray, Marzban, and 3x-ui Integration
tags:
  - egress-manager
  - networking
  - xray
  - operations
status: active
---

# Xray, Marzban, and 3x-ui Integration

Egress Manager discovers supported Xray-family installations without changing them. Native route bindings map an existing Xray `inboundTag` directly to an existing Xray `outboundTag`; traffic is not transparently passed through sing-box.

> [!important] Ownership boundary
> Every discovered configuration, panel database, and service is foreign-owned. Egress Manager can write only `/etc/xray/zzzz-egress-manager-routing.json`, and only after the standalone `xray.service` loader boundary has been proven. It never writes Marzban or 3x-ui configuration or databases.

## Discovery

Discovery checks only these fixed locations:

| Kind | Configuration | Service | Mutation |
| --- | --- | --- | --- |
| Standalone Xray | `/etc/xray/config.json` or `/usr/local/etc/xray/config.json` | `xray.service` | Read-only unless the managed-fragment boundary is proven |
| Marzban | `/var/lib/marzban/xray_config.json` | `marzban.service` | Always read-only |
| 3x-ui | `/usr/local/x-ui/bin/config.json` | `x-ui.service` | Always read-only |

Files must be regular, non-symlink JSON files no larger than 256 KiB. Ambiguous candidates, malformed JSON, duplicate or unsafe tags, and unstable reads are rejected.

## Enabling the standalone managed fragment

The loaded `xray.service` must have one simple `ExecStart` that proves an absolute `-confdir` matching the discovered configuration root. It must not include `-config`. A compatible command is:

```ini
ExecStart=/usr/local/bin/xray run -confdir /etc/xray
```

The operator remains responsible for ensuring the service already loads its intended configuration from that directory. Egress Manager does not rewrite the unit or migrate existing configuration.

The directory must contain JSON only. Snapshot limits are 128 files and 1 MiB total. Symlinks, unsupported formats, and foreign files sorting after `zzzz-egress-manager-routing.json` make the installation read-only because Xray merge order could no longer be preserved safely.

## Review and apply

1. Create bindings using tags returned by discovery.
2. Select **Review & apply**.
3. Confirm the public owned-action list. Candidate and foreign configuration contents remain private.
4. Apply the exact reviewed foreign-state, fragment-state, and candidate hashes.

Apply performs native Xray validation in a private temporary confdir, journals authenticated recovery data, rechecks external state, replaces only the owned fragment atomically, restarts `xray.service`, and verifies it is active. Any changed hash makes the review stale and aborts the operation.

> [!warning] Service restart
> A successful mutation restarts `xray.service`. Plan an appropriate maintenance window for active connections.

## Rollback and recovery

If validation, restart, or verification fails, Egress Manager restores or removes only its owned fragment and restarts the service. An interrupted transaction is recovered on daemon startup only when exactly one writable standalone boundary can be rediscovered.

If foreign files changed concurrently, rollback does not overwrite them. Preserve those files, resolve the external change, verify `xray.service`, refresh discovery, and create a new review.

## Limitations

- Marzban and 3x-ui are discovery-only because their lifecycles may regenerate Xray configuration.
- Direct panel database writes and full-file rewrites are unsupported.
- JSONC, YAML, TOML, mixed-format confdirs, and ambiguous loaders are unsupported.
- Only native `inboundTag` to `outboundTag` bindings are managed.
- Egress Manager does not create Xray inbounds or outbounds and does not double-proxy Xray traffic through sing-box.
- Removing the last enabled binding removes the owned fragment after the same review and safety checks.

## Troubleshooting

- **Read only:** inspect the reported limitation; confirm `systemctl show xray.service` exposes exactly one compatible absolute `-confdir` command.
- **Stale review:** another process changed the confdir or owned fragment. Refresh, review again, and do not replace foreign changes.
- **Native validation failed:** validate the existing confdir with the same Xray binary and correct the foreign configuration outside Egress Manager.
- **Restart failed:** inspect `systemctl status xray.service` and the service journal. The owned fragment should already have been rolled back.
- **Recovery refused:** more than one or no writable standalone boundary was discovered. Restore an unambiguous service/configuration boundary before restarting Egress Manager.

## References

- [Xray multiple configuration files](https://xtls.github.io/en/config/features/multiple.html)
- [Xray `run` command and `-confdir`](https://github.com/XTLS/Xray-core/blob/main/main/run.go)
- [Xray environment variables](https://xtls.github.io/en/config/env.html)
- [Marzban Xray configuration defaults](https://github.com/Gozargah/Marzban/blob/master/config.py)
- [3x-ui configuration paths](https://github.com/MHSanaei/3x-ui/blob/main/CONTRIBUTING.md)
