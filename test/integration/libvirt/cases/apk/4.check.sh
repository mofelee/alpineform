assert_remote "authoritative ownership leaves only the declared repository" \
  "test \"\$(grep -Ev '^[[:space:]]*(#|$)' /etc/apk/repositories)\" = 'https://dl-cdn.alpinelinux.org/alpine/v3.24/main'"
assert_remote "state records authoritative repository ownership" \
  "grep -Fq '\"ownership\": \"authoritative\"' /var/lib/alpineform/state.json"
