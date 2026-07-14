assert_remote "explicitly absent Compose project has no owned runtime or files" \
  "test -z \"\$(docker ps -aq --filter label=com.docker.compose.project=smoke)\" && test ! -e /opt/alpineform-docker/smoke/compose.yaml && test ! -e /opt/alpineform-docker/smoke/.env"
assert_remote "Docker remains installed and running after project removal" \
  "apk info -e docker && rc-service docker status 2>&1 | grep -q 'status: started'"
assert_local "adopted project is explicitly deleted" \
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1], encoding="utf-8")); a="host.cihost.docker.project[" + json.dumps("smoke") + "]"; assert any(c["address"] == a and c["action"] == "delete" for c in d["changes"])' \
  "$LOG_DIR/4.pre-apply-plan.json"
