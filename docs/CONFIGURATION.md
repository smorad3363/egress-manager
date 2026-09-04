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
  "haproxy_config_path": "/var/lib/egress-manager/haproxy.cfg",
  "haproxy_runtime_socket_path": "/run/egress-manager/haproxy-runtime.sock",
  "haproxy_pid_path": "/run/egress-manager/haproxy.pid",
  "session_cookie_name": "egress_session",
  "ssh_ports": [22],
  "protected_management_cidrs": []
}
```

Non-loopback listeners require absolute `tls_certificate_path` and `tls_private_key_path` values. Session cookies are always `Secure`, `HttpOnly`, and `SameSite=Strict`.

Every active SSH listener and the panel port are excluded from NAT capture. Add canonical local management networks to `protected_management_cidrs`; a forward whose listen address is inside one of these networks is rejected.

The IPC key file contains exactly 32 random bytes encoded as 64 hexadecimal characters. It must be a regular file and must not grant permissions to other users. The intended mode is `0640`; the Unix socket mode is `0660`.

```sh
egressd --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
egress-web --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
```

Provision an administrator without placing the password in process arguments:

```sh
printf '%s\n' "$EGRESS_ADMIN_PASSWORD" | egress-web provision-admin --username operator --password-stdin --config /etc/egress-manager/config.json --ipc-key /etc/egress-manager/ipc.key
```

The installer will generate the key and choose a free, non-protected panel port in Phase 11.
