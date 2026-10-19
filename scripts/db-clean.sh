#!/usr/bin/env bash
# ==============================================================================
# Karmarat Database Clean & Reset Script
#
# Terminates active connections, drops all service databases, recreates them,
# applies all Go microservice migrations, and optionally seeds demo data.
#
# Safety:
#   Refuses to run in production without --force-production-wipe and interactive
#   confirmation.
#
# Usage:
#   ./scripts/db-clean.sh [--seed] [--force] [--force-production-wipe]
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
COMPOSE_FILE="${COMPOSE_FILE:-deploy/docker-compose.yml}"

SEED=false
FORCE=false
FORCE_PROD_WIPE=false

for arg in "$@"; do
  case $arg in
    --seed)
      SEED=true
      shift
      ;;
    --force)
      FORCE=true
      shift
      ;;
    --force-production-wipe)
      FORCE_PROD_WIPE=true
      shift
      ;;
    *)
      ;;
  esac
done

echo "=========================================================="
echo " Karmarat Database Clean & Reset"
echo " Environment: $PLATFORM_ENV"
echo " Container:   $POSTGRES_CONTAINER"
echo " User:        $POSTGRES_USER"
echo "=========================================================="

# ------------------------------------------------------------------------------
# Production Guardrail
# ------------------------------------------------------------------------------
if [[ "$PLATFORM_ENV" == "production" ]]; then
  if [[ "$FORCE_PROD_WIPE" != "true" ]]; then
    echo "❌ ERROR: Cannot run db-clean in PRODUCTION environment without --force-production-wipe!"
    exit 1
  fi

  echo "⚠️  CRITICAL WARNING: YOU ARE RUNNING DB-CLEAN IN PRODUCTION!"
  echo "This will IRREVERSIBLY WIPE all employer, candidate, and platform data!"
  read -rp "Type 'CONFIRM-WIPE-PRODUCTION' to proceed: " CONFIRM_TXT
  if [[ "$CONFIRM_TXT" != "CONFIRM-WIPE-PRODUCTION" ]]; then
    echo "Aborted by user."
    exit 1
  fi
elif [[ "$FORCE" != "true" ]]; then
  read -rp "Are you sure you want to drop and reset all service databases? (y/N): " CONFIRM_NON_PROD
  if [[ "$CONFIRM_NON_PROD" != "y" && "$CONFIRM_NON_PROD" != "Y" ]]; then
    echo "Reset aborted."
    exit 0
  fi
fi

# Ensure PostgreSQL container is running
if ! docker ps --format '{{.Names}}' | grep -q "^${POSTGRES_CONTAINER}$"; then
  echo "PostgreSQL container '$POSTGRES_CONTAINER' is not running. Starting infrastructure..."
  docker compose -f "$COMPOSE_FILE" up -d postgres
  sleep 3
fi

# Service databases list
DATABASES=(
  identity
  companies
  jobs
  applications
  candidates
  messaging
  notifications
  subscriptions
  payments
  support
  admin
  interviews
  offers
  audit
)

echo ""
echo "[1/3] Dropping and recreating service databases..."
for db in "${DATABASES[@]}"; do
  echo "      Resetting database: $db"
  # Terminate active client connections
  docker exec -i "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d postgres -c \
    "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '${db}' AND pid <> pg_backend_pid();" >/dev/null 2>&1 || true

  # Drop database if exists
  docker exec -i "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d postgres -c \
    "DROP DATABASE IF EXISTS \"${db}\";" >/dev/null

  # Recreate database
  docker exec -i "$POSTGRES_CONTAINER" psql -U "$POSTGRES_USER" -d postgres -c \
    "CREATE DATABASE \"${db}\" OWNER \"${POSTGRES_USER}\";" >/dev/null
done
echo "      All 14 service databases recreated cleanly."

# ------------------------------------------------------------------------------
# 2. Run Database Migrations
# ------------------------------------------------------------------------------
echo ""
echo "[2/3] Applying service schema migrations..."
make migrate

# ------------------------------------------------------------------------------
# 3. Optional Seeding
# ------------------------------------------------------------------------------
if [[ "$SEED" == "true" ]]; then
  echo ""
  echo "[3/3] Seeding demo tenants and pipelines..."
  if [[ -x "${REPO_ROOT}/scripts/seed-uat.sh" ]]; then
    "${REPO_ROOT}/scripts/seed-uat.sh" || echo "Warning: Seeding script finished with warnings."
  fi
else
  echo ""
  echo "[3/3] Skipping seeding (run with --seed or 'make seed-uat' to load demo data)."
fi

echo ""
echo "=========================================================="
echo " Database clean & reset completed successfully!"
echo "=========================================================="
