assert_v1_survives() {
  local description=$1
  assert_remote "$description" \
    "test \"\$(/usr/local/bin/apf-ci-source-tool)\" = alpineform-musl-source-v1 && ! grep -Fq alpineform-ci-secret-sentinel /var/lib/alpineform/state.json"
}

wait_for_build_cleanup() {
  local attempt
  for attempt in $(seq 1 30); do
    if ssh_vm "! apk info | grep -Eq '^\\.alpineform-build-' && test -z \"\$(find /var/tmp/alpineform/builds /srv/alpineform-profile-builds /srv/alpineform-host-builds /srv/alpineform-instance-builds -mindepth 1 -print -quit 2>/dev/null)\" && test -z \"\$(find /run/alpineform/build-inputs /run/alpineform/build-runtime -mindepth 1 -print -quit 2>/dev/null)\""; then
      return 0
    fi
    sleep 1
  done
  return 1
}

expect_failed_apply() {
  local name=$1 config=$2
  shift 2
  if apf apply -f "$config" --auto-approve --color never "$@" >"$LOG_DIR/failure-$name.log" 2>&1; then
    fail "$name source build unexpectedly succeeded"
  fi
  cat "$LOG_DIR/failure-$name.log"
  wait_for_build_cleanup || fail "$name did not clean owned build state"
  assert_v1_survives "$name leaves the previous installation and state intact"
}

expect_failed_apply checksum "$CASE_DIR/checksum-invalid.apf.hcl"
assert_local "checksum mismatch is rejected before build execution" \
  grep -Fq 'input checksum mismatch before execution' "$LOG_DIR/failure-checksum.log"
expect_failed_apply compiler "$CASE_DIR/compiler-failure.apf.hcl" --debug
expect_failed_apply missing-output "$CASE_DIR/missing-output.apf.hcl"
expect_failed_apply symlink-output "$CASE_DIR/symlink-output.apf.hcl"

HOME="$APF_HOME" APF_SSH_CONFIG="$APF_HOME/.ssh/config" \
  "$APF_BIN" apply -f "$CASE_DIR/cancellation.apf.hcl" --auto-approve --color never \
  >"$LOG_DIR/failure-cancellation.log" 2>&1 &
cancel_pid=$!
workspace_ready=0
for attempt in $(seq 1 30); do
  if ssh_vm "test \"\$(find /srv/alpineform-instance-builds -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')\" = 1"; then
    workspace_ready=1
    break
  fi
  sleep 1
done
(( workspace_ready == 1 )) || fail "cancelled source build did not create its selected private workspace"
assert_remote "running build uses one private instance-root child without persisting its secret" \
  "test \"\$(find /srv/alpineform-instance-builds -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')\" = 1 && workspace=\$(find /srv/alpineform-instance-builds -mindepth 1 -maxdepth 1 -type d -print -quit) && test \"\$(stat -c '%U:%G:%a' \"\$workspace\")\" = root:root:700 && test -z \"\$(find /var/tmp/alpineform/builds /srv/alpineform-profile-builds /srv/alpineform-host-builds -mindepth 1 -print -quit 2>/dev/null)\" && ! grep -R -Fq alpineform-ci-secret-sentinel /srv/alpineform-instance-builds"
kill -TERM "$cancel_pid"
if wait "$cancel_pid"; then
  fail "cancelled source build unexpectedly succeeded"
fi
cat "$LOG_DIR/failure-cancellation.log"
wait_for_build_cleanup || fail "cancelled source build did not clean owned processes and workspace"
assert_v1_survives "cancelled source build leaves the previous installation and state intact"

run_remote "mount a small selected workspace root to force ENOSPC" \
  "mkdir -p /srv/alpineform-instance-builds && chmod 0700 /srv/alpineform-instance-builds && mount -t tmpfs -o size=2m,mode=0700 tmpfs /srv/alpineform-instance-builds"
set +e
apf apply -f "$CASE_DIR/disk-full.apf.hcl" --auto-approve --color never >"$LOG_DIR/failure-disk-full.log" 2>&1
disk_status=$?
set -e
cat "$LOG_DIR/failure-disk-full.log"
assert_local "ENOSPC reports the selected root, derived work path, and bounded capacity" \
  grep -Eq 'staging_root=/srv/alpineform-instance-builds work_path=/srv/alpineform-instance-builds/[0-9a-f]{64} available_kib=([0-9]+|unknown)' "$LOG_DIR/failure-disk-full.log"
disk_cleanup_status=0
wait_for_build_cleanup || disk_cleanup_status=$?
run_remote "unmount the source-build ENOSPC fixture" "umount /srv/alpineform-instance-builds"
assert_remote "source-build ENOSPC fixture is unmounted" \
  "! grep -Fq ' /srv/alpineform-instance-builds ' /proc/mounts"
if (( disk_status == 0 )); then
  fail "disk-full source build unexpectedly succeeded"
fi
if (( disk_cleanup_status != 0 )); then
  fail "disk-full source build did not clean owned state while the constrained root was mounted"
fi
assert_v1_survives "disk-full source build leaves the previous installation and state intact"

apf plan -f "$CASE_DIR/1.apf.hcl" --format json >"$LOG_DIR/leftover-plan.json"
read -r virtual owner identity marker < <(python3 - "$LOG_DIR/leftover-plan.json" <<'PY'
import json
import sys

document = json.load(open(sys.argv[1], encoding="utf-8"))
for change in document["changes"]:
    if change["address"].endswith(".build.dependencies"):
        desired = change["desired"]
        print(desired["virtual_package"], desired["owner_id"], desired["build_identity"], desired["marker_path"])
        break
else:
    raise SystemExit("source-build dependency node not found")
PY
)
workspace="/var/tmp/alpineform/builds/$identity"
run_remote "inject a recoverable owned virtual package and workspace" \
  "apk --quiet add --virtual '$virtual' build-base bubblewrap zlib-dev && mkdir -p '$workspace' \"\$(dirname '$marker')\" && chmod 0700 '$workspace' && printf '%s\\n%s\\n%s\\n' '$virtual' '$owner' '$identity' > '$marker' && chmod 0600 '$marker'"
apf apply -f "$CASE_DIR/1.apf.hcl" --auto-approve --color never >"$LOG_DIR/leftover-recovery.log"
assert_remote "owned interrupted-build leftovers are reconciled" \
  "! apk info -e '$virtual' && test ! -e '$marker' && test ! -e '$workspace' && grep -qx zlib-dev /etc/apk/world"

run_remote "drift the installed source-build output" \
  "printf drift > /usr/local/bin/apf-ci-source-tool && chmod 0700 /usr/local/bin/apf-ci-source-tool"
