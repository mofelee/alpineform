COMPONENT_SOURCE_ADDRESSES=(
  'host.cihost.component.binary.artifact.source["amd64"]'
  'host.cihost.component.config_file.artifact.source["any"]'
  'host.cihost.component.protected_archive.artifact.source["amd64"]'
  'host.cihost.component.root_ca.artifact.source["any"]'
)
COMPONENT_INSTALL_ADDRESSES=(
  'host.cihost.component.binary.artifact.install["/usr/local/bin/apf-protected-tool"]'
  'host.cihost.component.config_file.artifact.install["/etc/alpineform-protected.conf"]'
  'host.cihost.component.protected_archive.artifact.install["/opt/alpineform-protected"]'
  'host.cihost.component.root_ca.artifact.install["/usr/local/share/ca-certificates/alpineform-protected-root.crt"]'
)
LITERAL_SOURCE_ADDRESSES=(
  'host.cihost.component.archive.artifact.source["any"]'
  'host.cihost.component.tool.artifact.source["amd64"]'
)
LITERAL_INSTALL_ADDRESSES=(
  'host.cihost.component.archive.artifact.install["/opt/apf-ci-bundle"]'
  'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]'
)
LITERAL_FILE_ADDRESS='host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]'
LITERAL_SCRIPT_ADDRESS='host.cihost.script["record_component_change"]'
COMPONENT_PACKAGE_ADDRESS='host.cihost.component.root_ca.packages.package["ca-certificates"]'
COMPONENT_DOWNLOADER_ADDRESS='host.cihost.packages.package["wget"]'

assert_no_protected_file() {
  local path=$1 description=$2 value
  for value in "${APF_PROTECTED_VALUES[@]}" "${APF_PROTECTED_VALUE_DIGESTS[@]}"; do
    if [[ -n "$value" ]] && LC_ALL=C grep -aF -- "$value" "$path" >/dev/null 2>&1; then
      printf 'protected artifact sentinel detected; unsafe output removed\n' >"$path"
      fail "$description leaked a protected artifact sentinel"
      return 1
    fi
  done
}

assert_no_protected_logs() {
  local path
  if [[ -e "${APF_COMPONENT_STDERR_LEAK:-$CASE_WORK/components-apf.stderr.leak}" ]]; then
    fail "AlpineForm stderr leaked a protected artifact sentinel"
  fi
  while IFS= read -r path; do
    assert_no_protected_file "$path" "$(basename "$path")"
  done < <(find "$LOG_DIR" -type f | sort)
}

capture_remote_metadata() {
  local metadata="$CASE_WORK/components-remote-metadata.$CURRENT_STEP.$APF_TEST_PHASE.tmp"
  ssh_vm 'set -eu
if [ -f /var/lib/alpineform/state.json ]; then
  printf "%s\n" STATE
  cat /var/lib/alpineform/state.json
fi
for root in /var/cache/alpineform/components /var/lib/alpineform/ca-certificates /opt/alpineform-protected; do
  if [ -e "$root" ]; then
    find "$root" -exec stat -c "%n|%F|%u|%g|%a|%s" {} \; | LC_ALL=C sort
  fi
done
for file in \
  /var/lib/alpineform/ca-certificates/root_ca/protected/any.updated \
  /opt/alpineform-protected/.alpineform-artifact.sha256 \
  /opt/alpineform-protected/.alpineform-manifest.sha256; do
  if [ -f "$file" ]; then
    printf "METADATA %s\n" "$file"
    cat "$file"
  fi
done
' >"$metadata"
  assert_no_protected_file "$metadata" "remote state and cache metadata"
  mv "$metadata" "$LOG_DIR/$CURRENT_STEP.$APF_TEST_PHASE.remote-metadata.log"
}

capture_server_log() {
  local captured="$CASE_WORK/components-server-paths.$CURRENT_STEP.$APF_TEST_PHASE.tmp"
  local server_output="$CASE_WORK/components-server-output.$CURRENT_STEP.$APF_TEST_PHASE.tmp"
  if ! ssh_vm test -f /var/tmp/apf-component-http/requests.log; then
    return
  fi
  ssh_vm cat /var/tmp/apf-component-http/requests.log >"$captured"
  assert_no_protected_file "$captured" "sanitized component server log"
  python3 - "$captured" <<'PY'
import pathlib
import sys

allowed = {
    "/bundle.tar.gz",
    "/tool",
    "/mirror-a/tool-amd64",
    "/mirror-a/component.conf",
    "/mirror-a/bundle-amd64.tar.gz",
    "/mirror-a/root.crt",
    "/mirror-b/tool-amd64",
    "/mirror-b/component.conf",
    "/mirror-b/bundle-amd64.tar.gz",
    "/mirror-b/root.crt",
}
for line in pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").splitlines():
    if line not in allowed or "?" in line or "#" in line:
        raise SystemExit(f"unsanitized or unexpected fixture request path: {line!r}")
PY
  mv "$captured" "$LOG_DIR/$CURRENT_STEP.$APF_TEST_PHASE.server-paths.log"
  ssh_vm cat /var/tmp/apf-component-http/server.out >"$server_output"
  assert_no_protected_file "$server_output" "protected component server output"
  mv "$server_output" "$LOG_DIR/$CURRENT_STEP.$APF_TEST_PHASE.server-output.log"
}

assert_all_protected_surfaces() {
  capture_remote_metadata
  capture_server_log
  assert_no_protected_logs
}

ensure_component_fixture_server() {
  if ssh_vm 'set -eu
pid_file=/var/tmp/apf-component-http/server.pid
test -s "$pid_file"
pid=$(cat "$pid_file")
kill -0 "$pid"
tr "\000" " " <"/proc/$pid/cmdline" | grep -Fq /var/tmp/apf-component-http/fixture-server.py
'; then
    return
  fi
  run_remote "restart the query-sanitizing component fixture server after reboot" \
    "nohup python3 /var/tmp/apf-component-http/fixture-server.py --bind 127.0.0.1 --port 18080 --directory /var/tmp/apf-component-http --log /var/tmp/apf-component-http/requests.log >>/var/tmp/apf-component-http/server.out 2>&1 & echo \$! > /var/tmp/apf-component-http/server.pid"
  assert_remote "restarted component fixture server returns the pinned protected tool" \
    "attempt=0; until pid=\$(cat /var/tmp/apf-component-http/server.pid) && kill -0 \"\$pid\" 2>/dev/null && test \"\$(wget -qO- 'http://127.0.0.1:18080/mirror-a/tool-amd64?reboot-readiness=1' | sha256sum | awk '{print \$1}')\" = '$APF_TOOL_SHA'; do attempt=\$((attempt + 1)); test \"\$attempt\" -lt 20; sleep 1; done"
}

assert_selected_amd64_sources() {
  local plan=$1 expected_mode=$2
  python3 - "$plan" "$expected_mode" <<'PY'
import json
import sys

path, expected_mode = sys.argv[1:]
with open(path, encoding="utf-8") as plan_file:
    document = json.load(plan_file)
if document.get("format_version") != "alpineform.plan.alpha1" or document.get("mode") != expected_mode:
    raise SystemExit(f"unexpected plan header: {document.get('format_version')!r}, {document.get('mode')!r}")
protected_sources = {
    'host.cihost.component.binary.artifact.source["amd64"]',
    'host.cihost.component.config_file.artifact.source["any"]',
    'host.cihost.component.protected_archive.artifact.source["amd64"]',
    'host.cihost.component.root_ca.artifact.source["any"]',
}
literal_sources = {
    'host.cihost.component.archive.artifact.source["any"]',
    'host.cihost.component.tool.artifact.source["amd64"]',
}
graph_sources = {
    node.get("address")
    for node in document.get("graph", [])
    if node.get("kind") == "component_artifact_source"
}
if graph_sources != protected_sources | literal_sources:
    raise SystemExit(f"unexpected selected sources: expected {sorted(protected_sources | literal_sources)!r}, got {sorted(graph_sources)!r}")
if any('source["arm64"]' in address for address in graph_sources):
    raise SystemExit(f"arm64 source selected on amd64 VM: {sorted(graph_sources)!r}")
summary = document.get("summary", {})
expected_graph_nodes = 24 if expected_mode == "offline" else 16
if summary.get("managed_resources") != 16 or summary.get("graph_nodes") != expected_graph_nodes:
    raise SystemExit(f"unexpected combined component graph counts: {summary!r}")
if expected_mode == "offline":
    for name, count in {"create": 16, "update": 0, "adopt": 0, "delete": 0, "destroy": 0, "forget": 0, "no_op": 0}.items():
        if summary.get(name, 0) != count:
            raise SystemExit(f"expected offline summary.{name}={count}, got {summary.get(name, 0)!r}")
protected_addresses = protected_sources | {
    'host.cihost.component.binary.artifact.install["/usr/local/bin/apf-protected-tool"]',
    'host.cihost.component.config_file.artifact.install["/etc/alpineform-protected.conf"]',
    'host.cihost.component.protected_archive.artifact.install["/opt/alpineform-protected"]',
    'host.cihost.component.root_ca.artifact.install["/usr/local/share/ca-certificates/alpineform-protected-root.crt"]',
}
changes = {change.get("address"): change for change in document.get("changes", [])}
for address in protected_addresses:
    change = changes.get(address)
    if change is None or change.get("desired") != {"protected": True}:
        raise SystemExit(f"protected plan entry is not redacted: {address}: {change!r}")
script_address = 'host.cihost.script["record_component_change"]'
script_relationships = [
    'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]',
    'host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]',
]
graph_by_address = {node.get("address"): node for node in document.get("graph", [])}
script_node = graph_by_address.get(script_address)
if script_node is None or script_node.get("depends_on") != script_relationships or script_node.get("triggered_by") != script_relationships:
    raise SystemExit(f"shared literal-source graph relationships changed: {script_node!r}")
script_change = changes.get(script_address)
expected_active = [] if script_change is not None and script_change.get("action") == "no-op" else script_relationships
if script_change is None or script_change.get("depends_on") != script_relationships or script_change.get("triggered_by", []) != expected_active:
    raise SystemExit(f"shared literal-source script relationships changed: {script_change!r}")
PY
}

assert_four_install_repair_plan() {
  local plan=$1
  python3 - "$plan" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as plan_file:
    document = json.load(plan_file)
sources = {
    'host.cihost.component.binary.artifact.source["amd64"]',
    'host.cihost.component.config_file.artifact.source["any"]',
    'host.cihost.component.protected_archive.artifact.source["amd64"]',
    'host.cihost.component.root_ca.artifact.source["any"]',
}
installs = {
    'host.cihost.component.binary.artifact.install["/usr/local/bin/apf-protected-tool"]',
    'host.cihost.component.config_file.artifact.install["/etc/alpineform-protected.conf"]',
    'host.cihost.component.protected_archive.artifact.install["/opt/alpineform-protected"]',
    'host.cihost.component.root_ca.artifact.install["/usr/local/share/ca-certificates/alpineform-protected-root.crt"]',
}
packages = {
    'host.cihost.component.root_ca.packages.package["ca-certificates"]',
    'host.cihost.packages.package["wget"]',
}
literal = {
    'host.cihost.component.archive.artifact.install["/opt/apf-ci-bundle"]',
    'host.cihost.component.archive.artifact.source["any"]',
    'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]',
    'host.cihost.component.tool.artifact.source["amd64"]',
    'host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]',
    'host.cihost.script["record_component_change"]',
}
expected = {address: "no-op" for address in sources}
expected.update({address: "update" for address in installs})
expected.update({address: "no-op" for address in packages})
expected.update({address: "no-op" for address in literal})
changes = {change.get("address"): change for change in document.get("changes", [])}
actual = {address: change.get("action") for address, change in changes.items()}
if actual != expected:
    raise SystemExit(f"unexpected install drift plan: expected {expected!r}, got {actual!r}")
for address in installs:
    if changes[address].get("triggered_by", []) != []:
        raise SystemExit(f"install repair retained an inactive source trigger: {changes[address]!r}")
script = changes['host.cihost.script["record_component_change"]']
if script.get("triggered_by", []) != []:
    raise SystemExit(f"protected install drift reruns the legacy shared script: {script!r}")
summary = document.get("summary", {})
for name, count in {"create": 0, "update": 4, "adopt": 0, "delete": 0, "destroy": 0, "forget": 0, "no_op": 12}.items():
    if summary.get(name, 0) != count:
        raise SystemExit(f"expected summary.{name}={count}, got {summary.get(name, 0)!r}")
PY
}

assert_cache_only_repair_plan() {
  local plan=$1
  python3 - "$plan" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as plan_file:
    document = json.load(plan_file)
sources = {
    'host.cihost.component.binary.artifact.source["amd64"]',
    'host.cihost.component.config_file.artifact.source["any"]',
    'host.cihost.component.protected_archive.artifact.source["amd64"]',
    'host.cihost.component.root_ca.artifact.source["any"]',
}
verified_installs = {
    'host.cihost.component.binary.artifact.install["/usr/local/bin/apf-protected-tool"]',
    'host.cihost.component.config_file.artifact.install["/etc/alpineform-protected.conf"]',
    'host.cihost.component.root_ca.artifact.install["/usr/local/share/ca-certificates/alpineform-protected-root.crt"]',
}
archive_source = 'host.cihost.component.protected_archive.artifact.source["amd64"]'
archive_install = 'host.cihost.component.protected_archive.artifact.install["/opt/alpineform-protected"]'
packages = {
    'host.cihost.component.root_ca.packages.package["ca-certificates"]',
    'host.cihost.packages.package["wget"]',
}
literal = {
    'host.cihost.component.archive.artifact.install["/opt/apf-ci-bundle"]',
    'host.cihost.component.archive.artifact.source["any"]',
    'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]',
    'host.cihost.component.tool.artifact.source["amd64"]',
    'host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]',
    'host.cihost.script["record_component_change"]',
}
expected = {address: "create" for address in sources}
expected.update({address: "no-op" for address in verified_installs})
expected[archive_install] = "update"
expected.update({address: "no-op" for address in packages})
expected.update({address: "no-op" for address in literal})
changes = {change.get("address"): change for change in document.get("changes", [])}
actual = {address: change.get("action") for address, change in changes.items()}
if actual != expected:
    raise SystemExit(f"unexpected cache-only repair plan: expected {expected!r}, got {actual!r}")
for address in verified_installs:
    if changes[address].get("triggered_by", []) != []:
        raise SystemExit(f"verified install retained an active cache trigger: {changes[address]!r}")
archive_change = changes[archive_install]
if archive_change.get("depends_on") != [archive_source] or archive_change.get("triggered_by") != [archive_source]:
    raise SystemExit(f"protected archive repair lost its conservative source trigger: {archive_change!r}")
script = changes['host.cihost.script["record_component_change"]']
if script.get("triggered_by", []) != []:
    raise SystemExit(f"protected cache repair reruns the legacy shared script: {script!r}")
summary = document.get("summary", {})
for name, count in {"create": 4, "update": 1, "adopt": 0, "delete": 0, "destroy": 0, "forget": 0, "no_op": 11}.items():
    if summary.get(name, 0) != count:
        raise SystemExit(f"expected summary.{name}={count}, got {summary.get(name, 0)!r}")
PY
}

assert_combined_component_repair_plan() {
  local plan=$1
  python3 - "$plan" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as plan_file:
    document = json.load(plan_file)
protected_sources = {
    'host.cihost.component.binary.artifact.source["amd64"]',
    'host.cihost.component.config_file.artifact.source["any"]',
    'host.cihost.component.protected_archive.artifact.source["amd64"]',
    'host.cihost.component.root_ca.artifact.source["any"]',
}
protected_installs = {
    'host.cihost.component.binary.artifact.install["/usr/local/bin/apf-protected-tool"]',
    'host.cihost.component.config_file.artifact.install["/etc/alpineform-protected.conf"]',
    'host.cihost.component.protected_archive.artifact.install["/opt/alpineform-protected"]',
    'host.cihost.component.root_ca.artifact.install["/usr/local/share/ca-certificates/alpineform-protected-root.crt"]',
}
literal_sources = {
    'host.cihost.component.archive.artifact.source["any"]',
    'host.cihost.component.tool.artifact.source["amd64"]',
}
literal_repairs = {
    'host.cihost.component.archive.artifact.install["/opt/apf-ci-bundle"]',
    'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]',
    'host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]',
    'host.cihost.script["record_component_change"]',
}
packages = {
    'host.cihost.component.root_ca.packages.package["ca-certificates"]',
    'host.cihost.packages.package["wget"]',
}
expected = {address: "no-op" for address in protected_sources | literal_sources | packages}
expected.update({address: "update" for address in protected_installs | literal_repairs})
changes = {change.get("address"): change for change in document.get("changes", [])}
actual = {address: change.get("action") for address, change in changes.items()}
if actual != expected:
    raise SystemExit(f"unexpected combined repair plan: expected {expected!r}, got {actual!r}")
for address in protected_installs:
    if changes[address].get("triggered_by", []) != []:
        raise SystemExit(f"protected install repair retained an inactive source trigger: {changes[address]!r}")
relationships = [
    'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]',
    'host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]',
]
script = changes['host.cihost.script["record_component_change"]']
if script.get("depends_on") != relationships or script.get("triggered_by") != relationships:
    raise SystemExit(f"combined repair lost shared literal triggers: {script!r}")
summary = document.get("summary", {})
for name, count in {"create": 0, "update": 8, "adopt": 0, "delete": 0, "destroy": 0, "forget": 0, "no_op": 8}.items():
    if summary.get(name, 0) != count:
        raise SystemExit(f"expected summary.{name}={count}, got {summary.get(name, 0)!r}")
PY
}

assert_exact_teardown_plan() {
  local plan=$1
  python3 - "$plan" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as plan_file:
    document = json.load(plan_file)
expected = {
    'host.cihost.component.archive.artifact.install["/opt/apf-ci-bundle"]': "destroy",
    'host.cihost.component.archive.artifact.source["any"]': "delete",
    'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]': "destroy",
    'host.cihost.component.tool.artifact.source["amd64"]': "delete",
    'host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]': "forget",
    'host.cihost.script["record_component_change"]': "forget",
    'host.cihost.component.binary.artifact.source["amd64"]': "delete",
    'host.cihost.component.config_file.artifact.source["any"]': "delete",
    'host.cihost.component.protected_archive.artifact.source["amd64"]': "delete",
    'host.cihost.component.root_ca.artifact.source["any"]': "delete",
    'host.cihost.component.binary.artifact.install["/usr/local/bin/apf-protected-tool"]': "destroy",
    'host.cihost.component.config_file.artifact.install["/etc/alpineform-protected.conf"]': "destroy",
    'host.cihost.component.protected_archive.artifact.install["/opt/alpineform-protected"]': "destroy",
    'host.cihost.component.root_ca.artifact.install["/usr/local/share/ca-certificates/alpineform-protected-root.crt"]': "destroy",
    'host.cihost.component.root_ca.packages.package["ca-certificates"]': "forget",
    'host.cihost.packages.package["wget"]': "forget",
}
actual = {change.get("address"): change.get("action") for change in document.get("changes", [])}
if actual != expected:
    raise SystemExit(f"unexpected teardown plan: expected {expected!r}, got {actual!r}")
summary = document.get("summary", {})
for name, count in {"create": 0, "update": 0, "adopt": 0, "delete": 6, "destroy": 6, "forget": 4, "no_op": 0}.items():
    if summary.get(name, 0) != count:
        raise SystemExit(f"expected summary.{name}={count}, got {summary.get(name, 0)!r}")
if summary.get("managed_resources") != 16 or summary.get("graph_nodes") != 16:
    raise SystemExit(f"unexpected teardown counts: {summary!r}")
PY
}

assert_component_state() {
  local description=$1
  ASSERTION_COUNT=$((ASSERTION_COUNT + 1))
  log "ASSERT $ASSERTION_COUNT: $description"
  ssh_vm python3 - "$APF_LEGACY_TOOL_SHA" "$APF_LEGACY_ARCHIVE_SHA" <<'PY' || fail "$description"
import json
import sys

legacy_tool_sha, legacy_archive_sha = sys.argv[1:]

sources = {
    'host.cihost.component.binary.artifact.source["amd64"]': "/var/cache/alpineform/components/binary/protected/amd64/artifact",
    'host.cihost.component.config_file.artifact.source["any"]': "/var/cache/alpineform/components/config_file/protected/any/artifact",
    'host.cihost.component.protected_archive.artifact.source["amd64"]': "/var/cache/alpineform/components/protected_archive/protected/amd64/artifact",
    'host.cihost.component.root_ca.artifact.source["any"]': "/var/cache/alpineform/components/root_ca/protected/any/artifact",
}
installs = {
    'host.cihost.component.binary.artifact.install["/usr/local/bin/apf-protected-tool"]': "/usr/local/bin/apf-protected-tool",
    'host.cihost.component.config_file.artifact.install["/etc/alpineform-protected.conf"]': "/etc/alpineform-protected.conf",
    'host.cihost.component.protected_archive.artifact.install["/opt/alpineform-protected"]': "/opt/alpineform-protected",
    'host.cihost.component.root_ca.artifact.install["/usr/local/share/ca-certificates/alpineform-protected-root.crt"]': "/usr/local/share/ca-certificates/alpineform-protected-root.crt",
}
literal_sources = {
    'host.cihost.component.archive.artifact.source["any"]': f"/var/cache/alpineform/components/archive/{legacy_archive_sha}/artifact",
    'host.cihost.component.tool.artifact.source["amd64"]': f"/var/cache/alpineform/components/tool/{legacy_tool_sha}/artifact",
}
literal_installs = {
    'host.cihost.component.archive.artifact.install["/opt/apf-ci-bundle"]': "/opt/apf-ci-bundle",
    'host.cihost.component.tool.artifact.install["/usr/local/bin/apf-ci-tool"]': "/usr/local/bin/apf-ci-tool",
}
literal_forgotten = {
    'host.cihost.component.tool.files.file["/etc/apf-ci-component.conf"]',
    'host.cihost.script["record_component_change"]',
}
packages = {
    'host.cihost.component.root_ca.packages.package["ca-certificates"]',
    'host.cihost.packages.package["wget"]',
}
with open("/var/lib/alpineform/state.json", encoding="utf-8") as state_file:
    state = json.load(state_file)
if state.get("product") != "alpineform" or state.get("schema_version") != 2 or state.get("host") != "cihost":
    raise SystemExit(f"unexpected state header: {state!r}")
resources = state.get("resources", {})
expected = set(sources) | set(installs) | set(literal_sources) | set(literal_installs) | literal_forgotten | packages
if set(resources) != expected:
    raise SystemExit(f"unexpected state resources: expected {sorted(expected)!r}, got {sorted(resources)!r}")
if state.get("component_identities", {}) != {}:
    raise SystemExit(f"unexpected physical component bindings: {state.get('component_identities')!r}")
for address, path in {**sources, **installs}.items():
    resource = resources[address]
    if resource.get("protected") is not True or "desired" in resource or "observed" in resource:
        raise SystemExit(f"protected resource persisted raw desired or observed data: {address}: {resource!r}")
    deletion = resource.get("delete", {})
    if deletion.get("path") != path:
        raise SystemExit(f"unstable deletion identity for {address}: {deletion!r}")
for address, path in {**literal_sources, **literal_installs}.items():
    resource = resources[address]
    if resource.get("protected") is True:
        raise SystemExit(f"literal source resource became protected: {address}: {resource!r}")
    if resource.get("delete", {}).get("path") != path:
        raise SystemExit(f"unstable literal deletion identity for {address}: {resource!r}")
for address in literal_forgotten:
    if resources[address].get("delete_behavior", "") != "":
        raise SystemExit(f"legacy forget behavior changed for {address}: {resources[address]!r}")
PY
}

assert_component_runtime() {
  assert_remote "protected binary and file installs are converged" \
    "test \"\$(/usr/local/bin/apf-protected-tool)\" = alpineform-integration-tool && test \"\$(stat -c %a /usr/local/bin/apf-protected-tool)\" = 755 && grep -qx 'mode=protected-artifact-inputs' /etc/alpineform-protected.conf && test \"\$(stat -c %a /etc/alpineform-protected.conf)\" = 644"
  assert_remote "protected archive tree and static provider marker are converged" \
    "grep -qx 'AlpineForm archive integration fixture' /opt/alpineform-protected/bin/message.txt && grep -qx 'architecture=amd64' /opt/alpineform-protected/share/platform.txt && grep -qx 'libc=musl' /opt/alpineform-protected/share/platform.txt && test ! -e /opt/alpineform-protected/unmanaged && test \"\$(cat /opt/alpineform-protected/.alpineform-artifact.sha256)\" = alpineform-protected-archive-v1"
  assert_remote "four protected caches retain stable labelled paths" \
    "cmp -s /var/cache/alpineform/components/binary/protected/amd64/artifact /var/tmp/apf-component-http/mirror-a/tool-amd64 && cmp -s /var/cache/alpineform/components/config_file/protected/any/artifact /var/tmp/apf-component-http/mirror-a/component.conf && cmp -s /var/cache/alpineform/components/protected_archive/protected/amd64/artifact /var/tmp/apf-component-http/mirror-a/bundle-amd64.tar.gz && cmp -s /var/cache/alpineform/components/root_ca/protected/any/artifact /var/tmp/apf-component-http/mirror-a/root.crt && test \"\$(find /var/cache/alpineform/components -type f -path '*/protected/*/artifact' | wc -l | tr -d ' ')\" = 4 && test \"\$(find /var/cache/alpineform/components -type f -path '*/protected/*/artifact' -exec stat -c %a {} \; | sort -u)\" = 600"
  assert_remote "protected CA install, trust bundle, and static marker are converged" \
    "cmp -s /usr/local/share/ca-certificates/alpineform-protected-root.crt /var/tmp/apf-component-http/mirror-a/root.crt && grep -aFq '$APF_CA_PROBE' /etc/ssl/certs/ca-certificates.crt && test \"\$(stat -c %a /usr/local/share/ca-certificates/alpineform-protected-root.crt)\" = 644 && test \"\$(stat -c %a /var/lib/alpineform/ca-certificates/root_ca/protected/any.updated)\" = 600 && test \"\$(cat /var/lib/alpineform/ca-certificates/root_ca/protected/any.updated)\" = alpineform-protected-ca-v1"
}

assert_literal_component_runtime() {
  local expected_runs=1
  if [[ "$CURRENT_STEP" != 1 || "$APF_TEST_PHASE" != applied ]]; then
    expected_runs=2
  fi
  assert_remote "literal binary, file, and archive resources remain converged" \
    "test \"\$(sha256sum /usr/local/bin/apf-ci-tool | awk '{print \$1}')\" = '$APF_LEGACY_TOOL_SHA' && test \"\$(stat -c %a /usr/local/bin/apf-ci-tool)\" = 755 && grep -qx 'enabled=true' /etc/apf-ci-component.conf && grep -qx 'AlpineForm archive integration fixture' /opt/apf-ci-bundle/bin/message.txt && grep -qx 'libc=musl' /opt/apf-ci-bundle/share/platform.txt && test ! -e /opt/apf-ci-bundle/unmanaged"
  assert_remote "literal artifact caches retain their checksum identities" \
    "cmp -s /var/cache/alpineform/components/tool/$APF_LEGACY_TOOL_SHA/artifact /var/tmp/apf-component-http/tool && cmp -s /var/cache/alpineform/components/archive/$APF_LEGACY_ARCHIVE_SHA/artifact /var/tmp/apf-component-http/bundle.tar.gz && test \"\$(stat -c %a /var/cache/alpineform/components/tool/$APF_LEGACY_TOOL_SHA/artifact)\" = 600 && test \"\$(stat -c %a /var/cache/alpineform/components/archive/$APF_LEGACY_ARCHIVE_SHA/artifact)\" = 600"
  assert_remote "literal triggers remain deduplicated independently of protected repairs" \
    "test \"\$(wc -l < /var/lib/alpineform/component-ci-runs | tr -d ' ')\" = $expected_runs && test \"\$(wc -l < /var/lib/alpineform/component-ci-triggers | tr -d ' ')\" = 2 && grep -Fxq 'host.cihost.component.tool.artifact.install[\"/usr/local/bin/apf-ci-tool\"]' /var/lib/alpineform/component-ci-triggers && grep -Fxq 'host.cihost.component.tool.files.file[\"/etc/apf-ci-component.conf\"]' /var/lib/alpineform/component-ci-triggers"
}

assert_literal_source_requests() {
  assert_remote "literal sources are downloaded once and remain no-op compatible" \
    "test \"\$(grep -xc '/tool' /var/tmp/apf-component-http/requests.log)\" = 1 && test \"\$(grep -xc '/bundle.tar.gz' /var/tmp/apf-component-http/requests.log)\" = 1"
}

component_install_fingerprint() {
  ssh_vm 'set -eu
{
  for file in \
    /usr/local/bin/apf-protected-tool \
    /etc/alpineform-protected.conf \
    /usr/local/share/ca-certificates/alpineform-protected-root.crt \
    /etc/ssl/certs/ca-certificates.crt \
    /var/lib/alpineform/ca-certificates/root_ca/protected/any.updated; do
    stat -c "%n|%d|%i|%Z|%F|%u|%g|%a|%s" "$file"
    sha256sum "$file"
  done
  find /opt/alpineform-protected -exec stat -c "%n|%d|%i|%Z|%F|%u|%g|%a|%s" {} \; | LC_ALL=C sort
  find /opt/alpineform-protected -type f | LC_ALL=C sort | while IFS= read -r file; do
    sha256sum "$file"
  done
} | sha256sum | awk "{print \$1}"
'
}

component_preservation_fingerprint() {
  ssh_vm 'set -eu
{
  for root in \
    /var/lib/alpineform/state.json \
    /var/cache/alpineform/components \
    /usr/local/bin/apf-ci-tool \
    /etc/apf-ci-component.conf \
    /opt/apf-ci-bundle \
    /var/lib/alpineform/component-ci-triggers \
    /var/lib/alpineform/component-ci-runs \
    /var/lib/alpineform/scripts \
    /usr/local/bin/apf-protected-tool \
    /etc/alpineform-protected.conf \
    /opt/alpineform-protected \
    /usr/local/share/ca-certificates/alpineform-protected-root.crt \
    /etc/ssl/certs/ca-certificates.crt \
    /var/lib/alpineform/ca-certificates; do
    if [ -d "$root" ]; then
      find "$root" -exec stat -c "%n|%F|%u|%g|%a|%s" {} \; | LC_ALL=C sort
      find "$root" -type f | LC_ALL=C sort | while IFS= read -r file; do sha256sum "$file"; done
    else
      stat -c "%n|%F|%u|%g|%a|%s" "$root"
      sha256sum "$root"
    fi
  done
} | sha256sum | awk "{print \$1}"
'
}

assert_wrong_checksum_preserves_runtime() {
  local before after requests_before requests_after debug_log="$LOG_DIR/1.wrong-checksum.debug.log"
  before="$(component_preservation_fingerprint)"
  requests_before="$(ssh_vm "grep -xc '/mirror-a/tool-amd64' /var/tmp/apf-component-http/requests.log")"
  if apf apply -f "$CASE_DIR/wrong-checksum.apf.hcl" --auto-approve --debug --color never >"$debug_log" 2>&1; then
    fail "wrong-checksum protected artifact apply unexpectedly succeeded"
  fi
  after="$(component_preservation_fingerprint)"
  requests_after="$(ssh_vm "grep -xc '/mirror-a/tool-amd64' /var/tmp/apf-component-http/requests.log")"
  assert_local "wrong-checksum apply preserves install, state, trust, and cache trees" test "$before" = "$after"
  assert_local "wrong-checksum apply fetches exactly one rejected candidate" \
    test "$requests_after" -eq "$((requests_before + 1))"
  assert_local "wrong-checksum debug apply identifies the failed protected source update" \
    grep -Fq 'debug phase=operation host="cihost" operation=update address="host.cihost.component.binary.artifact.source[\"amd64\"]" status=failed' "$debug_log"
  assert_no_protected_file "$debug_log" "wrong-checksum debug failure log"
  assert_remote "wrong-checksum failure leaves no download or replacement temporary" \
    "test -z \"\$(find /var/cache/alpineform/components /usr/local/bin /etc /opt /usr/local/share/ca-certificates /var/lib/alpineform/ca-certificates -type f \\( -name '.alpineform-download.*' -o -name '.alpineform-component.*' -o -name '.alpineform-ca-candidate.*' -o -name '.alpineform-ca-prior.*' \\) -print -quit 2>/dev/null)\" && test -z \"\$(find /opt -maxdepth 1 -type d \\( -name '.alpineform-archive-work.*' -o -name '.alpineform-archive-old.*' \\) -print -quit 2>/dev/null)\""
  apf check -f "$CASE_DIR/1.apf.hcl" --color never | tee "$LOG_DIR/1.post-failure.check.log"
  assert_component_runtime
  assert_literal_component_runtime
  assert_literal_source_requests
  assert_component_state "wrong-checksum failure leaves the exact combined state identity set"
}

assert_mirror_a_selection() {
  local expected_tool_requests=1
  if [[ "$APF_TEST_PHASE" == repaired ]]; then
    expected_tool_requests=2
  elif [[ "$APF_TEST_PHASE" == rebooted ]]; then
    expected_tool_requests=3
  fi
  assert_remote "mirror A served selected amd64 and architecture-independent artifacts only" \
    "test \"\$(grep -xc '/mirror-a/tool-amd64' /var/tmp/apf-component-http/requests.log)\" = $expected_tool_requests && test \"\$(grep -xc '/mirror-a/component.conf' /var/tmp/apf-component-http/requests.log)\" = 1 && test \"\$(grep -xc '/mirror-a/bundle-amd64.tar.gz' /var/tmp/apf-component-http/requests.log)\" = 1 && test \"\$(grep -xc '/mirror-a/root.crt' /var/tmp/apf-component-http/requests.log)\" = 1 && ! grep -q 'arm64' /var/tmp/apf-component-http/requests.log"
}

assert_mirror_b_unused() {
  assert_remote "content-equivalent mirror B initially performs no download" \
    "! grep -q '^/mirror-b/' /var/tmp/apf-component-http/requests.log"
}

assert_mirror_b_cache_repair_requests() {
  assert_remote "cache-only repair requests each selected mirror-B path exactly once" \
    "test \"\$(grep -xc '/mirror-b/tool-amd64' /var/tmp/apf-component-http/requests.log)\" = 1 && test \"\$(grep -xc '/mirror-b/component.conf' /var/tmp/apf-component-http/requests.log)\" = 1 && test \"\$(grep -xc '/mirror-b/bundle-amd64.tar.gz' /var/tmp/apf-component-http/requests.log)\" = 1 && test \"\$(grep -xc '/mirror-b/root.crt' /var/tmp/apf-component-http/requests.log)\" = 1 && test \"\$(grep -c '^/mirror-b/' /var/tmp/apf-component-http/requests.log)\" = 4 && ! grep -q 'arm64' /var/tmp/apf-component-http/requests.log"
}

assert_teardown_runtime() {
  assert_remote "component teardown removes literal and protected sources and installs" \
    "test ! -e /usr/local/bin/apf-ci-tool && test ! -e /opt/apf-ci-bundle && test ! -e /usr/local/bin/apf-protected-tool && test ! -e /etc/alpineform-protected.conf && test ! -e /opt/alpineform-protected && test ! -e /usr/local/share/ca-certificates/alpineform-protected-root.crt && test ! -e /var/cache/alpineform/components/tool/$APF_LEGACY_TOOL_SHA && test ! -e /var/cache/alpineform/components/archive/$APF_LEGACY_ARCHIVE_SHA && test ! -e /var/cache/alpineform/components/binary/protected/amd64 && test ! -e /var/cache/alpineform/components/config_file/protected/any && test ! -e /var/cache/alpineform/components/protected_archive/protected/amd64 && test ! -e /var/cache/alpineform/components/root_ca/protected/any && test ! -e /var/lib/alpineform/ca-certificates/root_ca/protected && test -z \"\$(find /var/cache/alpineform/components /var/lib/alpineform/ca-certificates -type f \( -name '.alpineform-owned' -o -name '.alpineform-download.*' -o -name '.alpineform-ca.*' \) -print -quit 2>/dev/null)\" && ! grep -aFq '$APF_CA_PROBE' /etc/ssl/certs/ca-certificates.crt"
  assert_remote "legacy file and shared-script outputs follow their explicit forget actions" \
    "grep -qx 'enabled=true' /etc/apf-ci-component.conf && test \"\$(wc -l < /var/lib/alpineform/component-ci-runs | tr -d ' ')\" = 2 && test \"\$(wc -l < /var/lib/alpineform/component-ci-triggers | tr -d ' ')\" = 2 && test \"\$(find /var/lib/alpineform/scripts -type f -name '*.outputs' | wc -l | tr -d ' ')\" = 1"
  assert_remote "component teardown leaves empty state without physical identities" \
    "python3 -c 'import json; state=json.load(open(\"/var/lib/alpineform/state.json\", encoding=\"utf-8\")); assert state.get(\"resources\") == {}; assert state.get(\"component_identities\", {}) == {}'"
}
