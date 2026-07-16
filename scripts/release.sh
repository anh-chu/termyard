#!/usr/bin/env bash
# Release script — single source of truth for version bumps.
# Usage: ./scripts/release.sh [patch|minor|major]
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

BUMP="${1:-patch}"
if [[ ! "$BUMP" =~ ^(major|minor|patch)$ ]]; then
  echo "ERROR: argument must be patch, minor, or major (got: $BUMP)" >&2
  exit 1
fi

# Read current version from version.go (single source of truth)
CURRENT=$(grep -oP 'var VERSION = "\K[^"]+' pkg/common/version.go)
if [ -z "$CURRENT" ]; then
  echo "ERROR: could not read VERSION from pkg/common/version.go" >&2
  exit 1
fi

IFS='.' read -r MAJOR MINOR PATCH_NUM <<< "$CURRENT"

case "$BUMP" in
  major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH_NUM=0 ;;
  minor) MINOR=$((MINOR + 1)); PATCH_NUM=0 ;;
  patch) PATCH_NUM=$((PATCH_NUM + 1)) ;;
esac

NEW_VERSION="${MAJOR}.${MINOR}.${PATCH_NUM}"
NEW_TAG="v${NEW_VERSION}"

echo "Bumping: $CURRENT -> $NEW_VERSION ($BUMP)"

# Update all version files
# 1. pkg/common/version.go
sed -i "s/var SUMMARY = \"v${CURRENT}\"/var SUMMARY = \"v${NEW_VERSION}\"/" pkg/common/version.go
sed -i "s/var VERSION = \"${CURRENT}\"/var VERSION = \"${NEW_VERSION}\"/" pkg/common/version.go

# 2. web/package.json
if [ -f web/package.json ]; then
  sed -i "s/\"version\": \"${CURRENT}\"/\"version\": \"${NEW_VERSION}\"/" web/package.json
fi

# 3. web/package-lock.json
if [ -f web/package-lock.json ]; then
  sed -i "s/\"version\": \"${CURRENT}\"/\"version\": \"${NEW_VERSION}\"/g" web/package-lock.json
fi

# 4. .release-please-manifest.json
if [ -f .release-please-manifest.json ]; then
  echo "{\".\":\"${NEW_VERSION}\"}" > .release-please-manifest.json
fi

# Verification — abort if any file wasn't updated
echo "Verifying..."
ERRORS=0

GO_VER=$(grep -oP 'var VERSION = "\K[^"]+' pkg/common/version.go)
if [ "$GO_VER" != "$NEW_VERSION" ]; then
  echo "  FAIL: pkg/common/version.go still has $GO_VER (expected $NEW_VERSION)" >&2
  ERRORS=$((ERRORS + 1))
else
  echo "  OK: pkg/common/version.go = $NEW_VERSION"
fi

if [ -f web/package.json ]; then
  PKG_VER=$(grep -oP '"version": "\K[^"]+' web/package.json)
  if [ "$PKG_VER" != "$NEW_VERSION" ]; then
    echo "  FAIL: web/package.json still has $PKG_VER (expected $NEW_VERSION)" >&2
    ERRORS=$((ERRORS + 1))
  else
    echo "  OK: web/package.json = $NEW_VERSION"
  fi
fi

if [ -f web/package-lock.json ]; then
  LOCK_VER=$(grep -m1 -oP '"version": "\K[^"]+' web/package-lock.json)
  if [ "$LOCK_VER" != "$NEW_VERSION" ]; then
    echo "  FAIL: web/package-lock.json still has $LOCK_VER (expected $NEW_VERSION)" >&2
    ERRORS=$((ERRORS + 1))
  else
    echo "  OK: web/package-lock.json = $NEW_VERSION"
  fi
fi

if [ "$ERRORS" -gt 0 ]; then
  echo "ABORT: $ERRORS file(s) failed verification. Nothing committed." >&2
  exit 1
fi

# Check tag doesn't already exist
if git rev-parse "$NEW_TAG" >/dev/null 2>&1; then
  echo "ERROR: tag $NEW_TAG already exists" >&2
  exit 1
fi

# Stage, commit, push
git add pkg/common/version.go web/package.json web/package-lock.json .release-please-manifest.json
git commit -m "chore(release): ${NEW_VERSION}"
git push origin master

echo ""
echo "Pushed $NEW_TAG. Release workflow should trigger now."
echo "Check: gh run list --workflow=release-please.yml --limit 1"
