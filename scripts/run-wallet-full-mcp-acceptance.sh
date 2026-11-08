#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/account_restore_generation_test.go wallet/rgb11_review_r1_test.go wallet/account_managed_data_capacity_test.go wallet/account_final_failure_test.go wallet/account_management_gate_latency_test.go wallet/dkvs_final_boundary_test.go wallet/dkvs_lock_order_test.go wallet/account_password_binding_regression_test.go wallet/account_storage_reuse_test.go
/usr/bin/shasum -a 256 wallet/account_management_state.go wallet/account_management_state_sync_test.go wallet/account_management_activation_latency_test.go wallet/account_snapshot_ack_race_test.go wallet/account_managed_data_capacity_test.go wallet/account_final_failure_test.go > "$out/wallet-full-mcp-v5-input-20261030.sha256"
git --no-optional-locks diff --no-ext-diff --no-textconv > "$out/wallet-full-mcp-v5-input-20261030.patch"
/usr/bin/shasum -a 256 "$out/wallet-full-mcp-v5-input-20261030.patch" > "$out/wallet-full-mcp-v5-patch-20261030.sha256"
/usr/bin/shasum -a 256 go.mod go.sum ../../indexer/go.mod ../../indexer/go.sum ../../satoshinet/go.mod ../../satoshinet/go.sum ../../xaplib/chainindex/go.mod > "$out/wallet-full-mcp-v5-dependencies-20261030.sha256"
find wallet -type f -name '*.go' -exec /usr/bin/shasum -a 256 {} + > "$out/wallet-full-mcp-v5-wallet-sources-20261030.sha256"
find ../../indexer ../../satoshinet ../../transcend ../../xaplib/chainindex -type f -name '*.go' -exec /usr/bin/shasum -a 256 {} + > "$out/wallet-full-mcp-v5-dependency-sources-20261030.sha256"
git --no-optional-locks status --short > "$out/wallet-full-mcp-v5-status-20261030.txt"
/usr/local/go/bin/go test -json -count=1 -timeout=20m ./wallet > "$out/wallet-full-mcp-v5-20261030.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/wallet-full-mcp-v5-20261030.exit"
/usr/bin/shasum -a 256 "$out/wallet-full-mcp-v5-20261030.jsonl"
tail -n 10 "$out/wallet-full-mcp-v5-20261030.jsonl"
exit "$rc"
