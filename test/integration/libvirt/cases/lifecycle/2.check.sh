assert_remote "recorded destroy removes the file and directory" \
  "test ! -e /srv/alpineform-lifecycle"
assert_remote "lifecycle teardown leaves no managed resources" \
  "grep -Fq '\"resources\": {}' /var/lib/alpineform/state.json"
