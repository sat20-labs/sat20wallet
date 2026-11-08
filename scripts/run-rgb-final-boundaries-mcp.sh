#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/rgb11_backup_material_test.go wallet/rgb11_conflict_cas_test.go wallet/rgb11_conflict_recovery_boundary_test.go wallet/rgb11_private_cancel_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=5m ./wallet -run '^(TestRGB11PrivateCancelRequiresExactUnpublishedRecoveryDelta|TestRGB11PrivateCancelPreservesUncertainPublication|TestRGB11ConflictCandidateCASRejectionRequiresReconciliation|TestRGB11ConflictCandidateMarkerFailureIsAtomic|TestRGB11ColdRestoredSettledOutOfBandSenderCanSupplyMissedRecipient)$' > "$out/rgb-final-boundaries-mcp-v2-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/rgb-final-boundaries-mcp-v2-20261030.exit"
tail -n 12 "$out/rgb-final-boundaries-mcp-v2-20261030.jsonl"
exit "$rc"
