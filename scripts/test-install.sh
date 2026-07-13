#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/alpineform-installer-test.XXXXXX")"
VERSION="v0.1.0-installer-test"
ARTIFACT="apf_${VERSION}_linux_amd64.tar.gz"
SERVER_PID=""

cleanup() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

mkdir -p "$WORK/archive/docs" "$WORK/archive/examples" "$WORK/release"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags \
    "-X github.com/mofelee/alpineform/internal/version.Version=$VERSION -X github.com/mofelee/alpineform/internal/version.Commit=installer-test -X github.com/mofelee/alpineform/internal/version.Date=2026-07-13T00:00:00Z" \
    -o "$WORK/archive/apf" ./cmd/apf
)
cp "$ROOT_DIR/README.md" "$ROOT_DIR/LICENSE" "$ROOT_DIR/NOTICE.md" "$ROOT_DIR/CHANGELOG.md" "$WORK/archive/"
cp "$ROOT_DIR/examples/quickstart.apf.hcl" "$WORK/archive/examples/"
cp "$ROOT_DIR/docs/compatibility-policy.md" "$WORK/archive/docs/"
tar -C "$WORK/archive" -czf "$WORK/release/$ARTIFACT" .
(
  cd "$WORK/release"
  sha256sum "$ARTIFACT" >checksums.txt
)

APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/prefix" --os linux --arch amd64
"$WORK/prefix/bin/apf" version | grep -Fq "$VERSION"
test -f "$WORK/prefix/share/alpineform/docs/compatibility-policy.md"
test -f "$WORK/prefix/share/alpineform/examples/quickstart.apf.hcl"

APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/prefix" --os linux --arch amd64 |
  grep -Fq "already installed"
APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/dry-run" --os darwin --arch arm64 --dry-run |
  grep -Fq "apf_${VERSION}_darwin_arm64.tar.gz"

cp "$WORK/release/$ARTIFACT" "$WORK/release/$ARTIFACT.valid"
printf 'corrupt\n' >>"$WORK/release/$ARTIFACT"
if APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/corrupt" --os linux --arch amd64 >/dev/null 2>&1; then
  printf 'installer accepted an archive with a mismatched checksum\n' >&2
  exit 1
fi
mv "$WORK/release/$ARTIFACT.valid" "$WORK/release/$ARTIFACT"

python3 - "$WORK/release" "$WORK/server-port" "$VERSION" "$ARTIFACT" <<'PY' &
import http.server
import json
import pathlib
import sys

release_dir = pathlib.Path(sys.argv[1])
port_file = pathlib.Path(sys.argv[2])
version = sys.argv[3]
artifact = sys.argv[4]


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.headers.get("Authorization") != "Bearer installer-token":
            self.send_error(401)
            return
        if self.path == f"/repos/mofelee/alpineform/releases/tags/{version}":
            base = f"http://127.0.0.1:{self.server.server_port}/releases/assets"
            body = json.dumps(
                {
                    "assets": [
                        {"url": f"{base}/archive", "name": artifact},
                        {"url": f"{base}/checksums", "name": "checksums.txt"},
                    ]
                },
                indent=2,
            ).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
        elif self.path == "/releases/assets/archive":
            body = (release_dir / artifact).read_bytes()
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
        elif self.path == "/releases/assets/checksums":
            body = (release_dir / "checksums.txt").read_bytes()
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
        else:
            self.send_error(404)
            return
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
port_file.write_text(str(server.server_port), encoding="ascii")
server.serve_forever()
PY
SERVER_PID=$!
for _ in {1..50}; do
  [[ ! -s "$WORK/server-port" ]] || break
  sleep 0.1
done
[[ -s "$WORK/server-port" ]]
server_port="$(<"$WORK/server-port")"
APF_GITHUB_API_BASE_URL="http://127.0.0.1:${server_port}/repos/mofelee/alpineform" \
  GITHUB_TOKEN=installer-token "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/private-prefix" --os linux --arch amd64
"$WORK/private-prefix/bin/apf" version | grep -Fq "$VERSION"

printf 'installer tests passed\n'
