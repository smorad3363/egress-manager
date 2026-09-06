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
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.3/scripts/install/install.sh | sudo sh -s -- --version v0.1.0-alpha.3
```

Node.js, npm, and pnpm are release-build dependencies only. They are deliberately not installed on the target gateway because the React application is compiled before the release bundle is produced.

The installer keeps the API/panel on `127.0.0.1` and chooses a random free port. It never changes firewall or routing state during installation. Existing configuration, IPC key, and database are retained on repeat runs.

## Administrator and browser panel

Provision the first administrator without exposing the password in process arguments:

```sh
sudo -v
read -rsp "Admin password: " EGRESS_ADMIN_PASSWORD; echo
printf '%s\n' "$EGRESS_ADMIN_PASSWORD" | sudo /usr/local/lib/egress-manager/bin/egress-web provision-admin --username operator --password-stdin
unset EGRESS_ADMIN_PASSWORD
```

Read the selected panel port:

```sh
sudo sed -n 's/^[[:space:]]*"listen_port":[[:space:]]*\([0-9][0-9]*\),*$/\1/p' /etc/egress-manager/config.json
```

Keep the panel private and forward the selected port over SSH, for example when the port is `47733`:

```sh
ssh -L 47733:127.0.0.1:47733 root@SERVER_IP
```

Then open `http://localhost:47733/login` in the local browser.

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

Bundle mode verifies `MANIFEST.sha256`, installs only the `.deb` files shipped inside the bundle with APT repository access disabled, and does not download Xray, sing-box, npm packages, or other runtime dependencies from the network.

Xray is installed under `/usr/local/lib/egress-manager/bin/xray` for Egress Manager integration and validation. The installer does not enable a standalone Xray service and does not overwrite a foreign Xray, Marzban, or 3x-ui installation.

Verify later with:

```sh
sudo /usr/local/lib/egress-manager/verify.sh
```
