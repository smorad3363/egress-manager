#!/usr/bin/env python3
from pathlib import Path

path = Path("scripts/install/install-secure.sh")
text = path.read_text(encoding="utf-8")
old = 'version="${EGRESS_VERSION:-v0.1.0-alpha.7}"'
new = 'version="${EGRESS_VERSION:-v0.1.0-alpha.9}"'
count = text.count(old)
if count != 1:
    raise SystemExit(f"expected one secure installer version default, found {count}")
path.write_text(text.replace(old, new, 1), encoding="utf-8")
