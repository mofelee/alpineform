source "$CASE_DIR/assertions.sh"

APF_COMPONENT_STDERR_LOG="$LOG_DIR/components.apf.stderr.log"
APF_COMPONENT_STDERR_LEAK="$CASE_WORK/components-apf.stderr.leak"
: >"$APF_COMPONENT_STDERR_LOG"
apf() {
  local stderr_file="$CASE_WORK/components-apf.stderr.tmp" status=0
  HOME="$APF_HOME" APF_SSH_CONFIG="$APF_HOME/.ssh/config" "$APF_BIN" "$@" 2>"$stderr_file" || status=$?
  if ! assert_no_protected_file "$stderr_file" "AlpineForm stderr"; then
    : >"$APF_COMPONENT_STDERR_LEAK"
    status=1
  fi
  cat "$stderr_file" >&2
  cat "$stderr_file" >>"$APF_COMPONENT_STDERR_LOG"
  rm -f "$stderr_file"
  return "$status"
}

fixture_source="$ROOT_DIR/test/integration/libvirt/fixtures/components"
fixture_work="$CASE_WORK/component-fixtures"
fixture_http="$fixture_work/http"
mirror_a="$fixture_http/mirror-a"
mkdir -p "$mirror_a" "$fixture_work/archive-amd64" "$fixture_work/archive-arm64/apf-ci/bin" "$fixture_work/archive-arm64/apf-ci/share"

cp "$fixture_source/tool" "$fixture_http/tool"
chmod 0755 "$fixture_http/tool"
tar --sort=name --mtime='UTC 2020-01-01' --owner=0 --group=0 --numeric-owner \
  -C "$fixture_source/archive-root" -czf "$fixture_http/bundle.tar.gz" apf-ci

cp "$fixture_source/tool" "$mirror_a/tool-amd64"
printf '\n# protected artifact fixture\n' >>"$mirror_a/tool-amd64"
chmod 0755 "$mirror_a/tool-amd64"
printf '#!/bin/sh\nprintf '\''wrong-architecture-arm64\\n'\''\n' >"$mirror_a/tool-arm64"
chmod 0755 "$mirror_a/tool-arm64"
printf 'mode=protected-artifact-inputs\n' >"$mirror_a/component.conf"
cp "$CASE_DIR/fixtures/root.crt" "$mirror_a/root.crt"
cp -a "$fixture_source/archive-root/apf-ci" "$fixture_work/archive-amd64/apf-ci"
printf 'protected=true\n' >"$fixture_work/archive-amd64/apf-ci/share/protected.txt"
tar --sort=name --mtime='UTC 2020-01-01' --owner=0 --group=0 --numeric-owner \
  -C "$fixture_work/archive-amd64" -czf "$mirror_a/bundle-amd64.tar.gz" apf-ci
printf 'Wrong architecture archive fixture\n' >"$fixture_work/archive-arm64/apf-ci/bin/message.txt"
printf 'architecture=arm64\nlibc=musl\n' >"$fixture_work/archive-arm64/apf-ci/share/platform.txt"
tar --sort=name --mtime='UTC 2020-01-01' --owner=0 --group=0 --numeric-owner \
  -C "$fixture_work/archive-arm64" -czf "$mirror_a/bundle-arm64.tar.gz" apf-ci

APF_LEGACY_TOOL_SHA="$(sha256sum "$fixture_http/tool" | awk '{print $1}')"
APF_LEGACY_ARCHIVE_SHA="$(sha256sum "$fixture_http/bundle.tar.gz" | awk '{print $1}')"
APF_TOOL_SHA="$(sha256sum "$mirror_a/tool-amd64" | awk '{print $1}')"
APF_TOOL_ARM64_SHA="$(sha256sum "$mirror_a/tool-arm64" | awk '{print $1}')"
APF_FILE_SHA="$(sha256sum "$mirror_a/component.conf" | awk '{print $1}')"
APF_ARCHIVE_SHA="$(sha256sum "$mirror_a/bundle-amd64.tar.gz" | awk '{print $1}')"
APF_ARCHIVE_ARM64_SHA="$(sha256sum "$mirror_a/bundle-arm64.tar.gz" | awk '{print $1}')"
APF_CA_SHA="$(sha256sum "$mirror_a/root.crt" | awk '{print $1}')"
APF_WRONG_SHA=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff
APF_CA_PROBE="$(sed -n '2p' "$mirror_a/root.crt")"
export APF_LEGACY_TOOL_SHA APF_LEGACY_ARCHIVE_SHA APF_TOOL_SHA APF_TOOL_ARM64_SHA
export APF_FILE_SHA APF_ARCHIVE_SHA APF_ARCHIVE_ARM64_SHA APF_CA_SHA
export APF_WRONG_SHA APF_CA_PROBE
assert_local "literal and protected fixture digests remain distinct" \
  test "$APF_LEGACY_TOOL_SHA" != "$APF_TOOL_SHA" -a "$APF_LEGACY_ARCHIVE_SHA" != "$APF_ARCHIVE_SHA"

for config in "$CASE_DIR/1.apf.hcl" "$CASE_DIR/2.apf.hcl"; do
  sed -i \
    -e "s/6666666666666666666666666666666666666666666666666666666666666666/$APF_LEGACY_TOOL_SHA/g" \
    -e "s/7777777777777777777777777777777777777777777777777777777777777777/$APF_LEGACY_ARCHIVE_SHA/g" \
    -e "s/0000000000000000000000000000000000000000000000000000000000000000/$APF_TOOL_SHA/g" \
    -e "s/1111111111111111111111111111111111111111111111111111111111111111/$APF_TOOL_ARM64_SHA/g" \
    -e "s/2222222222222222222222222222222222222222222222222222222222222222/$APF_FILE_SHA/g" \
    -e "s/3333333333333333333333333333333333333333333333333333333333333333/$APF_ARCHIVE_SHA/g" \
    -e "s/4444444444444444444444444444444444444444444444444444444444444444/$APF_ARCHIVE_ARM64_SHA/g" \
    -e "s/5555555555555555555555555555555555555555555555555555555555555555/$APF_CA_SHA/g" \
    "$config"
done
cp "$CASE_DIR/1.apf.hcl" "$CASE_DIR/wrong-checksum.apf.hcl"
sed -i "s/$APF_TOOL_SHA/$APF_WRONG_SHA/g" "$CASE_DIR/wrong-checksum.apf.hcl"

APF_MIRROR_A=http://127.0.0.1:18080/mirror-a
APF_MIRROR_B=http://127.0.0.1:18080/mirror-b
APF_QUERY_A=alpineform-ci-component-query-a-sentinel
APF_QUERY_B=alpineform-ci-component-query-b-sentinel
APF_PROTECTED_VALUES=(
  "$APF_MIRROR_A"
  "$APF_MIRROR_B"
  "$APF_QUERY_A"
  "$APF_QUERY_B"
  "$APF_MIRROR_A/tool-amd64?token=$APF_QUERY_A"
  "$APF_MIRROR_A/tool-arm64?token=$APF_QUERY_A"
  "$APF_MIRROR_A/component.conf?token=$APF_QUERY_A"
  "$APF_MIRROR_A/bundle-amd64.tar.gz?token=$APF_QUERY_A"
  "$APF_MIRROR_A/bundle-arm64.tar.gz?token=$APF_QUERY_A"
  "$APF_MIRROR_A/root.crt?token=$APF_QUERY_A"
  "$APF_MIRROR_B/tool-amd64?token=$APF_QUERY_B"
  "$APF_MIRROR_B/tool-arm64?token=$APF_QUERY_B"
  "$APF_MIRROR_B/component.conf?token=$APF_QUERY_B"
  "$APF_MIRROR_B/bundle-amd64.tar.gz?token=$APF_QUERY_B"
  "$APF_MIRROR_B/bundle-arm64.tar.gz?token=$APF_QUERY_B"
  "$APF_MIRROR_B/root.crt?token=$APF_QUERY_B"
  "$APF_TOOL_SHA"
  "$APF_TOOL_ARM64_SHA"
  "$APF_FILE_SHA"
  "$APF_ARCHIVE_SHA"
  "$APF_ARCHIVE_ARM64_SHA"
  "$APF_CA_SHA"
  "$APF_WRONG_SHA"
)
APF_PROTECTED_VALUE_DIGESTS=()
for protected_value in "${APF_PROTECTED_VALUES[@]}"; do
  APF_PROTECTED_VALUE_DIGESTS+=("$(printf %s "$protected_value" | sha256sum | awk '{print $1}')")
done

assert_remote "fresh target has no component fixture ownership" \
  "test ! -e /usr/local/bin/apf-ci-tool && test ! -e /etc/apf-ci-component.conf && test ! -e /opt/apf-ci-bundle && test ! -e /var/lib/alpineform/component-ci-triggers && test ! -e /var/lib/alpineform/component-ci-runs && test ! -e /usr/local/bin/apf-protected-tool && test ! -e /etc/alpineform-protected.conf && test ! -e /opt/alpineform-protected && test ! -e /usr/local/share/ca-certificates/alpineform-protected-root.crt && test ! -e /var/cache/alpineform/components && test ! -e /var/lib/alpineform/ca-certificates && test ! -e /var/tmp/apf-component-http"
run_remote "create literal and protected component fixture directories" \
  "mkdir -p /var/tmp/apf-component-http/mirror-a /var/tmp/apf-component-http/mirror-b"
copy_to_vm "$fixture_http/tool" /var/tmp/apf-component-http/tool
copy_to_vm "$fixture_http/bundle.tar.gz" /var/tmp/apf-component-http/bundle.tar.gz
for fixture in tool-amd64 tool-arm64 component.conf bundle-amd64.tar.gz bundle-arm64.tar.gz root.crt; do
  copy_to_vm "$mirror_a/$fixture" "/var/tmp/apf-component-http/mirror-a/$fixture"
done
copy_to_vm "$CASE_DIR/fixture-server.py" /var/tmp/apf-component-http/fixture-server.py
run_remote "create byte-identical mirror B fixture content" \
  "cp -a /var/tmp/apf-component-http/mirror-a/. /var/tmp/apf-component-http/mirror-b/"
run_remote "start query-sanitizing protected component fixture server" \
  "nohup python3 /var/tmp/apf-component-http/fixture-server.py --bind 127.0.0.1 --port 18080 --directory /var/tmp/apf-component-http --log /var/tmp/apf-component-http/requests.log >/var/tmp/apf-component-http/server.out 2>&1 & echo \$! > /var/tmp/apf-component-http/server.pid"
assert_remote "component fixture server returns the pinned literal and protected tools" \
  "attempt=0; until test \"\$(wget -qO- 'http://127.0.0.1:18080/mirror-a/tool-amd64?readiness=1' | sha256sum | awk '{print \$1}')\" = '$APF_TOOL_SHA'; do attempt=\$((attempt + 1)); test \"\$attempt\" -lt 20; sleep 1; done; test \"\$(wget -qO- http://127.0.0.1:18080/tool | sha256sum | awk '{print \$1}')\" = '$APF_LEGACY_TOOL_SHA'"
run_remote "clear the public fixture readiness request" ": > /var/tmp/apf-component-http/requests.log"

apf validate -f "$CASE_DIR/wrong-checksum.apf.hcl" | tee "$LOG_DIR/1.wrong-checksum.validate.log"
apf plan --offline -f "$CASE_DIR/1.apf.hcl" --format text --html "$LOG_DIR/1.explicit-offline-plan.html" --color never \
  | tee "$LOG_DIR/1.explicit-offline-plan.txt"
apf plan -f "$CASE_DIR/1.apf.hcl" --format text --html "$LOG_DIR/1.explicit-online-plan.html" --color never \
  | tee "$LOG_DIR/1.explicit-online-plan.txt"
assert_no_protected_logs
