#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/rgb11_address_batch_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=8m ./wallet -run '^(TestRGB11UnsignedExpiredCancelCannotResumeAndReleasesInputs|TestCancelExpiredRGB11TransferKeepsLocalOperationsAvailable|TestRGB11ReviewMixedACKBatchCancellation|TestRGB11ConflictCandidateInterruptedBeforeDirtyReload)$' > "$out/rgb-registration-fixtures-mcp-v1-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/rgb-registration-fixtures-mcp-v1-20261030.exit"
tail -n 12 "$out/rgb-registration-fixtures-mcp-v1-20261030.jsonl"
exit "$rc"
