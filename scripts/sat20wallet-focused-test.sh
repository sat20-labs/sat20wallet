#!/bin/zsh
set -euo pipefail

GO_BIN="/usr/local/go/bin/go"
cd "/Users/yingfeng/github/sat20wallet/sdk"
variant="${1:-}"
timeout_ms="${2:-600000}"
timeout_pattern='^[1-9][0-9]*$'
testcase_pattern='^Test([A-Z0-9_][A-Za-z0-9_]*)$'
if [[ "$#" -lt 1 || "$#" -gt 2 ]]; then
  print -u2 "usage: sat20wallet-focused-test.sh <safety|account-ready|rgb11-expired|rgb11-broadcast|sweep|TestCaseName> [timeoutMs]"
  exit 2
fi
if [[ ! "$timeout_ms" =~ $timeout_pattern ]]; then
  print -u2 "timeoutMs must be a positive integer in milliseconds: $timeout_ms"
  exit 2
fi
module="$($GO_BIN list -m 2>/dev/null || true)"
if [[ "$module" != "github.com/sat20-labs/sat20wallet/sdk" ]]; then
  print -u2 "expected sat20wallet SDK module, got: ${module:-<none>}"
  exit 2
fi

case "$variant" in
  safety)
    pattern='^(TestCommitmentExportReportsMissingRemoteCommitment|TestSafetySnapshotFailsClosedWithoutWatchtower|TestBuildSweepRejectsNonInitiatorWallet|TestCommitmentExportRequiresChannelPoint|TestPunishStatusFiltersCurrentRemoteWithoutDeletingEvidence)$'
    ;;
  account-ready)
    pattern='^TestWaitAccountManagedDataReadyCancelsAndConverges$'
    ;;
  rgb11-expired)
    pattern='^TestCancelExpiredRGB11Transfer'
    ;;
  rgb11-broadcast)
    pattern='^(Test.*RGB11.*Broadcast.*|TestRGB11ChainReconciliation.*)$'
    ;;
  sweep)
    pattern='^(Test.*Sweep.*|TestBuildSweepRejectsNonInitiatorWallet)$'
    ;;
  *)
    if [[ ! "$variant" =~ $testcase_pattern ]]; then
      print -u2 "unsupported focused test variant: $variant"
      exit 2
    fi
    pattern="^${variant}$"
    ;;
esac

printf '+ %q test ./wallet -run %q -count=1 -timeout=%qms\n' "$GO_BIN" "$pattern" "$timeout_ms"
exec "$GO_BIN" test ./wallet -run "$pattern" -count=1 "-timeout=${timeout_ms}ms"
