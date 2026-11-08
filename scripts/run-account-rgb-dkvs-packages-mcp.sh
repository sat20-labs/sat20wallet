#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/go test -race -json -count=1 -timeout=15m ./account/... ./wallet/dkvs/... ./wallet/rgb11/... > "$out/account-rgb-dkvs-packages-race-mcp-v1-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/account-rgb-dkvs-packages-race-mcp-v1-20261030.exit"
tail -n 12 "$out/account-rgb-dkvs-packages-race-mcp-v1-20261030.jsonl"
exit "$rc"
