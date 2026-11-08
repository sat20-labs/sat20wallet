#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/dkvs_pwa_final_review_test.go wallet/dkvs_subscription_protocol_test.go wallet/dkvs_review_fifth_test.go wallet/dkvs_review_sixth_test.go wallet/dkvs_review_contract_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=5m ./wallet -run '^(TestDKVSThreeFixRestartPendingReadDoesNotGrantWrites|TestDKVSThreeFixOfflineRecoveryBlocksLocalEdits|TestDKVSReviewCompletedWatchCannotReviveExpiredSiblingScope|TestDKVSReviewConfirmedReplicaReadBoundaries|TestDKVSPWAFinalReview|TestDKVSFinalManagedPrefixesUseCurrentGenerationSync|TestDKVSCurrentSyncRejectsSourceGenerationRollback|TestDKVSFinalRejectsUnapprovedEndpointSwitch|TestDKVSFinalCASConflictRefreshesRebuildsAndRetries)$' > "$out/dkvs-boundaries-mcp-v2-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/dkvs-boundaries-mcp-v2-20261030.exit"
tail -n 12 "$out/dkvs-boundaries-mcp-v2-20261030.jsonl"
exit "$rc"
