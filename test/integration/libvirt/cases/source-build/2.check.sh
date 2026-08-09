assert_remote "workspace-root-only change keeps the v1 installation and inode" \
  "test \"\$(/usr/local/bin/apf-ci-source-tool)\" = alpineform-musl-source-v1 && test \"\$(stat -c '%d:%i' /usr/local/bin/apf-ci-source-tool)\" = \"\$(cat /var/lib/alpineform/source-build-v1.identity)\""
assert_local "workspace-root-only change is a complete no-op" \
  python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/2.pre-apply-plan.json"
assert_remote "unused default and overridden build roots remain clean" \
  "test -z \"\$(find /var/tmp/alpineform/builds /srv/alpineform-profile-builds /srv/alpineform-host-builds /srv/alpineform-instance-builds -mindepth 1 -print -quit 2>/dev/null)\" && test -z \"\$(find /run/alpineform/build-inputs /run/alpineform/build-runtime -mindepth 1 -print -quit 2>/dev/null)\""

if [[ "$APF_TEST_PHASE" == rebooted ]]; then
  run_remote "constrain /var/tmp before the configured-root rebuild" \
    "mount -t tmpfs -o size=2m,mode=1777 tmpfs /var/tmp"
fi
