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

## Online installation

The online bootstrap downloads one complete release bundle for the detected Ubuntu release and CPU architecture. The bundle contains the prebuilt browser panel, Egress Manager binaries, pinned sing-box and Xray binaries, and the Ubuntu package dependency closure used by the offline installer.

```sh
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.4/scripts/install/install.sh | sudo sh -s -- --version v0.1.0-alpha.4
```

Node.js, npm, and pnpm are release-build dependencies only. They are deliberately not installed on the target gateway because the React application is compiled before the release bundle is produced.

By default the installer exposes the panel directly on `0.0.0.0` over plain HTTP and selects a random free high port. Existing configuration, IPC key, administrator database, and runtime state are retained on repeat installs. Existing alpha configurations bound to `127.0.0.1` are migrated to direct HTTP mode by the default install command.

> [!warning]
> Direct HTTP is intentionally an insecure compatibility mode. Login credentials and session traffic are not encrypted. Use it only on trusted networks or when another network layer protects the connection. The installer does not automatically modify UFW or cloud-provider firewall rules.

To keep the panel localhost-only instead:

```sh
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.4/scripts/install/install.sh | sudo sh -s -- --version v0.1.0-alpha.4 --local-only
```

## Shell management

After installation run the interactive management menu:

```sh
sudo egress-manager
```

Direct commands are also available:

```sh
sudo egress-manager status
sudo egress-manager url
sudo egress-manager restart
sudo egress-manager logs
sudo egress-manager expose
sudo egress-manager local
sudo egress-manager port 47733
sudo egress-manager admin operator
sudo egress-manager verify
sudo egress-manager firewall-open
```

`firewall-open` only opens the selected TCP panel port when UFW is installed and currently active. Cloud-provider security groups/firewalls remain outside the installer and must be configured separately if they block the port.

## Administrator and browser panel

Provision or reset an administrator from the shell manager:

```sh
sudo egress-manager admin operator
```

The manager prompts for the password without putting it in process arguments.

Show the browser URL:

```sh
egress-manager url
```

When direct HTTP mode is enabled, open the printed address directly in a browser, for example:

```text
http://SERVER_IP:47733/login
```

No SSH tunnel is required in direct HTTP mode.

## Fully offline installation

Every release publishes both ZIP and tar.gz full bundles for each supported Ubuntu release and architecture. For Ubuntu 22.04 amd64, download this asset on an internet-connected machine and copy it to the server:

```text
egress-manager-offline-ubuntu22.04-amd64.zip
```

If Python 3 is present on the offline server, extract and install the ZIP in one shell line:

```sh
rm -rf /tmp/egress-offline && mkdir -p /tmp/egress-offline && python3 -m zipfile -e ./egress-manager-offline-ubuntu22.04-amd64.zip /tmp/egress-offline && sudo sh /tmp/egress-offline/egress-manager-offline-ubuntu22.04-amd64/install.sh --public-http
```

A machine too minimal to extract ZIP can use the matching tar.gz bundle, which needs only the standard Ubuntu tar/gzip tools:

```sh
d="$(mktemp -d)" && tar -xzf ./egress-manager-offline-ubuntu22.04-amd64.tar.gz -C "$d" && sudo sh "$d/egress-manager-offline-ubuntu22.04-amd64/install.sh" --public-http
```

Bundle mode verifies `MANIFEST.sha256`, installs only the `.deb` files shipped inside the bundle with APT repository access disabled, and does not download Xray, sing-box, npm packages, or other runtime dependencies from the network.

Xray is installed under `/usr/local/lib/egress-manager/bin/xray` for Egress Manager integration and validation. The installer does not enable a standalone Xray service and does not overwrite a foreign Xray, Marzban, or 3x-ui installation.

Verify later with:

```sh
sudo egress-manager verify
```
