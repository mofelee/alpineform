source "$CASE_DIR/assertions.sh"

APF_MOVED_WORKER_SHA="$(sha256sum "$CASE_DIR/fixtures/worker" | awk '{print $1}')"
APF_MOVED_BUILDER_V1_SHA="$(sha256sum "$CASE_DIR/fixtures/builder-v1.c" | awk '{print $1}')"
APF_MOVED_BUILDER_V2_SHA="$(sha256sum "$CASE_DIR/fixtures/builder-v2.c" | awk '{print $1}')"
APF_MOVED_VERIFY_SHA="$(sha256sum "$CASE_DIR/fixtures/verify-env.sh" | awk '{print $1}')"
APF_MOVED_BUILDER_OWNER="$(printf %s host.cihost.component.legacy_builder | sha256sum | awk '{print substr($1, 1, 32)}')"
APF_MOVED_CURRENT_BUILDER_OWNER="$(printf %s host.cihost.component.builder | sha256sum | awk '{print substr($1, 1, 32)}')"
export APF_MOVED_WORKER_SHA APF_MOVED_BUILDER_V1_SHA APF_MOVED_BUILDER_V2_SHA APF_MOVED_VERIFY_SHA
export APF_MOVED_BUILDER_OWNER APF_MOVED_CURRENT_BUILDER_OWNER

test "$APF_MOVED_WORKER_SHA" = 34f2f6e93348efd93f6cf4f422cae7ce9acb6d4d988021a0ff952423f5785817
test "$APF_MOVED_BUILDER_V1_SHA" = 198d67880171dae77829c260a76df1444064478d17d1bac20b98421e1ee93c8a
test "$APF_MOVED_BUILDER_V2_SHA" = beb6f914afb6f037b39f84bf64230f06486a1432773c3a1af70103212a82cc92
test "$APF_MOVED_VERIFY_SHA" = 2e603c628f40de312db74e6043c9512ca2e16c51176edb843c3f9ad15817999f

assert_remote "fresh target has no component-moved package or account ownership" \
  "! apk info -e jq >/dev/null 2>&1 && ! getent passwd apfmoved >/dev/null && ! getent passwd 2401 >/dev/null && ! getent group apfmoved >/dev/null && ! getent group 2401 >/dev/null && test ! -e /var/lib/alpineform-moved/home"
assert_remote "fresh target preserves Alpine OpenSSH privilege-separation ownership" \
  "test \"\$(stat -c '%U:%G' /var/empty)\" = root:root && permissions=\$(stat -c '%a' /var/empty) && test \"\$((0\$permissions & 022))\" -eq 0"
assert_remote "fresh target has no component-moved service, paths, caches, or state" \
  "test ! -e /usr/local/bin/apf-moved-worker && test ! -e /usr/local/bin/apf-moved-builder && test ! -e /etc/init.d/apf-moved-worker && test ! -e /etc/conf.d/apf-moved-worker && test ! -e /etc/runlevels/default/apf-moved-worker && test ! -L /etc/runlevels/default/apf-moved-worker && test ! -e /etc/alpineform-moved && test ! -e /var/lib/alpineform-moved && test ! -e /var/lib/alpineform && test ! -e /var/cache/alpineform && test ! -e /var/tmp/alpineform && test ! -e /tmp/apf-component-moved-http && test ! -e /tmp/apf-component-moved-readonly.before && test ! -e /tmp/apf-component-moved-readonly.after"

run_remote "create the component-moved artifact fixture directory" \
  "rm -rf /tmp/apf-component-moved-http && mkdir -p /tmp/apf-component-moved-http"
copy_to_vm "$CASE_DIR/fixtures/worker" /tmp/apf-component-moved-http/worker
run_remote "start the guest-local component-moved artifact server" \
  "nohup python3 -m http.server 18081 --bind 127.0.0.1 --directory /tmp/apf-component-moved-http >/tmp/apf-component-moved-http/server.log 2>&1 & echo \$! > /tmp/apf-component-moved-http/server.pid"
assert_remote "component-moved fixture server returns the pinned worker" \
  "test \"\$(wget -qO- http://127.0.0.1:18081/worker | sha256sum | awk '{print \$1}')\" = '$APF_MOVED_WORKER_SHA'"
