#!/bin/bash
set -eu
cd /Users/yingfeng/github/sat20wallet/sdk
out=data/account-v11-mcp-build
mkdir -p "$out"
fingerprint() {
  /usr/bin/find /Users/yingfeng/github/sat20wallet /Users/yingfeng/github/transcend /Users/yingfeng/github/satoshinet /Users/yingfeng/github/indexer /Users/yingfeng/github/rgb11 \
    \( -name .git -o -name data -o -name node_modules -o -name .cache -o -name dist -o -name .output \) -prune -o \
    -type f \( -name '*.go' -o -name go.mod -o -name go.sum -o -name '*.ts' -o -name '*.vue' -o -name '*.mjs' -o -name package.json -o -name pnpm-lock.yaml -o -name package-lock.json \) -print0 \
    | /usr/bin/xargs -0 /usr/bin/shasum -a 256 | LC_ALL=C /usr/bin/sort
}
fingerprint > "$out/inputs-before.txt"
go build ./...
go test -mod=readonly -c -o "$out/account.test" ./account
go test -mod=readonly -c -o "$out/wallet.test" ./wallet
go test -mod=readonly -c -o "$out/e2e.test" ./e2e
fingerprint > "$out/inputs-after.txt"
/usr/bin/cmp "$out/inputs-before.txt" "$out/inputs-after.txt"
/usr/bin/shasum -a 256 "$out/account.test" "$out/wallet.test" "$out/e2e.test"
