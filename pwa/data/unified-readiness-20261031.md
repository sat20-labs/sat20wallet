# Unified account readiness candidate (2026-10-31)

Status: data-only draft; not applied to PWA/SDK sources. This draft is for parent review after the active single-Manager acceptance run finishes. No business test or node was run here.

## Shared readiness boundary

`accountReadiness(status)` carries discovery and write eligibility as independent fields. `inactive` is a completed online absence result; `active` is an active service result; `active-waiting-backup` remains its own state and does not count as write eligibility. Write eligibility requires all three explicit facts: `recovery_configured`, not `activation_pending`, and `dkvs_write_allowed`.

First-backup recoverability is not inferred from `pending_changes`, dirty state, or write eligibility. For PWA J, it is established only when a separate recovery device completes the real authenticated `previewRecovery`, and the expected wallet names/account counts and both password fields render. H2 uses `WaitAccountManagedDataReady` plus a fresh authenticated `LoadAccountManagementStateForRecovery` result and the paid fee proofs on the state/data records.

## Candidate changes

- **H2 — `TestRealSatoshiNetAccountManagementAutopaySync`:** before Owner recovery material is created, Guardian confirms the already-ready `paid` AUTOPAY option at the existing 100-record capacity, creates/publishes its own 2/2 package, activates account management, waits for its first confirmed backup, and reads that state/data back using the Guardian mnemonic. The test also checks the paid mode, no second funding transaction, and AUTOPAY fee proofs. The Owner's existing package creation → Guardian capsule verification/recovery → Owner activation order and contract payment setup remain intact. After Owner package creation, a fresh non-funding authorization is used for the Owner's Guardian capsule.
- **J — `TestPWAGuardianRecovery`:** the source remains open after `recovery_configured`; it closes only after the first separate device has successfully loaded the latest managed account state through the real preview flow and rendered the expected wallet catalog and password inputs. The existing Guardian-response combinations, actual password entry, restore, catalog equality, and no-secret assertions stay in place. No password-field timeout was added.
- **D — `TestPWAGuardianPaidQuote`:** when focused by itself and no `guardian_setup` fixture is supplied, the runner creates independent real owner/Guardian pages, obtains a Guardian contact through the PWA UI, checks the owner remains discovered/inactive, and uses the real PWA 2/3 package generator. It cancels the pending storage authorization after capturing the material. It does not activate the target owner. The target case's 200-record quote, Guardian's 100-record quote, cancellation, and zero-broadcast assertions are unchanged.
- **I:** the existing retry candidate is not duplicated or folded into this patch. Reuse `sdk/data/case-i-retry-draft-20261031T041700Z/case-i-diagnostic.patch` (SHA-256 `778a421ac6df7a00f6dd7b75d0ada8b37374bb85ad799586c669916250622b62`) and, after recording the held-state facts, review the existing `production-draft.patch` (SHA-256 `4da88a4b511938b35967adae4d9af5bea06d66d61ef7c8ee4f50fa2d5933e3e8`). Both patches pass `git apply --check` against the current worktree. The retry draft wakes the existing sync mechanism; this candidate adds no worker.

## Existing I evidence and open diagnosis

The previous `TestPWABackupStatus` run (`sdk/data/new-candidate-20261030T1918Z/h1-active-fixture-apply-20261031T041005Z/d5-focused-runs-20261031T042006Z/07-backup-status-i/run.jsonl`, SHA-256 `b133644a3d089504dc417285b76d9aa6b6bff8262c0ca5d90bd2851bc3824f9c`) reached `ready → local-commit → write-held`, then failed the original assertion because the UI still showed `待同步变更：0`, `Synchronized`, `Writable`, and zero awaiting requests. It does not contain the additional `held-observed` fields. Do not reinterpret this as proof of a pending write or change the original assertion from these logs. The existing I diagnostic patch is the next evidence step; its failure/boundary diagnosis remains open until that patch is run in the coordinated window.

## Files and checks

Candidate patch: `pwa/data/unified-readiness-20261031.patch`.

Baseline source SHA-256 values, unchanged while preparing this draft:

- `pwa/scripts/verify/account-management-usage-e2e.mjs`: `7a1b48bf8fd3bfb736969ea5a990e3c0b95fb99a722fb8dc687c1359b3cb35f9`
- `sdk/e2e/account_management_dkvs_e2e_test.go`: `5d6769bdff562b147c0ae5f756d7565d819ffb0203f5ca75b5c1274eda118917`
- `sdk/e2e/account_management_browser_e2e_test.go`: `340667e862980b98191b32a578003d1765a1a3763dc8976b5a44bacfda8689ee`
- `sdk/e2e/pwa_real_regtest_browser_e2e_test.go`: `2e0bc469d2c77295871df1d945133970d8a497a60384bdf4472d01ea9f40a4f5`

Checks against temporary copies only: unified patch applies cleanly (`git apply --check`); candidate JS parses (`node --check`); candidate Go file parses/formats (`gofmt -d` produced no output). No source files, runner files, or index/staging entries were changed by this draft. No business tests, browser sessions, or test nodes were started.

Remaining: parent review, wait for the current single-Manager acceptance window to finish, then apply the reviewed patch and hand the exact selectors to Luna for coordinated execution. The real test results remain pending.
