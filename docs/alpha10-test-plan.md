# Alpha.10 release test plan

Before release:

1. `go test ./...`
2. `go vet ./...`
3. Native listener-relay validation against pinned Xray 26.7.28.
4. Frontend typecheck, lint, production build, and Playwright suite.
5. Ubuntu 22.04, 24.04, and 26.04 secure/offline installation checks.
6. Verify `/usr/local/lib/egress-manager/VERSION` exists after service start and matches the build version.
7. Verify `egress-manager-xray-relay.service` uses only `/usr/local/lib/egress-manager/bin/xray` and `/var/lib/egress-manager/private/xray-relay.json`.
8. Verify production foreign-Xray discovery has an empty candidate catalog and performs no filesystem or service probes.
9. Repeat secure install and verify administrator/database/TLS preservation.
10. Confirm package-preservation check reports no removed pre-existing Ubuntu packages.
