#!/usr/bin/env python3

import argparse
import json
import sys


FORMAT_VERSION = "alpineform.plan.alpha1"
REQUIRED_COUNTERS = ("move", "create", "update", "delete", "no_op")
OPTIONAL_COUNTERS = ("adopt", "destroy", "forget")


def fail(message: str) -> int:
    print(message, file=sys.stderr)
    return 1


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="assert an exact component-move plan and its resource actions"
    )
    parser.add_argument("plan_json", metavar="PLAN_JSON")
    parser.add_argument("--host", required=True)
    parser.add_argument(
        "--move",
        action="append",
        required=True,
        nargs=2,
        metavar=("FROM_ADDRESS", "TO_ADDRESS"),
        help="exact realized move mapping; repeat for every move",
    )
    parser.add_argument(
        "--update-address",
        action="append",
        default=[],
        metavar="ADDRESS",
        help="moved destination expected to update; all other destinations must be no-op",
    )
    parser.add_argument(
        "--trigger",
        action="append",
        default=[],
        nargs=2,
        metavar=("TARGET_ADDRESS", "SOURCE_ADDRESS"),
        help="expected triggered_by edge between moved destinations; repeat for every edge",
    )
    return parser.parse_args()


def load_document(path: str) -> dict:
    try:
        with open(path, encoding="utf-8") as plan_file:
            document = json.load(plan_file)
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read plan JSON: {error}") from error
    if not isinstance(document, dict) or document.get("format_version") != FORMAT_VERSION:
        raise ValueError(f"expected an {FORMAT_VERSION} JSON document")
    return document


def summary_counter(summary: dict, name: str, *, required: bool) -> int:
    if required and name not in summary:
        raise ValueError(f"expected summary.{name}")
    value = summary.get(name, 0)
    if isinstance(value, bool) or not isinstance(value, int) or value < 0:
        raise ValueError(f"expected summary.{name} to be a non-negative integer")
    return value


def main() -> int:
    args = parse_args()
    try:
        document = load_document(args.plan_json)
        summary = document.get("summary")
        if not isinstance(summary, dict):
            raise ValueError("expected plan summary object")

        mappings = [(source, destination) for source, destination in args.move]
        sources = [source for source, _ in mappings]
        destinations = [destination for _, destination in mappings]
        if len(sources) != len(set(sources)):
            raise ValueError("--move source addresses must be unique")
        if len(destinations) != len(set(destinations)):
            raise ValueError("--move destination addresses must be unique")
        host_prefix = "host." + args.host + "."
        if any(
            not source.startswith(host_prefix) or not destination.startswith(host_prefix)
            for source, destination in mappings
        ):
            raise ValueError(f"all move addresses must belong to host {args.host}")

        updates = set(args.update_address)
        if len(updates) != len(args.update_address):
            raise ValueError("--update-address values must be unique")
        unknown_updates = updates.difference(destinations)
        if unknown_updates:
            raise ValueError(
                "update addresses are not move destinations: "
                + ", ".join(sorted(unknown_updates))
            )

        expected_triggers: dict[str, list[str]] = {}
        for target, source in args.trigger:
            if target not in destinations or source not in destinations:
                raise ValueError("--trigger addresses must also be move destinations")
            expected_triggers.setdefault(target, []).append(source)
        for target in expected_triggers:
            expected_triggers[target].sort()

        expected_moves = sorted(
            (
                {
                    "host": args.host,
                    "from": source,
                    "to": destination,
                }
                for source, destination in mappings
            ),
            key=lambda move: (move["host"], move["from"], move["to"]),
        )
        moves = document.get("moves")
        if moves != expected_moves:
            raise ValueError(
                "realized moves do not match the exact expected mappings: "
                f"expected {expected_moves!r}, got {moves!r}"
            )

        expected_counts = {
            "move": len(mappings),
            "create": 0,
            "update": len(updates),
            "adopt": 0,
            "delete": 0,
            "destroy": 0,
            "forget": 0,
            "no_op": len(mappings) - len(updates),
        }
        for name in REQUIRED_COUNTERS + OPTIONAL_COUNTERS:
            actual = summary_counter(summary, name, required=name in REQUIRED_COUNTERS)
            if actual != expected_counts[name]:
                raise ValueError(
                    f"expected summary.{name}={expected_counts[name]}, got {actual}"
                )

        expected_actions = {
            destination: "update" if destination in updates else "no-op"
            for destination in destinations
        }
        changes = document.get("changes")
        if not isinstance(changes, list):
            raise ValueError("expected plan changes array")
        actual_actions: dict[str, str] = {}
        for change in changes:
            if not isinstance(change, dict):
                raise ValueError(f"expected plan change object, got {change!r}")
            address = change.get("address")
            action = change.get("action")
            if not isinstance(address, str) or not isinstance(action, str):
                raise ValueError(f"plan change lacks an address or action: {change!r}")
            if address in actual_actions:
                raise ValueError(f"duplicate plan change address: {address}")
            actual_actions[address] = action
            actual_triggers = change.get("triggered_by", [])
            if actual_triggers != expected_triggers.get(address, []):
                raise ValueError(
                    f"unexpected triggers for {address}: expected "
                    f"{expected_triggers.get(address, [])!r}, got {actual_triggers!r}"
                )
        if actual_actions != expected_actions:
            raise ValueError(
                "resource actions do not match moved destinations: "
                f"expected {expected_actions!r}, got {actual_actions!r}"
            )

        for name in ("managed_resources", "graph_nodes"):
            actual = summary_counter(summary, name, required=True)
            if actual != len(expected_actions):
                raise ValueError(
                    f"expected summary.{name}={len(expected_actions)}, got {actual}"
                )
    except ValueError as error:
        return fail(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
