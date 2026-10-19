#!/usr/bin/env bash
# ==============================================================================
# Universal Monorepo Rebranding & Renaming Engine
#
# Comprehensively renames, re-scopes, and re-brands the entire platform across:
#   1. Package definitions & workspaces (package.json, pnpm-lock.yaml, etc.)
#   2. Documentation & guides (Markdown .md, .mdx, text files)
#   3. All coding files (.go, .ts, .tsx, .js, .mjs, .json, .yaml, .yml, .sql, .sh, etc.)
#   4. Comments, docstrings, identifiers, string literals, and type definitions
#   5. Go module paths, imports, and checksums (go.mod, go.sum)
#   6. NPM / PNPM package scopes (@oldscope/* -> @newscope/*)
#   7. UI titles, brand labels, and marketing copy
#   8. Domain names, URLs, emails, and hostnames (apex, subdomains, localhost)
#   9. Docker image tags, container names, compose configs, and env files
#  10. NATS stream names and event subject constants
#  11. Database names, database users, and tenancy reserved-slugs tables
#  12. File and directory names containing the brand / term
#
# Usage:
#   ./scripts/rebrand.sh --old <OldName> --new <NewName> [options]
#   ./scripts/rebrand.sh <OldName> <NewName> [options]
#
# Examples:
#   ./scripts/rebrand.sh --old reqruitbook --new Karmarat --dry-run
#   ./scripts/rebrand.sh ReqruitBook Karmarat
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

OLD_INPUT=""
NEW_INPUT=""
DRY_RUN=false
FORCE=false
SKIP_INSTALL=false
SKIP_TIDY=false
SKIP_VERIFY=false

# ------------------------------------------------------------------------------
# Parse Arguments
# ------------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case $1 in
    --old|-o)
      OLD_INPUT="$2"
      shift 2
      ;;
    --new|-n)
      NEW_INPUT="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=true
      shift
      ;;
    --force|-f)
      FORCE=true
      shift
      ;;
    --skip-install)
      SKIP_INSTALL=true
      shift
      ;;
    --skip-tidy)
      SKIP_TIDY=true
      shift
      ;;
    --skip-verify)
      SKIP_VERIFY=true
      shift
      ;;
    -h|--help)
      echo "Universal Monorepo Rebranding & Renaming Engine"
      echo ""
      echo "Usage:"
      echo "  $0 --old <OldName> --new <NewName> [options]"
      echo "  $0 <OldName> <NewName> [options]"
      echo ""
      echo "Options:"
      echo "  --old, -o <Name>      Original name to replace (e.g. reqruitbook or ReqruitBook)"
      echo "  --new, -n <Name>      Target new name (e.g. Karmarat)"
      echo "  --dry-run             Preview affected files and renames without modifying"
      echo "  --force, -f           Bypass uncommitted git changes warning"
      echo "  --skip-install        Skip running package manager install after renaming"
      echo "  --skip-tidy           Skip running 'go mod tidy' after renaming"
      echo "  --skip-verify         Skip running 'make verify' post-rebrand checks"
      exit 0
      ;;
    *)
      if [[ -z "$OLD_INPUT" ]]; then
        OLD_INPUT="$1"
        shift
      elif [[ -z "$NEW_INPUT" ]]; then
        NEW_INPUT="$1"
        shift
      else
        echo "Unknown option or extra argument: $1"
        exit 1
      fi
      ;;
  esac
done

# Interactive fallback if arguments omitted
if [[ -z "$OLD_INPUT" ]]; then
  if [[ -t 0 ]]; then
    read -rp "Enter the original brand/name to replace (e.g. reqruitbook): " OLD_INPUT
  fi
fi

if [[ -z "$NEW_INPUT" ]]; then
  if [[ -t 0 ]]; then
    read -rp "Enter the new target brand/name (e.g. Karmarat): " NEW_INPUT
  fi
fi

if [[ -z "$OLD_INPUT" || -z "$NEW_INPUT" ]]; then
  echo "❌ ERROR: Both original (--old) and target (--new) names are required."
  echo ""
  echo "Usage:"
  echo "  $0 --old <OldName> --new <NewName> [options]"
  echo "  $0 <OldName> <NewName> [options]"
  echo ""
  echo "Example:"
  echo "  $0 --old reqruitbook --new Karmarat --dry-run"
  exit 1
fi

# ------------------------------------------------------------------------------
# Compute Case Variations via Python
# ------------------------------------------------------------------------------
CASES_JSON=$(python3 - <<EOF
import json, re, sys

def tokenize(s):
    parts = re.split(r'[-_\s]+', s)
    tokens = []
    for part in parts:
        subparts = re.findall(r'[A-Z]?[a-z0-9]+|[A-Z]+(?=[A-Z][a-z0-9]|\b)', part)
        if subparts:
            tokens.extend(subparts)
        elif part:
            tokens.append(part)
    return tokens if tokens else [s]

def variations(name):
    tokens = tokenize(name)
    lower = "".join(t.lower() for t in tokens)
    upper = "".join(t.upper() for t in tokens)
    title = "".join(t.capitalize() for t in tokens)
    
    # If the input has explicit inner camel/Pascal case, preserve it as pascal
    if any(c.isupper() for c in name[1:]):
        pascal = name
    else:
        pascal = "".join(t.capitalize() for t in tokens)
        
    return {
        "lower": lower,
        "upper": upper,
        "title": title,
        "pascal": pascal,
        "tokens": tokens
    }

old_vars = variations("${OLD_INPUT}")
new_vars = variations("${NEW_INPUT}")

print(json.dumps({"old": old_vars, "new": new_vars}))
EOF
)

OLD_LOWER=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['old']['lower'])")
OLD_TITLE=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['old']['title'])")
OLD_UPPER=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['old']['upper'])")
OLD_PASCAL=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['old']['pascal'])")

NEW_LOWER=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['new']['lower'])")
NEW_TITLE=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['new']['title'])")
NEW_UPPER=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['new']['upper'])")
NEW_PASCAL=$(echo "$CASES_JSON" | python3 -c "import sys, json; print(json.load(sys.stdin)['new']['pascal'])")

echo "=========================================================="
echo " Universal Monorepo Rebranding Engine"
echo "=========================================================="
echo " Original Input: $OLD_INPUT"
echo " Target Input:   $NEW_INPUT"
echo "----------------------------------------------------------"
echo " Lowercase:      $OLD_LOWER  ->  $NEW_LOWER"
echo " Titlecase:      $OLD_TITLE  ->  $NEW_TITLE"
echo " PascalCase:     $OLD_PASCAL  ->  $NEW_PASCAL"
echo " Uppercase:      $OLD_UPPER  ->  $NEW_UPPER"
echo " Dry Run:        $DRY_RUN"
echo "=========================================================="

# Check for uncommitted git changes if not in dry-run and not forced
if [[ "$DRY_RUN" != "true" && "$FORCE" != "true" ]]; then
  if [[ -n "$(git status --porcelain 2>/dev/null)" ]]; then
    echo "⚠️  WARNING: You have uncommitted changes in Git."
    read -rp "Do you want to proceed anyway? (y/N): " PROCEED
    if [[ "$PROCEED" != "y" && "$PROCEED" != "Y" ]]; then
      echo "Aborted by user."
      exit 0
    fi
  fi
fi

# ------------------------------------------------------------------------------
# Step 1: Text Content Replacement (via Cross-Platform Python Engine)
# ------------------------------------------------------------------------------
echo ""
echo "[1/5] Scanning and rewriting file contents across entire repository..."

python3 - <<EOF
import os, sys, re

repo_root = "${REPO_ROOT}"
dry_run = "${DRY_RUN}" == "true"

old_input = "${OLD_INPUT}"
new_input = "${NEW_INPUT}"

def tokenize(s):
    parts = re.split(r'[-_\s]+', s)
    tokens = []
    for part in parts:
        subparts = re.findall(r'[A-Z]?[a-z0-9]+|[A-Z]+(?=[A-Z][a-z0-9]|\b)', part)
        if subparts:
            tokens.extend(subparts)
        elif part:
            tokens.append(part)
    return tokens if tokens else [s]

old_tokens = tokenize(old_input)
new_tokens = tokenize(new_input)

# Target casing variations
new_lower = "".join(t.lower() for t in new_tokens)
new_upper = "".join(t.upper() for t in new_tokens)
new_title = "".join(t.capitalize() for t in new_tokens)
new_pascal = new_input if any(c.isupper() for c in new_input[1:]) else "".join(t.capitalize() for t in new_tokens)

new_kebab = "-".join(t.lower() for t in new_tokens)
new_snake = "_".join(t.lower() for t in new_tokens)
new_screaming = "_".join(t.upper() for t in new_tokens)

old_kebab = "-".join(t.lower() for t in old_tokens)
old_snake = "_".join(t.lower() for t in old_tokens)
old_screaming = "_".join(t.upper() for t in old_tokens)
old_base = "".join(t.lower() for t in old_tokens)

# Multi-token compound replacements to apply first (if applicable)
compounds = []
if len(old_tokens) > 1:
    compounds.append((old_screaming, new_screaming))
    compounds.append((old_kebab, new_kebab))
    compounds.append((old_snake, new_snake))

# Universal case-insensitive regex for base term
base_pattern = re.compile(re.escape(old_base), re.IGNORECASE)

def replacer(match):
    text = match.group(0)
    if text.isupper():
        return new_upper
    if text.islower():
        return new_lower
    if text.istitle():
        return new_title
    # Mixed / PascalCase / CamelCase (e.g. ReqruitBook -> Karmarat or SuperHire)
    return new_pascal

# Directories to strictly ignore
IGNORE_DIRS = {
    ".git",
    "node_modules",
    ".next",
    "dist",
    "build",
    "bin",
    "out",
    ".pnpm-store",
    "backups",
    ".run",
    ".idea",
    ".vscode",
    ".turbo"
}

# Binary and non-source extensions to ignore
IGNORE_EXTS = {
    ".png", ".jpg", ".jpeg", ".gif", ".ico", ".webp",
    ".woff", ".woff2", ".ttf", ".eot",
    ".pdf", ".zip", ".tar", ".gz", ".sql.gz", ".tar.gz",
    ".exe", ".so", ".dylib", ".pem", ".key", ".DS_Store"
}

modified_files = 0
total_replacements = 0

for root, dirs, files in os.walk(repo_root):
    # Prune ignored directories in-place
    dirs[:] = [d for d in dirs if d not in IGNORE_DIRS]

    for file in files:
        if file in {".DS_Store", "jwt-private.pem", "jwt-public.pem"}:
            continue

        _, ext = os.path.splitext(file)
        if ext.lower() in IGNORE_EXTS:
            continue

        filepath = os.path.join(root, file)
        
        # Don't touch the rebrand script itself
        if os.path.abspath(filepath) == os.path.abspath("${SCRIPT_DIR}/rebrand.sh"):
            continue

        try:
            with open(filepath, "rb") as f_raw:
                sample = f_raw.read(4096)
                if b"\x00" in sample:
                    # Skip binary files safely
                    continue

            with open(filepath, "r", encoding="utf-8", errors="ignore") as f:
                content = f.read()
        except Exception:
            continue

        # Check if file has any match (compounds or base term)
        has_compound = any(c[0] in content for c in compounds)
        has_base = (old_base in content.lower())

        if has_compound or has_base:
            original_content = content
            count = 0
            
            # 1. Apply multi-word compounds if defined
            for c_old, c_new in compounds:
                if c_old in content:
                    count += content.count(c_old)
                    content = content.replace(c_old, c_new)

            # 2. Apply smart case-preserving replacement
            matches = list(base_pattern.finditer(content))
            if matches:
                count += len(matches)
                content = base_pattern.sub(replacer, content)

            if count > 0 and content != original_content:
                rel_path = os.path.relpath(filepath, repo_root)
                modified_files += 1
                total_replacements += count
                print(f"      {'[DRY-RUN] Would update' if dry_run else 'Updated'}: {rel_path} ({count} occurrences)")

                if not dry_run:
                    with open(filepath, "w", encoding="utf-8") as f:
                        f.write(content)

print(f"\n      Summary: {modified_files} files affected, {total_replacements} total occurrences replaced.")
EOF

# ------------------------------------------------------------------------------
# Step 2: File & Directory Renaming
# ------------------------------------------------------------------------------
echo ""
echo "[2/5] Checking for files and directories requiring rename..."
python3 - <<EOF
import os, sys, re

repo_root = "${REPO_ROOT}"
dry_run = "${DRY_RUN}" == "true"

old_input = "${OLD_INPUT}"
new_input = "${NEW_INPUT}"

def tokenize(s):
    parts = re.split(r'[-_\s]+', s)
    tokens = []
    for part in parts:
        subparts = re.findall(r'[A-Z]?[a-z0-9]+|[A-Z]+(?=[A-Z][a-z0-9]|\b)', part)
        if subparts:
            tokens.extend(subparts)
        elif part:
            tokens.append(part)
    return tokens if tokens else [s]

old_tokens = tokenize(old_input)
new_tokens = tokenize(new_input)

new_lower = "".join(t.lower() for t in new_tokens)
new_upper = "".join(t.upper() for t in new_tokens)
new_title = "".join(t.capitalize() for t in new_tokens)
new_pascal = new_input if any(c.isupper() for c in new_input[1:]) else "".join(t.capitalize() for t in new_tokens)

new_kebab = "-".join(t.lower() for t in new_tokens)
new_snake = "_".join(t.lower() for t in new_tokens)
new_screaming = "_".join(t.upper() for t in new_tokens)

old_kebab = "-".join(t.lower() for t in old_tokens)
old_snake = "_".join(t.lower() for t in old_tokens)
old_screaming = "_".join(t.upper() for t in old_tokens)
old_base = "".join(t.lower() for t in old_tokens)

compounds = []
if len(old_tokens) > 1:
    compounds.append((old_screaming, new_screaming))
    compounds.append((old_kebab, new_kebab))
    compounds.append((old_snake, new_snake))

base_pattern = re.compile(re.escape(old_base), re.IGNORECASE)

def replacer(match):
    text = match.group(0)
    if text.isupper():
        return new_upper
    if text.islower():
        return new_lower
    if text.istitle():
        return new_title
    return new_pascal

def rename_str(s):
    for c_old, c_new in compounds:
        s = s.replace(c_old, c_new)
    return base_pattern.sub(replacer, s)

IGNORE_DIRS = {".git", "node_modules", ".next", "dist", "build", "backups", ".pnpm-store", ".turbo"}
to_rename = []

for root, dirs, files in os.walk(repo_root, topdown=False):
    # Skip any path within ignored directories
    path_parts = set(root.split(os.sep))
    if path_parts.intersection(IGNORE_DIRS):
        continue

    for f in files:
        if old_base in f.lower() or any(c[0] in f for c in compounds):
            old_path = os.path.join(root, f)
            new_f = rename_str(f)
            if new_f != f:
                new_path = os.path.join(root, new_f)
                to_rename.append((old_path, new_path))

    for d in dirs:
        if old_base in d.lower() or any(c[0] in d for c in compounds):
            old_path = os.path.join(root, d)
            new_d = rename_str(d)
            if new_d != d:
                new_path = os.path.join(root, new_d)
                to_rename.append((old_path, new_path))

for old_path, new_path in to_rename:
    rel_old = os.path.relpath(old_path, repo_root)
    rel_new = os.path.relpath(new_path, repo_root)
    print(f"      {'[DRY-RUN] Would rename' if dry_run else 'Renaming'}: {rel_old} -> {rel_new}")
    if not dry_run:
        os.rename(old_path, new_path)

if not to_rename:
    print("      No files or directories needed renaming.")
EOF

# If dry run, stop here
if [[ "$DRY_RUN" == "true" ]]; then
  echo ""
  echo "=========================================================="
  echo " Dry run complete. No files or directories were modified."
  echo " To apply changes live, run without --dry-run."
  echo "=========================================================="
  exit 0
fi

# ------------------------------------------------------------------------------
# Step 3: Regenerate Reserved Slugs (if present in repo)
# ------------------------------------------------------------------------------
echo ""
echo "[3/5] Regenerating reserved tenancy slugs..."
if [[ -f "scripts/gen-reserved-slugs.sh" ]]; then
  ./scripts/gen-reserved-slugs.sh
  echo "      Reserved slugs synchronized between Go and TypeScript."
else
  echo "      No reserved slugs generator found (skipping)."
fi

# ------------------------------------------------------------------------------
# Step 4: Synchronize Go & Node / PNPM Ecosystems
# ------------------------------------------------------------------------------
echo ""
echo "[4/5] Synchronizing dependency ecosystems..."

if [[ "$SKIP_TIDY" != "true" ]]; then
  if [[ -f "go.mod" ]] && command -v go >/dev/null 2>&1; then
    echo "      Running 'go mod tidy' to refresh Go module and checksums..."
    go mod tidy
  else
    echo "      Skipping 'go mod tidy' (not a Go workspace or 'go' not found)."
  fi
fi

if [[ "$SKIP_INSTALL" != "true" ]]; then
  if [[ -f "package.json" || -f "pnpm-workspace.yaml" ]] && command -v pnpm >/dev/null 2>&1; then
    echo "      Running 'pnpm install' to link @${NEW_LOWER}/* workspace packages..."
    pnpm install --no-frozen-lockfile
  elif [[ -f "package.json" ]] && command -v npm >/dev/null 2>&1; then
    echo "      Running 'npm install' to link workspace packages..."
    npm install
  else
    echo "      Skipping package install (no package.json or package manager found)."
  fi
fi

# ------------------------------------------------------------------------------
# Step 5: Verification Suite
# ------------------------------------------------------------------------------
echo ""
echo "[5/5] Running verification gate..."

if [[ "$SKIP_VERIFY" != "true" ]]; then
  if [[ -f "Makefile" ]] && grep -q '^verify:' Makefile; then
    echo "      Executing 'make verify' to ensure zero compiler or lint regressions..."
    if make verify; then
      echo "      ✅ Verification suite passed cleanly with exit code 0!"
    else
      echo "      ❌ Verification encountered errors. Inspect output above."
      exit 1
    fi
  else
    echo "      No 'verify' target found in Makefile (skipping)."
  fi
else
  echo "      Skipping verification gate (--skip-verify passed)."
fi

echo ""
echo "=========================================================="
echo " Rebranding to '${NEW_PASCAL}' completed successfully! 🎉"
echo "=========================================================="
echo " Next steps:"
echo "   1. Review git diff:   git diff --stat"
echo "   2. Start stack:       make dev (or make prod-up)"
echo "   3. Commit changes:    git commit -am 'feat: rebrand platform to ${NEW_PASCAL}'"
echo "=========================================================="
