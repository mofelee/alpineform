#!/usr/bin/env bash

set -euo pipefail

visibility="${APF_REPOSITORY_VISIBILITY:-}"
private_enabled="${APF_PRIVATE_ATTESTATIONS_ENABLED:-false}"

if [[ -z "$visibility" ]]; then
  command -v gh >/dev/null 2>&1 || {
    printf 'required command not found: gh\n' >&2
    exit 1
  }
  [[ -n "${GITHUB_REPOSITORY:-}" ]] || {
    printf 'GITHUB_REPOSITORY is required to check attestation eligibility\n' >&2
    exit 1
  }
  visibility="$(gh api "repos/${GITHUB_REPOSITORY}" --jq .visibility)"
fi

case "$visibility" in
  public)
    printf 'GitHub artifact attestations are available for this public repository.\n'
    ;;
  private|internal)
    if [[ "$private_enabled" != true ]]; then
      message="GitHub artifact attestations require a public repository or confirmed Enterprise Cloud support."
      if [[ "${GITHUB_ACTIONS:-}" == true ]]; then
        printf '::error::%s\n' "$message" >&2
      else
        printf '%s\n' "$message" >&2
      fi
      exit 1
    fi
    printf 'Private/internal GitHub artifact attestations were explicitly confirmed.\n'
    ;;
  *)
    printf 'unsupported repository visibility: %s\n' "$visibility" >&2
    exit 1
    ;;
esac
