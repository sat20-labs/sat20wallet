#!/bin/zsh
set -euo pipefail

GO_BIN="/usr/local/go/bin/go"
cd "/Users/yingfeng/github/sat20wallet/sdk"
export TEST_RUN_LOG_ROOT_OVERRIDE="/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd -P)"
source "/Users/yingfeng/mcp/local_access/scripts/commands/test-run-log.sh"

variant="${1:-}"
timeout_ms="${2:-600000}"
timeout_pattern='^[1-9][0-9]*$'
if [[ "$#" -lt 1 || "$#" -gt 2 ]]; then
  print -u2 "usage: sat20wallet-sdk-test.sh <all|e2e|live> [timeoutMs]"
  exit 2
fi
if [[ ! "$timeout_ms" =~ $timeout_pattern ]]; then
  print -u2 "timeoutMs must be a positive integer in milliseconds: $timeout_ms"
  exit 2
fi
if [[ ! -x "$GO_BIN" ]]; then
  print -u2 "Go executable not found: $GO_BIN"
  exit 2
fi

module="$($GO_BIN list -m 2>/dev/null || true)"
if [[ "$module" != "github.com/sat20-labs/sat20wallet/sdk" ]]; then
  print -u2 "expected sat20wallet SDK module, got: ${module:-<none>}"
  exit 2
fi

test_run_log_init "sat20wallet-sdk-test" "$variant" "$timeout_ms"

run() {
  test_run_exec_supervised "$@"
}

case "$variant" in
  all)
    run /bin/bash "$SCRIPT_DIR/sat20wallet-sdk-native.sh"
    ;;
  e2e)
    workspace_root="$(cd ../.. && pwd -P)"
    for dependency in satoshinet indexer rgb11 transcend; do
      if [[ ! -d "$workspace_root/$dependency" ]]; then
        print -u2 "missing sibling dependency required by wallet E2E: $workspace_root/$dependency"
        exit 2
      fi
    done

    # The e2e harness builds the SatoshiNet core and miner runtimes itself,
    # including rpctest,wallet_plugin and rpctest,stp_plugin where required.
    run /usr/bin/env -u SAT20WALLET_RUN_LIVE_NETWORK_TESTS \
      "$GO_BIN" test -p=1 -parallel=1 ./e2e "-timeout=${timeout_ms}ms" -count=1
    ;;
  live)
    run /usr/bin/env SAT20WALLET_RUN_LIVE_NETWORK_TESTS=1 \
      "$GO_BIN" test -p=1 -parallel=1 ./wallet/... "-timeout=${timeout_ms}ms" -count=1
    ;;
  *)
    print -u2 "unsupported sat20wallet SDK test variant: $variant"
    exit 2
    ;;
esac
