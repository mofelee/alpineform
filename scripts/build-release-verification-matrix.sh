#!/usr/bin/env bash

set -euo pipefail

RESULT_DIR="${1:?usage: build-release-verification-matrix.sh RESULT_DIR OUTPUT}"
OUTPUT="${2:?usage: build-release-verification-matrix.sh RESULT_DIR OUTPUT}"

[[ -d "$RESULT_DIR" ]] || {
  printf 'verification result directory not found: %s\n' "$RESULT_DIR" >&2
  exit 1
}

linux_amd64_installer=failed
darwin_amd64_installer=failed
darwin_arm64_installer=failed
supply_chain=failed
alpine_3_24_x86_64_quickstart=failed
result_count=0

while IFS= read -r -d '' result; do
  result_count=$((result_count + 1))
  while IFS= read -r line || [[ -n "$line" ]]; do
    case "$line" in
      linux_amd64_installer=yes) linux_amd64_installer=yes ;;
      darwin_amd64_installer=yes) darwin_amd64_installer=yes ;;
      darwin_arm64_installer=yes) darwin_arm64_installer=yes ;;
      supply_chain=yes) supply_chain=yes ;;
      alpine_3_24_x86_64_quickstart=yes) alpine_3_24_x86_64_quickstart=yes ;;
      '') ;;
      *)
        printf 'invalid verification result in %s: %s\n' "$result" "$line" >&2
        exit 1
        ;;
    esac
  done < "$result"
done < <(find "$RESULT_DIR" -type f -name '*.env' -print0)

(( result_count > 0 )) || {
  printf 'no verification result files found under %s\n' "$RESULT_DIR" >&2
  exit 1
}

cat > "$OUTPUT" <<EOF
| Verification | linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64 |
| --- | --- | --- | --- | --- |
| Artifact build | yes | yes | yes | yes |
| Installer | ${linux_amd64_installer} | build-only | ${darwin_amd64_installer} | ${darwin_arm64_installer} |
| Supply chain | ${supply_chain} | ${supply_chain} | ${supply_chain} | ${supply_chain} |
| Alpine 3.24 x86_64 quickstart | ${alpine_3_24_x86_64_quickstart} | n/a | n/a | n/a |
EOF

if grep -q failed "$OUTPUT"; then
  cat "$OUTPUT" >&2
  printf 'release verification matrix contains failed checks\n' >&2
  exit 1
fi
