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

The secure bootstrap downloads one complete release bundle for the detected Ubuntu release and CPU architecture. The bundle contains the prebuilt browser panel, Egress Manager binaries, the Bash management utility, pinned sing-box, Xray and lego binaries, and the Ubuntu package dependency closure used by the offline installer.

```sh
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.5/scripts/install/install-secure.sh | sudo sh -s -- --version v0.1.0-alpha.5
```

Node.js, npm, and pnpm are release-build dependencies only. They are deliberately not installed on the target gateway because the React application is compiled before the release bundle is produced.

The secure installer exposes the panel directly on `0.0.0.0` using HTTPS and the existing unprivileged management port. It detects the server IP, attempts a short-lived Let's Encrypt IP certificate when online, and falls back to a self-signed certificate with the server IP in subjectAltName when public issuance is unavailable. No SSH tunnel is required.

On a fresh interactive installation the installer prompts for an administrator username, password, and password confirmation after HTTPS becomes healthy. Password input is read from `/dev/tty`, so the prompt works when the bootstrap itself is executed through `curl | sh`. Use `--skip-admin` for unattended installation or `--admin-user USER` to force an administrator prompt with a preset username.

Existing configuration, IPC key, administrator database, TLS material, and runtime state are retained on repeat installs.

## Shell management

After installation run the interactive management menu:

```sh
sudo egress-manager
```

The menu provides service control, panel URL/status, logs, administrator creation, update, TLS status/renewal, firewall assistance, runtime versions, installation verification, and type-aware editing of every key currently present in `/etc/egress-manager/config.json`.

Direct commands are also available:

```sh
sudo egress-manager status
sudo egress-manager url
sudo egress-manager restart
sudo egress-manager logs
sudo egress-manager admin operator
sudo egress-manager update
sudo egress-manager update v0.1.0-alpha.5
sudo egress-manager config
sudo egress-manager config-show
sudo egress-manager config-get listen_port
sudo egress-manager config-set listen_port 47733
sudo egress-manager config-set protected_management_cidrs '["203.0.113.10/32"]'
sudo egress-manager expose
sudo egress-manager local
sudo egress-manager tls-status
sudo egress-manager tls-renew
sudo egress-manager verify
sudo egress-manager firewall-open
sudo egress-manager versions
```

`egress-manager update` queries GitHub Releases, selects the newest published release including prereleases, runs that release's secure installer, preserves the configured server IP/TLS mode, skips the fresh-install administrator prompt, and verifies the installation after the upgrade.

The generic configuration editor preserves JSON value types, validates important fields such as ports, IP addresses, paths, SSH port arrays and management CIDRs, writes the file atomically, restarts the services, and restores the previous configuration automatically if the new configuration cannot start the services.

`firewall-open` only opens the selected TCP panel port when UFW is installed and currently active. Cloud-provider security groups/firewalls remain outside the installer and must be configured separately if they block the port.

## Administrator and browser panel

Create another administrator at any time:

```sh
sudo egress-manager admin operator2
```

The manager prompts twice for the password without putting it in process arguments.

Show the browser URL:

```sh
egress-manager url
```

The installed panel is opened directly with the printed HTTPS address, for example:

```text
https://SERVER_IP:47733/login
```

A Let's Encrypt IP certificate is browser-trusted. The offline/self-signed fallback encrypts traffic but produces a browser trust warning until that certificate is explicitly trusted.

## Fully offline installation

Every release publishes both ZIP and tar.gz full bundles for each supported Ubuntu release and architecture. For Ubuntu 22.04 amd64, download this asset on an internet-connected machine and copy it to the server:

```text
egress-manager-offline-ubuntu22.04-amd64.zip
```

If Python 3 is present on the offline server, extract and install the ZIP in one shell line:

```sh
rm -rf /tmp/egress-offline && mkdir -p /tmp/egress-offline && python3 -m zipfile -e ./egress-manager-offline-ubuntu22.04-amd64.zip /tmp/egress-offline && sudo sh /tmp/egress-offline/egress-manager-offline-ubuntu22.04-amd64/install.sh
```

A machine too minimal to extract ZIP can use the matching tar.gz bundle, which needs only the standard Ubuntu tar/gzip tools:

```sh
d="$(mktemp -d)" && tar -xzf ./egress-manager-offline-ubuntu22.04-amd64.tar.gz -C "$d" && sudo sh "$d/egress-manager-offline-ubuntu22.04-amd64/install.sh"
```

Offline bundle mode does not contact an external CA by default. It generates a self-signed certificate locally with the detected/provided server IP in SAN. Use `--public-ca` only when the supposedly offline target actually has the network reachability required for ACME.

Bundle mode verifies `MANIFEST.sha256`, installs only the `.deb` files shipped inside the bundle with APT repository access disabled, and does not download Xray, sing-box, npm packages, or other runtime dependencies from the network.

Xray is installed under `/usr/local/lib/egress-manager/bin/xray` for Egress Manager integration and validation. The installer does not enable a standalone Xray service and does not overwrite a foreign Xray, Marzban, or 3x-ui installation.

Verify later with:

```sh
sudo egress-manager verify
```
