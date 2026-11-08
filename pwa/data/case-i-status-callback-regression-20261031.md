# I status callback regression candidate

This data-only patch was applied after `case-i-status-retry-reviewed-20261031.patch` to `sdk/wallet/account_management_status_test.go`:

- It extends `TestAccountManagementStatusExposesLastDKVSSyncError` coverage with a status callback that reads `Manager.GetAccountManagementStatus()` from inside the callback.
- It checks that callback snapshots expose both the DKVS error transition and its cleared state.
- Each callback runs with a one-second bound so a lock reentry regression fails the test rather than hanging the package.

The regression selector is `TestDKVSStatusCallback` (kept within the repository's 32-byte identifier limit).

Patch SHA-256: `e9541ba06f8194da65b0de0d90f07a25f0f429824cc550730b68852695f582df`.

The test compiled into `sdk/data/i-status-retry-20261031/wallet.test`; it was not executed. The unit case directly covers callback delivery/read safety and the error fields it reads. It does not prove the production worker emits each event; SDK retry/outbox regressions and PWA `TestPWABackupStatus` must exercise those production call sites and the user-visible retry flow.
