assert_remote "Docker packages have exact world intent" \
  "apk info -e docker && apk info -e docker-cli-compose && grep -qx docker /etc/apk/world && grep -qx docker-cli-compose /etc/apk/world"
assert_remote "Docker package versions are recorded from Alpine community" \
  "versions=\$(apk info -v docker docker-cli-compose); printf '%s\n' \"\$versions\"; printf '%s\n' \"\$versions\" | grep -Eq '^docker-[0-9]' && printf '%s\n' \"\$versions\" | grep -Eq '^docker-cli-compose-[0-9]'"
assert_remote "Docker community repository is managed" \
  "grep -Fq '# BEGIN ALPINEFORM REPOSITORY alpineform-docker-community' /etc/apk/repositories && grep -Fq 'https://dl-cdn.alpinelinux.org/alpine/v3.24/community' /etc/apk/repositories"
assert_remote "Docker OpenRC service is enabled and running" \
  "test -e /etc/runlevels/default/docker && rc-service docker status 2>&1 | grep -q 'status: started' && docker info >/dev/null"
assert_remote "Docker daemon configuration is canonical and active" \
  "test \"\$(stat -c '%U:%G:%a' /etc/docker/daemon.json)\" = root:root:644 && grep -Fq '\"log-driver\": \"json-file\"' /etc/docker/daemon.json && docker info --format '{{.LoggingDriver}}' | grep -qx json-file"
assert_remote "Docker group membership is present" \
  "awk -F: '\$1 == \"docker\" && (\",\" \$4 \",\") ~ /,operator,/' /etc/group | grep -q ."
assert_remote "Compose project is running with protected environment" \
  "test \"\$(docker ps -q --filter label=com.docker.compose.project=smoke | wc -l | tr -d ' ')\" = 2 && id=\$(docker ps -q --filter label=com.docker.compose.project=smoke --filter label=com.docker.compose.service=smoke); test -n \"\$id\" && docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' \"\$id\" | grep -Fqx 'APF_SECRET=alpineform-ci-secret-sentinel'"
assert_remote "Compose files are private regular files" \
  "test -f /opt/alpineform-docker/smoke/compose.yaml && test ! -L /opt/alpineform-docker/smoke/compose.yaml && test \"\$(stat -c '%U:%G:%a' /opt/alpineform-docker/smoke)\" = root:root:755 && test \"\$(stat -c '%U:%G:%a' /opt/alpineform-docker/smoke/compose.yaml)\" = root:root:600 && test \"\$(stat -c '%U:%G:%a' /opt/alpineform-docker/smoke/.env)\" = root:root:600"
assert_remote "Fresh stopped Compose project has stopped runtime and a named volume" \
  "test -n \"\$(docker ps -aq --filter label=com.docker.compose.project=retired --filter label=com.docker.compose.service=retired)\" && test -z \"\$(docker ps -q --filter label=com.docker.compose.project=retired)\" && docker volume inspect retired_retired-data >/dev/null"

if [[ "$APF_TEST_PHASE" == applied ]]; then
  before_compose="$(ssh_vm "sha256sum /opt/alpineform-docker/smoke/compose.yaml | cut -d ' ' -f1")"
  before_container="$(ssh_vm "docker ps -q --filter label=com.docker.compose.project=smoke --filter label=com.docker.compose.service=smoke")"
  before_state="$(ssh_vm "sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1")"
  if apf apply -f "$CASE_DIR/invalid-compose.apf.hcl" --auto-approve --color never >"$LOG_DIR/docker-invalid-compose.log" 2>&1; then
    fail "invalid Compose configuration unexpectedly applied"
  fi
  if grep -Fq 'alpineform-ci-secret-sentinel' "$LOG_DIR/docker-invalid-compose.log"; then
    fail "invalid protected Compose failure leaked the secret sentinel"
  fi
  after_compose="$(ssh_vm "sha256sum /opt/alpineform-docker/smoke/compose.yaml | cut -d ' ' -f1")"
  after_container="$(ssh_vm "docker ps -q --filter label=com.docker.compose.project=smoke --filter label=com.docker.compose.service=smoke")"
  after_state="$(ssh_vm "sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1")"
  assert_local "invalid Compose preflight preserved persistent content" test "$before_compose" = "$after_compose"
  assert_local "invalid Compose preflight preserved the running container" test "$before_container" = "$after_container"
  assert_local "invalid Compose preflight preserved AlpineForm state" test "$before_state" = "$after_state"

  before_daemon="$(ssh_vm "sha256sum /etc/docker/daemon.json | cut -d ' ' -f1")"
  before_pid="$(ssh_vm "pidof dockerd")"
  before_container="$(ssh_vm "docker ps -q --filter label=com.docker.compose.project=smoke --filter label=com.docker.compose.service=smoke")"
  before_state="$(ssh_vm "sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1")"
  if apf apply -f "$CASE_DIR/invalid-daemon.apf.hcl" --auto-approve --color never >"$LOG_DIR/docker-invalid-daemon.log" 2>&1; then
    fail "Docker-invalid daemon configuration unexpectedly applied"
  fi
  if ! grep -Fq 'apply.docker_daemon_config' "$LOG_DIR/docker-invalid-daemon.log"; then
    fail "Docker-invalid daemon failure did not reach dockerd validation"
  fi
  after_daemon="$(ssh_vm "sha256sum /etc/docker/daemon.json | cut -d ' ' -f1")"
  after_pid="$(ssh_vm "pidof dockerd")"
  after_container="$(ssh_vm "docker ps -q --filter label=com.docker.compose.project=smoke --filter label=com.docker.compose.service=smoke")"
  after_state="$(ssh_vm "sha256sum /var/lib/alpineform/state.json | cut -d ' ' -f1")"
  assert_local "dockerd validation preserved the previous daemon file" test "$before_daemon" = "$after_daemon"
  assert_local "dockerd validation failure did not restart Docker" test "$before_pid" = "$after_pid"
  assert_local "dockerd validation failure preserved the running project" test "$before_container" = "$after_container"
  assert_local "dockerd validation failure preserved AlpineForm state" test "$before_state" = "$after_state"

  run_remote "crash Docker to exercise degraded observation and recovery" \
    "kill -9 \$(pidof dockerd); rc-service docker stop >/dev/null 2>&1 || true; while docker info >/dev/null 2>&1; do sleep 1; done"
  if apf check -f "$CASE_DIR/1.apf.hcl" >"$LOG_DIR/docker-crash-check.log" 2>&1; then
    fail "check unexpectedly accepted a crashed Docker daemon"
  fi
  apf apply -f "$CASE_DIR/1.apf.hcl" --auto-approve --color never | tee "$LOG_DIR/docker-crash-repair.log"
  apf plan -f "$CASE_DIR/1.apf.hcl" --format json | tee "$LOG_DIR/docker-crash-repair-noop.json"
  python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/docker-crash-repair-noop.json"
  apf check -f "$CASE_DIR/1.apf.hcl" --color never | tee "$LOG_DIR/docker-crash-repair-check.log"
  assert_remote "crashed Docker and degraded Compose state recover together" \
    "rc-service docker status 2>&1 | grep -q 'status: started' && test \"\$(docker ps -q --filter label=com.docker.compose.project=smoke | wc -l | tr -d ' ')\" = 2"
fi

if [[ "$APF_TEST_PHASE" == repaired ]]; then
  repaired_pid="$(ssh_vm "pidof dockerd")"
  assert_local "daemon metadata/content drift triggered a Docker restart" test "$docker_pid_before_drift" != "$repaired_pid"
fi
