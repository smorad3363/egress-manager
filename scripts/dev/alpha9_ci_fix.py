#!/usr/bin/env python3
from pathlib import Path

path = Path("web/tests/usability.spec.ts")
text = path.read_text(encoding="utf-8")
old = 'await expect(page.getByText("رله Xray")).toBeVisible();'
new = 'await expect(page.getByText("رله Xray", { exact: true })).toBeVisible();'
count = text.count(old)
if count != 1:
    raise SystemExit(f"expected one Persian relay assertion, found {count}")
path.write_text(text.replace(old, new, 1), encoding="utf-8")
