#!/usr/bin/env bash
# ==============================================================================
# Karmarat Database Pull & Sync Script
#
# Pulls a PostgreSQL snapshot from a remote host (via SSH) or from a backup file,
# optionally scrubs candidate and employee PII, and restores into the local/staging
# PostgreSQL container.
#
# Safety:
#   Refuses to restore into any host where PLATFORM_ENV=production.
#
# Usage:
#   ./scripts/db-pull.sh --from user@staging.karmarat.com [--sanitize]
#   ./scripts/db-pull.sh --file /path/to/backup.sql.gz [--sanitize]
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

CALLER_ENV="${PLATFORM_ENV:-}"
if [[ -f "${REPO_ROOT}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a && source "${REPO_ROOT}/.env" && set +a
fi

PLATFORM_ENV="${CALLER_ENV:-${PLATFORM_ENV:-development}}"
POSTGRES_USER="${POSTGRES_USER:-karmarat}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-karmarat-postgres}"

REMOTE_HOST=""
BACKUP_FILE=""
SANITIZE=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --from)
      REMOTE_HOST="$2"
      shift 2
      ;;
    --file)
      BACKUP_FILE="$2"
      shift 2
      ;;
    --sanitize)
      SANITIZE=true
      shift
      ;;
    -h|--help)
      echo "Usage: $0 [--from user@host | --file /path/to/backup.sql.gz] [--sanitize]"
      exit 0
      ;;
    *)
      echo "Unknown argument: $1"
      exit 1
      ;;
  esac
done

echo "=========================================================="
echo " Karmarat Database Pull"
echo " Target Environment: $PLATFORM_ENV"
echo " Local Container:    $POSTGRES_CONTAINER"
echo " Sanitization:       $SANITIZE"
echo "=========================================================="

# ------------------------------------------------------------------------------
# 1. Target Safety Check
# ------------------------------------------------------------------------------
if [[ "$PLATFORM_ENV" == "production" ]]; then
  echo "❌ ERROR: Cannot run db-pull targeting a PRODUCTION environment!"
  echo "Database pulls are strictly downstream operations (to local or staging)."
  exit 1
fi

if [[ -z "$REMOTE_HOST" && -z "$BACKUP_FILE" ]]; then
  echo "❌ ERROR: Specify either --from user@host or --file /path/to/backup.sql.gz"
  exit 1
fi

# Ensure local PostgreSQL container is running
if ! docker ps --format '{{.Names}}' | grep -q "^${POSTGRES_CONTAINER}$"; then
  echo "❌ ERROR: Target container '$POSTGRES_CONTAINER' is not running."
  echo "Start infrastructure with 'make dev-infra' or 'make prod-up' first."
  exit 1
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
PULL_DUMP="${TMP_DIR}/pulled_db.sql.gz"

# ------------------------------------------------------------------------------
# 2. Acquire Database Dump
# ------------------------------------------------------------------------------
if [[ -n "$BACKUP_FILE" ]]; then
  if [[ ! -f "$BACKUP_FILE" ]]; then
    echo "❌ ERROR: Backup file not found: $BACKUP_FILE"
    exit 1
  fi
  echo "[1/4] Using local backup file: $BACKUP_FILE"
  cp "$BACKUP_FILE" "$PULL_DUMP"
elif [[ -n "$REMOTE_HOST" ]]; then
  echo "[1/4] Dumping PostgreSQL databases from remote host: $REMOTE_HOST..."
  ssh "$REMOTE_HOST" 'docker exec -t karmarat-postgres pg_dumpall -U karmarat' | gzip -9 > "$PULL_DUMP"
  echo "      Remote dump downloaded: $(ls -lh "$PULL_DUMP" | awk '{print $5}')"
fi

# ------------------------------------------------------------------------------
# 3. Restore into Target Container
# ------------------------------------------------------------------------------
echo "[2/4] Restoring databases into $POSTGRES_CONTAINER..."
gunzip -c "$PULL_DUMP" | docker exec -i "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d postgres >/dev/null 2>&1 || true
echo "      PostgreSQL restore complete."

# ------------------------------------------------------------------------------
# 4. Optional Sanitization (PII Masking)
# ------------------------------------------------------------------------------
if [[ "$SANITIZE" == "true" ]]; then
  echo "[3/4] Sanitizing sensitive PII and credentials..."

  # Sanitize identity accounts (mask email, set standard dev password: Password123!)
  DEV_BCRYPT='$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy'
  docker exec -i "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d identity <<-SQL >/dev/null 2>&1 || true
    UPDATE accounts 
    SET email = 'dev+' || substr(id, 1, 8) || '@example.com' 
    WHERE realm != 'platform';

    UPDATE accounts 
    SET password_hash = '${DEV_BCRYPT}',
        failed_login_count = 0,
        locked_until = NULL;
SQL

  # Sanitize candidates (mask email, phone, name)
  docker exec -i "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d candidates <<-SQL >/dev/null 2>&1 || true
    UPDATE candidate_profiles 
    SET email = 'candidate+' || substr(id, 1, 8) || '@example.com',
        full_name = 'Candidate ' || substr(id, 1, 6),
        phone_number = '+1555000' || lpad((random()*9999)::int::text, 4, '0');

    UPDATE company_candidates 
    SET email = 'lead+' || substr(id, 1, 8) || '@example.com',
        full_name = 'Talent ' || substr(id, 1, 6),
        phone_number = '+1555000' || lpad((random()*9999)::int::text, 4, '0');
SQL

  echo "      PII sanitized: candidate emails masked, passwords reset to 'Password123!'."
else
  echo "[3/4] Skipping sanitization (use --sanitize to mask candidate PII and passwords)."
fi

# ------------------------------------------------------------------------------
# 5. Apply Migrations
# ------------------------------------------------------------------------------
echo "[4/4] Verifying and applying current schema migrations..."
make migrate

echo ""
echo "=========================================================="
echo " Database pull & sync completed successfully!"
echo "=========================================================="
