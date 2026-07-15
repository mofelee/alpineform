test "$(sha256sum "$CASE_DIR/fixtures/tool-v1.c" | awk '{print $1}')" = 3764b3a8b3b7a021738231ecc9310011da67487f41d6f1732a02d53b6ef903e6
test "$(sha256sum "$CASE_DIR/fixtures/tool-v2.c" | awk '{print $1}')" = 488e4dab8ecb6a92a12a75ddb5acb2b5fa6c1437c7880987ee7d0de2c11d6ad1
test "$(sha256sum "$CASE_DIR/fixtures/verify-env.sh" | awk '{print $1}')" = 734fc94faf2e2dcb43d63d205b44641c21576976b0564a3a7d80f970e9acd77f

run_remote "install a separately world-owned shared build dependency" \
  "apk --quiet add zlib-dev"
assert_remote "shared dependency has explicit APK world intent" \
  "grep -qx zlib-dev /etc/apk/world && apk info --exists zlib-dev"
