# D Guardian contact selector candidate (2026-10-31)

Historical exact patch; applied to `pwa/scripts/verify/account-management-usage-e2e.mjs` after baseline SHA `03de371620d967c6d56b7bdcb3d1021d55e3fbb4beef510edc03a35768d98d01`. Current runner SHA after D is `1a1928e062bc661353872704810e0d805b732f18df22b4f3b64e5f7ad3bffc27`. The fresh Guardian flow runs `guardianIdentity`, which displays one readonly contact textarea. `guardianReceiptOutput` is shown only after accepting a friend's setup. The patch scopes the output to the `Guardian 工具` heading and `复制联系信息` button, asserts one contact textarea there, then validates the required `network`, 32-byte hex `mailbox_id`, and non-empty `recovery_public_key`. It preserves the independent Guardian identity assertion and the existing 100/200 quote checks.

Baseline SHA-256 (`pwa/scripts/verify/account-management-usage-e2e.mjs`): `03de371620d967c6d56b7bdcb3d1021d55e3fbb4beef510edc03a35768d98d01`
