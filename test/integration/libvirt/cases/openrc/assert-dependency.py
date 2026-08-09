#!/usr/bin/env python3

import json
import sys
from collections import Counter


FORMAT_VERSION = "alpineform.plan.alpha1"
HOST = "host.cihost"
PACKAGE = 'host.cihost.packages.package["jq"]'
CONFIG = 'host.cihost.files.file["/etc/alpineform-dependency.json"]'
RAW_INIT = 'host.cihost.files.file["/etc/init.d/apf-ci-raw"]'
RAW_SERVICE = 'host.cihost.services.service["apf-ci-raw"]'
WORKER_INIT = 'host.cihost.files.file["/etc/init.d/apf-ci-worker"]'
WORKER_CONF = 'host.cihost.files.file["/etc/conf.d/apf-ci-worker"]'
WORKER_SERVICE = 'host.cihost.services.service["apf-ci-worker"]'
ADDRESSES = {
    PACKAGE,
    CONFIG,
    RAW_INIT,
    RAW_SERVICE,
    WORKER_INIT,
    WORKER_CONF,
    WORKER_SERVICE,
}
ACTIONS = ("create", "update", "adopt", "delete", "destroy", "forget", "no-op")


PLAN_ACTIONS = {
    "create": {address: "create" for address in ADDRESSES},
    "repair": {
        PACKAGE: "create",
        CONFIG: "update",
        RAW_INIT: "update",
        RAW_SERVICE: "update",
        WORKER_INIT: "no-op",
        WORKER_CONF: "no-op",
        WORKER_SERVICE: "update",
    },
    "noop": {address: "no-op" for address in ADDRESSES},
    "cleanup": {
        PACKAGE: "delete",
        CONFIG: "delete",
        RAW_INIT: "no-op",
        RAW_SERVICE: "update",
        WORKER_INIT: "no-op",
        WORKER_CONF: "no-op",
        WORKER_SERVICE: "no-op",
    },
    "recreate": {
        PACKAGE: "create",
        CONFIG: "create",
        RAW_INIT: "no-op",
        RAW_SERVICE: "update",
        WORKER_INIT: "no-op",
        WORKER_CONF: "no-op",
        WORKER_SERVICE: "no-op",
    },
    "forget": {address: "forget" for address in ADDRESSES},
}


def fail(message: str) -> None:
    raise SystemExit(message)


def load_document(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as source:
            document = json.load(source)
    except (OSError, json.JSONDecodeError) as error:
        fail(f"cannot read {path}: {error}")
    if not isinstance(document, dict):
        fail(f"expected {path} to contain a JSON object")
    return document


def indexed(items: object, name: str) -> tuple[list[dict], dict[str, dict]]:
    if not isinstance(items, list) or any(not isinstance(item, dict) for item in items):
        fail(f"expected {name} to be an array of objects")
    addresses = [item.get("address") for item in items]
    if any(not isinstance(address, str) for address in addresses):
        fail(f"expected every {name} entry to have an address")
    if len(set(addresses)) != len(addresses):
        fail(f"duplicate address in {name}: {addresses!r}")
    return items, dict(zip(addresses, items))


def assert_relationships(
    collection: str, entries: dict[str, dict], stage: str
) -> None:
    config = entries[CONFIG]
    service = entries[RAW_SERVICE]
    if stage == "forget":
        if config.get("depends_on", []) or service.get("depends_on", []):
            fail(f"{collection} exposed structural relationships for state-only orphans")
        if service.get("triggered_by", []):
            fail(f"{collection} exposed triggers for state-only orphans")
        return

    expected_config = sorted([HOST, PACKAGE])
    expected_service = sorted([HOST, CONFIG, RAW_INIT])
    if config.get("depends_on", []) != expected_config:
        fail(
            f"{collection} config depends_on = {config.get('depends_on', [])!r}, "
            f"want {expected_config!r}"
        )
    if service.get("depends_on", []) != expected_service:
        fail(
            f"{collection} raw service depends_on = {service.get('depends_on', [])!r}, "
            f"want {expected_service!r}"
        )
    expected_triggers: list[str] = []
    if collection == "graph" and stage != "cleanup":
        expected_triggers = [RAW_INIT]
    if collection == "changes" and stage in ("create", "repair"):
        expected_triggers = [RAW_INIT]
    if service.get("triggered_by", []) != expected_triggers:
        fail(
            f"{collection} raw service triggered_by = "
            f"{service.get('triggered_by', [])!r}, want {expected_triggers!r}"
        )
    if CONFIG in service.get("triggered_by", []):
        fail(f"{collection} treated authored config ordering as a trigger")


def assert_plan(path: str, stage: str) -> None:
    expected_actions = PLAN_ACTIONS.get(stage)
    if expected_actions is None:
        fail(f"unknown plan stage {stage!r}")
    document = load_document(path)
    if document.get("format_version") != FORMAT_VERSION or document.get("mode") != "online":
        fail(f"expected an online {FORMAT_VERSION} document")
    if document.get("hosts") != ["cihost"] or document.get("moves") != []:
        fail(f"unexpected hosts or moves: {document.get('hosts')!r}, {document.get('moves')!r}")

    changes, changes_by_address = indexed(document.get("changes"), "changes")
    graph, graph_by_address = indexed(document.get("graph"), "graph")
    if set(changes_by_address) != ADDRESSES or set(graph_by_address) != ADDRESSES:
        fail(
            "unexpected managed addresses: "
            f"changes={sorted(changes_by_address)!r}, graph={sorted(graph_by_address)!r}"
        )
    actual_actions = {
        address: change.get("action") for address, change in changes_by_address.items()
    }
    if actual_actions != expected_actions:
        fail(f"unexpected {stage} actions: want {expected_actions!r}, got {actual_actions!r}")

    summary = document.get("summary")
    if not isinstance(summary, dict):
        fail("expected plan summary object")
    expected_counts = Counter(expected_actions.values())
    if summary.get("move", 0) != 0:
        fail(f"summary.move = {summary.get('move')!r}, want 0")
    summary_keys = {"no-op": "no_op"}
    for action in ACTIONS:
        key = summary_keys.get(action, action)
        if summary.get(key, 0) != expected_counts[action]:
            fail(
                f"summary.{key} = {summary.get(key, 0)!r}, "
                f"want {expected_counts[action]}"
            )
    if summary.get("managed_resources") != len(ADDRESSES) or summary.get("graph_nodes") != len(ADDRESSES):
        fail(f"unexpected managed resource counts: {summary!r}")

    assert_relationships("changes", changes_by_address, stage)
    assert_relationships("graph", graph_by_address, stage)

    position = {change["address"]: index for index, change in enumerate(changes)}
    if stage == "cleanup":
        ordered = [RAW_INIT, RAW_SERVICE, CONFIG, PACKAGE]
    elif stage == "forget":
        ordered = [RAW_SERVICE, CONFIG, PACKAGE]
    else:
        ordered = [PACKAGE, CONFIG, RAW_SERVICE]
        if position[RAW_INIT] >= position[RAW_SERVICE]:
            fail("raw init was not ordered before the raw service")
    indexes = [position[address] for address in ordered]
    if indexes != sorted(indexes):
        fail(f"unexpected {stage} execution order for {ordered!r}: {indexes!r}")


def assert_state(path: str, mode: str) -> None:
    document = load_document(path)
    if (
        document.get("product") != "alpineform"
        or document.get("schema_version") != 3
        or document.get("host") != "cihost"
    ):
        fail(
            "unexpected state header: "
            f"product={document.get('product')!r}, "
            f"schema_version={document.get('schema_version')!r}, "
            f"host={document.get('host')!r}"
        )
    resources = document.get("resources")
    if not isinstance(resources, dict):
        fail("expected state resources object")
    if mode == "present":
        expected_addresses = ADDRESSES
    elif mode == "pruned":
        expected_addresses = ADDRESSES - {PACKAGE, CONFIG}
    elif mode == "empty":
        expected_addresses = set()
    else:
        fail(f"unknown state mode {mode!r}")
    if set(resources) != expected_addresses:
        fail(
            f"unexpected {mode} state resources: want {sorted(expected_addresses)!r}, "
            f"got {sorted(resources)!r}"
        )
    if mode == "empty":
        return
    expected_dependencies = {}
    if mode == "present":
        expected_dependencies = {CONFIG: [PACKAGE], RAW_SERVICE: [CONFIG]}
    for address, resource in resources.items():
        if not isinstance(resource, dict):
            fail(f"state resource {address!r} is not an object")
        actual = resource.get("depends_on", [])
        expected = expected_dependencies.get(address, [])
        if actual != expected:
            fail(f"state dependency {address!r} = {actual!r}, want {expected!r}")


def main() -> None:
    if len(sys.argv) != 4 or sys.argv[1] not in ("plan", "state"):
        fail("usage: assert-dependency.py plan|state JSON_FILE STAGE")
    if sys.argv[1] == "plan":
        assert_plan(sys.argv[2], sys.argv[3])
    else:
        assert_state(sys.argv[2], sys.argv[3])


if __name__ == "__main__":
    main()
