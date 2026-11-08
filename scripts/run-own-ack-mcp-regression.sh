#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/account_snapshot_ack_race_test.go wallet/account_management_state.go wallet/account_management_state_sync_test.go
/usr/bin/shasum -a 256 wallet/account_snapshot_ack_race_test.go wallet/account_management_state.go > "$out/own-ack-mcp-race-v2-input-20261029.sha256"
/usr/local/go/bin/go test -json -race -count=1 -timeout=8m ./wallet -run '^TestAccount(OwnACKDuringSnapshotCaptureDoesNotConflict|TTLCommittedLostACKColdRetry|TemporaryTTLPolicyChangeConverges|NoopCompletionPreservesNewEdit|NoopProfileFailureCanRetry|NoopFinalizationWindowPreservesUpdates|ManagedPutAckPreservesChangesDuringNetworkWait|ManagementRereview20261006)$' > "$out/own-ack-mcp-race-v2-20261029.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/own-ack-mcp-race-v2-20261029.exit"
/usr/bin/shasum -a 256 "$out/own-ack-mcp-race-v2-20261029.jsonl"
tail -n 18 "$out/own-ack-mcp-race-v2-20261029.jsonl"
exit "$rc"
