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

Runtime configuration and secure administrator provisioning are documented in [docs/CONFIGURATION.md](docs/CONFIGURATION.md).
