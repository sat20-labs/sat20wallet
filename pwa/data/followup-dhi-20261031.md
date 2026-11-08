# D/H2/I follow-up candidate (2026-10-31)

Superseded review bundle. Do not apply this combined patch: the D and H2 portions have been applied and evolved; the current I-only candidate is `case-i-status-retry-reviewed-20261031.patch`. No business tests were run by this author.

## Evidence and proposed scope

- **D / `TestPWAGuardianPaidQuote`:** the real Guardian tools area produces one readonly contact textarea after `guardianIdentity`. `guardianReceiptOutput` only appears after accepting a friend's setup. The candidate scopes the contact lookup under the `Guardian 工具` heading and the `复制联系信息` button, asserts exactly one textarea in that generated contact panel, then validates the required `network`, 32-byte hex `mailbox_id`, and non-empty `recovery_public_key`. The independent Guardian mailbox assertion and existing 100/200 payment assertions remain intact.
- **H2 / `TestRealSatoshiNetAccountManagementAutopaySync`:** current SDK source still defines `accountManagedStatePath = "account/state"` and `accountManagedDataBlobName = "account-managed-data"`; the production key helpers use `PersonalKey(rootPubKey, accountManagedStatePath)` and `BlobKey(accountID, accountManagedDataBlobName)`. The candidate adds the same first-backup readiness gate/status assertions after Owner activation that the Guardian path already uses, before checking the replicated Bootstrap copies. This separates a missing/incomplete first publication from later peer propagation. It does not change the key contract, fees, provider, peer setup, Guardian fee flow, or Owner's material → validation → activation order. If Core readiness passes but Bootstrap remains absent, that is still a replication failure to diagnose on a fresh run; the present failure alone does not prove that.
- **I / `TestPWABackupStatus`:** the run directly queried the public SDK status during the held POST and got `pending_changes=1`, `managed_data_dirty=true`, `dkvs_status=READY`, and `dkvs_write_allowed=true`, while the UI retained pending=0/Synchronized/no error. The Account Management screen currently refreshes status only on mount, visibility change, and a 30-second interval. The candidate dispatches a status event after wallet catalog persistence and when SDK background-sync error state changes; the page listens and re-reads the public status. It also adds a button that wakes the existing DKVS worker immediately. The existing SDK worker already retries network failures on its 5-second retry delay; no second worker or retry loop is introduced. The test checks the button causes another intercepted request while the failure gate is active, then preserves all existing dirty/pending/error/settled assertions and timeouts.

## Review cautions

- The second H2 run must distinguish the new Owner local first-backup gate from the existing Bootstrap peer wait. If local readiness does not complete, investigate publication; if it completes but Bootstrap remains absent, investigate the peer copy path. Do not describe an unobserved peer-copy failure as confirmed.
- The I event callback deliberately carries no error payload or wallet data. The UI obtains fresh state through `account.status()`. The 30-second poll remains as a fallback.
- The immediate retry method reuses `StartDKVSSync` and `wakeSync`; its behavior should be checked against the existing manager lifecycle before source application. This draft was not compiled.

## Baseline hashes

The patch was generated against these current working-tree files:
- `pwa/scripts/verify/account-management-usage-e2e.mjs`: `03de371620d967c6d56b7bdcb3d1021d55e3fbb4beef510edc03a35768d98d01`
- `sdk/e2e/account_management_dkvs_e2e_test.go`: `eac561b73d3fb5c07d7d40315d5d86588e7a501c94fe412eca7d1c246caa9019`
- `sdk/wallet/dkvs_active_manager.go`: `fcf0d5c27a9c6466e3bae391f32aeb96e7d82ff58cff4ca50c99c3491087f590`
- `sdk/wallet/dkvs_subscription_manager.go`: `f5051f0afa28768bc1f3694f7adedd3a6d9fee208a22395392ed7fc0c25dd9a1`
- `sdk/wallet/dkvs_subscription_sync.go`: `36037f9c2818dbd63e455119ee90bcb07d617cc0861a3a7a1b16e8aa49f8f1d5`
- `sdk/wasm/main.go`: `a13a5e449bea0cccb18b5e5e2588163d173f16f067d1fd535649c06423bd18db`
- `sdk/wasm/account_management.go`: `d1662cf6e4b485fc52f144bf5cbaaa50cf8ab98037c96597774a45151e1e359e`
- `pwa/utils/accountManagement.ts`: `ffaa23327c8a7007ee25ee97a65673cfb04f317d21dab239b1f75667a488a67b`
- `pwa/store/wallet.ts`: `8a5fb845c0cfdff9035d85b7a54e8ea73266022a3909087af30f1dc2b3f278e6`
- `pwa/entrypoints/popup/pages/wallet/settings/account-management/Index.vue`: `73cc2c32b61a108070ef186d05ec58b5d2773be1e9dd3a8b215c7ebc5393076e`
