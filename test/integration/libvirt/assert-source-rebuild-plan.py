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


def non_negative_integer(value: str) -> int:
    try:
        number = int(value)
    except ValueError as error:
        raise argparse.ArgumentTypeError("must be an integer") from error
    if number < 0:
        raise argparse.ArgumentTypeError("must be non-negative")
    return number


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="assert the exact changed resources in a source-input rebuild plan"
    )
    parser.add_argument("plan_json", metavar="PLAN_JSON")
    parser.add_argument(
        "--create-address",
        action="append",
        required=True,
        metavar="ADDRESS",
        help="exact source-build resource expected to be created; repeat for every create",
    )
    parser.add_argument(
        "--update-address",
        action="append",
        required=True,
        metavar="ADDRESS",
        help="exact source-build resource expected to be updated; repeat for every update",
    )
    parser.add_argument("--no-op-count", required=True, type=non_negative_integer)
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
        if document.get("moves") != []:
            raise ValueError(f"expected no pending component moves, got {document.get('moves')!r}")

        create_addresses = set(args.create_address)
        update_addresses = set(args.update_address)
        if len(create_addresses) != len(args.create_address):
            raise ValueError("--create-address values must be unique")
        if len(update_addresses) != len(args.update_address):
            raise ValueError("--update-address values must be unique")
        overlap = create_addresses.intersection(update_addresses)
        if overlap:
            raise ValueError(
                "addresses cannot be both create and update: " + ", ".join(sorted(overlap))
            )

        expected_counts = {
            "move": 0,
            "create": len(create_addresses),
            "update": len(update_addresses),
            "adopt": 0,
            "delete": 0,
            "destroy": 0,
            "forget": 0,
            "no_op": args.no_op_count,
        }
        for name in REQUIRED_COUNTERS + OPTIONAL_COUNTERS:
            actual = summary_counter(summary, name, required=name in REQUIRED_COUNTERS)
            if actual != expected_counts[name]:
                raise ValueError(
                    f"expected summary.{name}={expected_counts[name]}, got {actual}"
                )

        changes = document.get("changes")
        if not isinstance(changes, list):
            raise ValueError("expected plan changes array")
        actual_by_action = {"create": set(), "update": set(), "no-op": set()}
        seen = set()
        for change in changes:
            if not isinstance(change, dict):
                raise ValueError(f"expected plan change object, got {change!r}")
            address = change.get("address")
            action = change.get("action")
            if not isinstance(address, str) or action not in actual_by_action:
                raise ValueError(f"unexpected source-rebuild change: {change!r}")
            if address in seen:
                raise ValueError(f"duplicate plan change address: {address}")
            seen.add(address)
            actual_by_action[action].add(address)
            if action in ("create", "update"):
                summary_text = change.get("summary")
                if not isinstance(summary_text, str) or not summary_text.startswith("rebuild:"):
                    raise ValueError(
                        f"expected rebuild summary for {address}, got {summary_text!r}"
                    )

        if actual_by_action["create"] != create_addresses:
            raise ValueError(
                "create addresses do not match: "
                f"expected {sorted(create_addresses)!r}, got {sorted(actual_by_action['create'])!r}"
            )
        if actual_by_action["update"] != update_addresses:
            raise ValueError(
                "update addresses do not match: "
                f"expected {sorted(update_addresses)!r}, got {sorted(actual_by_action['update'])!r}"
            )
        if len(actual_by_action["no-op"]) != args.no_op_count:
            raise ValueError(
                f"expected {args.no_op_count} no-op changes, got {len(actual_by_action['no-op'])}"
            )

        expected_resources = len(create_addresses) + len(update_addresses) + args.no_op_count
        for name in ("managed_resources", "graph_nodes"):
            actual = summary_counter(summary, name, required=True)
            if actual != expected_resources:
                raise ValueError(f"expected summary.{name}={expected_resources}, got {actual}")
    except ValueError as error:
        return fail(str(error))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
