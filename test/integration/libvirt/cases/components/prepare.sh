fixture_source="$ROOT_DIR/test/integration/libvirt/fixtures/components"
fixture_work="$CASE_WORK/component-fixtures"
mkdir -p "$fixture_work/http"
cp "$fixture_source/tool" "$fixture_work/http/tool"
chmod 0755 "$fixture_work/http/tool"
tar --sort=name --mtime='UTC 2020-01-01' --owner=0 --group=0 --numeric-owner \
  -C "$fixture_source/archive-root" -czf "$fixture_work/http/bundle.tar.gz" apf-ci
APF_TOOL_SHA="$(sha256sum "$fixture_work/http/tool" | awk '{print $1}')"
APF_ARCHIVE_SHA="$(sha256sum "$fixture_work/http/bundle.tar.gz" | awk '{print $1}')"
export APF_TOOL_SHA APF_ARCHIVE_SHA
sed -i \
  -e "s/0000000000000000000000000000000000000000000000000000000000000000/$APF_TOOL_SHA/" \
  -e "s/1111111111111111111111111111111111111111111111111111111111111111/$APF_ARCHIVE_SHA/" \
  "$CASE_DIR/1.apf.hcl"
run_remote "create component fixture server directory" "rm -rf /tmp/apf-component-http && mkdir -p /tmp/apf-component-http"
copy_to_vm "$fixture_work/http/tool" /tmp/apf-component-http/tool
copy_to_vm "$fixture_work/http/bundle.tar.gz" /tmp/apf-component-http/bundle.tar.gz
run_remote "start guest-local component fixture server" \
  "nohup python3 -m http.server 18080 --bind 127.0.0.1 --directory /tmp/apf-component-http >/tmp/apf-component-http.log 2>&1 & echo \$! > /tmp/apf-component-http.pid"
assert_remote "component fixture server returns the pinned tool" \
  "test \"\$(wget -qO- http://127.0.0.1:18080/tool | sha256sum | awk '{print \$1}')\" = '$APF_TOOL_SHA'"
