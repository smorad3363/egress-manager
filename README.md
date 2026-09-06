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

Alpha releases support Ubuntu 22.04, 24.04, and 26.04 on amd64 and arm64. Release bundles include the prebuilt browser UI, pinned sing-box and Xray runtimes, plus per-Ubuntu offline dependency archives; Node/npm/pnpm are not required on the target host.

```sh
curl -fsSL https://raw.githubusercontent.com/smorad3363/egress-manager/v0.1.0-alpha.3/scripts/install/install.sh | sudo sh -s -- --version v0.1.0-alpha.3
```

See [scripts/install/README.md](scripts/install/README.md) for browser access, verification, administrator provisioning, and fully offline ZIP/tar.gz installation. Installation does not change firewall or routing state.

Runtime configuration and secure administrator provisioning are documented in [docs/CONFIGURATION.md](docs/CONFIGURATION.md).

Xray, Marzban, and 3x-ui ownership boundaries and operations are documented in [docs/networking/XRAY_INTEGRATION.md](docs/networking/XRAY_INTEGRATION.md).
