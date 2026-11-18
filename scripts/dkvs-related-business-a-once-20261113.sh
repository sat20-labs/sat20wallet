#!/bin/zsh
set -euo pipefail

GO_BIN="/usr/local/go/bin/go"
REPO="/Users/yingfeng/github/sat20wallet"
SDK="$REPO/sdk"
DKVS="/Users/yingfeng/github/satoshinet/indexer/indexer/dkvs"
work="$(/usr/bin/mktemp -d /private/tmp/sat20-dkvs-business-a.XXXXXX)"

cleanup() {
  exit_status=$?
  if [[ "$exit_status" -eq 0 ]]; then
    /bin/rm -rf "$work"
    print "temporary DKVS business-A test artifacts cleaned: $work"
  else
    print -u2 "temporary DKVS business-A artifacts preserved for inspection: $work"
  fi
}
trap cleanup EXIT

mkdir -p "$work/gocache" "$work/go-tmp" "$work/tmp"
export GOCACHE="$work/gocache"
export GOTMPDIR="$work/go-tmp"
export TMPDIR="$work/tmp"
export GOPROXY=off
export GOSUMDB=off
export GOFLAGS=-mod=readonly
export GOTOOLCHAIN=local
export PATH="/usr/local/go/bin:$PATH"
unset SAT20WALLET_E2E_ARTIFACTS || true

check_sha() {
  file="$1"
  expected="$2"
  actual="$(/usr/bin/shasum -a 256 "$file" | /usr/bin/awk '{print $1}')"
  print "source_sha256=$actual path=$file"
  if [[ "$actual" != "$expected" ]]; then
    print -u2 "unexpected source SHA for $file: $actual"
    exit 91
  fi
}
check_sha "$DKVS/batch_cas.go" "24af2c2467f0dee3df929e4fb2d6f5abe35451f2670695eeab01abdea6817b73"
check_sha "$DKVS/indexer.go" "120657d8c802f60506701d92d61c55da1524fea85c6ba95c9aef7c09a0db22d6"
check_sha "$DKVS/fee_usage.go" "4e92ee9d634ec46309f4ce1319d47cabf6ba6bf5d3c549a01b0ce2b6f710ab16"
check_sha "$DKVS/free_local.go" "69f4a42baf61d2008e97611938a0418b2bd478d79d257d3cac31c0102d75ebc4"
check_sha "$DKVS/mailbox_internal.go" "5326f68d5e99e41b7ea447eba7b0667e276da8e89833811f99b47bc288a32d1e"
check_sha "$DKVS/delete.go" "1e9dbef42295a1c353bd48c3843e0edb9288595d69cb032f6114a8f116cfe381"
check_sha "$DKVS/path_snapshot_apply.go" "4edbb33e56744cf78db15b0b220ff998f864bd205b19f98c31a2a23a7e0b82ed"
check_sha "$DKVS/atomic_apply.go" "15d6c485a4c46fb546c354a07e157f3372179578a3efa035d4777859d59559c7"
check_sha "$DKVS/paid_retention.go" "07b4329072e4d223a57d126b9febfa66e0c78c5d0fc19fedad8af8885f6ce7dd"
check_sha "$DKVS/quota_usage.go" "46f525b6c46cf3e3b7fb29c42d3f2ef45912e8c4eedc55048a1a0ad22fff0a11"
check_sha "$DKVS/topic_internal.go" "81c8fba2e5143d4324a057ebec28d8bbf2b3939b538d94c523904277322ea2a7"
check_sha "$DKVS/rpc_admission.go" "6da2a5290c3043e67edfb380439c163181ebc6e78c8acc8225d05b7529d284c6"
check_sha "$SDK/wallet/dkvs_subscription_sync.go" "6810e2f8631952ce77d67ae41313f2382815a7ddd982e8e2a667decdf424b097"
check_sha "$SDK/e2e/dkvs_bound_rpc_e2e_test.go" "e435a3daba03f7167945992800598e0c9ffa7a91ea7498020f8e4e053d58a3c8"
check_sha "$SDK/e2e/dkvs_write_context_security_e2e_test.go" "99c23a6ce5df51dd4cb22f98fef4fe2e78e0edbe4af880cdca7f385a5fdb5e0d"

print "isolated_tmp=$work"
print "go_version=$("$GO_BIN" version)"
print "scope=two existing SDK E2E tests, serial, fail-fast; httptest uses dynamic local ports and each fixture uses isolated Pebble t.TempDir"
cd "$SDK"
print "running only TestSDKDKVSBoundRPCHTTP"
"$GO_BIN" test -v -failfast -p=1 -parallel=1 -count=1 -timeout=8m -run='^TestSDKDKVSBoundRPCHTTP$' ./e2e
print "TestSDKDKVSBoundRPCHTTP_exit=0"
print "running only TestSDKDKVSWriteContextSecurity"
"$GO_BIN" test -v -failfast -p=1 -parallel=1 -count=1 -timeout=8m -run='^TestSDKDKVSWriteContextSecurity$' ./e2e
print "TestSDKDKVSWriteContextSecurity_exit=0"
print "sdk_e2e_business_a_exit=0"
