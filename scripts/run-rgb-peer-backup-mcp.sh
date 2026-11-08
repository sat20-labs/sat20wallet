#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/account_management_state.go wallet/account_restore_generation_test.go wallet/rgb11_review_r1_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=5m ./wallet -run '^(TestAccountColdRestoreInitializesLocalGeneration|TestAccountOwnACKDuringSnapshotCaptureDoesNotConflict|TestRGB11R1ExpiredBlindPeerSync|TestRGB11R1WitnessBatchIndependentRequests|TestRGB11R1ReceiptExpansionPeerSync)$' > "$out/rgb-peer-backup-mcp-v3-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/rgb-peer-backup-mcp-v3-20261030.exit"
tail -n 12 "$out/rgb-peer-backup-mcp-v3-20261030.jsonl"
exit "$rc"
