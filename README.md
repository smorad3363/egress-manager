# Egress Manager

Lightweight Linux control plane for safe NAT forwarding, HAProxy TCP balancing, proxy egress, and interface/subnet routing.

> [!warning]
> Project is under active development. Do not deploy on production gateways until release gates in [the roadmap](docs/PROJECT_ROADMAP.md) pass.

## Development

Requirements: Go 1.27.1, Node.js 24 LTS, pnpm 11.

```sh
go test ./...
go vet ./...
pnpm --dir web install --frozen-lockfile
pnpm --dir web typecheck
pnpm --dir web lint
pnpm --dir web build
```

Project recovery state lives in `.project/STATE.yaml`.

## Ubuntu installation

Alpha releases support Ubuntu 22.04, 24.04, and 26.04 on amd64 and arm64. Release bundles include the prebuilt browser UI, pinned sing-box and Xray runtimes, lego for HTTPS certificate management, the complete Python 3 runtime needed by the installer/manager, plus per-Ubuntu offline dependency archives; Node/npm/pnpm are not required on the target host.

```sh
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.7/scripts/install/install-secure.sh | sudo sh -s -- --version v0.1.0-alpha.7
```

The secure installer exposes the browser panel directly over HTTPS on the selected high port. It attempts a Let's Encrypt short-lived IP certificate when online and falls back to an IP-matching self-signed certificate when public issuance is unavailable. Fresh interactive installs prompt for an administrator username/password.

Alpha.7 makes installation observable: the one-line secure installer shows named stages and download progress, verifies the release checksum before extraction, detects and reports the server IP, stores a persistent install log under `/var/log/egress-manager/`, and on failure prints the failing stage plus the last 60 log lines. Release checksum sidecars use portable basenames so `sha256sum -c` works directly from the download directory.

Alpha.6 and later preserve existing Ubuntu packages during offline dependency installation: local APT uses `--no-upgrade --no-remove`, so any dependency plan that requires upgrading, downgrading, or removing an existing host package aborts before mutation.

After installation:

```sh
sudo egress-manager
```

See [scripts/install/README.md](scripts/install/README.md) for direct browser access, verification, administrator provisioning, firewall notes, and fully offline ZIP/tar.gz installation.

Runtime configuration and secure administrator provisioning are documented in [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

Xray, Marzban, and 3x-ui ownership boundaries and operations are documented in [docs/networking/XRAY_INTEGRATION.md](docs/networking/XRAY_INTEGRATION.md).
