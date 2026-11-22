#!/usr/bin/env python3
"""Map centralized Go tests and test-support sources onto package paths."""
import json
import sys
from pathlib import Path

root = Path(__file__).resolve().parent.parent
tests = root / "tests" / "go"
mapping = {}
for source in sorted(tests.rglob("*.go")):
    relative = source.relative_to(tests)
    is_test = source.name.endswith("_test.go")
    is_support = "testsupport" in relative.parts
    if not is_test and not is_support:
        continue
    target = root / relative
    if target.exists():
        raise SystemExit(f"Refusing to overlay an existing file: {target}")
    if is_test and not target.parent.is_dir():
        raise SystemExit(f"Go package directory does not exist: {target.parent}")
    if is_support:
        # Go's vet tool resolves the package directory before applying the
        # overlay, so the overlaid test-support directory must exist on disk.
        target.parent.mkdir(parents=True, exist_ok=True)
    mapping[str(target)] = str(source)
if not mapping:
    raise SystemExit("No Go tests found")
json.dump({"Replace": mapping}, sys.stdout, sort_keys=True)
