#!/usr/bin/env bash
set -euo pipefail
set +x

if [[ -z "${COORDINATOR_TEST_ADMIN_URL_FILE:-}" ]]; then
  echo "COORDINATOR_TEST_ADMIN_URL_FILE is required" >&2
  exit 2
fi

service_dir="$(cd "$(dirname "$0")/.." && pwd)"
repo_root="$(git -C "$service_dir" rev-parse --show-toplevel)"
marker="r6g_$(date -u +%Y%m%d_%H%M%S)_$$"
profile="$repo_root/.dev/integration/20261114/runtime/coordinator-task/g/$marker"
testdb=(go -C "$service_dir" run ./cmd/testdb)

cleanup() {
  status=$?
  trap - EXIT
  if [[ -d "$profile" ]]; then
    COORDINATOR_TEST_ADMIN_URL_FILE="$COORDINATOR_TEST_ADMIN_URL_FILE" \
      GOCACHE="${GOCACHE:-/tmp/flowcard-go-coordinator-g-gocache}" \
      "${testdb[@]}" cleanup --marker "$marker" --profile-dir "$profile" || status=$?
  fi
  exit "$status"
}
trap cleanup EXIT

env -u COORDINATOR_UPSTREAMS_FILE \
  COORDINATOR_TEST_ADMIN_URL_FILE="$COORDINATOR_TEST_ADMIN_URL_FILE" \
  GOCACHE="${GOCACHE:-/tmp/flowcard-go-coordinator-g-gocache}" \
  "${testdb[@]}" setup --marker "$marker" --profile-dir "$profile"

set -a
source "$profile/migration.env"
set +a
GOCACHE="${GOCACHE:-/tmp/flowcard-go-coordinator-g-gocache}" go -C "$service_dir" run ./cmd/migrate
GOCACHE="${GOCACHE:-/tmp/flowcard-go-coordinator-g-gocache}" go -C "$service_dir" run ./cmd/migrate

COORDINATOR_TEST_ADMIN_URL_FILE="$COORDINATOR_TEST_ADMIN_URL_FILE" \
GOCACHE="${GOCACHE:-/tmp/flowcard-go-coordinator-g-gocache}" \
  "${testdb[@]}" finalize --marker "$marker" --profile-dir "$profile"

set -a
source "$profile/runtime.env"
set +a
GOCACHE="${GOCACHE:-/tmp/flowcard-go-coordinator-g-gocache}" \
  go -C "$service_dir" test -mod=readonly -count=1 \
  -v -run '^(TestDiscoveryRecovery|TestAgentAuthority|TestCoordinatorRecovery)$' ./...
