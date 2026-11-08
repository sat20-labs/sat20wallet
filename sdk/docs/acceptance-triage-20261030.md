# 2026-10-30 acceptance triage

Updated 2026-10-30 12:44 UTC. This is investigation evidence, not approval to change production code or test assertions.

## Transcend TestAll v4

Result: FAIL_INCOMPLETE. Log: sibling transcend/data/sdk-acceptance-20261030/transcend-testall-v4-20261030.jsonl; summary and testall-sats-first-failure.txt in the same directory. 48 direct-case finished markers precede the failure; these are not equivalent to 48 terminal Go subtests. The ordinary sats transcend case's first deposit returns `contract not active` at manager_test.go:12797. Five later direct cases were not reached; a Luna executor is running them separately.

The failure cleanup defect is confirmed by source control flow: TestContract_Transcend_sats starts startBackgroundSender(&run), t.Fatal bypasses run=false, and clean waits on backgroundSendWG. The own test process was terminated after collecting evidence; no usable application-level Go stack was captured. A proposal to use the existing stoppable sender and guarantee failure cleanup is awaiting user approval.

Activation investigation: manager_test.go waitContractReady checks IsReady, which in SDK wallet/contract.go checks L2 readiness. DepositWithContract checks IsActive, which checks L1 activation. First SetEnableBlock sets CurrBlockL1 to EnableBlockL1-1. The log contains L1 missing-block 2->4, but also successful 3->3 resync; therefore this is NOT evidence of permanent block loss. Existing RGB/ATOM deposit helpers already explicitly wait for L1 activation. Likely missing ordinary-sats test precondition; exact failure-time runtime fields are not yet captured, so do not claim a proven production root cause or weaken activation checks.

## PWA non-RGB groups

pwa-non-rgb-wallet-groups-v1-20261030: l1-protocols 4 pass, dapp 9 pass, tools 9 pass, mining 3 pass, node 6 pass. wallet-basic 14 pass / 1 fail / 1 not run; mint 1 fail / 9 not run. No panic or pageerror. Inputs stable.

Both failures occur before their business assertions: wallet-basic-e2e.mjs:528-531 and wallet-mint-e2e.mjs:162-165 poll dkvs_write_allowed after importing an unactivated local-only wallet. account_management_state.go makes that flag conditional on RecoveryConfigured. Thus READY:pending is not proof local writes are unavailable. Proposed test-only condition distinguishes local_only, activated synced, and activation_pending; awaiting approval. Preserve actual operations and all asset/funds assertions.

## Wallet supplemental execution

The first compiled-binary run used sdk as cwd rather than sdk/wallet. The legacy fixture files exist; their missing-file failures are execution errors. That own run was stopped and 107 unstarted/unfinished/cwd-dependent cases selected for continuation from the correct package cwd. Keep original partial receipt, binary hash, and correction receipt; do not describe this as a completed full-package pass.

A second selection hazard: listing every test name in -test.run activates historical opt-in diagnostic runners that normally Skip in go test ./wallet. Examples: TestSDKCoreModulesValidation, TestReviewScopedValidation, TestReviewBaselinePublicationEvidence. The latter constructs an overlay using old implementation text. Separate their results from ordinary business regressions; do not change test assertions or treat obsolete negative-control source matching as a production failure. The executor was instructed not to accidentally activate additional opt-in runners through coverage selection.

Approved initialization corrections delivered by the sole Sol writer: identity snapshot local case removes unrelated preactivation Sync; sync snapshot root-signer case establishes actual activated baseline; remote action account selection activates before subscription synchronization; cold metadata restart test establishes baseline before creating pending edits. Latest five affected cases remain to be tested along with four preceding fixture corrections. Existing test binary predates these edits.

## Additional findings requiring classification, not automatic edits

- TestAccountFirstWalletSchedulesInitialBackup expects an unactivated wallet to upload automatically: contradicts current local-only rule.
- TestDKVSFinalAccountActivationRegistersReceiveAndMailboxPrefixes uses finalDKVSTestManager, which does not activate account management, before expecting activated subscriptions.
- TestDKVSReviewRootWrapperWriteConflictKeepsReceiving uses buildRootWrapperSource, which fabricates local encrypted envelopes without activating recovery; no rejected background PUT was observed. Preserve fault injection and receiving assertions when correcting setup.
- RGB background mailbox and expired cancellation tests expect all unconfigured public operations to fail; this conflicts with the explicit local-only rule. Do not silently weaken offline or activated-session protection.
- RGB two-scope cold restore expects a write failure before an explicit StartDKVSSync call. RestoreAccountManagementState itself calls finishAccountManagementRestorePostSync, which performs real registration and active-data maintenance. An explicit worker start is not by itself proof no valid baseline exists. Need a controlled genuinely unsynchronized restore fixture before classifying this as a production bypass.

## Pending decisions

NIA stale-candidate bounded rebuild proposal remains unimplemented pending approval. Binding/ACK semantic test adjustments, PWA two readiness conditions, and transcend failure cleanup remain pending. No commit, stage, push, deployment, or MCP execute_command service changes performed in this triage.
