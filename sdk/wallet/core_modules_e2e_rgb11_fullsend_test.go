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

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/account"
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

	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EF", Name: "E2E full-send recovery asset", Amounts: []uint64{777},
	})
	coreRequire(t, "issue full-send recovery NIA", err)
	coreCheckpoint(t, "issued_sender", sender, cfg, chain, senderMaterial)

	// Keep an independent database on the pre-send account revision. Its next
	// operation must adopt the account-selected pending phase before it may
	// reserve the same input.
	staleDevice := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, staleDevice)
	coreVerifyRGB11DirectProviderRoundTrip(t, sender, staleDevice, issued.AssetName)
	initialBalance, err := staleDevice.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read pre-send amount on independent device", err)
	coreAssert(t, initialBalance != nil && initialBalance.Value.Uint64() == 777,
		"independent device did not restore the full pre-send allocation")

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
				_, _, conflictErr := staleDevice.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
					ReceiverAddress: receiver.GetWallet().GetAddress(), AssetName: issued.AssetName,
					AmountRaw: "1", FeeRate: 2, MinConfirmations: 1,
				}, dkvsindexer.RecordVerificationOptions{})
				coreAssert(t, conflictErr != nil,
					"a second device prepared a competing spend against the account-selected pending full send")
				coreAssert(t, chain.broadcastCount() == beforeBroadcast,
					"competing preparation broadcast a transaction")
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

	// Both live accounts refresh after the controlled reorg: the former receiver
	// regains its input, while the former sender loses the now-orphaned recipient
	// output. The cold receiver then spends to that separate sender account.
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
	delivery, err := source.DeliverAndBroadcastConfiguredRGB11AddressTransfer(
		prepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver full-send consignment", err)
	coreAssert(t, delivery.AwaitingACK && !delivery.Broadcast,
		"full-send crossed the receiver ACK boundary")
	coreWaitRGB11MailboxACK(t, chain, destination, prepared, beforeBroadcast)
	coreAssertRGB11ACKRecoveryPublished(t, destination, prepared)
	coreAssert(t, chain.broadcastCount() == beforeBroadcast,
		"passive receiver ACK caused an early sender broadcast")
	coreWaitRGB11MailboxBroadcast(t, chain, source, prepared, beforeBroadcast)

	chain.confirm(prepared.TxID)
	_, err = source.RefreshRGB11State(context.Background())
	coreRequire(t, "settle full-send source", err)
	_, err = destination.RefreshRGB11State(context.Background())
	coreRequire(t, "settle full-send destination", err)
	coreAssertRGB11SettledRecoveryPublished(t, destination, prepared)
	coreAssertRGB11Balances(t, source, destination, assetName, wantSource, wantDestination)
	return prepared
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
		for _, transfer := range state.Transfers {
			if transfer != nil && transfer.Direction == "send" &&
				transfer.TransferID == prepared.State.TransferID && transfer.WitnessTxID == prepared.TxID &&
				transfer.ConsignmentHash == prepared.State.ConsignmentHash && transfer.Status == "broadcast" {
				matches++
			}
		}
		coreAssert(t, matches <= 1 && chain.broadcastCount() <= beforeBroadcast+1,
			"target ACK caused duplicate lifecycle or transaction broadcasts")
		if matches == 1 {
			coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
				"target ACK did not broadcast exactly once")
			return
		}
		coreAssert(t, time.Now().Before(deadline), "target ACK did not produce its broadcast lifecycle")
		time.Sleep(100 * time.Millisecond)
	}
}

func coreAssertRGB11ACKRecoveryPublished(t *testing.T, receiver *Manager,
	prepared *RGB11PreparedTransfer) {
	t.Helper()
	if receiver == nil || prepared == nil || prepared.State == nil ||
		prepared.TxID == "" || prepared.State.TransferID == "" || prepared.State.ConsignmentHash == "" {
		t.Fatal("core-e2e: missing accepted transfer identity for pre-broadcast recovery assertion")
	}
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
		foundTransfer, foundObject, foundPreparedReceive, foundRequest := false, false, false, false
		foundCurrentReceipt := false
		receiveRequestRecords, decodedReceiveRequests, matchingReceiveRequests := 0, 0, 0
		receiveRequestDecodeFailures := 0
		foundReceiveKey, foundReceiveReservation := false, false
		var stableRequest *corewallet.ReceiveRequest
		for _, record := range packageValue.EngineRecords {
			if record.Key != "wallet/receive/"+staged.ReceiveRequestID {
				continue
			}
			stableRequest, err = corewallet.DecodeReceiveRequest(record.Value)
			coreRequire(t, "decode exact receiver request for stable path checks", err)
			break
		}
		for _, record := range packageValue.ProjectionRecords {
			switch record.Key {
			case "transfer-" + staged.TransferID:
				foundTransfer = true
			case "object-" + wantHash:
				objectHash := sha256.Sum256(record.Value)
				foundObject = hex.EncodeToString(objectHash[:]) == wantHash
			case "validation-" + wantHash:
				foundCurrentReceipt = true
			case "prepared-receive-" + staged.TransferID:
				foundPreparedReceive = string(record.Value) == staged.ReceiveRequestID
			}
			if strings.HasPrefix(record.Key, "receive-key-") {
				if stableRequest != nil && len(stableRequest.WitnessScript) != 0 {
					witnessHash := sha256.Sum256(stableRequest.WitnessScript)
					if record.Key == "receive-key-"+hex.EncodeToString(witnessHash[:]) {
						foundReceiveKey = true
					}
				}
			}
			if record.Key == "receive-reservation-"+staged.ReceiveRequestID {
				foundReceiveReservation = true
			}
		}
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
			if request.RequestID == staged.ReceiveRequestID && request.Status == corewallet.ReceiveAcknowledged &&
				request.TransferID == staged.TransferID && request.ObjectHash == staged.ConsignmentHash &&
				request.WitnessTxID == staged.WitnessTxID && request.Invoice != "" &&
				request.Mode == corewallet.ReceiveWitness && len(request.WitnessScript) != 0 {
				matchingReceiveRequests++
				foundRequest = true
			}
		}
		if !foundTransfer && !foundObject && !foundPreparedReceive &&
			receiveRequestRecords == 0 && !foundCurrentReceipt {
			continue
		}
		relevantPackages++
		if !foundTransfer || !foundObject || !foundPreparedReceive || !foundRequest || foundCurrentReceipt ||
			receiveRequestRecords != 1 || receiveRequestDecodeFailures != 0 {
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
		if err != nil || recoveredTransfer.Direction != "receive" ||
			recoveredTransfer.Status != "awaiting_broadcast" ||
			recoveredTransfer.AckStatus != wantAck || recoveredTransfer.DeliveryAcknowledged != wantAcknowledged ||
			recoveredTransfer.ConsignmentHash != wantHash ||
			recoveredTransfer.ReceiveRequestID != staged.ReceiveRequestID ||
			recoveredTransfer.WitnessTxID != staged.WitnessTxID {
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
		if err != nil || recoveredRequest.Status != corewallet.ReceiveAcknowledged ||
			recoveredRequest.TransferID != staged.TransferID ||
			recoveredRequest.ObjectHash != wantHash ||
			recoveredRequest.WitnessTxID != staged.WitnessTxID ||
			recoveredRequest.Invoice == "" || len(recoveredRequest.WitnessScript) == 0 {
			t.Fatalf("stable ACK package did not cold-restore its receive request and witness path: err=%v", err)
		}
		if foundReceiveKey {
			key, err := projection.LoadReceiveKey(recoveredRequest.WitnessScript)
			if err != nil || key.RequestID != staged.ReceiveRequestID ||
				!bytes.Equal(key.WitnessScript, recoveredRequest.WitnessScript) {
				t.Fatalf("stable ACK package did not cold-restore its receiver key path: err=%v", err)
			}
		}
		if foundReceiveReservation {
			reservation, err := projection.LoadReceiveReservation(staged.ReceiveRequestID)
			if err != nil || reservation.RequestID != staged.ReceiveRequestID ||
				reservation.OutPoint == "" || reservation.ReservationID == "" {
				t.Fatalf("stable ACK package did not cold-restore its receive reservation: err=%v", err)
			}
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
	for _, payload := range payloads {
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
		coreAssert(t, foundProof && foundObject && foundReceipt && foundTransfer,
			"settled stable package omitted proof, consumer, validation receipt, or transfer lifecycle")
		return
	}
	t.Fatal("core-e2e: no stable RGB11 package available after settling receiver proof")
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
	revision := manager.accountProfile.ManagedDataRevision
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	bundle, _, err := manager.exportAccountManagedData(catalog, revision)
	coreRequire(t, "export complete account bundle for size sample", err)
	plain, err := account.EncodeManagedDataBundle(bundle)
	coreRequire(t, "encode account bundle for size sample", err)
	defer zeroBytes(plain)
	size.amd = len(plain)
	envelope, info, err := account.SealManagedDataBundleWithInfo(secret, accountID, bundle, nil)
	coreRequire(t, "seal account bundle for size sample", err)
	defer zeroBytes(envelope)
	size.ade = len(envelope)
	blob, err := EncodeDKVSBlobValue(envelope, nil)
	coreRequire(t, "wrap account envelope for size sample", err)
	defer zeroBytes(blob)
	size.dkb1 = len(blob)
	previousSize := previous[role]
	previous[role] = size
	t.Logf("core-e2e: RGB11 recovery-size role=%s hop=%d packages=%d RPK_raw=%d delta=%+d RPK_deflate_stream=%d delta=%+d RPK_encoded=%d delta=%+d AMD=%d delta=%+d ADE=%d delta=%+d DKB1=%d delta=%+d account_bundle_compressed=%t",
		role, hop, size.packages, size.rpkRaw, size.rpkRaw-previousSize.rpkRaw,
		size.rpkDeflate, size.rpkDeflate-previousSize.rpkDeflate,
		size.rpkEncoded, size.rpkEncoded-previousSize.rpkEncoded,
		size.amd, size.amd-previousSize.amd, size.ade, size.ade-previousSize.ade,
		size.dkb1, size.dkb1-previousSize.dkb1, info.Compressed)
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
