assert_remote "declaration removal forgets package without uninstalling it" \
  "apk info -e jq && grep -qx jq /etc/apk/world"
assert_remote "declaration removal forgets repository without editing it" \
  "grep -Fq '# BEGIN ALPINEFORM REPOSITORY main' /etc/apk/repositories"
assert_remote "forget step leaves no managed resources" \
  "grep -Fq '\"resources\": {}' /var/lib/alpineform/state.json"
