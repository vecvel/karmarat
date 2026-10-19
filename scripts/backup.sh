#!/usr/bin/env bash
# ==============================================================================
# Karmarat Automated Backup Script
#
# Creates consistent, compressed backups of:
#   1. All PostgreSQL databases (via pg_dumpall)
#   2. MinIO object storage assets (resumes, offer letters, brand assets)
#
# Prunes backups older than RETENTION_DAYS and supports offsite sync.
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Load environment configuration if available
if [[ -f "${REPO_ROOT}/.env" ]]; then
  # shellcheck disable=SC1091
  set -a && source "${REPO_ROOT}/.env" && set +a
fi

COMPOSE_FILE="${COMPOSE_FILE:-${REPO_ROOT}/deploy/docker-compose.prod.yml}"
if [[ ! -f "$COMPOSE_FILE" ]]; then
  COMPOSE_FILE="${REPO_ROOT}/deploy/docker-compose.yml"
fi

POSTGRES_USER="${POSTGRES_USER:-karmarat}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-karmarat-postgres}"
MINIO_CONTAINER="${MINIO_CONTAINER:-karmarat-minio}"
BACKUP_DIR="${BACKUP_DIR:-${REPO_ROOT}/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-14}"
TIMESTAMP="$(date +"%Y%m%d_%H%M%S")"

mkdir -p "${BACKUP_DIR}"

echo "=========================================================="
echo " Starting Karmarat Backup: ${TIMESTAMP}"
echo " Destination: ${BACKUP_DIR}"
echo "=========================================================="

# 1. Backup PostgreSQL
PG_BACKUP="${BACKUP_DIR}/karmarat_db_${TIMESTAMP}.sql.gz"
echo "[1/3] Dumping PostgreSQL databases..."

if docker ps --format '{{.Names}}' | grep -q "^${POSTGRES_CONTAINER}$"; then
  docker exec -t "${POSTGRES_CONTAINER}" pg_dumpall -U "${POSTGRES_USER}" | gzip -9 > "${PG_BACKUP}"
  sha256sum "${PG_BACKUP}" > "${PG_BACKUP}.sha256"
  echo "      PostgreSQL backup complete: $(ls -lh "${PG_BACKUP}" | awk '{print $5}')"
else
  echo "      WARNING: PostgreSQL container '${POSTGRES_CONTAINER}' is not running. Skipping DB dump."
fi

# 2. Backup MinIO Object Storage
MINIO_BACKUP="${BACKUP_DIR}/karmarat_minio_${TIMESTAMP}.tar.gz"
echo "[2/3] Archiving MinIO object storage..."

if docker ps --format '{{.Names}}' | grep -q "^${MINIO_CONTAINER}$"; then
  docker exec -t "${MINIO_CONTAINER}" tar -czf - -C /data . > "${MINIO_BACKUP}"
  sha256sum "${MINIO_BACKUP}" > "${MINIO_BACKUP}.sha256"
  echo "      MinIO backup complete: $(ls -lh "${MINIO_BACKUP}" | awk '{print $5}')"
else
  echo "      WARNING: MinIO container '${MINIO_CONTAINER}' is not running. Skipping object storage archive."
fi

# 3. Offsite Cloud Sync (Optional)
echo "[3/3] Retention cleanup and offsite replication..."
if [[ -n "${OFFSITE_S3_BUCKET:-}" ]]; then
  echo "      Syncing backups to offsite storage: ${OFFSITE_S3_BUCKET}"
  if command -v aws &> /dev/null; then
    aws s3 sync "${BACKUP_DIR}" "${OFFSITE_S3_BUCKET}" --exclude "*" --include "*${TIMESTAMP}*"
  elif command -v mc &> /dev/null; then
    mc mirror --overwrite "${BACKUP_DIR}" "${OFFSITE_S3_BUCKET}"
  else
    echo "      Notice: aws-cli or mc not installed. Skipping remote sync."
  fi
fi

# Purge backups older than RETENTION_DAYS
echo "      Purging local backups older than ${RETENTION_DAYS} days..."
find "${BACKUP_DIR}" -type f \( -name "*.sql.gz*" -o -name "*.tar.gz*" \) -mtime "+${RETENTION_DAYS}" -delete

echo "=========================================================="
echo " Backup finished successfully at $(date +"%Y-%m-%d %H:%M:%S")"
echo " Files created:"
echo "   - ${PG_BACKUP}"
echo "   - ${MINIO_BACKUP}"
echo "=========================================================="
