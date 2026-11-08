#!/bin/sh
# Fixed local fake-DKVS regression batch; no live services or wallet data.
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/account_management_activation_latency_test.go
/usr/bin/shasum -a 256 wallet/account_management_activation_latency_test.go > "$out/paid-activation-mcp-race-input-20261029.sha256"
/usr/local/go/bin/go test -json -race -count=1 -timeout=5m ./wallet -run '^TestAccountPaidActivation(NetworkWaitDoesNotBlockLocalState|PreservesConcurrentLocalChanges|SelectionChangeDoesNotChangeRootIdentity|RejectsChangedRootConfiguration)$' > "$out/paid-activation-mcp-race-20261029.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/paid-activation-mcp-race-20261029.exit"
/usr/bin/shasum -a 256 "$out/paid-activation-mcp-race-20261029.jsonl"
tail -n 12 "$out/paid-activation-mcp-race-20261029.jsonl"
exit "$rc"
