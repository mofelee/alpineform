assert_remote "recorded account destroy removes the user and group" \
  "! getent passwd apfci >/dev/null && ! getent group apfci >/dev/null"
assert_remote "account teardown leaves no managed resources" \
  "grep -Fq '\"resources\": {}' /var/lib/alpineform/state.json"
