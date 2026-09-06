---
title: Runtime Configuration
tags:
  - egress-manager
  - operations
  - configuration
status: active
related:
  - "[[PROJECT_ROADMAP]]"
  - "[[SECURITY]]"
---

# Runtime Configuration

Both services read the same strict JSON configuration and IPC shared key. Unknown JSON fields are rejected.

```json
{
  "listen_address": "127.0.0.1",
  "listen_port": 43127,
  "data_directory": "/var/lib/egress-manager",
  "database_path": "/var/lib/egress-manager/egress-manager.db",
  "control_socket_path": "/run/egress-manager/egressd.sock",
  "operation_lock_path": "/var/lib/egress-manager/operation.lock",
  "haproxy_config_path": "/var/lib/egress-manager/haproxy.cfg",
  "haproxy_runtime_socket_path": "/run/egress-manager/haproxy-runtime.sock",
  "haproxy_pid_path": "/run/egress-manager/haproxy.pid",
  "sing_box_config_path": "/var/lib/egress-manager/sing-box.json",
  "routing_state_path": "/var/lib/egress-manager/routing.json",
  "bypass_state_path": "/var/lib/egress-manager/bypass.json",
  "interface_state_path": "/run/egress-manager/interface-outbounds/state.json",
  "interface_runtime_directory": "/run/egress-manager/interface-outbounds",
  "session_cookie_name": "egress_session",
  "ssh_ports": [22],
  "protected_management_cidrs": []
}
```

Non-loopback listeners require absolute `tls_certificate_path` and `tls_private_key_path` values. Session cookies are always `Secure`, `HttpOnly`, and `SameSite=Strict`.

Native WireGuard and OpenVPN runtime profiles are materialized with mode `0600` only below `interface_runtime_directory`; keep this directory on `/run` or another tmpfs. `interface_state_path` is secret-free but intentionally shares the volatile runtime boundary so desired encrypted database state is reconciled after reboot.

`operation_lock_path` is the mode-`0600` OS-backed global host-mutation lock. Keep it in the private persistent data directory. Existing configurations that omit it default to `<data_directory>/operation.lock`.

`bypass_state_path` is a secret-free persistent emergency marker. Existing configurations that omit it default to `<data_directory>/bypass.json`. Keep it private, absolute, and distinct from every other runtime path.

Every active SSH listener and the panel port are excluded from NAT capture. Add canonical local management networks to `protected_management_cidrs`; a forward whose listen address is inside one of these networks is rejected.

The IPC key file contains exactly 32 random bytes encoded as 64 hexadecimal characters. It must be a regular file and must not grant permissions to other users. The intended mode is `0640`; the Unix socket mode is `0660`.

```sh
egressd --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
egress-web --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
egressctl status --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
egressctl recover --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
egressctl bypass --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
```

`egressctl status --json` returns secret-free recovery, bypass, unfinished-journal, and global-lock metadata. It exits with code `3` when manual recovery is required. `egressctl recover` runs the ordered recovery coordinator and deliberately exits an active bypass by restoring the last applied routing state. `egressctl bypass` removes only protocol-`242` policy routes/rules recorded in the applied route state and the `inet egm_egress` table. NAT tables, services, desired state, and foreign network objects remain untouched. Repeating bypass is a verified no-op. Mutating commands exit with code `4` on lock contention and code `5` without local administrator privilege. All three commands support `--json`.

If subsystem startup recovery fails, the daemon remains available in degraded mode for authenticated inspection and `egressctl recover`. Ordinary apply operations are rejected with `recovery_required` until recovery succeeds and no unfinished transaction remains. `status --json` includes the latest sanitized recovery report. Authentication, database, configuration, or lock initialization failures remain fatal.

Route recovery replays the last applied routing and sing-box files, not pending route/outbound edits in the database. Keep these applied files on persistent storage. Complete IPv4/IPv6 inventory and current management-path checks are required. Foreign collisions or missing applied files leave recovery degraded; no replacement policy is guessed. Native-interface profiles continue to be reconstructed from encrypted desired state under the private runtime directory.

Provision an administrator without placing the password in process arguments:

```sh
printf '%s\n' "$EGRESS_ADMIN_PASSWORD" | egress-web provision-admin --username operator --password-stdin --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
```

The installer will generate the key and choose a free, non-protected panel port in Phase 11.
