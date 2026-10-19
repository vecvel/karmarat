#!/usr/bin/env bash
# ==============================================================================
# Karmarat Environment Sanity Checker
#
# Validates that an environment file satisfies security, routing, and
# operational invariants before deploying to Staging, UAT, or Production.
#
# Usage:
#   ./scripts/check-env.sh <path-to-env-file>
# ==============================================================================

set -euo pipefail

ENV_FILE="${1:-.env}"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "Error: Environment file '$ENV_FILE' not found." >&2
  exit 1
fi

echo "=========================================================="
echo " Validating Environment Configuration: $ENV_FILE"
echo "=========================================================="

ERRORS=0
WARNINGS=0

# Helper to read variable from env file without evaluating shell commands
get_env() {
  local key="$1"
  grep -E "^${key}=" "$ENV_FILE" | head -n 1 | cut -d'=' -f2- | tr -d '"' | tr -d "'" || true
}

PLATFORM_ENV=$(get_env "PLATFORM_ENV")
PLATFORM_HOSTNAME=$(get_env "PLATFORM_HOSTNAME")
POSTGRES_PASSWORD=$(get_env "POSTGRES_PASSWORD")
REDIS_PASSWORD=$(get_env "REDIS_PASSWORD")
INTERNAL_SERVICE_TOKEN=$(get_env "INTERNAL_SERVICE_TOKEN")
COOKIE_SECURE=$(get_env "COOKIE_SECURE")
LOG_LEVEL=$(get_env "LOG_LEVEL")

# 1. PLATFORM_ENV Check
if [[ -z "$PLATFORM_ENV" ]]; then
  echo "[-] ERROR: PLATFORM_ENV must be set (development, staging, uat, production)."
  ERRORS=$((ERRORS + 1))
else
  echo "[+] PLATFORM_ENV: $PLATFORM_ENV"
fi

# 2. PLATFORM_HOSTNAME Check
if [[ -z "$PLATFORM_HOSTNAME" ]]; then
  echo "[-] ERROR: PLATFORM_HOSTNAME is missing."
  ERRORS=$((ERRORS + 1))
else
  echo "[+] PLATFORM_HOSTNAME: $PLATFORM_HOSTNAME"
fi

# 3. Secret Hardening Checks (Staging, UAT, Production)
if [[ "$PLATFORM_ENV" != "development" && "$PLATFORM_ENV" != "local" ]]; then
  # Cookie Security
  if [[ "$COOKIE_SECURE" != "1" && "$COOKIE_SECURE" != "true" ]]; then
    echo "[-] ERROR: COOKIE_SECURE must be '1' or 'true' in $PLATFORM_ENV."
    ERRORS=$((ERRORS + 1))
  else
    echo "[+] COOKIE_SECURE: enabled"
  fi

  # Password strength / defaults checks
  if [[ "$POSTGRES_PASSWORD" =~ "replace-with" || "$POSTGRES_PASSWORD" == "karmarat" || -z "$POSTGRES_PASSWORD" ]]; then
    echo "[-] WARNING: POSTGRES_PASSWORD contains default placeholder value. Replace before live traffic."
    WARNINGS=$((WARNINGS + 1))
  else
    echo "[+] POSTGRES_PASSWORD: configured"
  fi

  if [[ "$REDIS_PASSWORD" =~ "replace-with" || "$REDIS_PASSWORD" == "karmarat" || -z "$REDIS_PASSWORD" ]]; then
    echo "[-] WARNING: REDIS_PASSWORD contains default placeholder value."
    WARNINGS=$((WARNINGS + 1))
  else
    echo "[+] REDIS_PASSWORD: configured"
  fi

  if [[ "$INTERNAL_SERVICE_TOKEN" =~ "replace-with" || ${#INTERNAL_SERVICE_TOKEN} -lt 20 ]]; then
    echo "[-] WARNING: INTERNAL_SERVICE_TOKEN is short or using placeholder. Recommended: 32+ characters."
    WARNINGS=$((WARNINGS + 1))
  else
    echo "[+] INTERNAL_SERVICE_TOKEN: configured (${#INTERNAL_SERVICE_TOKEN} chars)"
  fi

  # Log Level
  if [[ "$LOG_LEVEL" == "debug" && "$PLATFORM_ENV" == "production" ]]; then
    echo "[-] WARNING: LOG_LEVEL is 'debug' in production. Recommended: 'warn' or 'info'."
    WARNINGS=$((WARNINGS + 1))
  fi
fi

echo "----------------------------------------------------------"
echo " Validation Result: $ERRORS error(s), $WARNINGS warning(s)."
echo "----------------------------------------------------------"

if [[ $ERRORS -gt 0 ]]; then
  echo "FAILED: Please resolve configuration errors before starting services." >&2
  exit 1
else
  echo "PASSED: Configuration file structure is valid."
  exit 0
fi
