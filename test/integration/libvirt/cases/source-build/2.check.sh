assert_remote "source digest drift rebuilt the musl source tool" \
  "test \"\$(/usr/local/bin/apf-ci-source-tool)\" = alpineform-musl-source-v2 && test \"\$(stat -c %a /usr/local/bin/apf-ci-source-tool)\" = 755"
assert_local "source drift was reported as rebuild" grep -Fq 'rebuild:' "$LOG_DIR/2.pre-apply-plan.json"
assert_remote "only the current verified output cache remains" \
  "test \"\$(find /var/cache/alpineform/builds/outputs -type f -name artifact | wc -l | tr -d ' ')\" = 1"
