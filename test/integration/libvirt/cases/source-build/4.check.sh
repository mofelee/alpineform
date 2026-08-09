assert_remote "build-definition drift rebuilt the musl source tool" \
  "test \"\$(/usr/local/bin/apf-ci-source-tool)\" = alpineform-musl-definition-v3 && test \"\$(stat -c %a /usr/local/bin/apf-ci-source-tool)\" = 755"
assert_local "build-definition drift was reported as rebuild" grep -Fq 'rebuild:' "$LOG_DIR/4.pre-apply-plan.json"
assert_remote "final build cleanup retains only explicit shared APK intent" \
  "! grep -Eq '^\\.alpineform-build-' /etc/apk/world && grep -qx zlib-dev /etc/apk/world && apk info -e zlib-dev && test -z \"\$(find /var/tmp/alpineform/builds /srv/alpineform-profile-builds /srv/alpineform-host-builds /srv/alpineform-instance-builds -mindepth 1 -print -quit 2>/dev/null)\""
