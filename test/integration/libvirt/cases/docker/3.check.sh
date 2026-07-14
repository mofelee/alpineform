assert_remote "forgotten Compose project files remain unmanaged" \
  "test -f /opt/alpineform-docker/smoke/compose.yaml && test -f /opt/alpineform-docker/smoke/.env"
assert_remote "forgotten Compose project runtime remains stopped" \
  "test -n \"\$(docker ps -aq --filter label=com.docker.compose.project=smoke)\" && test -z \"\$(docker ps -q --filter label=com.docker.compose.project=smoke)\""

if [[ "$APF_TEST_PHASE" == rebooted ]]; then
  apf plan -f "$CASE_DIR/adopt.apf.hcl" --format json | tee "$LOG_DIR/docker-adopt-plan.json"
  assert_local "forgotten stopped project is planned for adoption" \
    python3 -c 'import json,sys; d=json.load(open(sys.argv[1], encoding="utf-8")); a="host.cihost.docker.project[" + json.dumps("smoke") + "]"; assert any(c["address"] == a and c["action"] == "adopt" for c in d["changes"])' \
    "$LOG_DIR/docker-adopt-plan.json"
  apf apply -f "$CASE_DIR/adopt.apf.hcl" --auto-approve --color never | tee "$LOG_DIR/docker-adopt-apply.log"
  apf plan -f "$CASE_DIR/adopt.apf.hcl" --format json | tee "$LOG_DIR/docker-adopt-noop-plan.json"
  python3 "$SCRIPT_DIR/assert-noop-plan.py" "$LOG_DIR/docker-adopt-noop-plan.json"
  apf check -f "$CASE_DIR/adopt.apf.hcl" --color never | tee "$LOG_DIR/docker-adopt-check.log"
fi
