#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/review_rgb11_baseline_regression_test.go wallet/review_rgb11_baseline_publish_regression_test.go wallet/rgb11_api_sync_gate_test.go wallet/rgb11_address_api_test.go wallet/rgb11_address_transfer_test.go wallet/rgb11_network_lock_audit_test.go wallet/rgb11_managed_operation_lock_test.go wallet/rgb11_managed_operation_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=5m ./wallet -run '^(TestReviewRGB11ExplicitSyncControlsRemoteRecoveryApplication|TestReviewRGB11ExplicitSyncImportsActiveAlongsideLocalCatalogEdits|TestRGB11MailboxConsumerUsesGenerationBeforeReplicaRead|TestRGB11AddressTransferSchemeA|TestRGB11PublicOperationsRequireRecoveryBeforeMutation|TestRGBNetworkBoundariesHoldNoWalletLocks|TestRGB11BackupNetworkWaitDoesNotBlockRGBState|TestRGB11ManagedOperationWaitDoesNotBlockRGBState|TestRGB11ManagedOperationKeepsCapturedScope|TestRGB11ManagedOperationWaitDoesNotBlockReadOnlyAPIs|TestRGB11ManagedOperationWithoutRecoveryKeepsChainLockFree|TestRGB11ManagedOperationCommitsOneGenerationBeforeBackup|TestRGB11NewInvoicePersistsLocallyBeforeAsynchronousBackup|TestAccountManagedSyncPublishesActiveRGB11TransitionToStableClosure)$' > "$out/rgb-async-fixtures-mcp-v4-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/rgb-async-fixtures-mcp-v4-20261030.exit"
tail -n 12 "$out/rgb-async-fixtures-mcp-v4-20261030.jsonl"
exit "$rc"
