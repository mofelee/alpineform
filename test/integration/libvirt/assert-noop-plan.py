#!/usr/bin/env python3

import json
import sys


FORMAT_VERSION = "alpineform.plan.alpha1"
REQUIRED_COUNTERS = ("move", "create", "update", "delete", "no_op")
OPTIONAL_COUNTERS = ("adopt", "destroy", "forget")


def fail(message: str) -> int:
    print(message, file=sys.stderr)
    return 1


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: assert-noop-plan.py PLAN_JSON", file=sys.stderr)
        return 2

    try:
        with open(sys.argv[1], encoding="utf-8") as plan_file:
            document = json.load(plan_file)
    except (OSError, json.JSONDecodeError) as error:
        return fail(f"cannot read plan JSON: {error}")

    if not isinstance(document, dict) or document.get("format_version") != FORMAT_VERSION:
        return fail(f"expected an {FORMAT_VERSION} JSON document")

    summary = document.get("summary")
    if not isinstance(summary, dict):
        return fail("expected plan summary object")

    counts = {}
    for name in REQUIRED_COUNTERS + OPTIONAL_COUNTERS:
        if name in REQUIRED_COUNTERS and name not in summary:
            return fail(f"expected summary.{name}")
        value = summary.get(name, 0)
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            return fail(f"expected summary.{name} to be a non-negative integer")
        counts[name] = value

    for name in ("move", "create", "update", "adopt", "delete", "destroy", "forget"):
        if counts[name] != 0:
            return fail(f"expected no-op plan after apply, got summary.{name}={counts[name]}")

    moves = document.get("moves")
    if moves != []:
        return fail(f"expected no realized moves after apply, got {moves!r}")

    changes = document.get("changes")
    if not isinstance(changes, list):
        return fail("expected plan changes array")
    for change in changes:
        if not isinstance(change, dict) or change.get("action") != "no-op":
            return fail(f"expected only no-op resource changes, got {change!r}")
    if len(changes) != counts["no_op"]:
        return fail(
            f"expected {counts['no_op']} no-op resource changes, got {len(changes)}"
        )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
