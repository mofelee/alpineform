#!/usr/bin/env python3

import json
import sys


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: assert-noop-plan.py PLAN_JSON", file=sys.stderr)
        return 2

    with open(sys.argv[1], encoding="utf-8") as plan_file:
        document = json.load(plan_file)

    if not isinstance(document, dict) or document.get("format_version") != "alpineform.plan.alpha1":
        print("expected an alpineform.plan.alpha1 JSON document", file=sys.stderr)
        return 1

    summary = document.get("summary")
    if not isinstance(summary, dict):
        print("expected plan summary object", file=sys.stderr)
        return 1

    for name in ("create", "update", "adopt", "delete", "destroy", "forget"):
        value = summary.get(name, 0)
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            print(f"expected summary.{name} to be a non-negative integer", file=sys.stderr)
            return 1
        if value != 0:
            print(f"expected no-op plan after apply, got summary.{name}={value}", file=sys.stderr)
            return 1

    no_op = summary.get("no_op")
    if isinstance(no_op, bool) or not isinstance(no_op, int) or no_op < 0:
        print("expected summary.no_op to be a non-negative integer", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
