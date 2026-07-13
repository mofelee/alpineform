assert_remote "managed repository marker is present" \
  "grep -Fq '# BEGIN ALPINEFORM REPOSITORY main' /etc/apk/repositories && grep -Fq 'https://dl-cdn.alpinelinux.org/alpine/v3.24/main' /etc/apk/repositories"
assert_remote "package is installed with exact world intent" \
  "apk info -e jq && grep -qx jq /etc/apk/world"
