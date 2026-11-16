#!/bin/sh
set -eu
set +x

: "${MIGRATION_DATABASE_URL:?separate migration URL required}"
: "${EXPECTED_DATABASE_NAME:?explicit expected database required}"

# Every connection is checked before DDL. Driver/subprocess diagnostics may
# contain connection strings, so only fixed phase names are emitted here.
check() {
  if ! /opt/flowcard/browser-gateway/deploytool "$1" >/dev/null 2>&1; then
    echo "Go deployment $1 failed; no runtime rollout performed" >&2
    exit 1
  fi
}
check check-admin

export DATABASE_URL="$MIGRATION_DATABASE_URL"
export UNDERWRITING_MIGRATION_DATABASE_URL="$MIGRATION_DATABASE_URL"
export BROWSER_GATEWAY_ADMIN_DATABASE_URL="$MIGRATION_DATABASE_URL"
for module in company-onboarding shortfall-detection underwriting-engine funding-collection go-coordinator browser-gateway; do
  case "$module" in
    funding-collection|browser-gateway)
      if ! "/opt/flowcard/$module/migrate" --database-name "$EXPECTED_DATABASE_NAME" >/dev/null 2>&1; then
        echo "Go migration failed: $module" >&2; exit 1
      fi
      ;;
    *)
      if ! "/opt/flowcard/$module/migrate" >/dev/null 2>&1; then
        echo "Go migration failed: $module" >&2; exit 1
      fi
      ;;
  esac
  echo "Go migration completed: $module"
done
check bootstrap
echo "All Go migrations and restricted-runtime provisioning completed"
