#!/usr/bin/env bash
# ==============================================================================
# Karmarat Disaster Recovery Restore Script
#
# Restores PostgreSQL databases and MinIO object storage from an archive file.
#
# Usage:
#   ./scripts/restore.sh /path/to/karmarat_db_YYYYMMDD_HHMMSS.sql.gz
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Load environment configuration if available
if [[ -f "${REPO_ROOT}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a && source "${REPO_ROOT}/.env" && set +a
fi

POSTGRES_USER="${POSTGRES_USER:-karmarat}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-karmarat-postgres}"
MINIO_CONTAINER="${MINIO_CONTAINER:-karmarat-minio}"

if [[ $# -lt 1 ]]; then
  echo "Usage: $0 <path_to_db_backup.sql.gz> [path_to_minio_backup.tar.gz]"
  exit 1
fi

PG_BACKUP="$1"
MINIO_BACKUP="${2:-}"

if [[ ! -f "$PG_BACKUP" ]]; then
  echo "Error: Database backup file not found: $PG_BACKUP"
  exit 1
fi

echo "=========================================================="
echo " CAUTION: KARMARAT DISASTER RECOVERY RESTORE"
echo " Target DB Backup:    $PG_BACKUP"
if [[ -n "$MINIO_BACKUP" ]]; then
  echo " Target MinIO Backup: $MINIO_BACKUP"
fi
echo "=========================================================="
echo " This action will overwrite existing databases in $POSTGRES_CONTAINER!"
read -rp " Type 'RESTORE' to proceed: " CONFIRMATION

if [[ "$CONFIRMATION" != "RESTORE" ]]; then
  echo "Restore aborted by user."
  exit 0
fi

# 1. Restore PostgreSQL
echo "[1/2] Restoring PostgreSQL from $PG_BACKUP..."
gunzip -c "$PG_BACKUP" | docker exec -i "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d postgres
echo "      PostgreSQL restore complete."

# 2. Restore MinIO (Optional)
if [[ -n "$MINIO_BACKUP" && -f "$MINIO_BACKUP" ]]; then
  echo "[2/2] Restoring MinIO assets from $MINIO_BACKUP..."
  docker exec -i "$MINIO_CONTAINER" tar -xzf - -C /data < "$MINIO_BACKUP"
  echo "      MinIO asset restore complete."
else
  echo "[2/2] No MinIO backup specified or found. Skipping object storage restore."
fi

echo "=========================================================="
echo " Restore completed successfully!"
echo "=========================================================="
