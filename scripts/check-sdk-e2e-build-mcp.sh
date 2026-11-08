#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/go test -mod=readonly -run '^$' ./e2e > "$out/e2e-build-mcp-v2-20261030.log" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/e2e-build-mcp-v2-20261030.exit"
cat "$out/e2e-build-mcp-v2-20261030.log"
exit "$rc"
