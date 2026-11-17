#!/usr/bin/env python3
"""Map centralized Go tests onto their original package paths for go -overlay."""
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parent.parent
tests = root / "tests" / "go"
mapping = {}
for source in sorted(tests.rglob("*_test.go")):
    target = root / source.relative_to(tests)
    if target.exists():
        raise SystemExit(f"Refusing to overlay an existing file: {target}")
    if not target.parent.is_dir():
        raise SystemExit(f"Go package directory does not exist: {target.parent}")
    mapping[str(target)] = str(source)
if not mapping:
    raise SystemExit("No Go tests found")
json.dump({"Replace": mapping}, sys.stdout, sort_keys=True)
