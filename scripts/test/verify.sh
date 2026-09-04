#!/usr/bin/env sh
set -eu

go test ./...
go vet ./...
pnpm --dir web typecheck
pnpm --dir web lint
pnpm --dir web build
