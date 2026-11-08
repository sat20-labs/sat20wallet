#!/bin/sh
set -eu
cd /Users/yingfeng/github/sat20wallet || exit 2
command -v node
node --check pwa/scripts/verify/account-management-e2e.mjs
node --check pwa/scripts/verify/account-management-usage-e2e.mjs
git --no-optional-locks diff --check -- pwa/scripts/verify/account-management-e2e.mjs pwa/scripts/verify/account-management-usage-e2e.mjs sdk/go.mod
