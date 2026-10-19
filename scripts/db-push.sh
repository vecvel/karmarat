#!/usr/bin/env bash
# ==============================================================================
# Karmarat Database Push Script
#
# Pushes a local PostgreSQL dump or backup archive to a remote server over SSH.
# Useful for initial server bootstrapping or staging/UAT synchronization.
#
# Safety:
#   Requires explicit --force-production-overwrite and interactive confirmation
#   if targeting a production server.
#
# Usage:
#   ./scripts/db-push.sh --to user@staging.karmarat.com
#   ./scripts/db-push.sh --to user@prod.karmarat.com --file /path/to/backup.sql.gz --force-production-overwrite
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

REMOTE_HOST=""
BACKUP_FILE=""
FORCE_PROD=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --to)
      REMOTE_HOST="$2"
      shift 2
      ;;
    --file)
      BACKUP_FILE="$2"
      shift 2
      ;;
    --force-production-overwrite)
      FORCE_PROD=true
      shift
      ;;
    -h|--help)
      echo "Usage: $0 --to user@remote-host [--file /path/to/backup.sql.gz] [--force-production-overwrite]"
      exit 0
      ;;
    *)
      echo "Unknown argument: $1"
      exit 1
      ;;
  esac
done

if [[ -z "$REMOTE_HOST" ]]; then
  echo "❌ ERROR: --to user@remote-host is required."
  exit 1
fi

echo "=========================================================="
echo " Karmarat Database Push"
echo " Target Remote Host: $REMOTE_HOST"
echo " Source File:        ${BACKUP_FILE:-'Live Local PostgreSQL'}"
echo "=========================================================="

# ------------------------------------------------------------------------------
# 1. Inspect Remote Environment
# ------------------------------------------------------------------------------
echo "[1/4] Inspecting remote server environment..."
REMOTE_ENV=$(ssh "$REMOTE_HOST" 'grep -E "^PLATFORM_ENV=" .env 2>/dev/null | cut -d= -f2 || echo "unknown"')
echo "      Remote PLATFORM_ENV is: $REMOTE_ENV"

if [[ "$REMOTE_ENV" == "production" || "$REMOTE_HOST" =~ karmarat\.com$ && ! "$REMOTE_HOST" =~ (staging|uat) ]]; then
  echo ""
  echo "===================================================================="
  echo "🚨 CRITICAL PRODUCTION OVERWRITE WARNING 🚨"
  echo "Target Server: $REMOTE_HOST"
  echo "You are about to OVERWRITE all databases on a LIVE PRODUCTION SERVER!"
  echo "This will IRREVERSIBLY DESTROY all live employer and candidate data!"
  echo "===================================================================="
  echo ""

  if [[ "$FORCE_PROD" != "true" ]]; then
    echo "❌ ERROR: Operation blocked. You must pass --force-production-overwrite to push to production."
    exit 1
  fi

  read -rp "Type 'OVERWRITE-PRODUCTION-NOW' to confirm: " PROD_CONFIRM
  if [[ "$PROD_CONFIRM" != "OVERWRITE-PRODUCTION-NOW" ]]; then
    echo "Aborted by user."
    exit 1
  fi
else
  read -rp "Are you sure you want to overwrite databases on $REMOTE_HOST? (y/N): " CONFIRM
  if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
    echo "Push aborted."
    exit 0
  fi
fi

# ------------------------------------------------------------------------------
# 2. Prepare Source Dump
# ------------------------------------------------------------------------------
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
PUSH_DUMP="${TMP_DIR}/push_db.sql.gz"

if [[ -n "$BACKUP_FILE" ]]; then
  if [[ ! -f "$BACKUP_FILE" ]]; then
    echo "❌ ERROR: Backup file not found: $BACKUP_FILE"
    exit 1
  fi
  echo "[2/4] Using specified backup file: $BACKUP_FILE"
  cp "$BACKUP_FILE" "$PUSH_DUMP"
else
  echo "[2/4] Dumping local PostgreSQL databases..."
  LOCAL_PG_CONTAINER="${POSTGRES_CONTAINER:-karmarat-postgres}"
  LOCAL_PG_USER="${POSTGRES_USER:-karmarat}"
  if ! docker ps --format '{{.Names}}' | grep -q "^${LOCAL_PG_CONTAINER}$"; then
    echo "❌ ERROR: Local container '$LOCAL_PG_CONTAINER' is not running."
    exit 1
  fi
  docker exec -t "$LOCAL_PG_CONTAINER" pg_dumpall -U "$LOCAL_PG_USER" | gzip -9 > "$PUSH_DUMP"
  echo "      Local dump created: $(ls -lh "$PUSH_DUMP" | awk '{print $5}')"
fi

# ------------------------------------------------------------------------------
# 3. Stream Dump and Restore on Remote Server
# ------------------------------------------------------------------------------
echo "[3/4] Streaming database dump to $REMOTE_HOST and restoring..."
cat "$PUSH_DUMP" | ssh "$REMOTE_HOST" 'gunzip -c | docker exec -i karmarat-postgres psql -U karmarat -d postgres'
echo "      Remote PostgreSQL restore complete."

# ------------------------------------------------------------------------------
# 4. Remote Migration Verification
# ------------------------------------------------------------------------------
echo "[4/4] Verifying remote database state..."
ssh "$REMOTE_HOST" 'docker exec -i karmarat-postgres psql -U karmarat -d postgres -c "\l"' | grep -E "(identity|jobs|candidates|companies)" || true

echo ""
echo "=========================================================="
echo " Database push to $REMOTE_HOST completed successfully!"
echo "=========================================================="
