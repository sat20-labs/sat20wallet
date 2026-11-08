package wallet

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// coreRGBFullSendRecoveryColdRestoreE2E exercises complete transfers through
// the real CoreNode mailbox/ACK path, records actual nested-history size growth,
// and restores the last consumer on a fresh database before simulating a
// controlled L1 reorg. It runs as a subtest of TestSDKCoreModulesConnectedE2E.
func coreRGBFullSendRecoveryColdRestoreE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	coreTransferDeviceCleanup(t, sender, receiver)

	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EF", Name: "E2E full-send recovery asset", Amounts: []uint64{777},
	})
	coreRequire(t, "issue full-send recovery NIA", err)
	coreCheckpoint(t, "issued_sender", sender, cfg, chain, senderMaterial)

	// Keep an independent database on the pre-send account revision. Its next
	// operation must adopt the account-selected pending phase before it may
	// reserve the same input.
	coreSuspendRGBDevice(t, sender)
	staleDevice := coreRestoreE2E(t, cfg, chain, senderMaterial, false)
	coreSynchronizeRestoredAccount(t, staleDevice)
	coreVerifyRGB11DirectProviderRoundTrip(t, sender, staleDevice, issued.AssetName)
	initialBalance, err := staleDevice.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read pre-send amount on independent device", err)
	coreAssert(t, initialBalance != nil && initialBalance.Value.Uint64() == 777,
		"independent device did not restore the full pre-send allocation")
	coreRequire(t, "confirm healthy stale-device provider roundtrip",
		staleDevice.SyncAccountManagementState(context.Background()))
	coreWaitPrivateBackup(t, staleDevice)
	coreRequire(t, "resume full-send source after offline copy initialized", sender.StartDKVSSync())
	coreSynchronizeRestoredAccount(t, sender)
	coreWaitPrivateBackup(t, sender)

	_, err = sender.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish sender receive capability", err)
	_, err = receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish receiver receive capability", err)

	initialBroadcastCount := chain.broadcastCount()
	var lastPrepared *RGB11PreparedTransfer
	previousSizes := make(map[string]coreManagedRecoverySize, 2)
	for hop := 1; hop <= 20; hop++ {
		hopStarted := time.Now()
		source, destination := sender, receiver
		if hop%2 == 0 {
			source, destination = receiver, sender
		}
		beforeBroadcast := chain.broadcastCount()
		prepared := coreSendRGB11Amount(t, chain, source, destination, issued.AssetName, "777",
			beforeBroadcast, 0, 777, func(value *RGB11PreparedTransfer) {
				if hop != 1 {
					return
				}
				// The other device must actually receive this persisted intent
				// before it can test its input lock. Only one root device is online.
				coreSuspendRGBDevice(t, sender)
				coreRequire(t, "resume stale device for selected pending check", staleDevice.StartDKVSSync())
				coreSynchronizeRestoredAccount(t, staleDevice)
				_, refreshErr := staleDevice.RefreshRGB11State(context.Background())
				coreRequire(t, "refresh selected full-send pending on stale device", refreshErr)
				original, loadErr := sender.rgbManager.projectionStore.LoadPendingTransfer(value.State.TransferID)
				coreRequire(t, "read original full-send pending before handoff", loadErr)
				selected, loadErr := staleDevice.rgbManager.projectionStore.LoadPendingTransfer(value.State.TransferID)
				coreRequire(t, "read synchronized full-send pending on stale device", loadErr)
				coreAssert(t, original != nil && selected != nil && selected.State.WitnessTxID == value.TxID &&
					selected.State.ConsignmentHash == value.State.ConsignmentHash &&
					coreDigest(selected.State.InputOutPoints) == coreDigest(value.State.InputOutPoints) &&
					bytes.Equal(selected.SignedTx, original.SignedTx),
					"stale device did not adopt the exact selected pending transaction")
				coreAssertRGB11Reservation(t, staleDevice, value.State.TransferID, true)
				coreRequire(t, "authorize synchronized competing-device check",
					staleDevice.CheckAccountWrite(rgb11AccountManagedProviderID, coreSelectedRGBScope(staleDevice)))
				_, _, conflictErr := staleDevice.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
					ReceiverAddress: receiver.GetWallet().GetAddress(), AssetName: issued.AssetName,
					AmountRaw: "1", FeeRate: 2, MinConfirmations: 1,
				}, dkvsindexer.RecordVerificationOptions{})
				coreAssert(t, conflictErr != nil,
					"a second device prepared a competing spend against the account-selected pending full send")
				coreAssert(t, chain.broadcastCount() == beforeBroadcast,
					"competing preparation broadcast a transaction")
				coreSuspendRGBDevice(t, staleDevice)
				coreRequire(t, "resume selected full-send source after lock check", sender.StartDKVSSync())
				coreSynchronizeRestoredAccount(t, sender)
			})
		lastPrepared = prepared
		if hop == 20 {
			// The final transfer is receiver -> sender. Save its exact consumer
			// edge so the newly-created receiver database can test reorg recovery.
			if prepared == nil || prepared.State == nil || len(prepared.State.InputOutPoints) == 0 {
				t.Fatal("core-e2e: final full-send omitted its consumed input")
			}
		}
		coreLogManagedRecoverySize(t, hop, "sender", sender, previousSizes)
		coreLogManagedRecoverySize(t, hop, "receiver", receiver, previousSizes)
		t.Logf("core-e2e: RGB11 full-send hop=%d elapsed_ms=%d", hop, time.Since(hopStarted).Milliseconds())
	}
	if lastPrepared == nil || lastPrepared.State == nil || lastPrepared.TxID == "" ||
		len(lastPrepared.State.InputOutPoints) == 0 {
		t.Fatal("core-e2e: final full-send did not retain its consumer transaction and input")
	}

	// This database did not exist before the settled full sends. In particular,
	// it has no old active mailbox or local consumer journal to mask package
	// omissions.
	coreSuspendRGBDevice(t, receiver)
	coldRestoreStarted := time.Now()
	coldDevice := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, coldDevice)
	_, err = coldDevice.RefreshRGB11State(context.Background())
	coreRequire(t, "refresh cold-restored full-send history", err)
	t.Logf("core-e2e: RGB11 full-send cold_restore_elapsed_ms=%d", time.Since(coldRestoreStarted).Milliseconds())
	settledBalance, err := coldDevice.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold-restored full-send balance", err)
	coreAssert(t, coreRGB11BalanceIsZero(settledBalance),
		"cold restore resurrected the receiver's fully-spent allocation")
	state, err := coldDevice.GetRGB11State()
	coreRequire(t, "read cold-restored proof history", err)
	if state == nil {
		t.Fatal("core-e2e: cold restore returned no proof history")
	}
	foundConsumedProof, consumedProofIsCurrent := false, false
	for _, proof := range state.Proofs {
		if len(lastPrepared.State.InputOutPoints) != 0 && proof.OutPoint == lastPrepared.State.InputOutPoints[0] {
			foundConsumedProof = true
			consumedProofIsCurrent = rgb11ProofIsCurrent(proof)
		}
	}
	coreAssert(t, foundConsumedProof, "cold restore omitted the last consumed allocation proof/consumer history")
	coreAssert(t, !consumedProofIsCurrent, "cold restore made the last consumed allocation spendable")
	coreAssert(t, chain.broadcastCount() == initialBroadcastCount+20,
		"cold restore or settled-state adoption unexpectedly rebroadcast a transaction")

	coreReorgControlledTransaction(t, chain, lastPrepared.TxID)
	_, err = coldDevice.RefreshRGB11State(context.Background())
	coreRequire(t, "reconcile controlled full-send reorg", err)
	reorgBalance, err := coldDevice.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read receiver amount after controlled reorg", err)
	coreAssert(t, reorgBalance != nil && reorgBalance.Value.Uint64() == 777,
		"cold restore could not rederive the prior receiver allocation after its consumer reorged")
	if len(lastPrepared.State.InputOutPoints) == 0 {
		t.Fatal("core-e2e: last full-send operation lost its input outpoint")
	}
	reorgState, err := coldDevice.GetRGB11State()
	coreRequire(t, "read proof after controlled reorg", err)
	if reorgState == nil {
		t.Fatal("core-e2e: cold restore returned no proof history after reorg")
	}
	foundReactivatedInput := false
	for _, proof := range reorgState.Proofs {
		if proof.OutPoint == lastPrepared.State.InputOutPoints[0] && rgb11ProofIsCurrent(proof) {
			foundReactivatedInput = true
		}
	}
	coreAssert(t, foundReactivatedInput,
		"reorg recovery did not restore the last consumer's prior allocation proof")

	// Return the receiver root to its original device for the existing reorg
	// comparison, then hand it back to the cold device for the real re-spend.
	coreSuspendRGBDevice(t, coldDevice)
	coreRequire(t, "resume original receiver for reorg comparison", receiver.StartDKVSSync())
	coreSynchronizeRestoredAccount(t, receiver)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "remove reorged recipient allocation from sender", err)
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "reconcile live receiver after controlled reorg", err)
	senderAfterReorg, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read sender amount after controlled reorg", err)
	receiverAfterReorg, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read receiver amount after controlled reorg", err)
	coreAssert(t, coreRGB11BalanceIsZero(senderAfterReorg) && receiverAfterReorg != nil &&
		receiverAfterReorg.Value.Uint64() == 777,
		"controlled reorg did not restore sender/receiver balances before cold re-spend")
	coreSuspendRGBDevice(t, receiver)
	coreRequire(t, "resume cold receiver after original-device reorg comparison", coldDevice.StartDKVSSync())
	coreSynchronizeRestoredAccount(t, coldDevice)
	_, err = coldDevice.RefreshRGB11State(context.Background())
	coreRequire(t, "refresh cold receiver after real device handoff", err)

	// Re-spend after recovery. This is a transfer from the cold receiver account
	// to the distinct sender account, proving the restored proof can be consumed.
	beforeRebroadcast := chain.broadcastCount()
	prepared := coreSendRGB11Amount(t, chain, coldDevice, sender, issued.AssetName, "777",
		beforeRebroadcast, 0, 777, nil)
	coreAssert(t, prepared != nil && prepared.State != nil,
		"cold-restored device could not prepare a spend after reorg")
	afterSend, err := coldDevice.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold-restored balance after re-spend", err)
	receivedAgain, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read sender balance after cold re-spend", err)
	coreAssert(t, coreRGB11BalanceIsZero(afterSend) && receivedAgain != nil &&
		receivedAgain.Value.Uint64() == 777,
		"cold-restored proof did not survive a complete second send")
	coreSuspendRGBDevice(t, coldDevice)
	coreSuspendRGBDevice(t, sender)
}

// A wallet with no current allocation has no aggregate Decimal to return.
// Treat that nil as zero, while still rejecting a malformed nonnil Decimal.
func coreRGB11BalanceIsZero(balance *Decimal) bool {
	return balance == nil || (balance.Value != nil && balance.Value.Sign() == 0)
}

func coreVerifyRGB11DirectProviderRoundTrip(t *testing.T, source, target *Manager,
	assetName indexer.AssetName) {
	t.Helper()
	if source == nil || target == nil {
		t.Fatal("core-e2e: RGB11 provider roundtrip needs source and target managers")
	}
	catalog, err := source.accountManagedDataCatalog()
	coreRequire(t, "read source catalog for direct provider roundtrip", err)
	sourceProvider := &rgb11AccountManagedDataProvider{owner: source}
	payloads, err := sourceProvider.Export(catalog)
	coreRequire(t, "export real Core-issued RGB11 recovery closure", err)
	if err := sourceProvider.Validate(catalog, payloads); err != nil {
		t.Fatalf("core-e2e: validate source recovery closure: %v", err)
	}
	var foundSourceProof bool
	for _, payload := range payloads {
		packageValue, decodeErr := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
		coreRequire(t, "decode exported real RGB11 recovery closure", decodeErr)
		for _, record := range packageValue.ProjectionRecords {
			if strings.HasPrefix(record.Key, "proof-") && strings.Contains(record.Key, assetName.String()) {
				foundSourceProof = true
			}
		}
	}
	coreAssert(t, foundSourceProof, "source provider closure omitted the real issued allocation proof")

	targetCatalog, err := target.accountManagedDataCatalog()
	coreRequire(t, "read target catalog for direct provider roundtrip", err)
	coreAssert(t, targetCatalog.AccountID == catalog.AccountID,
		"provider source and target do not share the same account identity")
	targetProvider := &rgb11AccountManagedDataProvider{owner: target}
	if err := targetProvider.Import(targetCatalog, payloads); err != nil {
		t.Fatalf("core-e2e: target provider could not import selected real closure with fresh chain evidence: %v", err)
	}
	state, err := target.GetRGB11State()
	coreRequire(t, "read target state after direct provider import", err)
	if state == nil {
		t.Fatal("core-e2e: target provider import produced no RGB11 state")
	}
	var foundTargetProof bool
	for _, proof := range state.Proofs {
		if proof != nil && proof.AssetName == assetName && rgb11ProofIsCurrent(proof) {
			foundTargetProof = true
			break
		}
	}
	coreAssert(t, foundTargetProof,
		"target provider did not reconstruct the real allocation from imported closure and fresh evidence")
	balance, err := target.GetRGB11AssetBalance(&assetName)
	coreRequire(t, "read target balance after direct provider import", err)
	coreAssert(t, balance != nil && balance.Value.Uint64() == 777,
		"direct provider import did not recover the exact real issued balance")
}

func coreSendRGB11Amount(t *testing.T, chain *coreE2EChain, source, destination *Manager,
	assetName indexer.AssetName, amount string, beforeBroadcast int,
	wantSource, wantDestination uint64, afterPrepare func(*RGB11PreparedTransfer)) *RGB11PreparedTransfer {
	t.Helper()
	prepared, _, err := source.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: destination.GetWallet().GetAddress(), AssetName: assetName,
		AmountRaw: amount, FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare full-amount RGB11 transfer", err)
	coreAssert(t, prepared != nil && prepared.State != nil && prepared.State.Status == "prepared",
		"full-send did not retain its prepared operation")
	coreAssert(t, chain.broadcastCount() == beforeBroadcast,
		"full-send prepare unexpectedly broadcast before delivery/ACK")
	if afterPrepare != nil {
		afterPrepare(prepared)
	}
	coreDeliverRGB11OfflineACK(t, chain, source, destination, prepared, beforeBroadcast)

	chain.confirm(prepared.TxID)
	_, err = source.RefreshRGB11State(context.Background())
	coreRequire(t, "settle full-send source", err)
	_, err = destination.RefreshRGB11State(context.Background())
	coreRequire(t, "settle full-send destination", err)
	coreSyncRGBCheckpoint(t, source, destination)
	coreAssertRGB11SettledRecoveryPublished(t, destination, prepared)
	coreAssertRGB11Balances(t, source, destination, assetName, wantSource, wantDestination)
	return prepared
}

// Healthy recovery checkpoints need confirmed data, not a particular worker
// tick. Keep fault/outbox tests on their original asynchronous worker paths.
func coreSyncRGBCheckpoint(t *testing.T, managers ...*Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, manager := range managers {
		// Direct send/receive operates on the account root, independently of UI
		// selection. Its local commit already staged this scope's generation.
		view, err := manager.captureRGB11NetworkScope(true)
		coreRequire(t, "capture RGB recovery checkpoint scope", err)
		scope, err := view.rgb11AccountManagedScopeID()
		coreRequire(t, "read RGB recovery checkpoint scope", err)
		accountID, err := manager.RootAccountID()
		coreRequire(t, "read RGB recovery checkpoint account", err)
		targets, err := manager.captureAccountBackup(rgb11AccountManagedProviderID, scope)
		coreRequire(t, "capture committed RGB checkpoint generation", err)
		target, exists := targets[rgb11AccountManagedProviderID+"\x00"+scope]
		coreAssert(t, exists && target > 0, "RGB checkpoint has no committed module generation")
		waitErr := manager.WaitAccountBackup(ctx, rgb11AccountManagedProviderID, scope)
		if waitErr != nil {
			// Failure-only local observations. Do not retry, change readiness,
			// or log account identifiers, recovery bytes, or error payloads.
			status := manager.GetAccountManagementStatus()
			t.Logf("core-e2e: RGB checkpoint backup failure: actor=%d scope_tag=%s target=%d error_type=%T account_state=%q discovery=%q dkvs=%q writable=%t backup=%q pending_requests=%d error_code=%q has_error=%t",
				manager.debugID, coreCASTag(scope), target, waitErr, status.AccountState,
				status.DiscoveryStatus, status.DKVSStatus, status.DKVSWriteAllowed, status.BackupState,
				status.DKVSPendingRequests, status.LastDKVSSyncErrorCode, status.LastDKVSSyncError != "")
			manager.mutex.RLock()
			local, readErr := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
			manager.mutex.RUnlock()
			if readErr != nil || local == nil {
				t.Logf("core-e2e: RGB checkpoint backup local: present=%t read_error_type=%T", local != nil, readErr)
			} else {
				t.Logf("core-e2e: RGB checkpoint backup local: generation=%d captured=%d confirmed=%d baseline=%t restoring=%t conflict=%t has_error=%t",
					local.Generation, local.Captured, local.Confirmed, local.Baseline != nil,
					local.Restoring, local.Conflict, local.LastError != "")
			}
			if coordinator := manager.dkvs; coordinator != nil {
				coordinator.mu.Lock()
				worker, stopping := coordinator.done, coordinator.stopping
				jobQueued := coordinator.jobs[accountManagedStateJobID] != nil
				due := coordinator.jobDue[accountManagedStateJobID]
				requestCanceled := coordinator.requestCtx != nil && coordinator.requestCtx.Err() != nil
				coordinator.mu.Unlock()
				running := worker != nil
				select {
				case <-worker:
					running = false
				default:
				}
				t.Logf("core-e2e: RGB checkpoint backup worker: running=%t stopping=%t account_job_queued=%t job_due=%t job_overdue=%t request_canceled=%t",
					running, stopping, jobQueued, !due.IsZero(), !due.IsZero() && !due.After(time.Now()), requestCanceled)
			}
		}
		coreRequire(t, "confirm RGB recovery checkpoint backup", waitErr)
		manager.mutex.RLock()
		value, err := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
		manager.mutex.RUnlock()
		coreRequire(t, "read confirmed RGB checkpoint baseline", err)
		coreAssert(t, value != nil && !value.Deleted && value.Captured >= target && value.Confirmed >= target &&
			value.Baseline != nil && value.Baseline.Seq > 0 && value.Baseline.Key == value.Key,
			"RGB checkpoint did not capture and confirm its committed generation with a module baseline")
		coreRequire(t, "verify confirmed RGB checkpoint signature", dkvsindexer.VerifySignature(value.Baseline))
	}
}

// Observe the existing recovery boundaries by bringing the real devices online
// in order. Production may consume an ACK automatically whenever its sender is
// online; explicit frontend synchronization is not a broadcast barrier.
func coreDeliverRGB11OfflineACK(t *testing.T, chain *coreE2EChain,
	source, destination *Manager, prepared *RGB11PreparedTransfer, beforeBroadcast int) {
	t.Helper()
	sourceID, err := source.RootAccountID()
	coreRequire(t, "read offline-ACK sender identity", err)
	destinationID, err := destination.RootAccountID()
	coreRequire(t, "read offline-ACK receiver identity", err)
	coreAssert(t, sourceID != destinationID && prepared != nil && prepared.State != nil &&
		prepared.State.SenderAccountID == sourceID && prepared.State.ReceiverAccountID == destinationID,
		"offline-ACK transfer is not bound to two independent device roots")
	sourceOffline, destinationOffline := false, false
	defer func() {
		if sourceOffline {
			coreRequire(t, "resume sender after interrupted offline-ACK flow", source.StartDKVSSync())
			coreSynchronizeRestoredAccount(t, source)
		}
	}()
	defer func() {
		if destinationOffline {
			coreRequire(t, "resume receiver after interrupted offline-ACK flow", destination.StartDKVSSync())
			coreSynchronizeRestoredAccount(t, destination)
		}
	}()
	// The receiver goes offline before delivery, so its worker cannot publish an
	// ACK while the foreground delivery call still checks for that ACK.
	destinationOffline = true
	coreSuspendRGBDevice(t, destination)
	sourceOffline = true
	coreSuspendRGBDevice(t, source)
	sourceView, err := source.captureRGB11NetworkScope(true)
	coreRequire(t, "capture offline-ACK original sender scope", err)
	store := sourceView.rgbManager.projectionStore
	unsigned, err := store.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read original unsigned transfer before delivery", err)
	coreAssert(t, unsigned != nil && unsigned.State.Direction == "send" &&
		unsigned.State.SenderAccountID == sourceID && unsigned.State.ReceiverAccountID == destinationID &&
		unsigned.State.WitnessTxID == prepared.TxID && unsigned.State.ConsignmentHash == prepared.State.ConsignmentHash &&
		len(unsigned.State.InputOutPoints) > 0 && !unsigned.State.DeliveryAcknowledged &&
		unsigned.State.AckStatus != "accepted" && !rgb11PendingHasWitness(unsigned) &&
		chain.broadcastCount() == beforeBroadcast,
		"offline-ACK delivery lost its original unsigned, unacknowledged intent")
	delivery, err := source.DeliverAndBroadcastConfiguredRGB11AddressTransfer(
		prepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver consignment while receiver is offline", err)
	coreAssert(t, delivery != nil && delivery.TransferID == prepared.State.TransferID &&
		delivery.AwaitingACK && !delivery.Broadcast && chain.broadcastCount() == beforeBroadcast,
		"delivery crossed the offline receiver ACK boundary")
	coreRequire(t, "bring receiver online to accept its consignment", destination.StartDKVSSync())
	destinationOffline = false
	coreSynchronizeRestoredAccount(t, destination)
	coreWaitRGB11MailboxACK(t, chain, destination, prepared, beforeBroadcast)
	coreSyncRGBCheckpoint(t, destination)
	coreAssertRGB11ACKRecoveryPublished(t, destination, prepared)
	coreAssert(t, chain.broadcastCount() == beforeBroadcast,
		"offline sender broadcast before its receiver recovery checkpoint completed")
	coreRequire(t, "bring sender online to consume its accepted ACK", source.StartDKVSSync())
	sourceOffline = false
	coreSynchronizeRestoredAccount(t, source)
	coreWaitRGB11MailboxBroadcast(t, chain, source, prepared, beforeBroadcast)
	signed, err := store.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read durable signed transfer after accepted ACK", err)
	coreAssert(t, signed != nil && signed.State.SenderAccountID == sourceID &&
		signed.State.ReceiverAccountID == destinationID && signed.State.DeliveryAcknowledged &&
		signed.State.AckStatus == "accepted" && validRGB11SigningTransition(unsigned, signed),
		"ACK consumption changed the original transaction or lost its verified signed witness")
	raw, err := chain.GetRawTx(prepared.TxID)
	coreRequire(t, "read actual offline-ACK Bitcoin transaction", err)
	coreAssert(t, bytes.Equal(raw, signed.SignedTx) && chain.broadcastCount() == beforeBroadcast+1,
		"offline-ACK flow did not broadcast exactly its original signed transaction once")
	locks := source.utxoLockerL1.GetLockedUtxoList()
	for _, outpoint := range signed.State.InputOutPoints {
		lock := locks[outpoint]
		coreAssert(t, lock != nil && lock.ReservationID == signed.ReservationID,
			"broadcast transfer released its original input reservation before confirmation")
	}
}

// A cold database replays its account mailbox, including historical sends
// invalidated by a known reorg/re-spend. Only errors for already-terminal,
// authenticated receive identities are expected; the new transfer must ACK.
func coreAssertRGB11MailboxErrors(t *testing.T, receiver *Manager,
	current map[string]bool, result *RGB11AddressMailboxSyncResult) {
	t.Helper()
	coreAssert(t, result != nil, "mailbox returned no scan result")
	if result.Invalid == 0 {
		return
	}
	state, err := receiver.GetRGB11State()
	coreRequire(t, "read historical mailbox receive identities", err)
	historical := make(map[string]bool)
	for _, transfer := range state.Transfers {
		if transfer == nil || !transfer.AddressMode || transfer.Direction != "receive" ||
			current[transfer.TransferID] ||
			(transfer.Status != "settled" && transfer.Status != "orphaned" && transfer.Status != "rejected") {
			continue
		}
		id, err := rgb11AddressMessageID(transfer.TransferID)
		coreRequire(t, "derive terminal historical receive identity", err)
		historical["consignment "+id+":"] = true
	}
	coreAssert(t, len(result.ErrorDetails) == result.Invalid,
		"mailbox rejected messages without exact error identities")
	for _, detail := range result.ErrorDetails {
		known := false
		for prefix := range historical {
			if strings.HasPrefix(detail, prefix) {
				known = true
				break
			}
		}
		coreAssert(t, known && strings.Contains(detail, "inconsistent spend"),
			"mailbox rejected a current/unknown consignment or had an unexpected historical error: "+detail)
	}
	t.Logf("core-e2e: mailbox rejected only terminal historical consignments: %d", result.Invalid)
}

func coreWaitRGB11MailboxACK(t *testing.T, chain *coreE2EChain, receiver *Manager,
	prepared *RGB11PreparedTransfer, beforeBroadcast int) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for {
		result, err := receiver.SyncConfiguredRGB11AddressMailbox(context.Background(),
			dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		coreRequire(t, "receive target consignment and publish ACK", err)
		coreAssertRGB11MailboxErrors(t, receiver, map[string]bool{prepared.State.TransferID: true}, result)
		coreAssert(t, chain.broadcastCount() == beforeBroadcast,
			"receiver mailbox crossed the sender broadcast barrier")
		state, err := receiver.GetRGB11State()
		coreRequire(t, "read target receive ACK", err)
		matches := 0
		for _, transfer := range state.Transfers {
			if transfer != nil && transfer.Direction == "receive" &&
				transfer.TransferID == prepared.State.TransferID && transfer.WitnessTxID == prepared.TxID &&
				transfer.ConsignmentHash == prepared.State.ConsignmentHash &&
				transfer.DeliveryAcknowledged && transfer.AckStatus == "ack-sent" {
				matches++
			}
		}
		coreAssert(t, matches <= 1, "target consignment has duplicate receive ACKs")
		if matches == 1 {
			return
		}
		coreAssert(t, time.Now().Before(deadline), "target consignment did not publish its exact ACK")
		time.Sleep(100 * time.Millisecond)
	}
}

func coreWaitRGB11MailboxBroadcast(t *testing.T, chain *coreE2EChain, sender *Manager,
	prepared *RGB11PreparedTransfer, beforeBroadcast int) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for {
		result, err := sender.SyncConfiguredRGB11AddressMailbox(context.Background(),
			dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		coreRequire(t, "consume target ACK and broadcast", err)
		coreAssertRGB11MailboxErrors(t, sender, map[string]bool{prepared.State.TransferID: true}, result)
		state, err := sender.GetRGB11State()
		coreRequire(t, "read target broadcast lifecycle", err)
		matches := 0
		var target *rgb11wallet.TransferState
		for _, transfer := range state.Transfers {
			if transfer != nil && transfer.Direction == "send" &&
				transfer.TransferID == prepared.State.TransferID && transfer.WitnessTxID == prepared.TxID &&
				transfer.ConsignmentHash == prepared.State.ConsignmentHash {
				matches++
				target = transfer
			}
		}
		coreAssert(t, matches <= 1 && chain.broadcastCount() <= beforeBroadcast+1,
			"target ACK caused duplicate lifecycle or transaction broadcasts")
		status := ""
		if target != nil {
			status = target.Status
		}
		// A normal chain refresh can already have advanced broadcast to pending.
		// Require the actual signed witness and positive ACK, not a transient label.
		if matches == 1 && (status == "broadcast" || status == "pending") {
			pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
			coreRequire(t, "read exact signed target after ACK", err)
			coreAssert(t, pending != nil, "target broadcast lost its signed pending record")
			txStatus, err := chain.GetTxStatus(prepared.TxID)
			coreRequire(t, "read target witness mempool status", err)
			raw, err := chain.GetRawTx(prepared.TxID)
			coreRequire(t, "read actual target signed transaction", err)
			mempool := txStatus != nil && txStatus.TxID == prepared.TxID &&
				txStatus.InMempool && !txStatus.Confirmed && txStatus.Confirmations == 0
			identity := pending.State.Direction == "send" && pending.State.TransferID == prepared.State.TransferID &&
				pending.State.WitnessTxID == prepared.TxID && pending.State.ConsignmentHash == prepared.State.ConsignmentHash
			acknowledged := target.DeliveryAcknowledged && target.AckStatus == "accepted" &&
				pending.State.DeliveryAcknowledged && pending.State.AckStatus == "accepted"
			rawEqual := len(raw) > 0 && bytes.Equal(raw, pending.SignedTx)
			hasWitness := rgb11PendingHasWitness(pending)
			t.Logf("core-e2e: target broadcast facts: status=%q pending_status=%q identity=%t acknowledged=%t mempool=%t raw_equal=%t has_witness=%t broadcasts=%d",
				status, pending.State.Status, identity, acknowledged, mempool, rawEqual, hasWitness, chain.broadcastCount()-beforeBroadcast)
			coreAssert(t, identity && acknowledged && mempool && rawEqual && hasWitness,
				"target lifecycle has no exact ACK-backed signed transaction in the mempool")
			coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
				"target ACK did not broadcast exactly once")
			return
		}
		coreAssert(t, time.Now().Before(deadline), fmt.Sprintf(
			"target ACK did not produce its broadcast lifecycle: status=%q matches=%d broadcasts=%d",
			status, matches, chain.broadcastCount()-beforeBroadcast))
		time.Sleep(100 * time.Millisecond)
	}
}

func coreRGBManagerRole(t *testing.T, role string, manager *Manager) {
	t.Helper()
	manager.mutex.RLock()
	accountID, root := "", ""
	if manager.accountProfile != nil {
		accountID, root = manager.accountProfile.AccountID, manager.accountProfile.RootFingerprint
	}
	manager.mutex.RUnlock()
	t.Logf("RGB actor: role=%s manager=%d account_present=%t account_tag=%s root_tag=%s",
		role, manager.debugID, accountID != "", accountTraceScope(accountID), accountTraceScope(root))
}

// Failure-only observation: inspect existing local rows and one authoritative
// record. Never refresh, import, repair, publish or change projection readiness.
func coreRGBBackupFacts(t *testing.T, ctx context.Context, manager *Manager) {
	t.Helper()
	scope, err := manager.rgbManager.rgb11AccountManagedScopeID()
	if err != nil {
		t.Logf("RGB backup facts: manager=%d scope_error=%T", manager.debugID, err)
		return
	}
	manager.mutex.RLock()
	if manager.accountProfile == nil || manager.cfg == nil {
		manager.mutex.RUnlock()
		t.Logf("RGB backup facts: manager=%d profile_present=false", manager.debugID)
		return
	}
	accountID, network := manager.accountProfile.AccountID, manager.cfg.Chain
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	module, readErr := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
	if readErr != nil || module == nil {
		t.Logf("RGB backup facts: manager=%d scope_tag=%s module_present=%t read_error=%T",
			manager.debugID, accountTraceScope(scope), module != nil, readErr)
		return
	}
	entry, err := accountEntry(accountID, rgb11AccountManagedProviderID, scope)
	if err != nil {
		t.Logf("RGB backup facts: manager=%d entry_error=%T", manager.debugID, err)
		return
	}
	baseSeq, remoteSeq := uint64(0), uint64(0)
	baseHash, remoteHash := "", ""
	var baseValue, remoteValue []byte
	if module.Baseline != nil {
		baseSeq, baseHash = module.Baseline.Seq, dkvsindexer.RecordHash(module.Baseline).String()
		baseValue = module.Baseline.Value
	}
	client, remoteErr := manager.dkvs.primaryClient()
	if remoteErr == nil {
		readCtx, cancelRead := context.WithTimeout(ctx, 2*time.Second)
		defer cancelRead()
		record, err := client.GetRecordDirectContext(readCtx, entry.Key)
		remoteErr = err
		if err == nil && record != nil {
			remoteSeq, remoteHash = record.Seq, dkvsindexer.RecordHash(record).String()
			remoteValue = record.Value
		}
	}
	decode := func(encoded []byte) ([]byte, bool) {
		if len(encoded) == 0 {
			return nil, false
		}
		payload, err := openAccountPayload(secret, accountID, network, entry, encoded)
		return payload, err == nil
	}
	base, baseOK := decode(baseValue)
	defer zeroBytes(base)
	conflict, conflictOK := decode(module.ConflictValue)
	defer zeroBytes(conflict)
	remote, remoteOK := decode(remoteValue)
	defer zeroBytes(remote)
	catalog, localErr := manager.accountManagedDataCatalog()
	var local []byte
	if localErr == nil {
		local, localErr = manager.exportAccountPayload(entry, catalog)
	}
	defer zeroBytes(local)
	localOK := localErr == nil && len(local) != 0
	latest, latestErr := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
	latestHash := ""
	if latest != nil && latest.Baseline != nil {
		latestHash = dkvsindexer.RecordHash(latest.Baseline).String()
	}
	unchanged := latestErr == nil && latest != nil && latest.Generation == module.Generation &&
		latest.Captured == module.Captured && latest.Confirmed == module.Confirmed && latest.Conflict == module.Conflict &&
		latestHash == baseHash && bytes.Equal(latest.ConflictValue, module.ConflictValue)
	t.Logf("RGB backup facts: manager=%d scope_tag=%s same_module=%t generation=%d captured=%d confirmed=%d conflict=%t baseline_seq=%d baseline_hash=%s authoritative_seq=%d authoritative_hash=%s authoritative_error=%T conflict_value_bytes=%d conflict_value_digest=%s local_error=%T local_ok=%t baseline_ok=%t conflict_ok=%t authoritative_ok=%t local_eq_base=%t local_eq_conflict=%t base_eq_conflict=%t base_eq_authoritative=%t conflict_eq_authoritative=%t local_eq_authoritative=%t",
		manager.debugID, accountTraceScope(scope), unchanged, module.Generation, module.Captured, module.Confirmed, module.Conflict,
		baseSeq, baseHash, remoteSeq, remoteHash, remoteErr, len(module.ConflictValue), coreDigest(module.ConflictValue), localErr,
		localOK, baseOK, conflictOK, remoteOK, localOK && baseOK && bytes.Equal(local, base),
		localOK && conflictOK && bytes.Equal(local, conflict), baseOK && conflictOK && bytes.Equal(base, conflict),
		baseOK && remoteOK && bytes.Equal(base, remote), conflictOK && remoteOK && bytes.Equal(conflict, remote),
		localOK && remoteOK && bytes.Equal(local, remote))
}

func coreReceiveIdentityMatches(request *corewallet.ReceiveRequest, transfer *rgb11wallet.TransferState,
	invoice *invoicing.Invoice, wantHash, ack string, acknowledged bool) bool {
	if request == nil || transfer == nil || invoice == nil {
		return false
	}
	requestInvoiceMatches := request.Invoice != ""
	switch transfer.TransportMode {
	case RGB11ProxyTransport:
		requestInvoiceMatches = requestInvoiceMatches && !transfer.AddressMode &&
			!transfer.SyntheticInvoiceRemoved && request.Invoice == transfer.Invoice
	case RGB11AddressTransport:
		// Address delivery keeps the synthetic receive request (and its invoice)
		// in the engine snapshot, but deliberately removes that invoice from the
		// public transfer row. Bind this exception to the address-mode markers.
		requestInvoiceMatches = requestInvoiceMatches && transfer.AddressMode &&
			transfer.SyntheticInvoiceRemoved && transfer.Invoice == ""
	default:
		requestInvoiceMatches = false
	}
	return request.Version == corewallet.ReceiveVersion &&
		request.RequestID != "" && request.RequestID == transfer.ReceiveRequestID &&
		request.RecipientID != "" && request.RecipientID == transfer.RecipientID &&
		request.Status == corewallet.ReceiveAcknowledged &&
		request.TransferID != "" && request.TransferID == transfer.TransferID && request.ObjectHash == wantHash &&
		request.ObjectHash == transfer.ConsignmentHash && request.WitnessTxID == transfer.WitnessTxID &&
		requestInvoiceMatches &&
		request.Expiry > 0 && request.Expiry == transfer.Expiry &&
		invoice.Expiry != nil && *invoice.Expiry == request.Expiry &&
		invoice.Beneficiary.Network == rgb11InvoiceNetwork(GetChainParam()) &&
		transfer.Direction == "receive" && transfer.Status == "awaiting_broadcast" &&
		transfer.AckStatus == ack && transfer.DeliveryAcknowledged == acknowledged &&
		transfer.ReceiveRequestID == request.RequestID && transfer.WitnessTxID != "" &&
		transfer.ConsignmentHash == wantHash
}

func coreSameReceiveRequest(left, right *corewallet.ReceiveRequest) bool {
	if left == nil || right == nil {
		return false
	}
	leftSeal, leftErr := left.Seal.StrictBytes()
	rightSeal, rightErr := right.Seal.StrictBytes()
	return leftErr == nil && rightErr == nil && bytes.Equal(leftSeal, rightSeal) &&
		left.Version == right.Version && left.Mode == right.Mode && left.RequestID == right.RequestID &&
		left.RecipientID == right.RecipientID && bytes.Equal(left.WitnessScript, right.WitnessScript) &&
		left.Invoice == right.Invoice && left.RelayKey == right.RelayKey && left.AckKey == right.AckKey &&
		left.CreatedAt == right.CreatedAt && left.Expiry == right.Expiry && left.Status == right.Status &&
		left.TransferID == right.TransferID && left.ObjectHash == right.ObjectHash &&
		left.WitnessTxID == right.WitnessTxID && left.FailureCode == right.FailureCode
}

func coreReceiveReservationMatches(reservation *rgb11wallet.ReceiveReservation,
	request *corewallet.ReceiveRequest) bool {
	if reservation == nil || request == nil || request.Seal.TxID == nil || request.RequestID == "" {
		return false
	}
	expectedOutpoint := (wire.OutPoint{
		Hash: chainhash.Hash(*request.Seal.TxID), Index: request.Seal.Vout,
	}).String()
	return reservation.Version == 1 && reservation.RequestID == request.RequestID &&
		reservation.OutPoint == expectedOutpoint && reservation.ReservationID != "" &&
		reservation.Expiry == request.Expiry && reservation.Expiry > 0
}

func coreSameReceiveReservation(left, right *rgb11wallet.ReceiveReservation) bool {
	return left != nil && right != nil && left.Version == right.Version &&
		left.RequestID == right.RequestID && left.OutPoint == right.OutPoint &&
		left.ReservationID == right.ReservationID && left.Expiry == right.Expiry
}

func coreReceiveKeyMatches(key *rgb11wallet.ReceiveKey, request *corewallet.ReceiveRequest,
	invoice *invoicing.Invoice, receiver *Manager, accountIndex uint32) bool {
	if key == nil || request == nil || invoice == nil || receiver == nil || receiver.GetWallet() == nil ||
		key.Version != 1 || key.RequestID != request.RequestID || key.ScopeIndex != accountIndex ||
		key.Change != 1 || key.LogicalAddress == "" ||
		key.LogicalAddress != receiver.GetWallet().GetAddress() ||
		!bytes.Equal(key.WitnessScript, request.WitnessScript) || len(key.InternalPubKey) != 32 {
		return false
	}
	pathWallet, ok := receiver.GetWallet().(rgb11PathWallet)
	if !ok {
		return false
	}
	pubKey := pathWallet.GetPubKeyByPath(key.Change, key.Index)
	address := pathWallet.GetAddressByPath(key.Change, key.Index)
	if pubKey == nil || address == "" {
		return false
	}
	script, err := AddrToPkScript(address, GetChainParam())
	if err != nil || !bytes.Equal(script, key.WitnessScript) {
		return false
	}
	compressed := pubKey.SerializeCompressed()
	if len(compressed) != 33 || !bytes.Equal(key.InternalPubKey, compressed[1:]) {
		return false
	}
	return invoice.Beneficiary.InternalXOnly == nil ||
		bytes.Equal(invoice.Beneficiary.InternalXOnly[:], key.InternalPubKey)
}

func coreActiveWitnessMatches(request *corewallet.ReceiveRequest,
	invoice *invoicing.Invoice, receiver *Manager) bool {
	if request == nil || invoice == nil || receiver == nil || receiver.GetWallet() == nil {
		return false
	}
	script, err := AddrToPkScript(receiver.GetWallet().GetAddress(), GetChainParam())
	if err != nil || !bytes.Equal(script, request.WitnessScript) {
		return false
	}
	if invoice.Beneficiary.InternalXOnly == nil {
		return true
	}
	pubKey := receiver.GetWallet().GetPubKey()
	if pubKey == nil {
		return false
	}
	compressed := pubKey.SerializeCompressed()
	return len(compressed) == 33 && bytes.Equal(invoice.Beneficiary.InternalXOnly[:], compressed[1:])
}

func coreAssertRGB11ACKRecoveryPublished(t *testing.T, receiver *Manager,
	prepared *RGB11PreparedTransfer) {
	t.Helper()
	if receiver == nil || prepared == nil || prepared.State == nil ||
		prepared.TxID == "" || prepared.State.TransferID == "" || prepared.State.ConsignmentHash == "" {
		t.Fatal("core-e2e: missing accepted transfer identity for pre-broadcast recovery assertion")
	}
	// This assertion concerns this receive scope. An unrelated provider's
	// failed backup must not prevent an otherwise complete RGB ACK recovery.
	backupCtx, cancelBackup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBackup()
	backupErr := receiver.WaitAccountBackup(backupCtx, rgb11AccountManagedProviderID, coreSelectedRGBScope(receiver))
	if backupErr != nil {
		coreRGBBackupFacts(t, backupCtx, receiver)
	}
	coreRequire(t, "complete this receiver scope's real asynchronous backup", backupErr)
	state, err := receiver.GetRGB11State()
	coreRequire(t, "read staged receiver transition after ACK", err)
	if state == nil {
		t.Fatal("core-e2e: receiver has no staged RGB11 state after ACK")
	}
	wantHash := prepared.State.ConsignmentHash
	wantAck, wantAcknowledged := "ack-sent", true
	if prepared.State.TransportMode == RGB11ProxyTransport {
		// Proxy persists the prepared receive before posting its ACK. The
		// acknowledged engine request is its durable recovery obligation.
		wantAck, wantAcknowledged = "accepted", false
		// Standard Proxy transmits the strict transfer file, while Direct
		// transmits the original armor. Bind recovery to those exact bytes.
		raw, err := rgb11TransferFile([]byte(prepared.RecipientConsignment))
		coreRequire(t, "encode exact Proxy consignment for recovery checks", err)
		wantHash = rgb11ObjectHash(raw)
	}
	var staged *rgb11wallet.TransferState
	matches := 0
	for _, transfer := range state.Transfers {
		if transfer != nil && transfer.WitnessTxID == prepared.TxID &&
			transfer.ConsignmentHash == wantHash &&
			(prepared.State.TransportMode == RGB11ProxyTransport || transfer.TransferID == prepared.State.TransferID) &&
			transfer.Direction == "receive" {
			staged = transfer
			matches++
		}
	}
	coreAssert(t, matches == 1, "receiver must retain exactly one receive lifecycle for this transfer, witness and consignment")
	if prepared.State.TransportMode != RGB11ProxyTransport {
		coreAssert(t, staged.TransferID == prepared.State.TransferID, "Direct receive lost the sender transfer identity")
	}
	coreAssert(t, staged.Status == "awaiting_broadcast" &&
		staged.AckStatus == wantAck && staged.DeliveryAcknowledged == wantAcknowledged &&
		staged.ConsignmentHash == wantHash && staged.ReceiveRequestID != "",
		fmt.Sprintf("receiver ACK recovery mismatch: status=%s ack=%s acknowledged=%t request=%t",
			staged.Status, staged.AckStatus, staged.DeliveryAcknowledged, staged.ReceiveRequestID != ""))
	for _, proof := range state.Proofs {
		if proof != nil && proof.ConsignmentHash == wantHash {
			t.Fatal("receiver exposed a current consumer proof before sender broadcast")
		}
	}
	payloads, err := receiver.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	coreRequire(t, "read receiver stable RGB11 package after ACK", err)
	packagesChecked := 0
	relevantPackages := 0
	for _, payload := range payloads {
		packagesChecked++
		packageValue, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
		coreRequire(t, "decode receiver stable package after ACK", err)
		transferRecords, objectRecords, preparedReceiveRecords, receiptRecords := 0, 0, 0, 0
		objectHashMatches, preparedReceiveMatches := false, false
		receiveRequestRecords, decodedReceiveRequests := 0, 0
		receiveRequestDecodeFailures := 0
		foundReceiveKey, foundReceiveReservation := false, false
		var stableReservations []*rgb11wallet.ReceiveReservation
		var stableRequest *corewallet.ReceiveRequest
		for _, record := range packageValue.EngineRecords {
			if record.Key != "wallet/receive/"+staged.ReceiveRequestID {
				continue
			}
			receiveRequestRecords++
			request, decodeErr := corewallet.DecodeReceiveRequest(record.Value)
			if decodeErr != nil {
				receiveRequestDecodeFailures++
				continue
			}
			decodedReceiveRequests++
			if stableRequest == nil {
				stableRequest = request
			}
		}
		for _, record := range packageValue.ProjectionRecords {
			switch record.Key {
			case "transfer-" + staged.TransferID:
				transferRecords++
			case "object-" + wantHash:
				objectRecords++
				objectHash := sha256.Sum256(record.Value)
				objectHashMatches = hex.EncodeToString(objectHash[:]) == wantHash
			case "validation-" + wantHash:
				receiptRecords++
			case "prepared-receive-" + staged.TransferID:
				preparedReceiveRecords++
				preparedReceiveMatches = string(record.Value) == staged.ReceiveRequestID
			}
		}
		if transferRecords == 0 && objectRecords == 0 && preparedReceiveRecords == 0 &&
			receiveRequestRecords == 0 && receiptRecords == 0 {
			continue
		}
		relevantPackages++
		stableTransfers, transferErr := rgb11wallet.TransfersFromProjectionSnapshot(packageValue.ProjectionRecords)
		coreRequire(t, "decode receiver transfer from stable package", transferErr)
		var stableTransfer *rgb11wallet.TransferState
		stableTransferRecords := 0
		for _, transfer := range stableTransfers {
			if transfer != nil && transfer.TransferID == staged.TransferID && transfer.Direction == "receive" {
				stableTransferRecords++
				stableTransfer = transfer
			}
		}
		var invoice *invoicing.Invoice
		var invoiceErr error
		if stableRequest != nil {
			invoice, invoiceErr = invoicing.Parse(stableRequest.Invoice)
		} else {
			invoiceErr = fmt.Errorf("stable receive request is missing")
		}
		requestMatches := invoiceErr == nil &&
			coreReceiveIdentityMatches(stableRequest, stableTransfer, invoice, wantHash, wantAck, wantAcknowledged)
		branchMatches, receiveKeyRequired := false, false
		switch {
		case requestMatches && stableRequest.Mode == corewallet.ReceiveWitness &&
			invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout && len(stableRequest.WitnessScript) != 0:
			beneficiaryScript, beneficiaryErr := invoice.Beneficiary.WitnessScript()
			activeScript, activeErr := AddrToPkScript(receiver.GetWallet().GetAddress(), GetChainParam())
			witnessScriptMatches := beneficiaryErr == nil && activeErr == nil &&
				bytes.Equal(beneficiaryScript, stableRequest.WitnessScript)
			if witnessScriptMatches {
				receiveKeyRequired = !bytes.Equal(activeScript, stableRequest.WitnessScript)
				witnessHash := sha256.Sum256(stableRequest.WitnessScript)
				expectedKeyRecord := "receive-key-" + hex.EncodeToString(witnessHash[:])
				keyRecords := 0
				for _, record := range packageValue.ProjectionRecords {
					if record.Key == expectedKeyRecord {
						keyRecords++
					}
				}
				foundReceiveKey = keyRecords == 1
				branchMatches = (!receiveKeyRequired || keyRecords == 1) && keyRecords <= 1
			}
		case requestMatches && stableRequest.Mode == corewallet.ReceiveBlind &&
			invoice.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal && len(stableRequest.WitnessScript) == 0:
			concealed, concealErr := stableRequest.Seal.Conceal()
			reservations, reservationErr := rgb11wallet.ReceiveReservationsFromProjectionSnapshot(packageValue.ProjectionRecords)
			matchingReservations := 0
			for _, reservation := range reservations {
				if reservation != nil && reservation.RequestID == stableRequest.RequestID {
					matchingReservations++
					stableReservations = append(stableReservations, reservation)
				}
			}
			foundReceiveReservation = matchingReservations == 1 && len(stableReservations) == 1 &&
				coreReceiveReservationMatches(stableReservations[0], stableRequest)
			branchMatches = concealErr == nil &&
				bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) &&
				reservationErr == nil && foundReceiveReservation
		}
		matchingReceiveRequests := 0
		if requestMatches && branchMatches {
			matchingReceiveRequests = 1
		}
		foundRequest := receiveRequestRecords == 1 && decodedReceiveRequests == 1 &&
			receiveRequestDecodeFailures == 0 && matchingReceiveRequests == 1
		foundTransfer := transferRecords == 1 && stableTransferRecords == 1 && stableTransfer != nil
		foundObject := objectRecords == 1 && objectHashMatches
		foundPreparedReceive := preparedReceiveRecords == 1 && preparedReceiveMatches
		foundCurrentReceipt := receiptRecords != 0
		if !foundTransfer || !foundObject || !foundPreparedReceive || !foundRequest || foundCurrentReceipt ||
			receiveRequestRecords != 1 || receiveRequestDecodeFailures != 0 ||
			stableTransferRecords != 1 || (stableRequest != nil && stableRequest.Mode == corewallet.ReceiveBlind && !foundReceiveReservation) {
			t.Errorf("core-e2e: ACK stable-package checks failed (package=%d/%d transfer=%t object_raw_hash_matches=%t prepared_receive=%t receive_request=%t receive_request_records=%d decoded=%d identity_matches=%d decode_failures=%d receive_key_material=%t receive_reservation_material=%t premature_validation_receipt=%t)",
				packagesChecked, len(payloads), foundTransfer, foundObject,
				foundPreparedReceive, foundRequest, receiveRequestRecords, decodedReceiveRequests,
				matchingReceiveRequests, receiveRequestDecodeFailures, foundReceiveKey, foundReceiveReservation,
				foundCurrentReceipt)
			continue
		}
		snapshot, err := packageValue.WalletSnapshot()
		coreRequire(t, "convert acknowledged RGB11 package to cold snapshot", err)
		database := indexerdb.NewKVDB(t.TempDir())
		defer database.Close()
		localScope := rgb11StorageScope(1, snapshot.AccountIndex)
		projection := rgb11wallet.NewProjectionStore(database, nil)
		if err := projection.SetScope(localScope); err != nil {
			t.Fatal(err)
		}
		if err := projection.ImportSnapshot(snapshot.ProjectionRecords); err != nil {
			t.Fatalf("import acknowledged RGB11 projection snapshot: %v", err)
		}
		recoveredTransfer, err := projection.LoadTransferState(staged.TransferID)
		if err != nil || recoveredTransfer == nil || recoveredTransfer.Direction != "receive" ||
			recoveredTransfer.Status != "awaiting_broadcast" ||
			recoveredTransfer.AckStatus != wantAck || recoveredTransfer.DeliveryAcknowledged != wantAcknowledged ||
			recoveredTransfer.ConsignmentHash != wantHash ||
			recoveredTransfer.ReceiveRequestID != staged.ReceiveRequestID ||
			recoveredTransfer.WitnessTxID != staged.WitnessTxID ||
			recoveredTransfer.RecipientID != staged.RecipientID || recoveredTransfer.Invoice != staged.Invoice ||
			recoveredTransfer.Expiry != staged.Expiry {
			t.Fatalf("stable ACK package did not cold-restore its awaiting-broadcast lifecycle: err=%v", err)
		}
		recoveredObject, err := projection.LoadObject(wantHash)
		if err != nil {
			t.Fatalf("stable ACK package omitted the exact prepared consignment: %v", err)
		}
		objectHash := sha256.Sum256(recoveredObject)
		if hex.EncodeToString(objectHash[:]) != wantHash {
			t.Fatal("stable ACK package raw consignment bytes do not match its immutable hash")
		}
		preparedRequestID, err := projection.LoadPreparedReceive(staged.TransferID)
		if err != nil || preparedRequestID != staged.ReceiveRequestID {
			t.Fatalf("stable ACK package omitted receiver prepared-request identity: err=%v", err)
		}
		engineStore := rgb11wallet.NewEngineStore(database)
		if err := engineStore.SetScope(localScope); err != nil {
			t.Fatal(err)
		}
		if err := engineStore.ImportSnapshot(snapshot.EngineRecords); err != nil {
			t.Fatalf("import acknowledged RGB11 engine snapshot: %v", err)
		}
		recoveredEngine, err := corewallet.NewEngine(engineStore)
		if err != nil {
			t.Fatalf("reopen acknowledged RGB11 receive engine: %v", err)
		}
		recoveredRequest, err := recoveredEngine.LoadReceive(staged.ReceiveRequestID)
		if err != nil || !coreSameReceiveRequest(stableRequest, recoveredRequest) ||
			!coreReceiveIdentityMatches(recoveredRequest, recoveredTransfer, invoice, wantHash, wantAck, wantAcknowledged) {
			t.Fatalf("stable ACK package did not cold-restore the exact acknowledged receive request: err=%v", err)
		}
		switch recoveredRequest.Mode {
		case corewallet.ReceiveWitness:
			if receiveKeyRequired {
				key, err := projection.LoadReceiveKey(recoveredRequest.WitnessScript)
				if err != nil || !coreReceiveKeyMatches(key, recoveredRequest, invoice, receiver, snapshot.AccountIndex) {
					t.Fatalf("stable ACK package did not cold-restore the actual independent receive-key path: err=%v", err)
				}
			} else if !coreActiveWitnessMatches(recoveredRequest, invoice, receiver) {
				t.Fatal("stable ACK package witness does not match the active receiver wallet path")
			}
		case corewallet.ReceiveBlind:
			if !foundReceiveReservation || len(stableReservations) != 1 {
				t.Fatal("stable ACK package omitted the blind receive's exact outpoint reservation")
			}
			reservation, err := projection.LoadReceiveReservation(staged.ReceiveRequestID)
			if err != nil || !coreReceiveReservationMatches(reservation, recoveredRequest) ||
				!coreSameReceiveReservation(stableReservations[0], reservation) {
				t.Fatalf("stable ACK package did not cold-restore the exact blind receive reservation: err=%v", err)
			}
		default:
			t.Fatalf("stable ACK package cold-restored unsupported receive mode %q", recoveredRequest.Mode)
		}
		for _, outpoint := range staged.OutputOutPoints {
			proofPrefix := "proof-" + outpoint + "-"
			for _, record := range packageValue.ProjectionRecords {
				if strings.HasPrefix(record.Key, proofPrefix) {
					t.Fatalf("pre-broadcast receiver package contains a proof for output %s", outpoint)
				}
			}
		}
		return
	}
	t.Errorf("core-e2e: ACK stable recovery closure was absent or incomplete (packages_checked=%d relevant_packages=%d)",
		packagesChecked, relevantPackages)
	t.FailNow()
}

func coreAssertRGB11SettledRecoveryPublished(t *testing.T, receiver *Manager,
	prepared *RGB11PreparedTransfer) {
	t.Helper()
	if receiver == nil || prepared == nil || prepared.State == nil || prepared.State.ConsignmentHash == "" {
		t.Fatal("core-e2e: missing settled transfer identity for recovery assertion")
	}
	scopeID, err := receiver.rgbManager.rgb11AccountManagedScopeID()
	coreRequire(t, "resolve settled receiver recovery scope", err)
	backupCtx, cancelBackup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelBackup()
	backupErr := receiver.WaitAccountBackup(backupCtx, rgb11AccountManagedProviderID, scopeID)
	if backupErr != nil {
		coreRGBBackupFacts(t, backupCtx, receiver)
	}
	coreRequire(t, "complete this receiver scope's settled asynchronous backup", backupErr)
	state, err := receiver.GetRGB11State()
	coreRequire(t, "read settled receiver proof state", err)
	if state == nil {
		t.Fatal("core-e2e: settled receiver has no proof state")
	}
	var proofKey string
	for _, proof := range state.Proofs {
		if proof != nil && proof.ConsignmentHash == prepared.State.ConsignmentHash && rgb11ProofIsCurrent(proof) {
			proofKey = "proof-" + proof.OutPoint + "-" + proof.AssetName.String()
			break
		}
	}
	coreAssert(t, proofKey != "", "settled receiver has no current proof for its accepted consignment")
	if proofKey == "" {
		return
	}
	payloads, err := receiver.currentAccountManagedProviderPayloads(rgb11AccountManagedProviderID)
	coreRequire(t, "read settled receiver stable RGB11 package", err)
	payload, ok := payloads[scopeID]
	coreAssert(t, ok && payload.Scope == scopeID,
		"settled receiver scope is absent from its stable RGB11 package")
	packageValue, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
	coreRequire(t, "decode settled receiver stable RGB11 package", err)
	foundProof, foundObject, foundReceipt, foundTransfer := false, false, false, false
	for _, record := range packageValue.ProjectionRecords {
		switch record.Key {
		case proofKey:
			foundProof = true
		case "object-" + prepared.State.ConsignmentHash:
			foundObject = true
		case "validation-" + prepared.State.ConsignmentHash:
			foundReceipt = true
		case "transfer-" + prepared.State.TransferID:
			foundTransfer = true
		}
	}
	if !foundProof || !foundObject || !foundReceipt || !foundTransfer {
		t.Logf("settled recovery closure: proof=%t object=%t receipt=%t transfer=%t",
			foundProof, foundObject, foundReceipt, foundTransfer)
	}
	coreAssert(t, foundProof && foundObject && foundReceipt && foundTransfer,
		"settled stable package omitted proof, consumer, validation receipt, or transfer lifecycle")
}

type coreManagedRecoverySize struct {
	packages, rpkRaw, rpkDeflate, rpkEncoded, amd, ade, dkb1    int
	objectRows, objectBytes, uniqueObjects, uniqueObjectBytes   int
	duplicateObjectRows, duplicateContentDistinctKeys           int
	proofRows, receiptRows, outputRows                          int
	requiredRows, requiredKVBytes                               int
	terminalTransferRows, terminalTransferKVBytes               int
	terminalPendingRows, terminalPendingKVBytes                 int
	terminalPendingSignedPSBTBytes                              int
	activePendingSignedPSBTBytes                                int
	usedRequestRows, usedRequestKVBytes                         int
	terminalUsedRequestRows, terminalUsedRequestKVBytes         int
	activeUsedRequestRows, activeUsedRequestKVBytes             int
	unknownUsedRequestRows, unknownUsedRequestKVBytes           int
	terminalReceiveMaterialRows, terminalReceiveMaterialKVBytes int
	activeLifecycleRows, activeLifecycleKVBytes                 int
	unknownLifecycleRows, unknownLifecycleKVBytes               int
	unreferencedLocalRows, unreferencedLocalKVBytes             int
	unreferencedOutputRows, unreferencedOutputKVBytes           int
	canonicalizedProofReceiptRows, canonicalizedValueByteDelta  int
	uncategorizedRows, uncategorizedKVBytes                     int
}

func coreLogManagedRecoverySize(t *testing.T, hop int, role string, manager *Manager,
	previous map[string]coreManagedRecoverySize) {
	t.Helper()
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "build account catalog for recovery size sample", err)
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	providerPayloads, err := provider.Export(catalog)
	coreRequire(t, "export RGB11 packages for size sample", err)
	accounts, err := provider.accountsByScope()
	coreRequire(t, "resolve scopes for recovery size classification", err)
	var size coreManagedRecoverySize
	objectHashValues := make(map[string][]byte)
	contentHashKeys := make(map[[sha256.Size]byte]string)
	terminalStatus := func(status string) bool {
		switch status {
		case "settled", "rejected", "cancelled", "expired", "orphaned":
			return true
		default:
			return false
		}
	}
	addLifecycleRow := func(record rgb11wallet.SnapshotRecord, status string, known bool) {
		rowBytes := len(record.Key) + len(record.Value)
		switch {
		case known && terminalStatus(status):
			size.terminalReceiveMaterialRows++
			size.terminalReceiveMaterialKVBytes += rowBytes
		case known:
			size.activeLifecycleRows++
			size.activeLifecycleKVBytes += rowBytes
		default:
			size.unknownLifecycleRows++
			size.unknownLifecycleKVBytes += rowBytes
		}
	}
	for _, payload := range providerPayloads {
		packageValue, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
		coreRequire(t, "decode RGB11 package for size sample", err)
		size.packages++
		size.rpkDeflate += coreRecoveryPackageDeflateSize(t, payload.Payload)
		size.rpkEncoded += len(payload.Payload)
		size.rpkRaw += coreRecoveryPackageRawSize(t, payload.Payload)
		accountValue, ok := accounts[payload.Scope]
		if !ok {
			t.Fatalf("core-e2e: missing local account for recovery-size scope ordinal %d", size.packages)
		}
		scopeManager, err := manager.newScopedRGB11Manager(accountValue)
		coreRequire(t, "open scoped manager for recovery size sample", err)
		states, err := scopeManager.projectionStore.ListTransfers()
		coreRequire(t, "list transfer lifecycles for recovery size sample", err)
		statesByID := make(map[string]*rgb11wallet.TransferState, len(states))
		requestStatus := make(map[string]string)
		for _, state := range states {
			if state == nil || state.TransferID == "" {
				continue
			}
			statesByID[state.TransferID] = state
			if state.Direction == "receive" && state.ReceiveRequestID != "" {
				requestStatus[state.ReceiveRequestID] = state.Status
			}
		}
		pendingTransfers, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(packageValue.ProjectionRecords)
		coreRequire(t, "classify retained pending transfers in recovery package", err)
		pendingByID := make(map[string]*rgb11wallet.PendingTransfer, len(pendingTransfers))
		for _, pending := range pendingTransfers {
			if pending != nil && pending.State.TransferID != "" {
				pendingByID[pending.State.TransferID] = pending
			}
		}
		requestKeyStatus := make(map[string]string)
		requestIDs := make(map[string]struct{}, len(packageValue.EngineRecords))
		for _, record := range packageValue.EngineRecords {
			request, decodeErr := corewallet.DecodeReceiveRequest(record.Value)
			coreRequire(t, "decode engine receive request for size sample", decodeErr)
			requestIDs[request.RequestID] = struct{}{}
			if len(request.WitnessScript) != 0 {
				witnessHash := sha256.Sum256(request.WitnessScript)
				requestKeyStatus["receive-key-"+hex.EncodeToString(witnessHash[:])] = requestStatus[request.RequestID]
			}
		}
		stableProjection := make(map[string]rgb11wallet.SnapshotRecord, len(packageValue.ProjectionRecords))
		for _, record := range packageValue.ProjectionRecords {
			stableProjection[record.Key] = record
			rowBytes := len(record.Key) + len(record.Value)
			switch {
			case strings.HasPrefix(record.Key, "object-"):
				size.objectRows++
				size.objectBytes += len(record.Value)
				size.requiredRows++
				size.requiredKVBytes += rowBytes
				objectID := strings.TrimPrefix(record.Key, "object-")
				if previousValue, exists := objectHashValues[objectID]; exists {
					size.duplicateObjectRows++
					if !bytes.Equal(previousValue, record.Value) {
						t.Fatalf("core-e2e: duplicate immutable object identity has different bytes (object_rows=%d)", size.objectRows)
					}
				} else {
					objectHashValues[objectID] = append([]byte(nil), record.Value...)
					size.uniqueObjects++
					size.uniqueObjectBytes += len(record.Value)
				}
				valueHash := sha256.Sum256(record.Value)
				if firstID, exists := contentHashKeys[valueHash]; exists {
					if firstID != objectID {
						size.duplicateContentDistinctKeys++
					}
				} else {
					contentHashKeys[valueHash] = objectID
				}
			case strings.HasPrefix(record.Key, "proof-"):
				size.proofRows++
				size.requiredRows++
				size.requiredKVBytes += rowBytes
			case strings.HasPrefix(record.Key, "validation-"):
				size.receiptRows++
				size.requiredRows++
				size.requiredKVBytes += rowBytes
			case strings.HasPrefix(record.Key, "output-"):
				size.outputRows++
				size.requiredRows++
				size.requiredKVBytes += rowBytes
			case strings.HasPrefix(record.Key, "contract-object-"),
				strings.HasPrefix(record.Key, "local-name-"), strings.HasPrefix(record.Key, "registered-name-"):
				size.requiredRows++
				size.requiredKVBytes += rowBytes
			case strings.HasPrefix(record.Key, "transfer-"):
				if state := statesByID[strings.TrimPrefix(record.Key, "transfer-")]; state != nil {
					if terminalStatus(state.Status) {
						size.terminalTransferRows++
						size.terminalTransferKVBytes += rowBytes
					} else {
						size.activeLifecycleRows++
						size.activeLifecycleKVBytes += rowBytes
					}
				} else {
					size.unknownLifecycleRows++
					size.unknownLifecycleKVBytes += rowBytes
				}
			case strings.HasPrefix(record.Key, "pending-"):
				if pending := pendingByID[strings.TrimPrefix(record.Key, "pending-")]; pending != nil {
					if terminalStatus(pending.State.Status) {
						size.terminalPendingRows++
						size.terminalPendingKVBytes += rowBytes
						size.terminalPendingSignedPSBTBytes += len(pending.SignedPSBT)
					} else {
						size.activeLifecycleRows++
						size.activeLifecycleKVBytes += rowBytes
						size.activePendingSignedPSBTBytes += len(pending.SignedPSBT)
					}
				} else {
					size.unknownLifecycleRows++
					size.unknownLifecycleKVBytes += rowBytes
				}
			case strings.HasPrefix(record.Key, "prepared-receive-"):
				transferID := strings.TrimPrefix(record.Key, "prepared-receive-")
				if state := statesByID[transferID]; state != nil {
					addLifecycleRow(record, state.Status, true)
				} else {
					addLifecycleRow(record, "", false)
				}
			case strings.HasPrefix(record.Key, "receive-key-"):
				if status, known := requestKeyStatus[record.Key]; known {
					addLifecycleRow(record, status, true)
				} else {
					addLifecycleRow(record, "", false)
				}
			case strings.HasPrefix(record.Key, "receive-reservation-"):
				requestID := strings.TrimPrefix(record.Key, "receive-reservation-")
				if status, known := requestStatus[requestID]; known {
					addLifecycleRow(record, status, true)
				} else if _, known := requestIDs[requestID]; known {
					addLifecycleRow(record, "", true)
				} else {
					addLifecycleRow(record, "", false)
				}
			default:
				size.uncategorizedRows++
				size.uncategorizedKVBytes += rowBytes
			}
		}
		for _, record := range packageValue.EngineRecords {
			rowBytes := len(record.Key) + len(record.Value)
			request, decodeErr := corewallet.DecodeReceiveRequest(record.Value)
			coreRequire(t, "decode receive request for lifecycle size sample", decodeErr)
			status, used := requestStatus[request.RequestID]
			if used {
				size.usedRequestRows++
				size.usedRequestKVBytes += rowBytes
				if terminalStatus(status) {
					size.terminalUsedRequestRows++
					size.terminalUsedRequestKVBytes += rowBytes
				} else if status != "" {
					size.activeUsedRequestRows++
					size.activeUsedRequestKVBytes += rowBytes
				} else {
					size.unknownUsedRequestRows++
					size.unknownUsedRequestKVBytes += rowBytes
				}
			} else {
				size.activeLifecycleRows++
				size.activeLifecycleKVBytes += rowBytes
			}
		}

		// Compare the stable closure with the pre-filter local snapshot. Only
		// unreferenced output rows are classified as chain-rebuildable here;
		// other omitted rows remain separately reported rather than assumed safe.
		fullSnapshot, _, err := scopeManager.exportRGB11WalletSnapshot(packageValue.WalletID)
		coreRequire(t, "export local snapshot for stable-cache comparison", err)
		for _, record := range fullSnapshot.ProjectionRecords {
			if stableRecord, kept := stableProjection[record.Key]; kept {
				if (strings.HasPrefix(record.Key, "proof-") || strings.HasPrefix(record.Key, "validation-")) &&
					!bytes.Equal(record.Value, stableRecord.Value) {
					size.canonicalizedProofReceiptRows++
					size.canonicalizedValueByteDelta += len(record.Value) - len(stableRecord.Value)
				}
				continue
			}
			size.unreferencedLocalRows++
			size.unreferencedLocalKVBytes += len(record.Key) + len(record.Value)
			if strings.HasPrefix(record.Key, "output-") {
				size.unreferencedOutputRows++
				size.unreferencedOutputKVBytes += len(record.Key) + len(record.Value)
			}
		}
	}
	manager.mutex.RLock()
	if manager.accountProfile == nil || len(manager.accountSecret) == 0 {
		manager.mutex.RUnlock()
		t.Fatal("core-e2e: account secret unavailable for managed-data size sample")
	}
	accountID := manager.accountProfile.AccountID
	network := manager.cfg.Chain
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	for _, payload := range providerPayloads {
		entry, err := accountEntry(accountID, rgb11AccountManagedProviderID, payload.Scope)
		coreRequire(t, "resolve independent RGB module for size sample", err)
		blob, err := sealAccountPayload(secret, accountID, network, entry, payload.Payload)
		coreRequire(t, "encode independent RGB module for size sample", err)
		decoded, err := DecodeDKVSBlobValue(blob)
		coreRequire(t, "measure independent RGB module envelope", err)
		size.amd += len(payload.Payload)
		size.ade += len(decoded.Data)
		size.dkb1 += len(blob)
	}
	previousSize := previous[role]
	previous[role] = size
	t.Logf("core-e2e: RGB11 recovery-size role=%s hop=%d packages=%d RPK_raw=%d delta=%+d RPK_deflate_stream=%d delta=%+d RPK_encoded=%d delta=%+d RGB_payload_total=%d delta=%+d AMOE_total=%d delta=%+d DKB1_total=%d delta=%+d",
		role, hop, size.packages, size.rpkRaw, size.rpkRaw-previousSize.rpkRaw,
		size.rpkDeflate, size.rpkDeflate-previousSize.rpkDeflate,
		size.rpkEncoded, size.rpkEncoded-previousSize.rpkEncoded,
		size.amd, size.amd-previousSize.amd, size.ade, size.ade-previousSize.ade,
		size.dkb1, size.dkb1-previousSize.dkb1)
	t.Logf("core-e2e: RGB11 recovery-records role=%s hop=%d required_rows=%d required_kv_bytes=%d proofs=%d receipts=%d object_rows=%d unique_object_hashes=%d duplicate_object_rows=%d object_value_bytes=%d unique_object_value_bytes=%d duplicate_content_distinct_keys=%d outputs=%d terminal_transfers=%d terminal_transfer_kv_bytes=%d terminal_pending=%d terminal_pending_kv_bytes=%d terminal_pending_signed_psbt_bytes=%d active_pending_signed_psbt_bytes=%d terminal_used_requests=%d terminal_used_request_kv_bytes=%d active_used_requests=%d active_used_request_kv_bytes=%d unknown_used_requests=%d unknown_used_request_kv_bytes=%d terminal_receive_material_rows=%d terminal_receive_material_kv_bytes=%d active_lifecycle_rows=%d active_lifecycle_kv_bytes=%d unknown_lifecycle_rows=%d unknown_lifecycle_kv_bytes=%d unreferenced_local_rows=%d unreferenced_local_kv_bytes=%d chain_rebuildable_unreferenced_outputs=%d chain_rebuildable_unreferenced_output_kv_bytes=%d canonicalized_proof_receipt_rows=%d canonicalized_value_byte_delta=%+d uncategorized_rows=%d uncategorized_kv_bytes=%d record_bytes_exclude_codec_framing runtime_caches=not_in_snapshot_schema embedded_ancestor_repeat_bytes=not_separately_exposed_by_consignment_codec",
		role, hop, size.requiredRows, size.requiredKVBytes,
		size.proofRows, size.receiptRows, size.objectRows, size.uniqueObjects,
		size.duplicateObjectRows, size.objectBytes, size.uniqueObjectBytes,
		size.duplicateContentDistinctKeys, size.outputRows,
		size.terminalTransferRows, size.terminalTransferKVBytes,
		size.terminalPendingRows, size.terminalPendingKVBytes,
		size.terminalPendingSignedPSBTBytes, size.activePendingSignedPSBTBytes,
		size.terminalUsedRequestRows, size.terminalUsedRequestKVBytes,
		size.activeUsedRequestRows, size.activeUsedRequestKVBytes,
		size.unknownUsedRequestRows, size.unknownUsedRequestKVBytes,
		size.terminalReceiveMaterialRows, size.terminalReceiveMaterialKVBytes,
		size.activeLifecycleRows, size.activeLifecycleKVBytes,
		size.unknownLifecycleRows, size.unknownLifecycleKVBytes,
		size.unreferencedLocalRows, size.unreferencedLocalKVBytes,
		size.unreferencedOutputRows, size.unreferencedOutputKVBytes,
		size.canonicalizedProofReceiptRows, size.canonicalizedValueByteDelta,
		size.uncategorizedRows, size.uncategorizedKVBytes)
}

// coreRecoveryPackageDeflateSize counts only the raw-DEFLATE member. The RPK
// wire representation has a fixed four-byte magic and one-byte version before
// that member; report those bytes separately through RPK_encoded.
func coreRecoveryPackageDeflateSize(t *testing.T, encoded []byte) int {
	t.Helper()
	const (
		magic        = "R11K"
		codecVersion = byte(1)
		headerBytes  = len(magic) + 1
	)
	if len(encoded) <= headerBytes || string(encoded[:len(magic)]) != magic || encoded[len(magic)] != codecVersion {
		t.Fatal("core-e2e: malformed encoded recovery package during deflate-size measurement")
	}
	return len(encoded) - headerBytes
}

func coreRecoveryPackageRawSize(t *testing.T, encoded []byte) int {
	t.Helper()
	const (
		magic        = "R11K"
		codecVersion = byte(1)
		maxRawSize   = 4 * 1024 * 1024
	)
	if len(encoded) <= len(magic)+1 || string(encoded[:len(magic)]) != magic || encoded[len(magic)] != codecVersion {
		t.Fatal("core-e2e: malformed encoded recovery package during size measurement")
	}
	reader := flate.NewReader(bytes.NewReader(encoded[len(magic)+1:]))
	raw, err := io.ReadAll(io.LimitReader(reader, maxRawSize+1))
	closeErr := reader.Close()
	if err != nil || closeErr != nil || len(raw) == 0 || len(raw) > maxRawSize {
		zeroBytes(raw)
		t.Fatalf("core-e2e: invalid raw recovery package size: bytes=%d limit=%d read_error=%v close_error=%v",
			len(raw), maxRawSize, err, closeErr)
	}
	size := len(raw)
	zeroBytes(raw)
	return size
}

func coreReorgControlledTransaction(t *testing.T, chain *coreE2EChain, txid string) {
	t.Helper()
	if chain == nil || txid == "" {
		t.Fatal("core-e2e: missing controlled transaction for reorg")
	}
	chain.mu.Lock()
	defer chain.mu.Unlock()
	raw := chain.raw[txid]
	if len(raw) == 0 {
		t.Fatalf("core-e2e: controlled transaction %s is not present", txid)
	}
	tx := wire.NewMsgTx(2)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		t.Fatalf("core-e2e: decode controlled transaction for reorg: %v", err)
	}
	for _, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		if chain.spent[outpoint] == txid {
			delete(chain.spent, outpoint)
		}
	}
	for index := range tx.TxOut {
		delete(chain.outputs, fmt.Sprintf("%s:%d", txid, index))
	}
	delete(chain.raw, txid)
	chain.status[txid] = rgb11wallet.BitcoinTxStatus{TxID: txid}
}

func coreRGB11BalanceEquals(balance *Decimal, want uint64) bool {
	if want == 0 {
		return coreRGB11BalanceIsZero(balance)
	}
	return balance != nil && balance.Value != nil && balance.Value.Sign() >= 0 && balance.Value.IsUint64() && balance.Value.Uint64() == want
}

func coreAssertRGB11Balances(t *testing.T, source, destination *Manager, assetName indexer.AssetName, wantSource, wantDestination uint64) {
	t.Helper()
	sourceBalance, err := source.GetRGB11AssetBalance(&assetName)
	coreRequire(t, "read RGB11 source balance", err)
	destinationBalance, err := destination.GetRGB11AssetBalance(&assetName)
	coreRequire(t, "read RGB11 recipient balance", err)
	coreAssert(t, coreRGB11BalanceEquals(sourceBalance, wantSource) && coreRGB11BalanceEquals(destinationBalance, wantDestination), "RGB11 transfer balances do not match the expected allocation")
}
