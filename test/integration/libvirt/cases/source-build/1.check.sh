assert_remote "musl source build v1 is installed atomically" \
  "test \"\$(/usr/local/bin/apf-ci-source-tool)\" = alpineform-musl-source-v1 && test \"\$(stat -c %a /usr/local/bin/apf-ci-source-tool)\" = 755"
assert_remote "temporary build ownership is clean and shared world intent remains" \
  "! grep -Eq '^\\.alpineform-build-' /etc/apk/world && ! apk info | grep -Eq '^\\.alpineform-build-' && grep -qx zlib-dev /etc/apk/world && apk info --exists zlib-dev && ! apk info --exists build-base && ! apk info --exists bubblewrap"
assert_remote "source-build workspace and protected runtime files are clean" \
  "test -z \"\$(find /var/tmp/alpineform/builds -mindepth 1 -print -quit 2>/dev/null)\" && test -z \"\$(find /run/alpineform/build-runtime -mindepth 1 -print -quit 2>/dev/null)\""
assert_remote "source-build state is protected and contains no secret" \
  "grep -Fq '\"protected\": true' /var/lib/alpineform/state.json && ! grep -Fq alpineform-ci-secret-sentinel /var/lib/alpineform/state.json"
if [[ "$APF_TEST_PHASE" == repaired ]]; then
  assert_local "installed drift was reported as repair" grep -Fq 'repair:' "$LOG_DIR/1.drift-check.log"
fi
