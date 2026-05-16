#!/usr/bin/env bash
# Build Snuffleupagus for one PHP minor version. M41, ADR-0088.
#
# Usage: build.sh <php-minor>
#   build.sh 8.3
#
# Idempotent: if /usr/lib/php/jabali-snuffleupagus/<minor>/snuffleupagus.so
# already exists and the source SHA256 hasn't changed, skips rebuild.
#
# Caller (install.sh) supplies SNUFFLEUPAGUS_VERSION + SNUFFLEUPAGUS_SHA256.

set -euo pipefail

PHP_MINOR="${1:?php minor required, e.g. 8.3}"
SNUF_VERSION="${SNUFFLEUPAGUS_VERSION:?SNUFFLEUPAGUS_VERSION not set}"
SNUF_SHA256="${SNUFFLEUPAGUS_SHA256:?SNUFFLEUPAGUS_SHA256 not set}"

OUT_DIR="/usr/lib/php/jabali-snuffleupagus/${PHP_MINOR}"
OUT_SO="${OUT_DIR}/snuffleupagus.so"
PHPIZE="/usr/bin/phpize${PHP_MINOR}"
PHP_CONFIG="/usr/bin/php-config${PHP_MINOR}"

# Sanity checks.
if [[ ! -x "$PHPIZE" ]]; then
  echo "build.sh: phpize for PHP ${PHP_MINOR} not found at ${PHPIZE}" >&2
  exit 1
fi
if [[ ! -x "$PHP_CONFIG" ]]; then
  echo "build.sh: php-config for PHP ${PHP_MINOR} not found at ${PHP_CONFIG}" >&2
  exit 1
fi

# Idempotency: skip if .so present and the version-stamp matches.
STAMP_FILE="${OUT_DIR}/.jabali-version"
if [[ -f "$OUT_SO" && -f "$STAMP_FILE" ]] && \
   [[ "$(cat "$STAMP_FILE" 2>/dev/null)" == "${SNUF_VERSION}+${SNUF_SHA256}" ]]; then
  echo "build.sh: ${PHP_MINOR} already at v${SNUF_VERSION}, skip"
  exit 0
fi

WORK="$(mktemp -d /tmp/jabali-snuffleupagus-build.XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

cd "$WORK"

# Download source by tag, verify SHA256.
TARBALL="snuffleupagus-${SNUF_VERSION}.tar.gz"
curl -fsSL --retry 3 \
  "https://github.com/jvoisin/snuffleupagus/archive/refs/tags/v${SNUF_VERSION}.tar.gz" \
  -o "$TARBALL"

ACTUAL_SHA="$(sha256sum "$TARBALL" | awk '{print $1}')"
if [[ "$ACTUAL_SHA" != "$SNUF_SHA256" ]]; then
  echo "build.sh: SHA256 mismatch for ${TARBALL}" >&2
  echo "  expected: $SNUF_SHA256" >&2
  echo "  actual:   $ACTUAL_SHA" >&2
  exit 1
fi

tar -xzf "$TARBALL"
cd "snuffleupagus-${SNUF_VERSION}/src"

# Build.
"$PHPIZE"
./configure --with-php-config="$PHP_CONFIG"
make -j"$(nproc)"

# Install.
mkdir -p "$OUT_DIR"
# 0755, NOT the caller's umask-077 default of 0700: the per-user
# php-fpm master runs as User=<hosting-user> and must traverse this
# path to dlopen snuffleupagus.so at startup. A 0700 root dir here
# silently prevents the extension from loading on ALL web traffic
# (CLI/`php-fpm -i` as root still see it, masking the bug). The .so is
# a public compiled extension, no secrets.
chmod 0755 /usr/lib/php/jabali-snuffleupagus "$OUT_DIR"
install -m 0755 modules/snuffleupagus.so "$OUT_SO"
echo "${SNUF_VERSION}+${SNUF_SHA256}" > "$STAMP_FILE"

echo "build.sh: ${PHP_MINOR} ✓ ${SNUF_VERSION}"
