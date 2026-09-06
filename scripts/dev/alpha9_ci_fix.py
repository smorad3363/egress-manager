#!/usr/bin/env python3
from pathlib import Path


def replace_once(path: str, old: str, new: str) -> None:
    target = Path(path)
    text = target.read_text(encoding="utf-8")
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{path}: expected one match, found {count}: {old!r}")
    target.write_text(text.replace(old, new, 1), encoding="utf-8")


# The secure installer runs the core installer with --skip-start, so it must
# enable the conditional project-owned relay unit alongside the two main units.
replace_once(
    "scripts/install/install-secure.sh",
    'run_logged systemctl enable egressd.service egress-web.service || fail "could not enable Egress Manager services"',
    'run_logged systemctl enable egressd.service egress-web.service egress-manager-xray-relay.service || fail "could not enable Egress Manager services"',
)

# Relays display a deliberately shortened candidate hash; assert the rendered
# public prefix, not the full mocked secret-free hash.
replace_once(
    "web/tests/usability.spec.ts",
    'await expect(dialog.getByText("candidate-hash", { exact: false })).toBeVisible();',
    'await expect(dialog.getByText("candidate-ha…", { exact: true })).toBeVisible();',
)

# Persian already has a canonical translation for Cancel in translations-fa.ts.
replace_once(
    "web/tests/usability.spec.ts",
    'await relayDialog.getByRole("button", { name: "لغو" }).click();',
    'await relayDialog.getByRole("button", { name: "انصراف" }).click();',
)

# Alpha.9 makes ownership explicit: the Outbounds page only applies sing-box.
replace_once(
    "web/tests/design-system.spec.ts",
    'expect(calls).toContain("PUT /api/v1/outbounds");\n  await page.getByRole("button", { name: "Review & apply" }).click();\n  const plan = page.getByRole("dialog");',
    'expect(calls).toContain("PUT /api/v1/outbounds");\n  await page.getByRole("button", { name: "Review sing-box apply" }).click();\n  const plan = page.getByRole("dialog");',
)
replace_once(
    "web/tests/design-system.spec.ts",
    'await expect(plan.getByText("candidate-hash")).toHaveCount(0);\n  await plan.getByRole("button", { name: "Apply atomically" }).click();\n  await expect(plan).toBeHidden();\n  expect(calls).toContain("POST /api/v1/outbounds/apply");',
    'await expect(plan.getByText("candidate-hash")).toHaveCount(0);\n  await plan.getByRole("button", { name: "Apply sing-box" }).click();\n  await expect(plan).toBeHidden();\n  expect(calls).toContain("POST /api/v1/outbounds/apply");',
)

# Make the native relay probe deterministic and diagnostic. Use an explicit
# echo loop instead of copying a socket onto itself, and pin TCP on the local
# VLESS test server to match the client fixture.
replace_once(
    "internal/xrayrelay/native_test.go",
    '''\t\tbackendAccepts.Add(1)\n\t\tgo func() {\n\t\t\tdefer connection.Close()\n\t\t\t_, _ = io.Copy(connection, connection)\n\t\t}()''',
    '''\t\tbackendAccepts.Add(1)\n\t\tgo func() {\n\t\t\tdefer connection.Close()\n\t\t\tbuffer := make([]byte, 32*1024)\n\t\t\tfor {\n\t\t\t\tn, readErr := connection.Read(buffer)\n\t\t\t\tif n > 0 {\n\t\t\t\t\tif _, writeErr := connection.Write(buffer[:n]); writeErr != nil {\n\t\t\t\t\t\treturn\n\t\t\t\t\t}\n\t\t\t\t}\n\t\t\t\tif readErr != nil {\n\t\t\t\t\treturn\n\t\t\t\t}\n\t\t\t}\n\t\t}()''',
)
replace_once(
    "internal/xrayrelay/native_test.go",
    '''\t\t\t"settings": map[string]any{"clients": []any{map[string]any{"id": nativeRelayUUID}}, "decryption": "none"},\n\t\t}},''',
    '''\t\t\t"settings":       map[string]any{"clients": []any{map[string]any{"id": nativeRelayUUID}}, "decryption": "none"},\n\t\t\t"streamSettings": map[string]any{"network": "tcp"},\n\t\t}},''',
)
replace_once(
    "internal/xrayrelay/native_test.go",
    '''\tif _, err := io.ReadFull(connection, received); err != nil {\n\t\t_ = connection.Close()\n\t\tt.Fatalf("read relayed payload: %v", err)\n\t}''',
    '''\tif _, err := io.ReadFull(connection, received); err != nil {\n\t\t_ = connection.Close()\n\t\tt.Fatalf("read relayed payload: %v; backend_accepts=%d; relay_stderr=%q; server_stderr=%q", err, backendAccepts.Load(), relayProcess.stderr.String(), server.stderr.String())\n\t}''',
)
