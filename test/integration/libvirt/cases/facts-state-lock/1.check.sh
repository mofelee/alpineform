assert_remote "guest facts remain Alpine 3.24.1 x86_64 musl" \
  ". /etc/os-release && test \"\$ID\" = alpine && test \"\$VERSION_ID\" = 3.24.1 && test \"\$(apk --print-arch)\" = x86_64 && ldd --version 2>&1 | grep -qi musl"
assert_remote "managed facts marker is converged" \
  "grep -qx 'architecture=amd64' /etc/alpineform-ci-facts"
assert_remote "state has the AlpineForm identity, host, facts, and resource" \
  "grep -Fq '\"product\": \"alpineform\"' /var/lib/alpineform/state.json && grep -Fq '\"host\": \"cihost\"' /var/lib/alpineform/state.json && grep -Fq 'host.cihost.files.file' /var/lib/alpineform/state.json && grep -Fq '\"libc\": \"musl\"' /var/lib/alpineform/state.json"
assert_remote "state permissions are private" \
  "test \"\$(stat -c %a /var/lib/alpineform)\" = 700 && test \"\$(stat -c %a /var/lib/alpineform/state.json)\" = 600"
