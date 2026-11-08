#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/wallet_catalog_test.go wallet/wallet_key_derivation_test.go wallet/wallet_lock_test.go wallet/rgb11_scope_activation_test.go wallet/rgb11_review_refresh_regression_test.go wallet/rgb11_snapshot_failure_test.go wallet/rgb11_channel_send_test.go wallet/rgb11_unspent_sibling_recovery_test.go wallet/rgb11_transfer_test.go
/usr/local/go/bin/go test -race -json -count=1 -timeout=8m ./wallet -run '^(TestManagedRootWalletCannotBeDeleted|TestAccountSecretRecreatedFromRootAcrossDevices|TestUnlockWalletIsAtomicOnPasswordFailure|TestAccountRecoverySelectsRealRGB11WalletScope|TestRGB11UnchangedRefreshDoesNotRewriteSnapshot|TestRGB11AddressMetadataIncludedInFirstAtomicCommit|TestRGB11SnapshotFailureRevokesLiveReadiness|TestRGB11IssuedFungibleFirstReleaseSchemasSendReceive|TestRGB11RefreshUnspentSelfSendSibling|TestRGB11ChannelSendRecoversAfterObservedPeerBroadcast|TestRGB11ChannelRecoveryPublicResumeDoesNotWaitForRefresh)$' > "$out/wallet-tail-fixtures-mcp-race-v2-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/wallet-tail-fixtures-mcp-race-v2-20261030.exit"
tail -n 12 "$out/wallet-tail-fixtures-mcp-race-v2-20261030.jsonl"
exit "$rc"
