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
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.6/scripts/install/install-secure.sh | sudo sh -s -- --version v0.1.0-alpha.6
```

The secure installer exposes the browser panel directly over HTTPS on the selected high port. It attempts a Let's Encrypt short-lived IP certificate when online and falls back to an IP-matching self-signed certificate when public issuance is unavailable. Fresh interactive installs prompt for an administrator username/password.

Alpha.6 adds a package-safety invariant to offline installation: APT is run with `--no-remove`, so if the bundled dependency set would require removing any existing host package, installation aborts before package mutation. The bundle carries the complete Python 3 standard library instead of only `python3-minimal`.

After installation:

```sh
sudo egress-manager
```

See [scripts/install/README.md](scripts/install/README.md) for direct browser access, verification, administrator provisioning, firewall notes, and fully offline ZIP/tar.gz installation.

Runtime configuration and secure administrator provisioning are documented in [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

Xray, Marzban, and 3x-ui ownership boundaries and operations are documented in [docs/networking/XRAY_INTEGRATION.md](docs/networking/XRAY_INTEGRATION.md).
