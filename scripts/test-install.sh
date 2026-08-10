#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/alpineform-installer-test.XXXXXX")"
VERSION="v0.1.0-installer-test"
ARTIFACT="apf_${VERSION}_linux_amd64.tar.gz"
SERVER_PID=""
CONCURRENT_A_PID=""
CONCURRENT_B_PID=""
LEGACY_LOCK_PID=""
CROSS_USER_PID=""

cleanup() {
  touch "$WORK/concurrent-release" 2>/dev/null || true
  touch "$WORK/shared-release" 2>/dev/null || true
  for pid in "$CONCURRENT_A_PID" "$CONCURRENT_B_PID"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  if [[ -n "$LEGACY_LOCK_PID" ]]; then
    kill "$LEGACY_LOCK_PID" >/dev/null 2>&1 || true
    wait "$LEGACY_LOCK_PID" 2>/dev/null || true
  fi
  if [[ -n "$CROSS_USER_PID" ]]; then
    kill "$CROSS_USER_PID" >/dev/null 2>&1 || true
    wait "$CROSS_USER_PID" 2>/dev/null || true
  fi
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

process_identity() {
  identity_pid=$1
  if [[ -r "/proc/${identity_pid}/stat" ]]; then
    awk '
      {
        stat = $0
        sub(/^[0-9]+ \(.*\) /, "", stat)
        count = split(stat, field, " ")
        if (count >= 20) print "linux:" field[20]
        exit
      }
    ' "/proc/${identity_pid}/stat"
  else
    LC_ALL=C TZ=UTC ps -p "$identity_pid" -o lstart= |
      awk '{$1 = $1; if (NF) print "ps:" $0}'
  fi
}

mkdir -p "$WORK/archive/docs" "$WORK/archive/examples" "$WORK/archive/scripts" "$WORK/release"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags \
    "-X github.com/mofelee/alpineform/internal/version.Version=$VERSION -X github.com/mofelee/alpineform/internal/version.Commit=installer-test -X github.com/mofelee/alpineform/internal/version.Date=2026-07-13T00:00:00Z" \
    -o "$WORK/archive/apf" ./cmd/apf
)
cp "$ROOT_DIR/README.md" "$ROOT_DIR/README.zh-CN.md" "$ROOT_DIR/LICENSE" \
  "$ROOT_DIR/NOTICE.md" "$ROOT_DIR/NOTICE.zh-CN.md" \
  "$ROOT_DIR/SECURITY.md" "$ROOT_DIR/SECURITY.zh-CN.md" \
  "$ROOT_DIR/CHANGELOG.md" "$ROOT_DIR/CHANGELOG.zh-CN.md" "$WORK/archive/"
cp -R "$ROOT_DIR/docs/." "$WORK/archive/docs/"
cp -R "$ROOT_DIR/examples/." "$WORK/archive/examples/"
cp "$ROOT_DIR/scripts/documentation-package-files.txt" "$WORK/archive/scripts/"
tar -C "$WORK/archive" -czf "$WORK/release/$ARTIFACT" .
(
  cd "$WORK/release"
  sha256sum "$ARTIFACT" >checksums.txt
)

python3 "$ROOT_DIR/scripts/check-docs.py" --list-package-files >"$WORK/expected-documents"
cmp "$ROOT_DIR/scripts/documentation-package-files.txt" "$WORK/expected-documents"
"$ROOT_DIR/scripts/check-documentation-package.sh" archive "$WORK/release/$ARTIFACT"

cp -R "$WORK/archive" "$WORK/tampered-manifest-archive"
printf 'README.md\n' \
  >"$WORK/tampered-manifest-archive/scripts/documentation-package-files.txt"
tar -C "$WORK/tampered-manifest-archive" -czf "$WORK/tampered-manifest.tar.gz" .
if "$ROOT_DIR/scripts/check-documentation-package.sh" archive \
  "$WORK/tampered-manifest.tar.gz" >/dev/null 2>&1; then
  printf 'archive check accepted a replaced documentation package manifest\n' >&2
  exit 1
fi

cp -R "$WORK/archive" "$WORK/empty-root-archive"
: >"$WORK/empty-root-archive/README.zh-CN.md"
tar -C "$WORK/empty-root-archive" -czf "$WORK/empty-root.tar.gz" .
if "$ROOT_DIR/scripts/check-documentation-package.sh" archive \
  "$WORK/empty-root.tar.gz" >/dev/null 2>&1; then
  printf 'archive check accepted an empty required root document\n' >&2
  exit 1
fi

cp -R "$WORK/archive" "$WORK/missing-root-archive"
rm "$WORK/missing-root-archive/README.zh-CN.md"
mkdir -p "$WORK/missing-root-release"
tar -C "$WORK/missing-root-archive" \
  -czf "$WORK/missing-root-release/$ARTIFACT" .
(
  cd "$WORK/missing-root-release"
  sha256sum "$ARTIFACT" >checksums.txt
)
if APF_RELEASE_BASE_URL="file://$WORK/missing-root-release" \
  "$ROOT_DIR/scripts/install.sh" --version "$VERSION" \
  --prefix "$WORK/missing-root-prefix" --os linux --arch amd64 \
  >/dev/null 2>&1; then
  printf 'installer accepted an archive with a missing Chinese root document\n' >&2
  exit 1
fi

cp -R "$WORK/archive" "$WORK/symlink-archive"
rm "$WORK/symlink-archive/docs/localization-policy.zh.md"
ln -s /etc/hosts "$WORK/symlink-archive/docs/localization-policy.zh.md"
mkdir -p "$WORK/symlink-release"
tar -C "$WORK/symlink-archive" -czf "$WORK/symlink-release/$ARTIFACT" .
(
  cd "$WORK/symlink-release"
  sha256sum "$ARTIFACT" >checksums.txt
)
if "$ROOT_DIR/scripts/check-documentation-package.sh" archive \
  "$WORK/symlink-release/$ARTIFACT" >/dev/null 2>&1; then
  printf 'archive check accepted an escaping documentation symlink\n' >&2
  exit 1
fi
if APF_RELEASE_BASE_URL="file://$WORK/symlink-release" \
  "$ROOT_DIR/scripts/install.sh" --version "$VERSION" \
  --prefix "$WORK/symlink-prefix" --os linux --arch amd64 \
  >/dev/null 2>&1; then
  printf 'installer accepted an escaping documentation symlink\n' >&2
  exit 1
fi

cp -R "$WORK/archive" "$WORK/special-entry-archive"
mkfifo "$WORK/special-entry-archive/docs/unexpected.pipe"
mkdir -p "$WORK/special-entry-release"
tar -C "$WORK/special-entry-archive" \
  -czf "$WORK/special-entry-release/$ARTIFACT" .
(
  cd "$WORK/special-entry-release"
  sha256sum "$ARTIFACT" >checksums.txt
)
if "$ROOT_DIR/scripts/check-documentation-package.sh" archive \
  "$WORK/special-entry-release/$ARTIFACT" >/dev/null 2>&1; then
  printf 'archive check accepted a special filesystem entry\n' >&2
  exit 1
fi
if APF_RELEASE_BASE_URL="file://$WORK/special-entry-release" \
  "$ROOT_DIR/scripts/install.sh" --version "$VERSION" \
  --prefix "$WORK/special-entry-prefix" --os linux --arch amd64 \
  >/dev/null 2>&1; then
  printf 'installer accepted a special filesystem entry\n' >&2
  exit 1
fi

mkdir -p "$WORK/snapshot"
for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
  cp "$WORK/release/$ARTIFACT" "$WORK/snapshot/apf_${VERSION}_${platform}.tar.gz"
done
"$ROOT_DIR/scripts/check-documentation-package.sh" snapshot "$WORK/snapshot"
cp "$WORK/release/$ARTIFACT" "$WORK/snapshot/apf_${VERSION}_linux_s390x.tar.gz"
if "$ROOT_DIR/scripts/check-documentation-package.sh" snapshot "$WORK/snapshot" \
  >/dev/null 2>&1; then
  printf 'snapshot check accepted an unexpected fifth archive\n' >&2
  exit 1
fi
rm "$WORK/snapshot/apf_${VERSION}_linux_s390x.tar.gz"

mkdir -p "$WORK/prefixed-archive/prefix"
cp -R "$WORK/archive/." "$WORK/prefixed-archive/prefix/"
tar -C "$WORK/prefixed-archive" -czf "$WORK/prefixed.tar.gz" .
if "$ROOT_DIR/scripts/check-documentation-package.sh" archive "$WORK/prefixed.tar.gz" \
  >/dev/null 2>&1; then
  printf 'archive check accepted a layout the installer cannot consume\n' >&2
  exit 1
fi

cp -R "$WORK/archive" "$WORK/incomplete-archive"
rm "$WORK/incomplete-archive/docs/releases/v0.1.0-alpha.5.zh.md"
mkdir -p "$WORK/incomplete-release"
tar -C "$WORK/incomplete-archive" -czf "$WORK/incomplete-release/$ARTIFACT" .
(
  cd "$WORK/incomplete-release"
  sha256sum "$ARTIFACT" >checksums.txt
)
if APF_RELEASE_BASE_URL="file://$WORK/incomplete-release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/incomplete-prefix" --os linux --arch amd64 \
  >/dev/null 2>&1; then
  printf 'installer accepted an archive with a missing Chinese document\n' >&2
  exit 1
fi

unprivileged_run=()
unprivileged_chown=()
if [[ $(id -u) -eq 0 ]] && command -v runuser >/dev/null 2>&1; then
  unprivileged_run=(runuser -u nobody --)
  unprivileged_chown=(chown)
elif command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
  unprivileged_run=(sudo -n -u nobody --)
  unprivileged_chown=(sudo -n chown)
fi
if [[ ${#unprivileged_run[@]} -gt 0 ]] && id nobody >/dev/null 2>&1; then
  chmod 0755 "$WORK"
  cp "$ROOT_DIR/scripts/install.sh" "$WORK/install.sh"
  cp -R "$WORK/archive" "$WORK/unreadable-archive"
  chmod 000 "$WORK/unreadable-archive/docs/localization-policy.zh.md"
  mkdir -p "$WORK/unreadable-release" "$WORK/unreadable-prefix"
  tar -C "$WORK/unreadable-archive" -czf "$WORK/unreadable-release/$ARTIFACT" .
  (
    cd "$WORK/unreadable-release"
    sha256sum "$ARTIFACT" >checksums.txt
  )
  nobody_group="$(id -gn nobody)"
  mkdir -p "$WORK/unreadable-prefix/bin" "$WORK/unreadable-prefix/share/alpineform"
  printf 'previous binary\n' >"$WORK/unreadable-prefix/bin/apf"
  printf 'previous package data\n' >"$WORK/unreadable-prefix/share/alpineform/sentinel"
  "${unprivileged_chown[@]}" -R "nobody:${nobody_group}" "$WORK/unreadable-prefix"
  if "${unprivileged_run[@]}" env \
    APF_RELEASE_BASE_URL="file://$WORK/unreadable-release" \
    "$WORK/install.sh" --version "$VERSION" --prefix "$WORK/unreadable-prefix" \
    --os linux --arch amd64 >"$WORK/unreadable-output" 2>&1; then
    printf 'installer accepted an unreadable Chinese document\n' >&2
    exit 1
  fi
  grep -Fq "cannot read docs/localization-policy.zh.md" "$WORK/unreadable-output"
  grep -Fq "previous binary" "$WORK/unreadable-prefix/bin/apf"
  grep -Fq "previous package data" \
    "$WORK/unreadable-prefix/share/alpineform/sentinel"
  test ! -e "$WORK/unreadable-prefix/share/alpineform/README.md"
  "${unprivileged_chown[@]}" -R "$(id -u):$(id -g)" "$WORK/unreadable-prefix"
  chmod 0644 "$WORK/unreadable-archive/docs/localization-policy.zh.md"

  mkdir -p "$WORK/cross-user-prefix/.alpineform-install-locks"
  "${unprivileged_chown[@]}" -R "nobody:${nobody_group}" "$WORK/cross-user-prefix"
  sleep 2 &
  CROSS_USER_PID=$!
  process_identity "$CROSS_USER_PID" \
    >"$WORK/cross-user-prefix/.alpineform-install-locks/ticket.1.${CROSS_USER_PID}"
  "${unprivileged_run[@]}" env \
    APF_RELEASE_BASE_URL="file://$WORK/release" \
    "$WORK/install.sh" --version "$VERSION" --prefix "$WORK/cross-user-prefix" \
    --os linux --arch amd64 >"$WORK/cross-user-output"
  if kill -0 "$CROSS_USER_PID" 2>/dev/null; then
    printf 'installer removed a live cross-user publication ticket\n' >&2
    exit 1
  fi
  wait "$CROSS_USER_PID"
  CROSS_USER_PID=""
  test "$("$WORK/cross-user-prefix/bin/apf" --version)" = "apf $VERSION"
else
  printf 'unreadable-document installer test requires runuser or passwordless sudo\n' >&2
  exit 1
fi

APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/prefix" --os linux --arch amd64
"$WORK/prefix/bin/apf" version | grep -Fq "$VERSION"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree "$WORK/prefix/share/alpineform"
cmp "$ROOT_DIR/scripts/documentation-package-files.txt" \
  "$WORK/prefix/share/alpineform/documentation-package-files.txt"
test -f "$WORK/prefix/share/alpineform/examples/quickstart.apf.hcl"

copy_installed_prefix() {
  source_prefix=$1
  target_prefix=$2
  cp -R "$source_prefix" "$target_prefix"
  printf '%s\n' "$(cd "$target_prefix" && pwd -P)" \
    >"$target_prefix/bin/.alpineform-install-prefix"
}

real_mktemp="$(command -v mktemp)"
mkdir -p "$WORK/bsd-mktemp-bin"
cat >"$WORK/bsd-mktemp-bin/mktemp" <<EOF
#!/bin/sh
template=
for argument do
  template=\$argument
done
case "\$template" in
  *XXXXXX) ;;
  *) exit 64 ;;
esac
exec "$real_mktemp" "\$@"
EOF
chmod 0755 "$WORK/bsd-mktemp-bin/mktemp"
PATH="$WORK/bsd-mktemp-bin:$PATH" \
  APF_RELEASE_BASE_URL="file://$WORK/release" \
  "$ROOT_DIR/scripts/install.sh" --version "$VERSION" \
  --prefix "$WORK/bsd-mktemp-prefix" --os linux --arch amd64 \
  >"$WORK/bsd-mktemp-output"
test "$("$WORK/bsd-mktemp-prefix/bin/apf" --version)" = "apf $VERSION"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree \
  "$WORK/bsd-mktemp-prefix/share/alpineform"

assert_lock_queue_empty() {
  queue=$1
  if [[ -d "$queue" ]]; then
    test -z "$(find "$queue" -type f -print -quit)"
  fi
}

mkdir -p "$WORK/alias-prefix"
ln -s "$WORK/alias-prefix" "$WORK/alias-prefix-link"
if ! timeout 15 env APF_RELEASE_BASE_URL="file://$WORK/release" \
  "$ROOT_DIR/scripts/install.sh" --version "$VERSION" \
  --prefix "$WORK/alias-prefix" --bin-dir "$WORK/alias-prefix-link/." \
  --os linux --arch amd64 >"$WORK/alias-output" 2>&1; then
  printf 'installer deadlocked on equivalent prefix and binary lock paths\n' >&2
  exit 1
fi
test "$("$WORK/alias-prefix/apf" --version)" = "apf $VERSION"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree \
  "$WORK/alias-prefix/share/alpineform"
assert_lock_queue_empty "$WORK/alias-prefix/.alpineform-install-locks"

copy_installed_prefix "$WORK/prefix" "$WORK/rollback-prefix"
printf 'previous-only data\n' >"$WORK/rollback-prefix/share/alpineform/sentinel"
rollback_binary_sha="$(sha256sum "$WORK/rollback-prefix/bin/apf" | awk '{print $1}')"
real_mv="$(command -v mv)"
mkdir -p "$WORK/fail-bin-mv"
cat >"$WORK/fail-bin-mv/mv" <<EOF
#!/bin/sh
if [ "\${2:-}" = "$WORK/rollback-prefix/bin/apf" ]; then
  exit 1
fi
exec "$real_mv" "\$@"
EOF
chmod 0755 "$WORK/fail-bin-mv/mv"
if PATH="$WORK/fail-bin-mv:$PATH" \
  APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/rollback-prefix" --os linux --arch amd64 \
  >"$WORK/rollback-output" 2>&1; then
  printf 'installer accepted a failed binary publication\n' >&2
  exit 1
fi
grep -Fq "could not publish apf" "$WORK/rollback-output"
test "$(sha256sum "$WORK/rollback-prefix/bin/apf" | awk '{print $1}')" = \
  "$rollback_binary_sha"
grep -Fq "previous-only data" "$WORK/rollback-prefix/share/alpineform/sentinel"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree \
  "$WORK/rollback-prefix/share/alpineform"
test -z "$(find "$WORK/rollback-prefix/share" -maxdepth 1 \
  -name '.alpineform-*' -print -quit)"

copy_installed_prefix "$WORK/prefix" "$WORK/rollback-preserve-prefix"
printf 'recoverable previous data\n' \
  >"$WORK/rollback-preserve-prefix/share/alpineform/sentinel"
rollback_preserve_binary_sha="$(sha256sum \
  "$WORK/rollback-preserve-prefix/bin/apf" | awk '{print $1}')"
mkdir -p "$WORK/fail-bin-and-restore-mv"
cat >"$WORK/fail-bin-and-restore-mv/mv" <<EOF
#!/bin/sh
case "\${1:-}:\${2:-}" in
  *'/.alpineform-backup.'*'/previous:$WORK/rollback-preserve-prefix/share/alpineform'|\
  *:$WORK/rollback-preserve-prefix/bin/apf)
    exit 1
    ;;
esac
exec "$real_mv" "\$@"
EOF
chmod 0755 "$WORK/fail-bin-and-restore-mv/mv"
if PATH="$WORK/fail-bin-and-restore-mv:$PATH" \
  APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/rollback-preserve-prefix" \
  --os linux --arch amd64 >"$WORK/rollback-preserve-output" 2>&1; then
  printf 'installer accepted binary publication with failed data restore\n' >&2
  exit 1
fi
test "$(sha256sum "$WORK/rollback-preserve-prefix/bin/apf" | awk '{print $1}')" = \
  "$rollback_preserve_binary_sha"
test ! -e "$WORK/rollback-preserve-prefix/share/alpineform"
preserved_backup="$(find "$WORK/rollback-preserve-prefix/share" -maxdepth 1 \
  -type d -name '.alpineform-backup.*' -print -quit)"
test -n "$preserved_backup"
grep -Fq 'recoverable previous data' "$preserved_backup/previous/sentinel"
grep -Fq "could not restore package data from $preserved_backup/previous" \
  "$WORK/rollback-preserve-output"
assert_lock_queue_empty "$WORK/rollback-preserve-prefix/.alpineform-install-locks"
assert_lock_queue_empty "$WORK/rollback-preserve-prefix/bin/.alpineform-install-locks"

cp -R "$WORK/archive" "$WORK/mismatch-archive"
wrong_version="v0.1.0-wrong-installer-test"
(
  cd "$ROOT_DIR"
  CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags \
    "-X github.com/mofelee/alpineform/internal/version.Version=$wrong_version -X github.com/mofelee/alpineform/internal/version.Commit=installer-mismatch-test -X github.com/mofelee/alpineform/internal/version.Date=2026-07-13T00:00:00Z" \
    -o "$WORK/mismatch-archive/apf" ./cmd/apf
)
mkdir -p "$WORK/mismatch-release"
tar -C "$WORK/mismatch-archive" -czf "$WORK/mismatch-release/$ARTIFACT" .
(
  cd "$WORK/mismatch-release"
  sha256sum "$ARTIFACT" >checksums.txt
)
copy_installed_prefix "$WORK/prefix" "$WORK/mismatch-prefix"
printf 'preflight sentinel\n' >"$WORK/mismatch-prefix/share/alpineform/sentinel"
mismatch_binary_sha="$(sha256sum "$WORK/mismatch-prefix/bin/apf" | awk '{print $1}')"
if APF_RELEASE_BASE_URL="file://$WORK/mismatch-release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/mismatch-prefix" --os linux --arch amd64 \
  >"$WORK/mismatch-output" 2>&1; then
  printf 'installer accepted an archive binary reporting the wrong version\n' >&2
  exit 1
fi
grep -Fq "archive binary reports 'apf $wrong_version'" "$WORK/mismatch-output"
test "$(sha256sum "$WORK/mismatch-prefix/bin/apf" | awk '{print $1}')" = \
  "$mismatch_binary_sha"
grep -Fq "preflight sentinel" "$WORK/mismatch-prefix/share/alpineform/sentinel"

cp -R "$WORK/archive" "$WORK/nonexec-archive"
printf '\177ELFcorrupt\n' >"$WORK/nonexec-archive/apf"
mkdir -p "$WORK/nonexec-release"
tar -C "$WORK/nonexec-archive" -czf "$WORK/nonexec-release/$ARTIFACT" .
(
  cd "$WORK/nonexec-release"
  sha256sum "$ARTIFACT" >checksums.txt
)
copy_installed_prefix "$WORK/prefix" "$WORK/nonexec-prefix"
printf 'preflight sentinel\n' >"$WORK/nonexec-prefix/share/alpineform/sentinel"
nonexec_binary_sha="$(sha256sum "$WORK/nonexec-prefix/bin/apf" | awk '{print $1}')"
if APF_RELEASE_BASE_URL="file://$WORK/nonexec-release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/nonexec-prefix" --os linux --arch amd64 \
  >"$WORK/nonexec-output" 2>&1; then
  printf 'installer accepted a non-runnable archive binary\n' >&2
  exit 1
fi
grep -Fq "archive binary cannot run on linux/amd64" "$WORK/nonexec-output"
test "$(sha256sum "$WORK/nonexec-prefix/bin/apf" | awk '{print $1}')" = \
  "$nonexec_binary_sha"
grep -Fq "preflight sentinel" "$WORK/nonexec-prefix/share/alpineform/sentinel"

mkdir -p "$WORK/stale-lock-prefix/.alpineform-install-locks" \
  "$WORK/stale-lock-prefix/bin/.alpineform-install-locks"
: >"$WORK/stale-lock-prefix/.alpineform-install-locks/choosing.not-a-pid"
: >"$WORK/stale-lock-prefix/.alpineform-install-locks/ticket.1.99999999"
: >"$WORK/stale-lock-prefix/bin/.alpineform-install-locks/ticket.bad.bad"
APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/stale-lock-prefix" --os linux --arch amd64 \
  >"$WORK/stale-lock-output"
test "$("$WORK/stale-lock-prefix/bin/apf" --version)" = "apf $VERSION"
assert_lock_queue_empty "$WORK/stale-lock-prefix/.alpineform-install-locks"
assert_lock_queue_empty "$WORK/stale-lock-prefix/bin/.alpineform-install-locks"

mkdir -p "$WORK/live-legacy-prefix/bin" \
  "$WORK/live-legacy-prefix/.alpineform-install-locks"
process_identity "$$" \
  >"$WORK/live-legacy-prefix/.alpineform-install-locks/ticket.1.$$"
(
  sleep 2
  rm -f "$WORK/live-legacy-prefix/.alpineform-install-locks/ticket.1.$$"
  touch "$WORK/live-legacy-released"
) &
LEGACY_LOCK_PID=$!
APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/live-legacy-prefix" \
  --os linux --arch amd64 >"$WORK/live-legacy-output"
test -e "$WORK/live-legacy-released"
wait "$LEGACY_LOCK_PID"
LEGACY_LOCK_PID=""
test "$("$WORK/live-legacy-prefix/bin/apf" --version)" = "apf $VERSION"
assert_lock_queue_empty "$WORK/live-legacy-prefix/.alpineform-install-locks"
assert_lock_queue_empty "$WORK/live-legacy-prefix/bin/.alpineform-install-locks"

for boundary in backup binary; do
  signal_prefix="$WORK/signal-${boundary}-prefix"
  signal_wrapper="$WORK/signal-${boundary}-mv"
  copy_installed_prefix "$WORK/prefix" "$signal_prefix"
  cp "$WORK/mismatch-archive/apf" "$signal_prefix/bin/apf"
  printf 'pre-signal data\n' >"$signal_prefix/share/alpineform/sentinel"
  mkdir -p "$signal_wrapper"
  cat >"$signal_wrapper/mv" <<EOF
#!/bin/sh
case "$boundary:\${1:-}:\${2:-}" in
  backup:$signal_prefix/share/alpineform:$signal_prefix/share/.alpineform-backup.*|\
  binary:*:$signal_prefix/bin/apf)
    "$real_mv" "\$@" || exit
    : >"$WORK/signal-${boundary}-sent"
    kill -TERM -- "-\$PPID"
    exit 0
    ;;
esac
exec "$real_mv" "\$@"
EOF
  chmod 0755 "$signal_wrapper/mv"
  if ! setsid env PATH="$signal_wrapper:$PATH" \
    APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
    --version "$VERSION" --prefix "$signal_prefix" --os linux --arch amd64 \
    >"$WORK/signal-${boundary}-output" 2>&1; then
    printf 'installer left an inconsistent %s publication after group TERM\n' \
      "$boundary" >&2
    exit 1
  fi
  test -e "$WORK/signal-${boundary}-sent"
  test "$("$signal_prefix/bin/apf" --version)" = "apf $VERSION"
  test ! -e "$signal_prefix/share/alpineform/sentinel"
  "$ROOT_DIR/scripts/check-documentation-package.sh" tree \
    "$signal_prefix/share/alpineform"
  test -z "$(find "$signal_prefix" -type f -name '.alpineform-*' \
    ! -name '.alpineform-install-prefix' -print -quit)"
done

concurrent_b_artifact="apf_${wrong_version}_linux_amd64.tar.gz"
cp -R "$WORK/archive" "$WORK/concurrent-a-archive"
cp -R "$WORK/mismatch-archive" "$WORK/concurrent-b-archive"
printf '\nconcurrency marker A\n' >>"$WORK/concurrent-a-archive/README.md"
printf '\nconcurrency marker B\n' >>"$WORK/concurrent-b-archive/README.md"
mkdir -p "$WORK/concurrent-a-release" "$WORK/concurrent-b-release"
tar -C "$WORK/concurrent-a-archive" -czf "$WORK/concurrent-a-release/$ARTIFACT" .
tar -C "$WORK/concurrent-b-archive" \
  -czf "$WORK/concurrent-b-release/$concurrent_b_artifact" .
(
  cd "$WORK/concurrent-a-release"
  sha256sum "$ARTIFACT" >checksums.txt
)
(
  cd "$WORK/concurrent-b-release"
  sha256sum "$concurrent_b_artifact" >checksums.txt
)
mkdir -p "$WORK/concurrent-prefix/.alpineform-install-locks" \
  "$WORK/concurrent-prefix/bin/.alpineform-install-locks" "$WORK/pause-bin-mv"
: >"$WORK/concurrent-prefix/.alpineform-install-locks/ticket.1.99999999"
: >"$WORK/concurrent-prefix/bin/.alpineform-install-locks/ticket.1.99999999"
cat >"$WORK/pause-bin-mv/mv" <<EOF
#!/bin/sh
if [ "\${2:-}" = "$WORK/concurrent-prefix/bin/apf" ]; then
  : >"$WORK/concurrent-paused"
  while [ ! -e "$WORK/concurrent-release" ]; do
    sleep 0.05
  done
fi
exec "$real_mv" "\$@"
EOF
chmod 0755 "$WORK/pause-bin-mv/mv"
PATH="$WORK/pause-bin-mv:$PATH" \
  APF_RELEASE_BASE_URL="file://$WORK/concurrent-a-release" \
  "$ROOT_DIR/scripts/install.sh" --version "$VERSION" \
  --prefix "$WORK/concurrent-prefix" --os linux --arch amd64 \
  >"$WORK/concurrent-a-output" 2>&1 &
CONCURRENT_A_PID=$!
for _ in {1..100}; do
  [[ ! -e "$WORK/concurrent-paused" ]] || break
  kill -0 "$CONCURRENT_A_PID" 2>/dev/null || break
  sleep 0.1
done
[[ -e "$WORK/concurrent-paused" ]]
test -n "$(find "$WORK/concurrent-prefix/.alpineform-install-locks" \
  -maxdepth 1 -type f -name "ticket.*.${CONCURRENT_A_PID}.*" \
  -perm -004 -print -quit)"
grep -Fq "concurrency marker A" "$WORK/concurrent-prefix/share/alpineform/README.md"
APF_RELEASE_BASE_URL="file://$WORK/concurrent-b-release" \
  "$ROOT_DIR/scripts/install.sh" --version "$wrong_version" \
  --prefix "$WORK/concurrent-prefix" --os linux --arch amd64 \
  >"$WORK/concurrent-b-output" 2>&1 &
CONCURRENT_B_PID=$!
sleep 1.2
kill -0 "$CONCURRENT_B_PID" 2>/dev/null
grep -Fq "concurrency marker A" "$WORK/concurrent-prefix/share/alpineform/README.md"
touch "$WORK/concurrent-release"
wait "$CONCURRENT_A_PID"
CONCURRENT_A_PID=""
wait "$CONCURRENT_B_PID"
CONCURRENT_B_PID=""
test "$("$WORK/concurrent-prefix/bin/apf" --version)" = "apf $wrong_version"
grep -Fq "concurrency marker B" "$WORK/concurrent-prefix/share/alpineform/README.md"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree \
  "$WORK/concurrent-prefix/share/alpineform"
assert_lock_queue_empty "$WORK/concurrent-prefix/.alpineform-install-locks"
assert_lock_queue_empty "$WORK/concurrent-prefix/bin/.alpineform-install-locks"

mkdir -p "$WORK/shared-bin" "$WORK/shared-prefix-a" "$WORK/shared-prefix-b" \
  "$WORK/pause-shared-bin-mv"
cat >"$WORK/pause-shared-bin-mv/mv" <<EOF
#!/bin/sh
if [ "\${2:-}" = "$WORK/shared-bin/apf" ]; then
  : >"$WORK/shared-paused"
  while [ ! -e "$WORK/shared-release" ]; do
    sleep 0.05
  done
fi
exec "$real_mv" "\$@"
EOF
chmod 0755 "$WORK/pause-shared-bin-mv/mv"
PATH="$WORK/pause-shared-bin-mv:$PATH" \
  APF_RELEASE_BASE_URL="file://$WORK/concurrent-a-release" \
  "$ROOT_DIR/scripts/install.sh" --version "$VERSION" \
  --prefix "$WORK/shared-prefix-a" --bin-dir "$WORK/shared-bin" \
  --os linux --arch amd64 >"$WORK/shared-a-output" 2>&1 &
CONCURRENT_A_PID=$!
for _ in {1..100}; do
  [[ ! -e "$WORK/shared-paused" ]] || break
  kill -0 "$CONCURRENT_A_PID" 2>/dev/null || break
  sleep 0.1
done
[[ -e "$WORK/shared-paused" ]]
grep -Fq "concurrency marker A" \
  "$WORK/shared-prefix-a/share/alpineform/README.md"
APF_RELEASE_BASE_URL="file://$WORK/concurrent-b-release" \
  "$ROOT_DIR/scripts/install.sh" --version "$wrong_version" \
  --prefix "$WORK/shared-prefix-b" --bin-dir "$WORK/shared-bin" \
  --os linux --arch amd64 >"$WORK/shared-b-output" 2>&1 &
CONCURRENT_B_PID=$!
sleep 1.2
kill -0 "$CONCURRENT_B_PID" 2>/dev/null
test ! -e "$WORK/shared-prefix-b/share/alpineform"
touch "$WORK/shared-release"
wait "$CONCURRENT_A_PID"
CONCURRENT_A_PID=""
if wait "$CONCURRENT_B_PID"; then
  printf 'installer allowed two prefixes to claim one binary directory\n' >&2
  exit 1
fi
CONCURRENT_B_PID=""
grep -Fq "already belongs to prefix" "$WORK/shared-b-output"
test "$("$WORK/shared-bin/apf" --version)" = "apf $VERSION"
grep -Fq "concurrency marker A" "$WORK/shared-prefix-a/share/alpineform/README.md"
test ! -e "$WORK/shared-prefix-b/share/alpineform"
assert_lock_queue_empty "$WORK/shared-bin/.alpineform-install-locks"
assert_lock_queue_empty "$WORK/shared-prefix-a/.alpineform-install-locks"
assert_lock_queue_empty "$WORK/shared-prefix-b/.alpineform-install-locks"

cp -R "$WORK/prefix/share/alpineform" "$WORK/incomplete-tree"
rm "$WORK/incomplete-tree/docs/localization-policy.zh.md"
if "$ROOT_DIR/scripts/check-documentation-package.sh" tree "$WORK/incomplete-tree" \
  >/dev/null 2>&1; then
  printf 'documentation tree check accepted a missing Chinese document\n' >&2
  exit 1
fi

cp -R "$WORK/prefix/share/alpineform" "$WORK/missing-root-tree"
rm "$WORK/missing-root-tree/README.zh-CN.md"
if "$ROOT_DIR/scripts/check-documentation-package.sh" tree "$WORK/missing-root-tree" \
  >/dev/null 2>&1; then
  printf 'documentation tree check accepted a missing Chinese root document\n' >&2
  exit 1
fi

printf 'README.md\n' >"$WORK/prefix/share/alpineform/documentation-package-files.txt"
rm "$WORK/prefix/share/alpineform/docs/localization-policy.zh.md"
rm "$WORK/prefix/share/alpineform/examples/quickstart.apf.hcl"
rm "$WORK/prefix/share/alpineform/LICENSE"
touch "$WORK/prefix/share/alpineform/docs/obsolete.md"
APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/prefix" --os linux --arch amd64 \
  >"$WORK/repair-output"
grep -Fq "already installed" "$WORK/repair-output"
grep -Fq "verifying package data" "$WORK/repair-output"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree "$WORK/prefix/share/alpineform"
test -f "$WORK/prefix/share/alpineform/examples/quickstart.apf.hcl"
test -f "$WORK/prefix/share/alpineform/LICENSE"
test ! -e "$WORK/prefix/share/alpineform/docs/obsolete.md"

mkdir -p "$WORK/make-root/usr/local/share/alpineform/docs" \
  "$WORK/make-root/usr/local/share/alpineform/examples"
printf 'removed root document\n' \
  >"$WORK/make-root/usr/local/share/alpineform/REMOVED.zh-CN.md"
printf 'removed nested document\n' \
  >"$WORK/make-root/usr/local/share/alpineform/docs/removed.zh.md"
printf 'removed example\n' \
  >"$WORK/make-root/usr/local/share/alpineform/examples/removed.apf.hcl"

mkdir -p "$WORK/make-directory-root/usr/local/bin/apf" \
  "$WORK/make-directory-root/usr/local/share/alpineform"
printf 'directory target sentinel\n' \
  >"$WORK/make-directory-root/usr/local/share/alpineform/sentinel"
if make -C "$ROOT_DIR" install \
  BINARY="$WORK/make-directory-apf" \
  VERSION="$VERSION" \
  PREFIX=/usr/local \
  DESTDIR="$WORK/make-directory-root" >"$WORK/make-directory-output" 2>&1; then
  printf 'make install accepted an apf directory target\n' >&2
  exit 1
fi
test -d "$WORK/make-directory-root/usr/local/bin/apf"
grep -Fq 'directory target sentinel' \
  "$WORK/make-directory-root/usr/local/share/alpineform/sentinel"

make -C "$ROOT_DIR" install \
  BINARY="$WORK/make-apf" \
  VERSION="$VERSION" \
  PREFIX=/usr/local \
  DESTDIR="$WORK/make-root"
"$WORK/make-root/usr/local/bin/apf" version | grep -Fq "$VERSION"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree \
  "$WORK/make-root/usr/local/share/alpineform"
test -f "$WORK/make-root/usr/local/share/alpineform/examples/quickstart.apf.hcl"
test ! -e "$WORK/make-root/usr/local/share/alpineform/REMOVED.zh-CN.md"
test ! -e "$WORK/make-root/usr/local/share/alpineform/docs/removed.zh.md"
test ! -e "$WORK/make-root/usr/local/share/alpineform/examples/removed.apf.hcl"

cp -R "$WORK/make-root" "$WORK/make-rollback-root"
printf 'make rollback sentinel\n' \
  >"$WORK/make-rollback-root/usr/local/share/alpineform/sentinel"
make_binary_sha="$(sha256sum \
  "$WORK/make-rollback-root/usr/local/bin/apf" | awk '{print $1}')"
mkdir -p "$WORK/fail-make-bin-mv"
cat >"$WORK/fail-make-bin-mv/mv" <<EOF
#!/bin/sh
case "\${1:-}" in
  *.backup) ;;
  *)
    if [ "\${2:-}" = "$WORK/make-rollback-root/usr/local/bin/apf" ]; then
      exit 1
    fi
    ;;
esac
exec "$real_mv" "\$@"
EOF
chmod 0755 "$WORK/fail-make-bin-mv/mv"
if PATH="$WORK/fail-make-bin-mv:$PATH" make -C "$ROOT_DIR" install \
  BINARY="$WORK/make-rollback-apf" \
  VERSION="$VERSION" \
  PREFIX=/usr/local \
  DESTDIR="$WORK/make-rollback-root" >"$WORK/make-rollback-output" 2>&1; then
  printf 'make install accepted a failed binary publication\n' >&2
  exit 1
fi
test "$(sha256sum "$WORK/make-rollback-root/usr/local/bin/apf" | awk '{print $1}')" = \
  "$make_binary_sha"
grep -Fq 'make rollback sentinel' \
  "$WORK/make-rollback-root/usr/local/share/alpineform/sentinel"
test -z "$(find "$WORK/make-rollback-root/usr/local" \
  \( -name '*.stage.*' -o -name '*.backup' \) -print -quit)"

mkdir -p "$WORK/fail-make-bin-stage" \
  "$WORK/make-stage-leak-root/usr/local/bin" \
  "$WORK/make-stage-leak-root/usr/local/share"
cat >"$WORK/fail-make-bin-stage/mktemp" <<EOF
#!/bin/sh
case "\${2:-\${1:-}}" in
  */bin/.apf.stage.XXXXXX) exit 1 ;;
esac
exec "$real_mktemp" "\$@"
EOF
chmod 0755 "$WORK/fail-make-bin-stage/mktemp"
if PATH="$WORK/fail-make-bin-stage:$PATH" make -C "$ROOT_DIR" install \
  BINARY="$WORK/make-stage-leak-apf" \
  VERSION="$VERSION" \
  PREFIX=/usr/local \
  DESTDIR="$WORK/make-stage-leak-root" >"$WORK/make-stage-leak-output" 2>&1; then
  printf 'make install accepted a failed binary staging allocation\n' >&2
  exit 1
fi
test -z "$(find "$WORK/make-stage-leak-root/usr/local/share" -maxdepth 1 \
  -name 'alpineform.stage.*' -print -quit)"

APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/prefix" --os linux --arch amd64 \
  >"$WORK/same-version-output"
grep -Fq "already installed" "$WORK/same-version-output"
APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/dry-run" --os darwin --arch arm64 --dry-run \
  >"$WORK/dry-run-output"
grep -Fq "apf_${VERSION}_darwin_arm64.tar.gz" "$WORK/dry-run-output"

cp "$WORK/release/$ARTIFACT" "$WORK/release/$ARTIFACT.valid"
printf 'corrupt\n' >>"$WORK/release/$ARTIFACT"
if APF_RELEASE_BASE_URL="file://$WORK/release" "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/corrupt" --os linux --arch amd64 >/dev/null 2>&1; then
  printf 'installer accepted an archive with a mismatched checksum\n' >&2
  exit 1
fi
mv "$WORK/release/$ARTIFACT.valid" "$WORK/release/$ARTIFACT"

python3 - "$WORK/release" "$WORK/server-port" "$VERSION" "$ARTIFACT" <<'PY' &
import http.server
import json
import pathlib
import sys

release_dir = pathlib.Path(sys.argv[1])
port_file = pathlib.Path(sys.argv[2])
version = sys.argv[3]
artifact = sys.argv[4]


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.headers.get("Authorization") != "Bearer installer-token":
            self.send_error(401)
            return
        if self.path == f"/repos/mofelee/alpineform/releases/tags/{version}":
            base = f"http://127.0.0.1:{self.server.server_port}/releases/assets"
            body = json.dumps(
                {
                    "assets": [
                        {"url": f"{base}/archive", "name": artifact},
                        {"url": f"{base}/checksums", "name": "checksums.txt"},
                    ]
                },
                indent=2,
            ).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
        elif self.path == "/releases/assets/archive":
            body = (release_dir / artifact).read_bytes()
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
        elif self.path == "/releases/assets/checksums":
            body = (release_dir / "checksums.txt").read_bytes()
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
        else:
            self.send_error(404)
            return
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
port_file.write_text(str(server.server_port), encoding="ascii")
server.serve_forever()
PY
SERVER_PID=$!
for _ in {1..50}; do
  [[ ! -s "$WORK/server-port" ]] || break
  sleep 0.1
done
[[ -s "$WORK/server-port" ]]
server_port="$(<"$WORK/server-port")"
APF_GITHUB_API_BASE_URL="http://127.0.0.1:${server_port}/repos/mofelee/alpineform" \
  GITHUB_TOKEN=installer-token "$ROOT_DIR/scripts/install.sh" \
  --version "$VERSION" --prefix "$WORK/private-prefix" --os linux --arch amd64
"$WORK/private-prefix/bin/apf" version | grep -Fq "$VERSION"
"$ROOT_DIR/scripts/check-documentation-package.sh" tree \
  "$WORK/private-prefix/share/alpineform"

printf 'installer tests passed\n'
