#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/gofmt -w wallet/account_password_binding_regression_test.go wallet/account_storage_reuse_test.go wallet/dkvs_lock_order_test.go
/usr/local/go/bin/go test -json -count=1 -timeout=3m ./wallet -run '^(TestAccount(PasswordAuthenticatesPersistedCredentialsAcrossManagers|ColdUnlockUsesCommittedSelectionAcrossManagers|GuardianPasswordAuthenticationAndRotation|StorageBindingDoesNotHoldLocalScope|StorageAuthorizationOwnedByRootSDKSession)|TestPendingAccountStorageAuthorizationIsRootBoundNotSelectionBound|TestDKVSDeferredJobDoesNotStarveIndependentJobs)$' > "$out/session-fixtures-mcp-v1-20261029.jsonl" 2>&1
rc=$?
printf "%s\n" "$rc" > "$out/session-fixtures-mcp-v1-20261029.exit"
tail -n 10 "$out/session-fixtures-mcp-v1-20261029.jsonl"
exit "$rc"
