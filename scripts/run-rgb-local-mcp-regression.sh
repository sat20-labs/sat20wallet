#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/rgb11_account_scope_recovery_test.go wallet/dkvs_rgb_local_commit_test.go wallet/identity_snapshot_test.go wallet/legacy_gob_isolation_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=8m ./wallet -run '^(TestRGB11(LocalCommitDuringContinuousDKVSRefresh|LocalRecoveryCommitIsAtomicAndSurvivesRestart|AcknowledgedBackupFailureCompletesLocallyBeforeNewCommit|AcknowledgedBackupBeforeReceiveAllowsLocalCommit|RejectedCompactionSurvivesOlderACKAndRestart)|TestWalletIdentitySnapshotSurvivesAccountSwitch|TestAccountManagementSyncSnapshotCarriesRootSigner|TestLegacyGobChannelReadsPreserveOldFieldsAndAvoidNativeRecovery)$' > "$out/rgb-local-mcp-v3-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/rgb-local-mcp-v3-20261030.exit"
tail -n 12 "$out/rgb-local-mcp-v3-20261030.jsonl"
exit "$rc"
