assert_remote "Compose project is cleanly stopped" \
  "ids=\$(docker ps -aq --filter label=com.docker.compose.project=smoke); test -n \"\$ids\" && test -z \"\$(docker ps -q --filter label=com.docker.compose.project=smoke)\""
assert_remote "Docker service remains running while project is stopped" \
  "rc-service docker status 2>&1 | grep -q 'status: started'"
assert_local "removed project used recorded scoped destroy" \
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1], encoding="utf-8")); a="host.cihost.docker.project[" + json.dumps("retired") + "]"; assert any(c["address"] == a and c["action"] == "destroy" for c in d["changes"])' \
  "$LOG_DIR/2.pre-apply-plan.json"
assert_remote "recorded destroy removed only the retired project runtime and files" \
  "test -z \"\$(docker ps -aq --filter label=com.docker.compose.project=retired)\" && test -z \"\$(docker network ls -q --filter label=com.docker.compose.project=retired)\" && test ! -e /opt/alpineform-docker/retired/compose.yaml && test ! -e /opt/alpineform-docker/retired/.env && test -n \"\$(docker ps -aq --filter label=com.docker.compose.project=smoke)\""
assert_remote "recorded destroy retained the retired project's named volume" \
  "docker volume inspect retired_retired-data >/dev/null"
