#!/bin/sh

set -eu

if [ "$#" -eq 3 ] && [ "$1" = --quiet ] && [ "$2" = del ] && [ "$3" = jq ]; then
  if rc-service apf-ci-raw status >/dev/null 2>&1; then
    echo 'refusing jq deletion while apf-ci-raw is running' >&2
    exit 70
  fi
  if rc-update show default | grep -Eq '(^|[[:space:]])apf-ci-raw([[:space:]]|$)'; then
    echo 'refusing jq deletion while apf-ci-raw is enabled' >&2
    exit 71
  fi
  if [ -e /etc/alpineform-dependency.json ]; then
    echo 'refusing jq deletion while the managed config exists' >&2
    exit 72
  fi
  printf '%s\n' service-before-config-before-package > /run/apf-ci-dependency-delete.guard
fi

exec /sbin/apk "$@"
