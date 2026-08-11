#!/usr/bin/env bash
# Cron local: migrate-fleet-cron (flock + lock Redis/DB en API).
set -euo pipefail

LOCKFILE="${MIGRATE_LOCKFILE:-/tmp/tukifac-migrate-fleet.lock}"
TIMEOUT="${MIGRATE_TIMEOUT_SEC:-3600}"
LOG_DIR="${MIGRATE_LOG_DIR:-/var/log/tukifac}"
LOG_FILE="${LOG_DIR}/migrate-fleet.log"
API_BIN="${TUKIFAC_API_BIN:-./tukifac-api}"
WORKERS="${MIGRATE_WORKERS:-4}"
LIMIT="${MIGRATE_LIMIT:-100}"

mkdir -p "$LOG_DIR"

exec 9>"$LOCKFILE"
if ! flock -n 9; then
  exit 0
fi

echo "$(date -Iseconds) [start] migrate-fleet-cron workers=$WORKERS limit=$LIMIT" >> "$LOG_FILE"

timeout "$TIMEOUT" "$API_BIN" migrate-fleet-cron --workers="$WORKERS" --limit="$LIMIT" >> "$LOG_FILE" 2>&1
RC=$?

echo "$(date -Iseconds) [done] rc=$RC" >> "$LOG_FILE"
exit "$RC"
