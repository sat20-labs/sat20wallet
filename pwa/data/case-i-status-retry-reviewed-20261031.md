# I status refresh and immediate retry candidate (2026-10-31)

Data-only patch; not applied. It follows the public status evidence: during the held POST `account.status()` returned dirty/pending=1/READY/Writable while the page still rendered pending=0/Synchronized with no error. Existing page status refresh is mount/visibility/30-second polling; existing SDK worker already retries network failures at 5 seconds.

## Lock/callback review

- `flushDKVSOutbox` currently sets last error from inside `syncDKVSOnceResult`'s `runTransport` closure. The candidate removes that notification side effect. The background `run` calls `setLastSyncError` and dispatches the event only after `syncDKVSOnceResult` returns. At that point `runTransport`'s defer has released its coordinator slot, and account background jobs have returned, so no account/local-scope lock is held. `setLastSyncError` releases the DKVS manager mutex before any callback.
- The status callback emits an event with no status/error payload; PWA calls the async public `account.status()` API. `accountStatus` uses `createAsyncJsHandler`/`setTimeout`, so a listener does not synchronously enter `GetAccountManagementStatus` inside the originating Go stack. The 30-second refresh and visibility refresh remain fallback paths.
- `store/wallet.ts` emits after `walletStorage.batchUpdate` has persisted the catalog. The PWA `updateWalletName` path awaits that catalog sync after the SDK call, so this event covers the real rename case. SDK direct mutations enqueue the existing account sync job; the worker emits after successful job/outbox/receive work (`didWork` includes changed replica records), covering ACK clearing dirty state. No new worker is introduced.
- `refreshAccountStatus` gets a monotonically increasing request generation. Only the latest successful or failed response may publish to Vue state, preventing an older concurrent refresh from overwriting newer status.
- `RetryDKVSSync` calls the existing idempotent `StartDKVSSync` and wakes the same worker. In `run`, a wake cancels the current active long poll and resumes the existing sync cycle; the existing 5-second timed retry is unchanged.
- The runner preserves all existing pending/dirty/error/settled assertions and operation timeouts. It adds a short 4-second assertion that the clicked button produces another intercepted retry request while the failure route remains active.

This patch has not been compiled or business-tested. It was generated against the current working-tree files; no source was changed while creating it.

## Baseline hashes
- `sdk/wallet/dkvs_active_manager.go`: `fcf0d5c27a9c6466e3bae391f32aeb96e7d82ff58cff4ca50c99c3491087f590`
- `sdk/wallet/dkvs_subscription_manager.go`: `f5051f0afa28768bc1f3694f7adedd3a6d9fee208a22395392ed7fc0c25dd9a1`
- `sdk/wallet/dkvs_subscription_sync.go`: `36037f9c2818dbd63e455119ee90bcb07d617cc0861a3a7a1b16e8aa49f8f1d5`
- `sdk/wasm/main.go`: `a13a5e449bea0cccb18b5e5e2588163d173f16f067d1fd535649c06423bd18db`
- `sdk/wasm/account_management.go`: `d1662cf6e4b485fc52f144bf5cbaaa50cf8ab98037c96597774a45151e1e359e`
- `pwa/utils/accountManagement.ts`: `ffaa23327c8a7007ee25ee97a65673cfb04f317d21dab239b1f75667a488a67b`
- `pwa/store/wallet.ts`: `8a5fb845c0cfdff9035d85b7a54e8ea73266022a3909087af30f1dc2b3f278e6`
- `pwa/entrypoints/popup/pages/wallet/settings/account-management/Index.vue`: `73cc2c32b61a108070ef186d05ec58b5d2773be1e9dd3a8b215c7ebc5393076e`
- `pwa/scripts/verify/account-management-usage-e2e.mjs`: `1a1928e062bc661353872704810e0d805b732f18df22b4f3b64e5f7ad3bffc27`
