#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/bin/shasum -a 256 wallet/rgb11_account_scope_recovery_test.go wallet/dkvs_rgb_local_commit_test.go > "$out/rgb-scope-mcp-race-v1-20261030.sha256"
/usr/local/go/bin/go test -race -json -count=1 -timeout=12m ./wallet -run '^(TestRGB11AccountRecoveryTwoAssetScopesAndScopeBinding|TestRGB11LocalCommitDuringContinuousDKVSRefresh|TestRGB11LocalRecoveryCommitIsAtomicAndSurvivesRestart|TestRGB11AcknowledgedBackupFailureCompletesLocallyBeforeNewCommit|TestRGB11AcknowledgedBackupBeforeReceiveAllowsLocalCommit|TestRGB11RejectedCompactionSurvivesOlderACKAndRestart)$' > "$out/rgb-scope-mcp-race-v1-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/rgb-scope-mcp-race-v1-20261030.exit"
tail -n 12 "$out/rgb-scope-mcp-race-v1-20261030.jsonl"
exit "$rc"
