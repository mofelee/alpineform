assert_remote "source digest drift rebuilt the musl source tool outside constrained var-tmp" \
  "test \"\$(/usr/local/bin/apf-ci-source-tool)\" = alpineform-musl-source-v2 && test \"\$(stat -c %a /usr/local/bin/apf-ci-source-tool)\" = 755"
assert_local "source drift was reported as rebuild" grep -Fq 'rebuild:' "$LOG_DIR/3.pre-apply-plan.json"
assert_remote "configured-root rebuild retains one verified output and no temporary residue" \
  "test \"\$(find /var/cache/alpineform/builds/outputs -type f -name artifact | wc -l | tr -d ' ')\" = 1 && test -z \"\$(find /var/tmp/alpineform/builds /srv/alpineform-profile-builds /srv/alpineform-host-builds /srv/alpineform-instance-builds -mindepth 1 -print -quit 2>/dev/null)\" && test -z \"\$(find /run/alpineform/build-inputs /run/alpineform/build-runtime -mindepth 1 -print -quit 2>/dev/null)\""

if [[ "$APF_TEST_PHASE" == applied ]]; then
  assert_remote "instance root wins while the two-mebibyte var-tmp remains mounted" \
    "grep -Fq ' /var/tmp ' /proc/mounts && test \"\$(stat -c '%U:%G' /srv/alpineform-instance-builds)\" = root:root && permissions=\$(stat -c %a /srv/alpineform-instance-builds) && test \"\$((0\$permissions & 022))\" -eq 0"
  run_remote "unmount the successful constrained var-tmp fixture" "umount /var/tmp"
fi
