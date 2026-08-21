#!/bin/bash
# © 2026 Platform Engineering Labs Inc.
# SPDX-License-Identifier: FSL-1.1-ALv2
#
# Clean Environment Hook for the Fly.io plugin.
#
# Called before AND after conformance tests. Deletes every app in the target
# organization whose name starts with the test prefix.
#
# One DELETE /v1/apps/{name} is enough: destroying an app cascades to its
# machines, volumes, secrets, certificates and IP assignments. That is also why
# every conformance forma creates its own app — a run that dies mid-test leaves
# exactly one thing to clean up, and cleaning it stops the machine billing.
#
# Required env:
#   FLY_ACCESS_TOKEN or FLY_API_TOKEN   API token (flyctl's own precedence)
#   FLY_ORG                             Organization slug, or "personal"
#
# Optional:
#   TEST_PREFIX      default "formae-sdk-test-". Must match testdata/config/vars.pkl.
#   FLY_API_BASE     default https://api.machines.dev
#
# Idempotent. Exits 0 when credentials are absent so a contributor without a Fly
# account can still run `make lint` and `make test-unit`.

set -euo pipefail

TEST_PREFIX="${TEST_PREFIX:-formae-sdk-test-}"
API_BASE="${FLY_API_BASE:-https://api.machines.dev}"

# flyctl resolves FLY_ACCESS_TOKEN first, FLY_API_TOKEN second
# (superfly/flyctl internal/config/config.go). Match it.
TOKEN="${FLY_ACCESS_TOKEN:-${FLY_API_TOKEN:-}}"

if [[ -z "${TOKEN}" ]]; then
  echo "clean-environment.sh: no FLY_ACCESS_TOKEN or FLY_API_TOKEN — skipping cleanup"
  exit 0
fi
if [[ -z "${FLY_ORG:-}" ]]; then
  echo "clean-environment.sh: FLY_ORG unset — skipping cleanup"
  exit 0
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "clean-environment.sh: jq not found — cannot parse the app list, skipping cleanup" >&2
  exit 0
fi

auth_curl() {
  curl --silent --show-error --fail-with-body \
    --header "Authorization: Bearer ${TOKEN}" \
    --header "Accept: application/json" \
    "$@"
}

echo "clean-environment.sh: cleaning apps in org '${FLY_ORG}' with prefix '${TEST_PREFIX}'"

# GET /v1/apps requires org_slug; without it the API answers 404 rather than
# listing everything.
#
# The exit status is checked rather than the body: --fail-with-body writes the
# error payload to stdout and exits non-zero, so a 401 would otherwise flow into
# jq, match nothing, and print a false "no leftover test apps" all-clear.
if ! apps_json="$(auth_curl "${API_BASE}/v1/apps?org_slug=${FLY_ORG}")"; then
  echo "  ERROR: could not list apps in org '${FLY_ORG}' — bad token, wrong org, or API down." >&2
  echo "  Response: ${apps_json}" >&2
  echo "  Nothing was cleaned. Leftover test apps (and any machines still billing) may remain." >&2
  exit 0
fi

names="$(printf '%s' "${apps_json}" | jq -r --arg p "${TEST_PREFIX}" \
  '.apps[]? | select(.name != null) | select(.name | startswith($p)) | .name' || true)"

if [[ -z "${names}" ]]; then
  echo "  no leftover test apps"
  echo "clean-environment.sh: done"
  exit 0
fi

# App deletions are rate-limited to 100/minute. A conformance run leaves at most
# a handful of apps behind, so a plain loop stays well inside that.
while IFS= read -r name; do
  [[ -z "${name}" ]] && continue
  echo "  DELETE app ${name} (cascades machines, volumes, secrets, certs, IPs)"
  auth_curl -X DELETE "${API_BASE}/v1/apps/${name}" >/dev/null || \
    echo "    warning: delete failed for ${name}, continuing" >&2
done <<< "${names}"

echo "clean-environment.sh: done"
