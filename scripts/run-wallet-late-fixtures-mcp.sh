#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/review_account_merge_regression_test.go wallet/pwa_warm_unlock_latency_test.go wallet/pwa_warm_unlock_queued_test.go wallet/remote_action_rehydrate_test.go wallet/rgb11_broadcast_test.go wallet/review_rgb11_read_regression_test.go wallet/rgb11_framework_reservation_test.go wallet/rgb11_projection_prevention_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=8m ./wallet -run '^(TestAccountColdUnlockResumesPersistedMetadata|TestAccountFirstWalletSchedulesInitialBackup|TestAccountSubaccountConcurrentFieldsRequireReconciliation|TestReviewAccountConcurrentBundlesRequireReconciliation|TestPWAWarmUnlockDuringRGBNetworkWait|TestPWAWarmUnlockAndIdentitySwitchDuringRGBNetworkWait|TestUnlockWalletRehydratesInitiatorRemoteActionSigningWallet|TestUnlockWalletRemoteActionSignerMismatchFailsClosed|TestRestartDoesNotActivateClosedRemoteAction|TestReviewRGB11ReadDoesNotMixAtomicInvoiceGenerations|TestRGB11BroadcastPersistenceFailureRecoversFromBitcoinEvidence|TestRGB11ConflictCandidateFlushAtomic|TestRGB11ReservationBatchFailureDoesNotPublishInvoice|TestRGB11ReservationSnapshotFailureKeepsPreviousGeneration|TestRGB11ReservationInvoicePostCreateFailureRollsBack|TestRGB11RecoveryProjectionFlushFailureKeepsPriorStateAcrossRestart)$' > "$out/wallet-late-fixtures-mcp-v2-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/wallet-late-fixtures-mcp-v2-20261030.exit"
tail -n 12 "$out/wallet-late-fixtures-mcp-v2-20261030.jsonl"
exit "$rc"
