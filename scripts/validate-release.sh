#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for file in README.md LICENSE NOTICE.md CHANGELOG.md .goreleaser.yaml \
  scripts/install.sh docs/support-matrix.md docs/compatibility-policy.md \
  docs/security-model.md docs/operations-runbook.md docs/release-process.md \
  docs/releases/v0.1.0-alpha.1.md docs/releases/v0.1.0-alpha.2.md; do
  test -s "$ROOT_DIR/$file"
done

bash -n "$ROOT_DIR/scripts/test-install.sh" "$ROOT_DIR/scripts/validate-release.sh"
sh -n "$ROOT_DIR/scripts/install.sh"
cmp "$ROOT_DIR/examples/quickstart.apf.hcl" "$ROOT_DIR/test/release/quickstart/1.apf.hcl"

for example in "$ROOT_DIR"/examples/*.apf.hcl; do
  go run "$ROOT_DIR/cmd/apf" validate -f "$example" >/dev/null
  go run "$ROOT_DIR/cmd/apf" plan --offline -f "$example" --format json >/dev/null
done

python3 - "$ROOT_DIR" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
failed = False
for source in [root / "README.md", root / "SECURITY.md", *sorted((root / "docs").glob("*.md"))]:
    text = source.read_text(encoding="utf-8")
    for target in re.findall(r"\[[^]]*\]\(([^)]+)\)", text):
        path = target.split("#", 1)[0]
        if not path or "://" in path or path.startswith("mailto:"):
            continue
        if not (source.parent / path).resolve().exists():
            print(f"{source.relative_to(root)}: missing link target {target}", file=sys.stderr)
            failed = True
raise SystemExit(failed)
PY
