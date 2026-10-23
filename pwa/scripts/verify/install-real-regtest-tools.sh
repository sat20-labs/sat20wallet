#!/usr/bin/env bash
set -euo pipefail
umask 077

# Install isolated local dependencies for workspace-e2e.mjs. This installs no
# services, never uses the default Bitcoin datadir, and does not modify PATH or
# a global package manager. The target directory must not already exist.
#
# Verified on x86_64 macOS 26.6.2 with rustc/cargo 1.97.1 and Apple clang 17:
#   cargo build --locked --release --bin electrs
# using an empty, per-install CARGO_HOME and CARGO_TARGET_DIR.
#
# Usage:
#   scripts/verify/install-real-regtest-tools.sh /private/tmp/sat20-real-tools

CORE_VERSION=31.0
CORE_ARCHIVE="bitcoin-${CORE_VERSION}-x86_64-apple-darwin.tar.gz"
CORE_BASE="https://bitcoincore.org/bin/bitcoin-core-${CORE_VERSION}"
CORE_ARCHIVE_SHA256=56824dd705bc2a3b22d42e8aa02ed53498d491ff7c2c8aa96831333871887ead
ELECTRS_REPOSITORY=https://github.com/Blockstream/electrs.git
ELECTRS_COMMIT=63edb614d67f530ac725718ec0c3e42e96bb7d54

usage() {
  echo "Usage: $0 /absolute/path/to/new-tools-directory" >&2
}

if [[ $# -ne 1 ]]; then
  usage
  exit 2
fi

INSTALL_ROOT=${1%/}
if [[ -z "$INSTALL_ROOT" || "$INSTALL_ROOT" != /* || "$INSTALL_ROOT" == "/" ]]; then
  echo "error: install directory must be an absolute non-root path" >&2
  exit 2
fi
if [[ -e "$INSTALL_ROOT" || -L "$INSTALL_ROOT" ]]; then
  echo "error: install directory already exists; choose a new independent directory: $INSTALL_ROOT" >&2
  exit 2
fi

mkdir -m 700 -p "$INSTALL_ROOT"
chmod 700 "$INSTALL_ROOT"
LOG_PATH="$INSTALL_ROOT/install.log"
: > "$LOG_PATH"
chmod 600 "$LOG_PATH"
exec > >(tee -a "$LOG_PATH") 2>&1

echo "Install root: $INSTALL_ROOT"
echo "Log: $LOG_PATH"
echo "Platform: $(uname -sm)"
if [[ "$(uname -s)" != Darwin || "$(uname -m)" != x86_64 ]]; then
  echo "error: this pinned Core archive is for x86_64 macOS" >&2
  exit 1
fi

for tool in curl git tar shasum cargo rustc clang; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "error: required local build tool is missing: $tool" >&2
    exit 1
  fi
done

rustc --version
cargo --version
clang --version | sed -n '1,2p'
if command -v xcode-select >/dev/null 2>&1; then
  xcode-select -p
fi

WORK_DIR="$INSTALL_ROOT/work"
DOWNLOAD_DIR="$WORK_DIR/downloads"
BIN_DIR="$INSTALL_ROOT/bin"
mkdir -m 700 -p "$DOWNLOAD_DIR" "$BIN_DIR"

echo "Downloading official Bitcoin Core ${CORE_VERSION} archive and SHA256SUMS"
curl --fail --location --retry 3 --retry-delay 2 --silent --show-error \
  "$CORE_BASE/$CORE_ARCHIVE" --output "$DOWNLOAD_DIR/$CORE_ARCHIVE"
curl --fail --location --retry 3 --retry-delay 2 --silent --show-error \
  "$CORE_BASE/SHA256SUMS" --output "$DOWNLOAD_DIR/SHA256SUMS"

SUM_LINE=$(awk -v name="$CORE_ARCHIVE" '$2 == name { line = $0; count++ } END { if (count != 1) exit 1; print line }' "$DOWNLOAD_DIR/SHA256SUMS")
SUM_VALUE=${SUM_LINE%% *}
if [[ "$SUM_VALUE" != "$CORE_ARCHIVE_SHA256" ]]; then
  echo "error: official SHA256SUMS entry differs from pinned Core ${CORE_VERSION} checksum" >&2
  exit 1
fi
printf '%s\n' "$SUM_LINE" > "$DOWNLOAD_DIR/SHA256SUMS.verified"
(
  cd "$DOWNLOAD_DIR"
  shasum -a 256 -c SHA256SUMS.verified
)

CORE_EXTRACT="$WORK_DIR/core-extract"
mkdir -m 700 "$CORE_EXTRACT"
tar -xzf "$DOWNLOAD_DIR/$CORE_ARCHIVE" -C "$CORE_EXTRACT"
cp "$CORE_EXTRACT/bitcoin-${CORE_VERSION}/bin/bitcoind" "$BIN_DIR/bitcoind"
cp "$CORE_EXTRACT/bitcoin-${CORE_VERSION}/bin/bitcoin-cli" "$BIN_DIR/bitcoin-cli"
chmod 700 "$BIN_DIR/bitcoind" "$BIN_DIR/bitcoin-cli"

ELECTRS_SOURCE="$WORK_DIR/electrs-src"
echo "Fetching Blockstream/electrs commit $ELECTRS_COMMIT"
git clone --filter=blob:none --no-checkout "$ELECTRS_REPOSITORY" "$ELECTRS_SOURCE"
git -C "$ELECTRS_SOURCE" fetch --depth 1 origin "$ELECTRS_COMMIT"
git -C "$ELECTRS_SOURCE" checkout --detach FETCH_HEAD
ACTUAL_ELECTRS_COMMIT=$(git -C "$ELECTRS_SOURCE" rev-parse HEAD)
if [[ "$ACTUAL_ELECTRS_COMMIT" != "$ELECTRS_COMMIT" ]]; then
  echo "error: Electrs checkout is $ACTUAL_ELECTRS_COMMIT, expected $ELECTRS_COMMIT" >&2
  exit 1
fi

export CARGO_HOME="$WORK_DIR/cargo-home"
export CARGO_TARGET_DIR="$WORK_DIR/cargo-target"
mkdir -m 700 -p "$CARGO_HOME" "$CARGO_TARGET_DIR"
echo "Building Electrs from a clean per-install Cargo home and target directory"
(
  cd "$ELECTRS_SOURCE"
  cargo build --locked --release --bin electrs
)
cp "$CARGO_TARGET_DIR/release/electrs" "$BIN_DIR/electrs"
chmod 700 "$BIN_DIR/electrs"

VERSION_DATADIR="$WORK_DIR/version-probe"
mkdir -m 700 "$VERSION_DATADIR"
"$BIN_DIR/bitcoind" -datadir="$VERSION_DATADIR" -version
"$BIN_DIR/bitcoin-cli" -datadir="$VERSION_DATADIR" -version
rmdir "$VERSION_DATADIR"
ELECTRS_HELP="$WORK_DIR/electrs-help.txt"
"$BIN_DIR/electrs" --help > "$ELECTRS_HELP" 2>&1
for option in --network --jsonrpc-import --daemon-dir --daemon-rpc-addr \
  --db-dir --http-addr --electrum-rpc-addr --monitoring-addr; do
  if ! grep -Fq -- "$option" "$ELECTRS_HELP"; then
    echo "error: built Electrs CLI is missing required option $option" >&2
    exit 1
  fi
done

echo "Installed binary paths (copy these exports into the local test shell):"
printf 'export SAT20_REAL_BITCOIND=%q\n' "$BIN_DIR/bitcoind"
printf 'export SAT20_REAL_BITCOIN_CLI=%q\n' "$BIN_DIR/bitcoin-cli"
printf 'export SAT20_REAL_ELECTRS=%q\n' "$BIN_DIR/electrs"
echo "Electrs source commit: $ACTUAL_ELECTRS_COMMIT"
echo "Full installation log: $LOG_PATH"
