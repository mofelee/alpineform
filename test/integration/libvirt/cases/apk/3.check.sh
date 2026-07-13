assert_remote "explicit absence removes package and world intent" \
  "! apk info -e jq >/dev/null 2>&1 && ! grep -qx jq /etc/apk/world"
assert_remote "explicit absence removes the managed repository block" \
  "! grep -Fq '# BEGIN ALPINEFORM REPOSITORY main' /etc/apk/repositories"
