from pathlib import Path

path = Path("web/tests/design-system.spec.ts")
text = path.read_text()
text = text.replace('getByRole("button")).toHaveCount(7)', 'getByRole("button")).toHaveCount(8)')
relay_mock = '  await page.route("**/api/v1/relays?limit=100", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [] }) }));\n'
anchor = '  await page.route("**/api/v1/port-forwards?limit=100", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ items: [] }) }));\n'
if relay_mock not in text:
    if anchor not in text:
        raise SystemExit("design-system dashboard mock anchor not found")
    text = text.replace(anchor, relay_mock + anchor, 1)
path.write_text(text)
