---
title: Linux Installation
tags:
  - egress-manager
  - operations
  - installation
status: alpha
---

# Linux Installation

Supported hosts: Ubuntu 22.04, 24.04, and 26.04 on amd64 or arm64 with systemd.

```sh
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.2/scripts/install/install.sh | sudo sh -s -- --version v0.1.0-alpha.2
```

The installer binds the API to `127.0.0.1` on a random free port. It never changes firewall or routing state during installation. Existing configuration, IPC key, and database are retained on repeat runs.

Provision the first administrator without exposing the password in process arguments:

```sh
sudo -v
read -rsp "Admin password: " EGRESS_ADMIN_PASSWORD; echo
printf '%s\n' "$EGRESS_ADMIN_PASSWORD" | sudo /usr/local/lib/egress-manager/bin/egress-web provision-admin --username operator --password-stdin
unset EGRESS_ADMIN_PASSWORD
```

Verify later with:

```sh
sudo /usr/local/lib/egress-manager/verify.sh
```

> [!warning]
> This is an alpha release. The backend and safety engines are implemented, but the browser UI is not yet bundled into the API service.
