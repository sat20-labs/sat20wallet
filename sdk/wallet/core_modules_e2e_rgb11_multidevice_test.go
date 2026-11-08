package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// coreRGB11SameRootThreeDeviceE2E switches between independent paid-account
// devices only after the previous device's real backup has completed.
func coreRGB11SameRootThreeDeviceE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	seed *Manager, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	ctx := context.Background()
	coreTransferDeviceCleanup(t, seed, receiver)
	coreRGB11RetireDevice(t, seed)
	deviceA := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreRGB11UseDevice(t, deviceA)
	issued, err := deviceA.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EM", Name: "E2E same-root three-device asset", Amounts: []uint64{1000},
	})
	coreRequire(t, "issue same-root multi-device NIA", err)
	coreCheckpoint(t, "same_root_issue_authoritative", deviceA, cfg, chain, senderMaterial)

	exported, err := deviceA.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export same-root multi-device contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode same-root multi-device contract", err)
	_, err = receiver.ImportRGB11ContractFile(ctx, contract)
	coreRequire(t, "import standard contract on OOB receiver", err)
	endpoint, err := receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish same-root multi-device Direct endpoint", err)
	coreAssert(t, endpoint != nil && !endpoint.Temporary, "same-root test needs a paid Direct endpoint")

	coreRGB11RetireDevice(t, deviceA)
	deviceB := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreRGB11UseDevice(t, deviceB)
	coreRGB11RetireDevice(t, deviceB)
	deviceC := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreRGB11UseDevice(t, deviceC)
	coreRGB11RetireDevice(t, deviceC)
	seedRoot, err := seed.RootAccountID()
	coreRequire(t, "read seed same-root identity", err)
	for index, device := range []*Manager{deviceA, deviceB, deviceC} {
		root, err := device.RootAccountID()
		coreRequire(t, "read restored same-root identity", err)
		coreAssert(t, root == seedRoot, "device restored a different managed account root")
		for previous := 0; previous < index; previous++ {
			coreAssert(t, device.db != []*Manager{deviceA, deviceB, deviceC}[previous].db,
				"same-root devices share a local database")
		}
		balance, err := device.GetRGB11AssetBalance(&issued.AssetName)
		coreRequire(t, "read same-root initial balance", err)
		coreAssert(t, balance != nil && balance.Value.Uint64() == 1000,
			"same-root device did not recover the complete issued allocation")
	}
	coreRGB11UseDevice(t, deviceA)
	beforeTransfers := chain.broadcastCount()

	firstExpiry := time.Now().Add(30 * time.Second).Unix()
	firstPrepared, _, err := deviceA.PrepareConfiguredRGB11AddressTransfer(ctx, RGB11AddressSendRequest{
		ReceiverAddress: receiver.GetWallet().GetAddress(), AssetName: issued.AssetName,
		AmountRaw: "1", FeeRate: 2, MinConfirmations: 1, Expiry: firstExpiry,
	}, dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare first shared-input operation", err)
	firstPending := cloneRGB11BroadcastIntent(assertRGB11UnsignedPending(t, deviceA.rgbManager, firstPrepared.State.TransferID))
	coreAssert(t, overlapsRGB11Outpoints(issued.OutPoints, firstPending.State.InputOutPoints),
		"prepared transfer did not reserve the actual issued RGB carrier")
	coreAssert(t, chain.broadcastCount() == beforeTransfers, "prepare broadcast before delivery/ACK")
	coreAssertRGB11Reservation(t, deviceA, firstPrepared.State.TransferID, true)
	coreRGB11RetireDevice(t, deviceA)

	competitorInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create independent competing invoice", err)
	for _, device := range []*Manager{deviceB, deviceC} {
		coreRGB11UseDevice(t, device)
		coreAssertRGB11LockedPrepare(t, device, firstPending, competitorInvoice.Invoice)
		coreRGB11RetireDevice(t, device)
	}
	coreAssert(t, chain.broadcastCount() == beforeTransfers, "locked-input preparation broadcast a transaction")

	// B really imports A's confirmed preparation before expiry cancellation.
	coreRGB11UseDevice(t, deviceB)
	if delay := time.Until(time.Unix(firstExpiry, 0)); delay > 0 {
		time.Sleep(delay)
	}
	logExpiryCandidate := func(phase string, pending *rgb11wallet.PendingTransfer) {
		if pending == nil {
			return
		}
		now := time.Now().Unix()
		t.Logf("core-e2e: expiry cancel candidate: phase=%s actor=%d transfer_tag=%s now=%d requested_expiry=%d state_expiry=%d relay_expiry=%d requested_elapsed=%t state_elapsed=%t original_expiry_equal=%t direction=%q status=%q address_mode=%t transport=%q ack=%q durability=%q invoice_empty=%t synthetic_invoice_removed=%t",
			phase, deviceB.debugID, coreCASTag(pending.State.TransferID), now, firstExpiry, pending.State.Expiry,
			pending.State.RelayExpiry, now >= firstExpiry, now >= pending.State.Expiry,
			pending.State.Expiry == firstPending.State.Expiry, pending.State.Direction, pending.State.Status,
			pending.State.AddressMode, pending.State.TransportMode, pending.State.AckStatus, pending.State.RelayDurability,
			pending.State.Invoice == "", pending.State.SyntheticInvoiceRemoved)
	}
	cancelCandidate := assertRGB11UnsignedPending(t, deviceB.rgbManager, firstPrepared.State.TransferID)
	logExpiryCandidate("before_cancel", cancelCandidate)
	expiryCancelErr := deviceB.CancelExpiredRGB11Transfer(firstPrepared.State.TransferID)
	if expiryCancelErr != nil {
		refused, loadErr := deviceB.rgbManager.projectionStore.LoadPendingTransfer(firstPrepared.State.TransferID)
		t.Logf("core-e2e: expiry cancel refusal: actor=%d error_type=%T expired_cancel=%t inconsistent=%t load_error_type=%T",
			deviceB.debugID, expiryCancelErr, errors.Is(expiryCancelErr, ErrRGB11ExpiredCancel),
			errors.Is(expiryCancelErr, ErrRGB11Inconsistent), loadErr)
		if loadErr == nil {
			logExpiryCandidate("after_refusal", refused)
		}
	}
	coreRequire(t, "second device cancels before broadcast", expiryCancelErr)
	cancelled, err := deviceB.rgbManager.projectionStore.LoadPendingTransfer(firstPrepared.State.TransferID)
	coreRequire(t, "read actual local expiry cancellation", err)
	coreAssert(t, cancelled.State.Status == "rejected" && cancelled.State.AckStatus == "rejected" &&
		cancelled.State.RejectReason == rgb11RejectReasonInvoiceExpired,
		"local expiry cancellation lost its actual reason")
	coreAssertRGB11Reservation(t, deviceB, firstPrepared.State.TransferID, false, firstPending)
	coreAssertRGB11Balances(t, deviceB, receiver, issued.AssetName, 1000, 0)
	coreRGB11RetireDevice(t, deviceB)
	selectedCancel := coreRGB11SelectedTransfer(t, deviceB, cfg, firstPrepared.State.TransferID)
	coreAssert(t, selectedCancel.Status == "rejected" && selectedCancel.WitnessTxID == firstPrepared.TxID &&
		equalRGB11StringSet(selectedCancel.InputOutPoints, firstPending.State.InputOutPoints),
		"confirmed cancellation changed the original transaction/input identity")
	for _, device := range []*Manager{deviceA, deviceC} {
		coreRGB11UseDevice(t, device)
		canceled, loadErr := device.rgbManager.projectionStore.LoadPendingTransfer(firstPrepared.State.TransferID)
		coreRequire(t, "read imported terminal preparation", loadErr)
		coreAssert(t, coreDigest(canceled.State) == coreDigest(*selectedCancel) &&
			len(canceled.SignedTx) == 0 && len(canceled.SignedPSBT) == 0 &&
			len(canceled.RecipientConsignment) == 0 && len(canceled.LocalConsignment) == 0,
			"returning device changed the confirmed cancellation or revived private material")
		if device == deviceA {
			_, broadcastErr := device.BroadcastRGB11AddressTransfer(firstPrepared.State.TransferID)
			coreAssert(t, errors.Is(broadcastErr, ErrRGB11AddressDeliveryRequired),
				"returning computer broadcast the canceled preparation")
		} else {
			staleDelivery, deliveryErr := device.DeliverAndBroadcastConfiguredRGB11AddressTransfer(firstPrepared.State.TransferID, RGB11AddressDeliveryOptions{})
			coreRequire(t, "resume canceled preparation without side effects", deliveryErr)
			coreAssert(t, staleDelivery != nil && !staleDelivery.Broadcast && staleDelivery.TxID == "" &&
				staleDelivery.RecordKey == "" && staleDelivery.RecordHash == "" && staleDelivery.ObjectID == "",
				"returning phone delivered or broadcast a canceled preparation")
		}
		coreAssertRGB11Reservation(t, device, firstPrepared.State.TransferID, false, firstPending)
		coreRGB11RetireDevice(t, device)
	}
	noDelivery, err := receiver.SyncConfiguredRGB11AddressMailbox(ctx, dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "check receiver mailbox after canceled delivery attempt", err)
	coreAssert(t, noDelivery != nil && noDelivery.Received == 0 && noDelivery.ACKs == 0 && noDelivery.Invalid == 0,
		"canceled preparation reached the receiver mailbox")
	coreAssert(t, chain.broadcastCount() == beforeTransfers, "canceled operation reached the Bitcoin backend")

	// Keep the recipient offline until A's delivered intent is backed up and A
	// has retired. Its later ACK can then be consumed only by the selected B.
	coreRGB11RetireDevice(t, receiver)
	coreRGB11UseDevice(t, deviceA)
	directPrepared, _, err := deviceA.PrepareConfiguredRGB11AddressTransfer(ctx,
		RGB11AddressSendRequest{ReceiverAddress: receiver.GetWallet().GetAddress(),
			AssetName: issued.AssetName, AmountRaw: "400", FeeRate: 2, MinConfirmations: 1},
		dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare direct transfer after cross-device cancellation", err)
	coreAssert(t, overlapsRGB11Outpoints(firstPrepared.State.InputOutPoints, directPrepared.State.InputOutPoints) &&
		overlapsRGB11Outpoints(issued.OutPoints, directPrepared.State.InputOutPoints),
		"cancel did not free the actual issued carrier for the new intent")
	directBaseline := cloneRGB11BroadcastIntent(assertRGB11UnsignedPending(t, deviceA.rgbManager, directPrepared.State.TransferID))
	delivered, err := deviceA.DeliverAndBroadcastConfiguredRGB11AddressTransfer(directPrepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver direct consignment before ACK", err)
	coreAssert(t, delivered.AwaitingACK && !delivered.Broadcast && chain.broadcastCount() == beforeTransfers,
		"direct delivery crossed its positive-ACK barrier")
	coreRGB11RetireDevice(t, deviceA)
	coreRGB11UseDevice(t, receiver)
	coreWaitRGB11MailboxACK(t, chain, receiver, directPrepared, beforeTransfers)
	coreAssertRGB11ACKRecoveryPublished(t, receiver, directPrepared)

	// Observe B's real signed raw at the backend without pausing the call or
	// allowing another same-root device to write during that network request.
	signedReceipt := make(chan *rgb11wallet.PendingTransfer, 1)
	var receiptOnce sync.Once
	previousBEvidence := deviceB.rgbManager.evidence
	deviceB.rgbManager.evidence = &coreE2EObservedBroadcast{
		BitcoinEvidenceProvider: previousBEvidence,
		inspect: func(raw []byte) error {
			pending, loadErr := deviceB.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
			if loadErr != nil {
				return loadErr
			}
			if pending.State.Status != rgb11StatusBroadcastAttempted ||
				!validRGB11SigningTransition(directBaseline, pending) || !bytes.Equal(raw, pending.SignedTx) {
				return errors.New("backend did not receive the exact valid signed preparation")
			}
			receiptOnce.Do(func() { signedReceipt <- cloneRGB11BroadcastIntent(pending) })
			return nil
		},
	}
	t.Cleanup(func() {
		deviceB.StopDKVSSync()
		deviceB.rgbManager.evidence = previousBEvidence
	})
	coreRGB11UseDevice(t, deviceB)
	coreWaitRGB11MailboxBroadcast(t, chain, deviceB, directPrepared, beforeTransfers)
	var signedBaseline *rgb11wallet.PendingTransfer
	select {
	case signedBaseline = <-signedReceipt:
	default:
		t.Fatal("core-e2e: real B backend broadcast has no valid signed-intent receipt")
	}
	coreRGB11RetireDevice(t, deviceB)
	deviceB.rgbManager.evidence = previousBEvidence

	// A selected device refreshes chain facts before use. The same successfully
	// broadcast intent is pending while its original witness is in the mempool.
	assertDirectIntent := func(label string, device *Manager, pending *rgb11wallet.PendingTransfer) {
		t.Helper()
		coreAssert(t, pending != nil, "selected Direct device lost its signed intent")
		status, statusErr := chain.GetTxStatus(directPrepared.TxID)
		raw, rawErr := chain.GetRawTx(directPrepared.TxID)
		intentEqual := sameRGB11BroadcastIntent(signedBaseline, pending)
		stateValid := pending.State.Status == "broadcast" || pending.State.Status == "pending"
		mempool := statusErr == nil && status != nil && status.TxID == directPrepared.TxID &&
			status.InMempool && !status.Confirmed && status.Confirmations == 0
		rawEqual := rawErr == nil && bytes.Equal(raw, signedBaseline.SignedTx) && bytes.Equal(raw, pending.SignedTx)
		t.Logf("core-e2e: selected Direct intent: phase=%s actor=%d transfer_tag=%s baseline_status=%q status=%q ack=%q acknowledged=%t intent_equal=%t raw_equal=%t has_witness=%t mempool=%t broadcasts=%d",
			label, device.debugID, coreCASTag(pending.State.TransferID), signedBaseline.State.Status, pending.State.Status,
			pending.State.AckStatus, pending.State.DeliveryAcknowledged, intentEqual, rawEqual,
			rgb11PendingHasWitness(pending), mempool, chain.broadcastCount()-beforeTransfers)
		if !intentEqual || !stateValid {
			for _, pair := range [][2]reflect.Value{
				{reflect.ValueOf(signedBaseline.State), reflect.ValueOf(pending.State)},
				{reflect.ValueOf(*signedBaseline), reflect.ValueOf(*pending)},
			} {
				for i := 0; i < pair[0].NumField(); i++ {
					if !reflect.DeepEqual(pair[0].Field(i).Interface(), pair[1].Field(i).Interface()) {
						t.Logf("core-e2e: selected Direct difference: phase=%s actor=%d type=%s field=%s equal=false",
							label, device.debugID, pair[0].Type().Name(), pair[0].Type().Field(i).Name)
					}
				}
			}
		}
		coreRequire(t, "read original Direct witness status", statusErr)
		coreRequire(t, "read original Direct signed raw", rawErr)
		coreAssert(t, mempool && stateValid, "selected Direct status disagrees with its original unconfirmed witness")
		coreAssert(t, intentEqual && rawEqual && rgb11PendingHasWitness(pending),
			"selected Direct device changed the original immutable signed intent")
		coreAssert(t, pending.State.AckStatus == "accepted" && pending.State.DeliveryAcknowledged,
			"selected Direct device lost its positive recipient ACK")
		coreAssert(t, chain.broadcastCount() == beforeTransfers+1, "Direct device switch duplicated its original broadcast")
		coreAssertRGB11Reservation(t, device, directPrepared.State.TransferID, true)
	}

	// Metadata and rejected cancellation now occur after the completed network
	// call, on C's real selected copy of the same signed transaction.
	coreRGB11UseDevice(t, deviceC)
	const rebasedWalletName = "RGB11 same-root device switch E2E"
	coreRequire(t, "rename wallet on the next selected device",
		deviceC.UpdateWalletName(deviceC.GetAccountManagementStatus().RootWalletID, rebasedWalletName))
	coreAssertRGB11LockedPrepare(t, deviceC, signedBaseline, competitorInvoice.Invoice)
	cancelErr := deviceC.CancelExpiredRGB11Transfer(directPrepared.State.TransferID)
	coreAssert(t, errors.Is(cancelErr, ErrRGB11ExpiredCancel), "third device canceled a positively ACKed broadcast")
	afterCancel, err := deviceC.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
	coreRequire(t, "read signed intent after rejected cancellation", err)
	assertDirectIntent("C_after_metadata_cancel", deviceC, afterCancel)
	coreRGB11RetireDevice(t, deviceC)

	coreRGB11UseDevice(t, deviceA)
	txid, err := deviceA.BroadcastRGB11AddressTransfer(directPrepared.State.TransferID)
	coreRequire(t, "returning computer retries the original direct transaction", err)
	coreAssert(t, txid == directPrepared.TxID && chain.broadcastCount() == beforeTransfers+1,
		"idempotent retry changed the transaction or broadcast it twice")
	coreAssert(t, coreWalletName(deviceA, deviceA.GetAccountManagementStatus().RootWalletID) == rebasedWalletName,
		"returning computer did not adopt the confirmed metadata edit")
	currentIntent, err := deviceA.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
	coreRequire(t, "read sender intent after device switch", err)
	assertDirectIntent("A_after_idempotent_retry", deviceA, currentIntent)
	coreRGB11RetireDevice(t, deviceA)
	coreRGB11UseDevice(t, deviceB)
	coreAssert(t, errors.Is(deviceB.CancelExpiredRGB11Transfer(directPrepared.State.TransferID), ErrRGB11ExpiredCancel),
		"second device canceled a broadcast direct transaction")
	blockedDirect, err := deviceB.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
	coreRequire(t, "read direct intent after rejected cancellation", err)
	assertDirectIntent("B_after_rejected_cancel", deviceB, blockedDirect)
	coreRGB11RetireDevice(t, deviceB)
	coreRGB11UseDevice(t, deviceA)

	chain.confirm(txid)
	_, err = deviceA.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct transfer before next spend", err)
	_, err = receiver.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct receipt before next receive", err)
	coreAssertRGB11Balances(t, deviceA, receiver, issued.AssetName, 600, 400)
	coreAssert(t, chain.broadcastCount() == beforeTransfers+1, "direct settlement changed the effective broadcast count")

	// A completed unknown response is handed off with its exact signed intent.
	// No other same-root device is active inside the network call itself.
	secondInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create invoice for broadcast-wins ordering", err)
	secondPrepared, err := deviceA.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: secondInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare broadcast-wins OOB transfer", err)
	assertRGB11UnsignedPending(t, deviceA.rgbManager, secondPrepared.State.TransferID)
	_, err = receiver.PrepareRGB11Consignment(ctx, secondInvoice.RequestID, []byte(secondPrepared.RecipientConsignment))
	coreRequire(t, "receiver stages OOB proof before broadcast", err)
	beforeOOBBroadcast := chain.broadcastCount()
	realEvidence := deviceA.rgbManager.evidence
	deviceA.rgbManager.evidence = &coreE2EUnknownBroadcastEvidence{BitcoinEvidenceProvider: realEvidence}
	unknownTxID, unknownErr := deviceA.BroadcastRGB11OutOfBand([]string{secondPrepared.State.TransferID})
	deviceA.rgbManager.evidence = realEvidence
	coreAssert(t, errors.Is(unknownErr, ErrRGB11BroadcastResultUnknown) && errors.Is(unknownErr, context.DeadlineExceeded) &&
		unknownTxID == secondPrepared.TxID && chain.broadcastCount() == beforeOOBBroadcast,
		"unknown OOB response lost the txid, broadcast early, or changed error category")
	unknownPending, err := deviceA.rgbManager.projectionStore.LoadPendingTransfer(secondPrepared.State.TransferID)
	coreRequire(t, "read exact OOB intent after unknown response", err)
	unknownPending = cloneRGB11BroadcastIntent(unknownPending)
	coreAssert(t, unknownPending.State.Status == rgb11StatusBroadcastAttempted && rgb11PendingHasWitness(unknownPending) &&
		rgb11TransferKeepsInputsLocked(&unknownPending.State), "unknown OOB broadcast lost its signed intent or lock")
	coreRGB11RetireDevice(t, deviceA)
	coreRGB11UseDevice(t, deviceC)
	importedUnknown, err := deviceC.rgbManager.projectionStore.LoadPendingTransfer(secondPrepared.State.TransferID)
	coreRequire(t, "read third device's imported unknown intent", err)
	coreAssert(t, sameRGB11BroadcastIntent(unknownPending, importedUnknown), "handoff changed the unknown signed transaction")
	coreAssert(t, errors.Is(deviceC.CancelRGB11OutOfBandTransfer(secondPrepared.State.TransferID), ErrRGB11OutOfBandRequired),
		"third device canceled an OOB transaction after broadcast became uncertain")
	afterUnknownCancel, err := deviceC.rgbManager.projectionStore.LoadPendingTransfer(secondPrepared.State.TransferID)
	coreRequire(t, "read unknown intent after rejected cancellation", err)
	coreAssert(t, sameRGB11BroadcastIntent(unknownPending, afterUnknownCancel) &&
		afterUnknownCancel.State.Status == rgb11StatusBroadcastAttempted, "cancellation changed the unknown original intent")
	coreAssertRGB11Reservation(t, deviceC, secondPrepared.State.TransferID, true)
	coreRGB11RetireDevice(t, deviceC)
	coreRGB11UseDevice(t, deviceA)
	secondTxID, err := deviceA.BroadcastRGB11OutOfBand([]string{secondPrepared.State.TransferID})
	coreRequire(t, "retry the original OOB transaction", err)
	coreAssert(t, secondTxID == secondPrepared.TxID && chain.broadcastCount() == beforeOOBBroadcast+1,
		"unknown OOB retry changed or duplicated the transaction")
	retriedPending, err := deviceA.rgbManager.projectionStore.LoadPendingTransfer(secondPrepared.State.TransferID)
	coreRequire(t, "read retried OOB signed intent", err)
	coreAssert(t, sameRGB11BroadcastIntent(unknownPending, retriedPending), "retry rebuilt the unknown signed transaction")
	_, err = receiver.AcceptRGB11Consignment(ctx, secondInvoice.RequestID, []byte(secondPrepared.RecipientConsignment))
	coreRequire(t, "receiver accepts broadcast OOB consignment", err)

	chain.confirm(secondTxID)
	_, err = deviceA.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct and OOB transfer on computer", err)
	_, err = receiver.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct and OOB receipts", err)
	coreRGB11RetireDevice(t, deviceA)
	coreRGB11UseDevice(t, deviceC)
	remaining, err := deviceC.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read third-device balance after settlement", err)
	coreAssert(t, coreRGB11BalanceEquals(remaining, 599), "third device did not recover exactly 599")
	coreRGB11RetireDevice(t, receiver)
	coldReceiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreRGB11UseDevice(t, coldReceiver)
	priorReceived, err := coldReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold receiver's prior accepted allocation", err)
	coreAssert(t, coreRGB11BalanceEquals(priorReceived, 401), "cold receiver did not recover exactly 401")

	beforeFinalSend := chain.broadcastCount()
	finalPrepared := coreSendRGB11Amount(t, chain, deviceC, coldReceiver, issued.AssetName, "599", beforeFinalSend, 0, 1000, nil)
	coreAssert(t, finalPrepared != nil && finalPrepared.TxID != "", "cold third device did not create a new signed transaction")
	spent, err := deviceC.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read third-device balance after recovered spend", err)
	received, err := coldReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read recipient after recovered spend", err)
	coreAssert(t, coreRGB11BalanceIsZero(spent) && coreRGB11BalanceEquals(received, 1000),
		"device recovery and re-spend did not preserve asset conservation")
	coreRGB11RetireDevice(t, deviceC)
	coreRGB11RetireDevice(t, coldReceiver)
	coreRGB11UseDevice(t, receiver)
	originalReceiverBalance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read original recipient after cold-device send", err)
	coreAssert(t, originalReceiverBalance != nil && originalReceiverBalance.Value.Uint64() == 1000,
		"cold receiver did not converge to the original recipient device")
	for _, device := range []*Manager{deviceA, deviceB, deviceC} {
		coreRGB11UseDevice(t, device)
		coreAssert(t, coreWalletName(device, device.GetAccountManagementStatus().RootWalletID) == rebasedWalletName,
			"same-root metadata did not converge on the returning device")
		balance, err := device.GetRGB11AssetBalance(&issued.AssetName)
		coreRequire(t, "read returning sender after final spend", err)
		coreAssert(t, coreRGB11BalanceIsZero(balance), "returning sender revived the spent allocation")
		coreRGB11RetireDevice(t, device)
	}
	coreRGB11UseDevice(t, seed)
	currentSeedRoot, err := seed.RootAccountID()
	coreRequire(t, "read seed root after device lifecycle", err)
	coreAssert(t, currentSeedRoot == seedRoot, "seed changed managed root during recovery")
	coreAssert(t, chain.broadcastCount() == beforeTransfers+3, "cancel, sync, recovery or retry caused an extra broadcast")
}

// coreRGB11SameRootCASLoserE2E isolates the real account-CAS loser lifecycle
// from the broader three-device flow. It starts three distinct root-wallet
// databases at one confirmed module record, publishes two locally prepared
// private intents through the real CoreNode CAS together, and checks whether
// the losing device can
// explicitly cancel its never-delivered intent and continue.
func coreRGB11SameRootCASLoserE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	seed, receiver *Manager, senderMaterial *coreRecoveryMaterial) {
	t.Helper()
	ctx := context.Background()
	coreTransferDeviceCleanup(t, seed, receiver)
	coreRGB11RetireDevice(t, seed)
	deviceA := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, deviceA)
	rootID, err := seed.RootAccountID()
	coreRequire(t, "read R09 root wallet identity", err)
	deviceRoot, err := deviceA.RootAccountID()
	coreRequire(t, "read first R09 device root identity", err)
	coreAssert(t, deviceRoot == rootID, "first R09 device restored a different root wallet")
	chain.fund(t, deviceA.GetWallet().GetAddress(), 6)

	// Preserve the aggregate's nonempty history through the real SDK path.
	// This separate asset leaves the R09 competing allocation at exactly 1000.
	historyAsset, err := deviceA.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ERH", Name: "E2E R09 history", Amounts: []uint64{2},
	})
	coreRequire(t, "issue independent R09 historical asset", err)
	historyExport, err := deviceA.ExportRGB11Contract(historyAsset.ContractID)
	coreRequire(t, "export R09 historical contract", err)
	historyContract, err := base64.StdEncoding.DecodeString(historyExport.ContractConsignmentBase64)
	coreRequire(t, "decode R09 historical contract", err)
	_, err = receiver.ImportRGB11ContractFile(ctx, historyContract)
	coreRequire(t, "import R09 historical contract on receiver", err)
	_, err = receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish R09 historical receiver capability", err)
	historyTransfer := coreSendRGB11Amount(t, chain, deviceA, receiver,
		historyAsset.AssetName, "1", chain.broadcastCount(), 1, 1, nil)
	coreAssert(t, historyTransfer != nil && historyTransfer.State != nil,
		"R09 historical SDK transfer omitted its operation identity")
	coreWaitPrivateBackup(t, deviceA)
	coreWaitPrivateBackup(t, receiver)

	issued, err := deviceA.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ER", Name: "E2E real-CAS loser asset", Amounts: []uint64{1000},
	})
	coreRequire(t, "issue isolated R09 root-wallet NIA", err)
	coreCheckpoint(t, "R09_same_root_issue_authoritative", deviceA, cfg, chain, senderMaterial)
	coreRGB11RetireDevice(t, deviceA)

	deviceB := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreRGB11UseDevice(t, deviceB)
	coreRGB11RetireDevice(t, deviceB)
	deviceC := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreRGB11UseDevice(t, deviceC)
	coreRGB11RetireDevice(t, deviceC)
	devices := []*Manager{deviceA, deviceB, deviceC}
	t.Cleanup(func() {
		for _, device := range devices {
			device.StopDKVSSync()
		}
	})
	for index, device := range devices {
		deviceRoot, err := device.RootAccountID()
		coreRequire(t, "read R09 device root identity", err)
		coreAssert(t, deviceRoot == rootID, "R09 device restored a different root wallet")
		for previous := 0; previous < index; previous++ {
			coreAssert(t, device.db != devices[previous].db, "R09 devices share a local database")
		}
		balance, err := device.GetRGB11AssetBalance(&issued.AssetName)
		coreRequire(t, "read R09 same-root recovered allocation", err)
		coreAssert(t, balance != nil && balance.Value.Uint64() == 1000,
			"R09 device did not restore the full allocation before racing")
	}
	exported, err := deviceA.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export R09 standard RGB contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode R09 standard RGB contract", err)
	_, err = receiver.ImportRGB11ContractFile(ctx, contract)
	coreRequire(t, "import R09 contract on independent invoice receiver", err)

	baselineA := coreCaptureManagedProfile(deviceA)
	baselineB := coreCaptureManagedProfile(deviceB)
	baselineC := coreCaptureManagedProfile(deviceC)
	coreAssert(t, baselineA.revision != 0 && !baselineA.dirty && !baselineB.dirty && !baselineC.dirty &&
		baselineB.revision == baselineA.revision && baselineC.revision == baselineA.revision &&
		baselineB.hash == baselineA.hash && baselineC.hash == baselineA.hash,
		"three R09 root devices did not start from one confirmed RGB module record")
	invoices := make([]string, 2)
	for index := range invoices {
		invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
			AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreRequire(t, "create independent R09 out-of-band witness invoice", err)
		invoices[index] = invoice.Invoice
	}

	// Freeze the independently signed module baseline, rather than treating a
	// local business result or wallet catalog sequence as a remote publication.
	scope := coreSelectedRGBScope(deviceA)
	module, err := accountEntry(rootID, rgb11AccountManagedProviderID, scope)
	coreRequire(t, "identify exact R09 RGB module", err)
	baseline := coreReadRGBClosure(t, deviceA, cfg)
	defer coreZeroRGBPayloads(baseline.payloads)
	baseRecord := baseline.records[module.Key]
	coreAssert(t, baseRecord != nil && baseRecord.Seq == baselineA.revision &&
		dkvsindexer.RecordHash(baseRecord).String() == baselineA.hash,
		"R09 devices did not confirm the independently verified module baseline")
	// The signed baseline deduplicates historical consignments into immutable
	// objects; cold devices must still hold the complete inline local material.
	baseRecovery, err := rgb11wallet.DecodeRecoveryPackage(baseline.payloads[scope])
	coreRequire(t, "decode R09 historical module baseline", err)
	basePendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(baseRecovery.ProjectionRecords)
	coreRequire(t, "read R09 historical baseline operations", err)
	var baseHistory *rgb11wallet.PendingTransfer
	for _, pending := range basePendings {
		if pending.State.TransferID == historyTransfer.State.TransferID {
			coreAssert(t, baseHistory == nil, "R09 baseline duplicated its historical operation")
			baseHistory = pending
		}
	}
	coreAssert(t, baseHistory != nil && baseHistory.State.Status == "settled" &&
		baseHistory.State.WitnessTxID == historyTransfer.TxID && baseHistory.State.Asset.Name == historyAsset.AssetName &&
		baseHistory.RecipientObjectHash != "" && baseHistory.LocalObjectHash != "" &&
		len(baseHistory.RecipientConsignment) == 0 && len(baseHistory.LocalConsignment) == 0,
		"R09 baseline omitted its settled historical identity or deduplicated object references")
	var recipientObject, localObject []byte
	for _, record := range baseRecovery.ProjectionRecords {
		if record.Key == "object-"+baseHistory.RecipientObjectHash {
			recipientObject = record.Value
		}
		if record.Key == "object-"+baseHistory.LocalObjectHash {
			localObject = record.Value
		}
	}
	coreAssert(t, len(recipientObject) > 0 && len(localObject) > 0 &&
		rgb11ObjectHash(recipientObject) == baseHistory.RecipientObjectHash &&
		rgb11ObjectHash(localObject) == baseHistory.LocalObjectHash,
		"R09 baseline lost the immutable objects for its historical transfer")
	for _, device := range []*Manager{deviceB, deviceC} {
		coreAssert(t, coreSelectedRGBScope(device) == scope, "R09 historical fixture moved to a different scope")
		walletID, err := device.rgbManager.RGB11WalletID()
		coreRequire(t, "read R09 historical cold wallet identity", err)
		raw, _, err := device.rgbManager.readRGB11StrictSnapshot(walletID, true)
		coreRequire(t, "read R09 historical raw cold snapshot", err)
		pendings, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(raw.ProjectionRecords)
		coreRequire(t, "decode R09 historical raw cold operations", err)
		var historical *rgb11wallet.PendingTransfer
		for _, pending := range pendings {
			if pending.State.TransferID == historyTransfer.State.TransferID {
				coreAssert(t, historical == nil, "R09 cold device duplicated its historical operation")
				historical = pending
			}
		}
		coreAssert(t, historical != nil && historical.State.Status == "settled" &&
			historical.State.WitnessTxID == baseHistory.State.WitnessTxID &&
			historical.State.Asset.Name == historyAsset.AssetName &&
			historical.RecipientObjectHash == baseHistory.RecipientObjectHash &&
			historical.LocalObjectHash == baseHistory.LocalObjectHash &&
			bytes.Equal(historical.SignedTx, baseHistory.SignedTx) &&
			bytes.Equal(historical.RecipientConsignment, recipientObject) &&
			bytes.Equal(historical.LocalConsignment, localObject),
			"R09 cold history lost its exact inline consignments, object binding or witness")
		historyBalance, err := device.GetRGB11AssetBalance(&historyAsset.AssetName)
		coreRequire(t, "read R09 historical cold balance", err)
		coreAssert(t, historyBalance != nil && historyBalance.Value.Uint64() == 1,
			"R09 cold device lost its independent historical allocation")
		t.Logf("core-e2e: R09 historical fixture: actor=%d raw_inline=true remote_deduplicated=true object_binding=true witness_equal=true",
			device.debugID)
	}
	baseHash := dkvsindexer.RecordHash(baseRecord)
	beforeBroadcast := chain.broadcastCount()
	type prepareAttempt struct {
		device     *Manager
		invoice    string
		prepared   *RGB11PreparedTransfer
		pending    *rgb11wallet.PendingTransfer
		generation uint64
		err        error
	}
	type backupAttempt struct {
		device *Manager
		err    error
	}
	raceCtx, stopRace := context.WithTimeout(ctx, 60*time.Second)
	defer stopRace()
	casArrivals := make(chan struct{}, 2)
	casReplies := make(chan coreR09CASResult, 2)
	casRelease := make(chan struct{})
	var releaseOnce sync.Once
	releaseCAS := func() { releaseOnce.Do(func() { close(casRelease) }) }
	priorHTTP := make(map[*Manager]HttpClient, 2)
	restoreTransports := func() {
		for device, transport := range priorHTTP {
			device.http = transport
			device.dkvs.mu.Lock()
			for _, client := range device.dkvs.clients {
				client.Http = transport
			}
			device.dkvs.mu.Unlock()
		}
	}
	var attemptsWG sync.WaitGroup
	defer func() {
		stopRace()
		releaseCAS()
		for _, device := range []*Manager{deviceB, deviceC} {
			device.StopDKVSSync()
		}
		attemptsWG.Wait()
		restoreTransports()
	}()
	endpointID := ""
	for _, device := range []*Manager{deviceB, deviceC} {
		store, err := device.accountDKVSStore()
		coreRequire(t, "open R09 account client for real CAS race", err)
		config, err := store.client.GetDKVSClientConfig()
		coreRequire(t, "read R09 handshaken endpoint", err)
		coreAssert(t, config != nil && config.EndpointID != "", "R09 endpoint is not verified")
		if endpointID == "" {
			endpointID = config.EndpointID
		}
		coreAssert(t, config.EndpointID == endpointID, "R09 participants are bound to different Core endpoints")
		transport := store.client.Http
		priorHTTP[device] = transport
		gate := &coreE2EDirtyCASBarrierHTTP{
			HttpClient: transport, owner: device, arrivals: casArrivals, release: casRelease, results: casReplies,
			key: module.Key, accountID: rootID, endpoint: config.EndpointID, seq: baseRecord.Seq,
			precondition: dkvsindexer.WritePrecondition{ExpectedHash: &baseHash},
		}
		device.http = gate
		device.dkvs.mu.Lock()
		for _, client := range device.dkvs.clients {
			client.Http = gate
		}
		device.dkvs.mu.Unlock()
	}
	// Both actual publication workers race. The seed and observer remain off.
	for _, device := range []*Manager{deviceB, deviceC} {
		coreRequire(t, "start R09 real CAS participant", device.StartDKVSSync())
	}
	for _, device := range []*Manager{deviceB, deviceC} {
		coreWaitAccountOnline(t, device)
		coreAssert(t, len(casArrivals) == 0 && len(casReplies) == 0,
			"R09 startup attempted a dirty module publication before either preparation")
		coreRequire(t, "synchronize R09 participant before simultaneous preparation", device.SyncAccountManagementState(raceCtx))
		coreRequire(t, "verify R09 selected module write qualification", device.CheckAccountWrite(module.Provider, module.Scope))
		current := coreCaptureManagedProfile(device)
		coreAssert(t, !current.dirty && current.revision == baseRecord.Seq && current.hash == baseHash.String(),
			"R09 ready participant changed the frozen signed module baseline")
	}
	coreAssert(t, len(casArrivals) == 0 && len(casReplies) == 0,
		"R09 readiness synchronization published the target module before either preparation")
	startRace := make(chan struct{})
	raceResults := make(chan prepareAttempt, 2)
	for index, device := range []*Manager{deviceB, deviceC} {
		attempt := prepareAttempt{device: device, invoice: invoices[index]}
		attemptsWG.Add(1)
		go func() {
			defer attemptsWG.Done()
			select {
			case <-startRace:
			case <-raceCtx.Done():
				attempt.err = raceCtx.Err()
				raceResults <- attempt
				return
			}
			attempt.prepared, attempt.err = attempt.device.PrepareRGB11Transfer(raceCtx, RGB11SendRequest{
				Invoice: attempt.invoice, FeeRate: 2, MinConfirmations: 1,
			})
			raceResults <- attempt
		}()
	}
	close(startRace)
	for index := 0; index < 2; index++ {
		select {
		case <-casArrivals:
		case early := <-casReplies:
			t.Fatalf("core-e2e: R09 module request missed its exact barrier: error_type=%T", early.err)
		case <-raceCtx.Done():
			t.Fatal("core-e2e: both R09 local prepares did not reach the real module CAS barrier")
		}
	}
	localAttempts := make(map[*Manager]*prepareAttempt, 2)
	for index := 0; index < 2; index++ {
		var attempt prepareAttempt
		select {
		case attempt = <-raceResults:
		case <-raceCtx.Done():
			t.Fatal("core-e2e: local R09 prepare waited for its asynchronous backup")
		}
		coreRequire(t, "commit R09 preparation locally before remote CAS release", attempt.err)
		coreAssert(t, attempt.prepared != nil && attempt.prepared.State != nil && localAttempts[attempt.device] == nil,
			"R09 local prepare omitted or duplicated its durable operation")
		attempt.pending = cloneRGB11BroadcastIntent(assertRGB11UnsignedPending(t, attempt.device.rgbManager, attempt.prepared.State.TransferID))
		coreAssert(t, len(attempt.pending.RecipientConsignment) != 0 && len(attempt.pending.LocalConsignment) != 0 &&
			overlapsRGB11Outpoints(issued.OutPoints, attempt.pending.State.InputOutPoints),
			"R09 local prepare lost private recovery material or its actual RGB carrier")
		coreAssertRGB11Reservation(t, attempt.device, attempt.pending.State.TransferID, true)
		attempt.device.mutex.RLock()
		local, readErr := attempt.device.readAccountLocal(rootID, module.Provider, module.Scope)
		if local != nil {
			attempt.generation = local.Generation
		}
		attempt.device.mutex.RUnlock()
		coreRequire(t, "read R09 captured local generation", readErr)
		coreAssert(t, local != nil && attempt.generation > local.Confirmed, "R09 preparation is not a pending local generation")
		localAttempts[attempt.device] = &attempt
	}
	left, right := localAttempts[deviceB], localAttempts[deviceC]
	coreAssert(t, left != nil && right != nil && left.prepared.State.TransferID != right.prepared.State.TransferID &&
		overlapsRGB11Outpoints(left.pending.State.InputOutPoints, right.pending.State.InputOutPoints),
		"R09 did not create two distinct intents competing for the same actual input")
	coreAssert(t, chain.broadcastCount() == beforeBroadcast, "R09 local prepares broadcast before delivery or ACK")
	backupResults := make(chan backupAttempt, 2)
	for _, device := range []*Manager{deviceB, deviceC} {
		attemptsWG.Add(1)
		go func(device *Manager) {
			defer attemptsWG.Done()
			backupErr := device.WaitAccountBackup(raceCtx, module.Provider, module.Scope)
			backupResults <- backupAttempt{device: device, err: backupErr}
		}(device)
	}
	releaseCAS()
	var winner *RGB11PreparedTransfer
	var winnerDevice *Manager
	var loser *prepareAttempt
	var accepted coreR09CASResult
	observed := make(map[*Manager]coreR09CASResult, 2)
	for index := 0; index < 2; index++ {
		var reply coreR09CASResult
		select {
		case reply = <-casReplies:
		case <-raceCtx.Done():
			t.Fatal("core-e2e: R09 real CAS responses did not complete")
		}
		coreAssert(t, localAttempts[reply.device] != nil && reply.requestID != "" && observed[reply.device].requestID == "",
			"R09 CAS response omitted or duplicated the exact participant request")
		observed[reply.device] = reply
		t.Logf("core-e2e: R09 module CAS: actor=%d request_tag=%s record_tag=%s accepted=%t write_conflict=%t stale_generation=%t error_type=%T",
			reply.device.debugID, coreCASTag(reply.requestID), coreCASTag(reply.recordHash), reply.err == nil,
			errors.Is(reply.err, dkvsindexer.ErrWriteConflict), errors.Is(reply.err, dkvsindexer.ErrStaleGeneration), reply.err)
		if reply.err == nil {
			coreAssert(t, winner == nil, "two same-baseline R09 module publications were accepted")
			winnerDevice, winner, accepted = reply.device, localAttempts[reply.device].prepared, reply
		} else {
			coreAssert(t, loser == nil && (errors.Is(reply.err, dkvsindexer.ErrWriteConflict) ||
				errors.Is(reply.err, dkvsindexer.ErrStaleGeneration)),
				"R09 publication failed without a precise conflict or stale-authorization refusal")
			loser = localAttempts[reply.device]
		}
	}
	coreAssert(t, winner != nil && loser != nil && observed[deviceB].requestID != observed[deviceC].requestID &&
		observed[deviceB].recordHash != observed[deviceC].recordHash,
		"real R09 CAS did not select exactly one of two independently authorized module writes")
	for index := 0; index < 2; index++ {
		var backup backupAttempt
		select {
		case backup = <-backupResults:
		case <-raceCtx.Done():
			t.Fatal("core-e2e: R09 publication did not reach a confirmed or definite module-conflict result")
		}
		attempt := localAttempts[backup.device]
		coreAssert(t, attempt != nil, "R09 backup returned an unknown participant")
		t.Logf("core-e2e: R09 module backup: actor=%d confirmed=%t write_conflict=%t error_type=%T",
			backup.device.debugID, backup.err == nil, errors.Is(backup.err, dkvsindexer.ErrWriteConflict), backup.err)
		if backup.device == winnerDevice {
			coreRequire(t, "confirm accepted R09 module backup", backup.err)
		} else {
			coreAssert(t, backup.device == loser.device && errors.Is(backup.err, dkvsindexer.ErrWriteConflict),
				"R09 loser did not reach a definite module write conflict")
		}
		backup.device.mutex.RLock()
		local, readErr := backup.device.readAccountLocal(rootID, module.Provider, module.Scope)
		backup.device.mutex.RUnlock()
		coreRequire(t, "read durable R09 backup outcome", readErr)
		coreAssert(t, local != nil, "R09 backup lost its local module row")
		if backup.device == winnerDevice {
			coreAssert(t, local.Confirmed >= attempt.generation && local.Baseline != nil &&
				dkvsindexer.RecordHash(local.Baseline).String() == accepted.recordHash,
				"R09 winner has no confirmed receipt for its exact module generation")
		} else {
			coreAssert(t, local.Conflict && local.Generation > local.Confirmed,
				"R09 losing private generation was silently confirmed or discarded")
		}
	}
	selected := coreReadRGBClosure(t, deviceA, cfg)
	defer coreZeroRGBPayloads(selected.payloads)
	selectedRecord := selected.records[module.Key]
	coreAssert(t, selectedRecord != nil && selectedRecord.Seq == baseRecord.Seq+1 &&
		dkvsindexer.RecordHash(selectedRecord).String() == accepted.recordHash,
		"R09 authoritative module changed more than once or differs from the verified winning request")
	selectedPkg, err := rgb11wallet.DecodeRecoveryPackage(selected.payloads[scope])
	coreRequire(t, "decode R09 independently verified selected package", err)
	selectedTransfers, err := rgb11wallet.TransfersFromProjectionSnapshot(selectedPkg.ProjectionRecords)
	coreRequire(t, "read R09 independently verified selected transfer identities", err)
	selectedCount := 0
	for _, transfer := range selectedTransfers {
		if transfer == nil {
			continue
		}
		coreAssert(t, transfer.TransferID != loser.prepared.State.TransferID, "selected R09 package contains the private loser")
		if transfer.TransferID == winner.State.TransferID {
			selectedCount++
			coreAssert(t, transfer.Status == "prepared" && transfer.WitnessTxID == winner.TxID &&
				transfer.ConsignmentHash == winner.State.ConsignmentHash &&
				equalRGB11StringSet(transfer.InputOutPoints, winner.State.InputOutPoints),
				"selected R09 package changed the winning transaction or input identity")
		}
	}
	coreAssert(t, selectedCount == 1, "R09 selected package omitted or duplicated the winning operation")
	for _, attempt := range localAttempts {
		current := assertRGB11UnsignedPending(t, attempt.device.rgbManager, attempt.prepared.State.TransferID)
		coreAssert(t, coreDigest(current) == coreDigest(attempt.pending), "R09 backup outcome changed the original private preparation")
		coreAssertRGB11Reservation(t, attempt.device, current.State.TransferID, true)
	}
	coreAssert(t, chain.broadcastCount() == beforeBroadcast, "R09 publication broadcast before delivery or ACK")
	winnerPending := localAttempts[winnerDevice].pending
	losingPending := loser.pending
	losingTransfer := &losingPending.State
	coreAssert(t, overlapsRGB11Outpoints(winnerPending.State.InputOutPoints, losingPending.State.InputOutPoints),
		"R09 selected and private operations no longer compete for the actual input")
	// Let the real client finish its outcome before stopping/rewiring transport.
	coreRGB11RetireDevice(t, winnerDevice)
	loser.device.StopDKVSSync()
	attemptsWG.Wait()
	restoreTransports()

	remoteBeforeWinnerCancel := coreManagedRemoteRecords(t, deviceA, cfg)
	coreRequire(t, "resume private R09 loser for the active-winner negative control", loser.device.StartDKVSSync())
	conflictErr := loser.device.SyncAccountManagementState(ctx)
	coreAssert(t, errors.Is(conflictErr, dkvsindexer.ErrWriteConflict),
		"private R09 loser did not retain a definite conflict against the active winner")
	blockedCancel := loser.device.CancelRGB11OutOfBandTransfer(losingTransfer.TransferID)
	t.Logf("core-e2e: R09 active winner cancel: actor=%d nil=%t error_type=%T write_conflict=%t active_owner=%t out_of_band=%t inconsistent=%t path_unsynced=%t import_incomplete=%t snapshot_changed=%t stale_local=%t owner_mismatch=%t canceled=%t deadline=%t",
		loser.device.debugID, blockedCancel == nil, blockedCancel,
		errors.Is(blockedCancel, dkvsindexer.ErrWriteConflict), errors.Is(blockedCancel, ErrRGB11ManagedOperationActive),
		errors.Is(blockedCancel, ErrRGB11OutOfBandRequired), errors.Is(blockedCancel, ErrRGB11Inconsistent),
		errors.Is(blockedCancel, ErrDKVSPathNotSynced), errors.Is(blockedCancel, ErrAccountManagedDataImportIncomplete),
		errors.Is(blockedCancel, errAccountSnapshotChanged), errors.Is(blockedCancel, errRGB11StaleLocal),
		errors.Is(blockedCancel, ErrUtxoReservationOwner), errors.Is(blockedCancel, context.Canceled), errors.Is(blockedCancel, context.DeadlineExceeded))
	t.Logf("core-e2e: R09 active winner identity: actor=%d transfer_equal=%t witness_equal=%t consignment_equal=%t inputs_overlap=%t",
		loser.device.debugID, winnerPending.State.TransferID == losingTransfer.TransferID,
		winnerPending.State.WitnessTxID == losingTransfer.WitnessTxID,
		winnerPending.State.ConsignmentHash == losingTransfer.ConsignmentHash,
		overlapsRGB11Outpoints(winnerPending.State.InputOutPoints, losingTransfer.InputOutPoints))
	retained, err := loser.device.rgbManager.projectionStore.LoadPendingTransfer(losingTransfer.TransferID)
	privateUnchanged := err == nil && retained != nil && coreDigest(retained) == coreDigest(losingPending)
	ownerRetained := losingPending.ReservationID != "" && len(losingTransfer.InputOutPoints) > 0
	locksAfterCancel := loser.device.utxoLockerL1.GetLockedUtxoList()
	for _, outpoint := range losingTransfer.InputOutPoints {
		lock := locksAfterCancel[outpoint]
		ownerRetained = ownerRetained && lock != nil && lock.ReservationID == losingPending.ReservationID
	}
	remoteAfterBlockedCancel := coreManagedRemoteRecords(t, deviceA, cfg)
	noCancelBroadcast := chain.broadcastCount() == beforeBroadcast
	t.Logf("core-e2e: R09 active winner cancel invariants: actor=%d load_error_type=%T private_unchanged=%t owner_retained=%t remote_unchanged=%t broadcast_unchanged=%t",
		loser.device.debugID, err, privateUnchanged, ownerRetained,
		coreManagedRemoteRecordsEqual(remoteBeforeWinnerCancel, remoteAfterBlockedCancel), noCancelBroadcast)
	coreRequire(t, "read private R09 loser after the rejected active-winner cancellation", err)
	coreAssert(t, privateUnchanged, "rejected private cancellation changed the original recovery material")
	coreAssert(t, ownerRetained, "rejected private cancellation released the original input reservation")
	coreAssertManagedRemoteEqual(t, "R09 private cancel while remote winner is active", remoteBeforeWinnerCancel,
		remoteAfterBlockedCancel)
	coreAssert(t, noCancelBroadcast, "R09 active-winner private cancellation broadcast a transaction")
	coreAssert(t, errors.Is(blockedCancel, dkvsindexer.ErrWriteConflict) && errors.Is(blockedCancel, ErrRGB11ManagedOperationActive),
		"private loser cancellation was not rejected for the exact conflicting active input owner")

	// Use a third independent database to adopt and explicitly cancel the
	// selected winner. This leaves the authoritative input free while the losing
	// device still holds its never-exposed private signed intent.
	loser.device.StopDKVSSync() // Its unpublished losing intent cannot be backed up.
	coreRGB11UseDevice(t, deviceA)
	coreRequire(t, "third R09 device cancels selected winner", deviceA.CancelRGB11OutOfBandTransfer(winner.State.TransferID))
	coreAssertRGB11Reservation(t, deviceA, winner.State.TransferID, false, winnerPending)
	coreRGB11RetireDevice(t, deviceA)
	remoteAfterWinnerCancel := coreManagedRemoteRecords(t, deviceA, cfg)

	// The initial Sync is expected to stop at the selected account payload,
	// because it omits the local-only intent. The explicit public Cancel must
	// still provide a safe way to discard an intent which has never been shared.
	coreRequire(t, "resume R09 losing device for explicit recovery", loser.device.StartDKVSSync())
	syncErr := loser.device.SyncAccountManagementState(ctx)
	currentPending, loadErr := loser.device.rgbManager.projectionStore.LoadPendingTransfer(losingTransfer.TransferID)
	coreRequire(t, "reload R09 losing private pending after account sync", loadErr)
	coreAssert(t, bytes.Equal(currentPending.SignedTx, losingPending.SignedTx) &&
		currentPending.State.WitnessTxID == losingPending.State.WitnessTxID && coreDigest(currentPending) == coreDigest(losingPending),
		"R09 failed sync changed the losing transaction identity")
	coreAssertRGB11Reservation(t, loser.device, losingTransfer.TransferID, true)
	cancelErr := loser.device.CancelRGB11OutOfBandTransfer(losingTransfer.TransferID)
	if cancelErr != nil {
		coreAssert(t, errors.Is(syncErr, dkvsindexer.ErrWriteConflict), "R09 explicit cancellation failed outside the known module conflict")
		retained, loadErr := loser.device.rgbManager.projectionStore.LoadPendingTransfer(losingTransfer.TransferID)
		coreRequire(t, "reload R09 pending after failed explicit cancellation", loadErr)
		coreAssert(t, bytes.Equal(retained.SignedTx, losingPending.SignedTx) &&
			retained.State.WitnessTxID == losingPending.State.WitnessTxID && coreDigest(retained) == coreDigest(losingPending),
			"failed R09 cancellation changed the private transaction")
		coreAssertRGB11Reservation(t, loser.device, losingTransfer.TransferID, true)
		t.Errorf("core-e2e RED: same-root R09 CAS loser cannot sync selected state (%v) or explicitly cancel its unshared intent (%v)", syncErr, cancelErr)
		coreAssertManagedRemoteEqual(t, "failed R09 loser sync/cancel", remoteAfterWinnerCancel,
			coreManagedRemoteRecords(t, deviceA, cfg))
		coreAssert(t, chain.broadcastCount() == beforeBroadcast,
			"R09 CAS loser sync/cancel unexpectedly broadcast")
	} else {
		// An unpublished local loser is discarded rather than added to the
		// shared history. Check its exact old owner and public reservation too.
		_, loadErr := loser.device.rgbManager.projectionStore.LoadPendingTransfer(losingTransfer.TransferID)
		coreAssert(t, errors.Is(loadErr, indexer.ErrKeyNotFound), "explicit private cancellation retained the losing pending")
		for _, lock := range loser.device.utxoLockerL1.GetLockedUtxoList() {
			coreAssert(t, lock == nil || lock.ReservationID != losingPending.ReservationID,
				"discarded private batch still owns an input or change reservation")
		}
		coreAssert(t, loser.device.GetResv(rgb11TransferResvID(loser.device.rgbManager.rgb11ScopeKey(), "send:"+losingTransfer.TransferID)) == nil,
			"discarded private batch retained its common reservation")
		coreAssertManagedRemoteEqual(t, "explicit R09 private loser cancellation", remoteAfterWinnerCancel,
			coreManagedRemoteRecords(t, deviceA, cfg))
		coreRequire(t, "R09 losing device syncs after explicit local cancellation",
			loser.device.SyncAccountManagementState(ctx))
		_, err = loser.device.RefreshRGB11State(ctx)
		coreRequire(t, "refresh R09 losing device after explicit cancellation", err)
		beforeResend := chain.broadcastCount()
		resend := coreSendRGB11Amount(t, chain, loser.device, receiver,
			issued.AssetName, "1", beforeResend, 999, 1, nil)
		coreAssert(t, resend != nil && resend.TxID != "" && chain.broadcastCount() == beforeResend+1,
			"R09 CAS loser could not spend the source allocation after explicit cancellation and sync")
		coreWaitPrivateBackup(t, loser.device)
	}
	for _, device := range devices {
		device.StopDKVSSync()
	}
	coreRGB11UseDevice(t, seed)
}

// coreRGB11StableOnlyColdRestoreE2E puts both a valid stale RGB active cache
// and a corrupt cache with a valid old-style application ID in the real
// CoreNode mailbox. Two new databases must recover only the current stable
// account bundle; one then spends the recovered allocation through Direct.
func coreRGB11StableOnlyColdRestoreE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	seed, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	ctx := context.Background()
	coreTransferDeviceCleanup(t, seed, receiver)
	coreRGB11RetireDevice(t, seed)
	coreRGB11RetireDevice(t, receiver)
	source := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, source)
	coldReceiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, coldReceiver)
	issued, err := source.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EC", Name: "E2E stable-only cache asset", Amounts: []uint64{1000},
	})
	coreRequire(t, "issue stable-only cache NIA", err)
	exported, err := source.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export stable-only cache contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode stable-only cache contract", err)
	_, err = coldReceiver.ImportRGB11ContractFile(ctx, contract)
	coreRequire(t, "import stable-only cache contract", err)
	_, err = coldReceiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish stable-only cache Direct capability", err)

	// Capture an authentic, signed prepared sender closure that predates a
	// definitive local cancellation. If mailbox active data were still treated
	// as authority, this old prepared phase could resurrect the canceled lock.
	oldInvoice, err := coldReceiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create old-cache witness invoice", err)
	oldPrepared, err := source.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: oldInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare operation for stale active cache", err)
	walletID, err := source.RGB11WalletID()
	coreRequire(t, "read stable-only cache wallet identity", err)
	oldSnapshot, _, err := source.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "capture authentic old active sender snapshot", err)
	oldActive, err := rgb11wallet.ActiveRecoveryPackageFromSnapshot(oldSnapshot)
	coreRequire(t, "select authentic old active sender closure", err)
	oldActiveBytes, err := rgb11wallet.EncodeActiveRecoveryPackage(oldActive)
	coreRequire(t, "encode authentic old active sender closure", err)
	if oldPrepared == nil || oldPrepared.State == nil {
		t.Fatal("core-e2e: prepare returned no stale-cache transfer identity")
	}
	oldTransfers, err := rgb11wallet.TransfersFromProjectionSnapshot(oldActive.ProjectionRecords)
	coreRequire(t, "decode authentic old active transfer states", err)
	var oldTransfer *rgb11wallet.TransferState
	for _, transfer := range oldTransfers {
		if transfer != nil && transfer.TransferID == oldPrepared.State.TransferID {
			oldTransfer = transfer
			break
		}
	}
	coreAssert(t, oldTransfer != nil && oldTransfer.Status == "prepared" &&
		oldTransfer.WitnessTxID == oldPrepared.TxID,
		"stale mailbox fixture is not the original prepared transaction")
	oldPending, err := source.rgbManager.projectionStore.LoadPendingTransfer(oldPrepared.State.TransferID)
	coreRequire(t, "capture original stale-cache cancellation owner", err)
	oldPending = cloneRGB11BroadcastIntent(oldPending)
	coreAssertRGB11Reservation(t, source, oldPrepared.State.TransferID, true)
	coreRequire(t, "cancel never-shared stale-cache transaction",
		source.CancelRGB11OutOfBandTransfer(oldPrepared.State.TransferID))
	cancelled, err := source.rgbManager.projectionStore.LoadPendingTransfer(oldPrepared.State.TransferID)
	coreRequire(t, "read canceled stale-cache transaction", err)
	coreAssert(t, cancelled.State.Status == "rejected" && cancelled.State.AckStatus == "rejected" &&
		cancelled.State.RejectReason == RGB11RejectReasonUser && cancelled.State.WitnessTxID == oldPrepared.TxID &&
		cancelled.State.ConsignmentHash == oldPrepared.State.ConsignmentHash &&
		equalRGB11StringSet(cancelled.State.InputOutPoints, oldPrepared.State.InputOutPoints),
		"source did not durably cancel the original stale-cache transaction")
	coreAssertRGB11Reservation(t, source, oldPrepared.State.TransferID, false, oldPending)

	// Advance the authoritative bundle again with a real accepted Direct send,
	// so the stale prepared cache conflicts with both a terminal cancel and a
	// newer settled ownership history.
	beforeBroadcast := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, source, coldReceiver,
		issued.AssetName, "100", beforeBroadcast)
	_, err = source.RefreshRGB11State(ctx)
	coreRequire(t, "refresh authoritative stable-only cache source", err)
	_, err = coldReceiver.RefreshRGB11State(ctx)
	coreRequire(t, "refresh stable-only cache receiver", err)
	coreWaitPrivateBackup(t, source)
	coreWaitPrivateBackup(t, coldReceiver)
	stableBeforeCache := coreRemoteStableRGB(t, source, cfg)
	localBeforeCache := coreLocalRGBBundleDigests(t, source)
	remoteRecordsBeforeCache := coreManagedRemoteRecords(t, source, cfg)
	selectedCancel := coreRGB11SelectedTransfer(t, source, cfg, oldPrepared.State.TransferID)
	coreAssert(t, selectedCancel.Status == "rejected" && selectedCancel.TransferID == oldPrepared.State.TransferID &&
		selectedCancel.WitnessTxID == oldPrepared.TxID && selectedCancel.ConsignmentHash == oldPrepared.State.ConsignmentHash &&
		equalRGB11StringSet(selectedCancel.InputOutPoints, oldPrepared.State.InputOutPoints),
		"selected canceled history changed the original transaction/input identity")

	catalog, err := source.accountManagedDataCatalog()
	coreRequire(t, "read stable-only cache account catalog", err)
	scopeID, err := source.rgbManager.rgb11AccountManagedScopeID()
	coreRequire(t, "resolve stable-only cache RGB scope", err)
	if _, ok := accountManagedScopeSet(catalog)[scopeID]; !ok {
		t.Fatal("core-e2e: current RGB scope is absent from the stable account catalog")
	}
	staleEnvelope, staleHash, err := encodeAccountManagedActiveEnvelope(
		rgb11AccountManagedProviderID,
		AccountManagedDataPayload{Scope: scopeID, Payload: oldActiveBytes},
	)
	coreRequire(t, "encode valid stale RGB active cache", err)
	corruptEnvelope, corruptHash, err := encodeAccountManagedActiveEnvelope(
		rgb11AccountManagedProviderID,
		AccountManagedDataPayload{Scope: scopeID, Payload: []byte("obsolete cache body")},
	)
	coreRequire(t, "build corrupt RGB active cache", err)
	corruptEnvelope[len(corruptEnvelope)-1] ^= 0xff
	staleID := accountManagedActiveApplicationID(rgb11AccountManagedProviderID, scopeID, staleHash)
	corruptID := accountManagedActiveApplicationID(rgb11AccountManagedProviderID, scopeID, corruptHash)
	root, err := source.accountManagementRootWallet()
	coreRequire(t, "resolve stable-only cache root wallet", err)
	for _, item := range []struct {
		id   string
		body []byte
	}{
		{id: staleID, body: staleEnvelope},
		{id: corruptID, body: corruptEnvelope},
	} {
		_, err = source.sendWalletDirectMessage(root, item.id,
			AccountMessageKindManagedActive, catalog.AccountID, item.body)
		coreRequire(t, "publish legacy RGB cache to real CoreNode", err)
	}
	mailbox, err := source.freshDirectMessages(root)
	coreRequire(t, "read back old RGB caches from real CoreNode", err)
	foundStale, foundCorrupt := false, false
	for _, message := range mailbox {
		if message == nil || message.Payload == nil || message.Payload.Kind != AccountMessageKindManagedActive {
			continue
		}
		switch message.Payload.ApplicationID {
		case staleID:
			foundStale = bytes.Equal(message.Payload.Body, staleEnvelope)
		case corruptID:
			foundCorrupt = bytes.Equal(message.Payload.Body, corruptEnvelope)
		}
	}
	if !foundStale || !foundCorrupt {
		t.Fatalf("core-e2e: real CoreNode cache readback stale=%t corrupt=%t stale_bytes=%d", foundStale, foundCorrupt, len(staleEnvelope))
	}
	coreAssertManagedRemoteEqual(t, "legacy RGB cache publication", remoteRecordsBeforeCache,
		coreManagedRemoteRecords(t, source, cfg))

	beforeColdSync := chain.broadcastCount()
	sourceCatalog, sourceView := coreCanonicalCatalog(source), coreRGBSemantic(t, source)
	coreRGB11RetireDevice(t, source)
	var coldA, coldB *Manager
	for index := 0; index < 2; index++ {
		cold := coreRestoreE2E(t, cfg, chain, senderMaterial)
		coreAssert(t, source.db != cold.db && (index == 0 || coldA.db != cold.db),
			"stable-only cache recovery did not use independent databases")
		if index == 0 {
			coldA = cold
		} else {
			coldB = cold
		}
		beforeColdSync := chain.broadcastCount()
		coreRGB11UseDevice(t, cold)
		coreAssert(t, coreDigest(sourceCatalog) == coreDigest(coreCanonicalCatalog(cold)),
			"stable-only cold recovery changed the account catalog")
		coreAssertMapEqual(t, "stable-only cold recovery bundle", localBeforeCache,
			coreLocalRGBBundleDigests(t, cold))
		coreAssertMapEqual(t, "stable-only cold recovery authoritative bundle", stableBeforeCache,
			coreRemoteStableRGB(t, cold, cfg))
		coreAssert(t, coreDigest(sourceView) == coreDigest(coreRGBSemantic(t, cold)),
			"valid or corrupt legacy active cache changed recovered ownership, spendability, phase or lock")
		staleRecovered, err := cold.rgbManager.projectionStore.LoadPendingTransfer(oldPrepared.State.TransferID)
		coreRequire(t, "load stale transaction after cold recovery", err)
		// The verified selected package, rather than local delivery bookkeeping,
		// defines the entire terminal state that an independent device restores.
		coreAssert(t, coreDigest(staleRecovered.State) == coreDigest(*selectedCancel),
			"cold recovery resurrected a stale active cache over the selected canceled phase")
		coreAssertRGB11Reservation(t, cold, oldPrepared.State.TransferID, false, oldPending)
		coreAssert(t, chain.broadcastCount() == beforeColdSync,
			"cold stable-only cache recovery broadcast a transaction")
		coreRGB11RetireDevice(t, cold)
	}
	// The first independent cold database proves the recovered stable output is
	// actually spendable after it ignores both mailbox entries.
	coreAssert(t, coldA.db != coldB.db, "cold cache readers share a local database")
	coreRGB11UseDevice(t, coldA)
	beforeRespend := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, coldA, coldReceiver,
		issued.AssetName, "50", beforeRespend)
	remaining, err := coldA.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold sender balance after cache-isolation re-spend", err)
	received, err := coldReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold receiver balance after cache-isolation re-spend", err)
	coreAssert(t, remaining != nil && remaining.Value.Uint64() == 850 &&
		received != nil && received.Value.Uint64() == 150,
		"cold stable-only recovery could not complete a real subsequent Direct spend")
	coreAssert(t, chain.broadcastCount() == beforeColdSync+1,
		"cache recovery or re-spend caused an unexpected broadcast count")
	coreRGB11RetireDevice(t, coldA)
	coreRGB11RetireDevice(t, coldReceiver)
}

func coreRGB11RetireDevice(t *testing.T, device *Manager) {
	t.Helper()
	coreSuspendRGBDevice(t, device)
}

func coreRGB11UseDevice(t *testing.T, device *Manager) {
	t.Helper()
	coreRequire(t, "resume selected RGB device", device.StartDKVSSync())
	coreSynchronizeRestoredAccount(t, device)
	_, err := device.RefreshRGB11State(context.Background())
	coreRequire(t, "reconcile selected RGB device", err)
}

func coreRGB11SelectedTransfer(t *testing.T, device *Manager, cfg coreE2EConfig,
	transferID string) *rgb11wallet.TransferState {
	t.Helper()
	closure := coreReadRGBClosure(t, device, cfg)
	defer coreZeroRGBPayloads(closure.payloads)
	scope, err := device.rgbManager.rgb11AccountManagedScopeID()
	coreRequire(t, "identify selected RGB module scope", err)
	pkg, err := rgb11wallet.DecodeRecoveryPackage(closure.payloads[scope])
	coreRequire(t, "decode verified selected RGB recovery package", err)
	states, err := rgb11wallet.TransfersFromProjectionSnapshot(pkg.ProjectionRecords)
	coreRequire(t, "decode verified selected transfer states", err)
	var selected *rgb11wallet.TransferState
	for _, state := range states {
		if state != nil && state.TransferID == transferID {
			coreAssert(t, selected == nil, "selected package has duplicate transfer identities")
			selected = state
		}
	}
	coreAssert(t, selected != nil, "selected package omitted the required transfer")
	return selected
}

func coreAssertRGB11LockedPrepare(t *testing.T, device *Manager,
	expected *rgb11wallet.PendingTransfer, invoice string) {
	t.Helper()
	current, err := device.rgbManager.projectionStore.LoadPendingTransfer(expected.State.TransferID)
	coreRequire(t, "read actual imported input owner before competing prepare", err)
	coreAssert(t, sameRGB11BroadcastIntent(expected, current) &&
		equalRGB11StringSet(expected.State.InputOutPoints, current.State.InputOutPoints),
		"selected device did not import the exact original intent and input owner")
	tx, err := decodeRGB11BroadcastTx(current.SignedTx)
	coreRequire(t, "decode actual transaction inputs before competing prepare", err)
	inputs := make([]string, 0, len(tx.TxIn))
	for _, input := range tx.TxIn {
		inputs = append(inputs, input.PreviousOutPoint.String())
	}
	coreAssert(t, equalRGB11StringSet(inputs, current.State.InputOutPoints),
		"imported input identity differs from the actual transaction")
	coreAssertRGB11Reservation(t, device, current.State.TransferID, true)
	before := coreDigest(current)
	prepared, prepareErr := device.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if prepared != nil {
		overlap, inputCount := false, 0
		if prepared.State != nil {
			overlap = overlapsRGB11Outpoints(inputs, prepared.State.InputOutPoints)
			inputCount = len(prepared.State.InputOutPoints)
		}
		t.Fatalf("core-e2e: selected locked-input prepare returned a new operation: error_type=%T input_overlap=%t inputs=%d/%d",
			prepareErr, overlap, len(inputs), inputCount)
	}
	coreAssert(t, errors.Is(prepareErr, ErrRGB11InsufficientBalance),
		"known locked RGB carrier was not rejected as unavailable balance")
	after, err := device.rgbManager.projectionStore.LoadPendingTransfer(expected.State.TransferID)
	coreRequire(t, "read original input owner after competing prepare", err)
	coreAssert(t, coreDigest(after) == before, "rejected competing prepare changed the original private intent")
	coreAssertRGB11Reservation(t, device, expected.State.TransferID, true)
}

func coreAssertRGB11Reservation(t *testing.T, manager *Manager, transferID string, wantLocked bool,
	original ...*rgb11wallet.PendingTransfer) {
	t.Helper()
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	coreRequire(t, "load same-root transfer reservation", err)
	coreAssert(t, len(pending.State.InputOutPoints) > 0,
		"transfer closure omitted the original source input identity")
	expected := pending
	if !wantLocked {
		coreAssert(t, len(original) == 1 && original[0] != nil,
			"released reservation check requires the saved pre-cancellation owner and inputs")
		expected = original[0]
		coreAssert(t, expected.State.TransferID == transferID &&
			equalRGB11StringSet(expected.State.InputOutPoints, pending.State.InputOutPoints),
			"terminal transfer changed the saved pre-cancellation input identity")
	}
	coreAssert(t, expected.ReservationID != "" && len(expected.State.InputOutPoints) > 0,
		"reservation check has no nonempty original owner or source inputs")
	locks := manager.utxoLockerL1.GetLockedUtxoList()
	for _, outpoint := range expected.State.InputOutPoints {
		lock := locks[outpoint]
		if wantLocked {
			coreAssert(t, lock != nil && lock.ReservationID == expected.ReservationID,
				"pending transfer lost its original input reservation")
		} else if lock != nil && lock.ReservationID == expected.ReservationID {
			t.Fatalf("core-e2e: cancelled transfer still owns an input reservation: actor=%d point_tag=%s original_owner_tag=%s terminal_owner_empty=%t lock_reason=%s",
				manager.debugID, coreCASTag(outpoint), coreCASTag(expected.ReservationID), pending.ReservationID == "", lock.Reason)
		}
	}
}

func overlapsRGB11Outpoints(left, right []string) bool {
	set := make(map[string]struct{}, len(left))
	for _, item := range left {
		set[item] = struct{}{}
	}
	for _, item := range right {
		if _, ok := set[item]; ok {
			return true
		}
	}
	return false
}

// Observe only each participant's first authorized write of the exact RGB
// module over the frozen signed baseline. The underlying Core CAS stays real.
type coreR09CASResult struct {
	device                *Manager
	requestID, recordHash string
	err                   error
}

type coreE2EDirtyCASBarrierHTTP struct {
	HttpClient
	owner                    *Manager
	key, accountID, endpoint string
	seq                      uint64
	precondition             dkvsindexer.WritePrecondition
	arrivals                 chan<- struct{}
	release                  <-chan struct{}
	results                  chan<- coreR09CASResult
	once                     sync.Once
}

func (h *coreE2EDirtyCASBarrierHTTP) SendGetRequestContext(ctx context.Context, url *URL) ([]byte, error) {
	if client, ok := h.HttpClient.(ContextHttpClient); ok {
		return client.SendGetRequestContext(ctx, url)
	}
	return h.HttpClient.SendGetRequest(url)
}

func (h *coreE2EDirtyCASBarrierHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	return h.SendPostRequestContext(context.Background(), url, body)
}

func (h *coreE2EDirtyCASBarrierHTTP) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	var request DKVSBatchCASRequest
	var requestErr error
	target := false
	if url != nil && strings.HasSuffix(url.Path, "/v3/dkvs/records/batch-cas") {
		requestErr = json.Unmarshal(body, &request)
		target = requestErr != nil
		for _, mutation := range request.Mutations {
			target = target || mutation.Record != nil && mutation.Record.Key == h.key
		}
	}
	first := false
	if target {
		h.once.Do(func() { first = true })
	}
	observed := coreR09CASResult{device: h.owner, requestID: request.RequestID}
	if first {
		if requestErr == nil && (request.EndpointID != h.endpoint || request.RequestID == "" ||
			request.Authorization == nil || len(request.Mutations) != 1 || h.precondition.ExpectedHash == nil) {
			requestErr = errors.New("R09 target CAS identity or authorization is invalid")
		}
		if requestErr == nil {
			mutation := request.Mutations[0]
			if mutation.Record == nil || mutation.Record.Key != h.key || mutation.ExpectAbsent ||
				mutation.ExpectedETag != h.precondition.ExpectedHash.String() || mutation.Record.Seq != h.seq+1 {
				requestErr = errors.New("R09 target CAS does not use the exact signed module baseline")
			} else {
				observed.recordHash = dkvsindexer.RecordHash(mutation.Record).String()
				requestErr = dkvsindexer.VerifySignature(mutation.Record)
				if requestErr == nil {
					accountID, authErr := dkvsindexer.VerifyWalletWriteAuthorization([]dkvsindexer.CASMutation{
						{Record: mutation.Record, Precondition: h.precondition},
					}, dkvsindexer.BatchCASOptions{EndpointID: request.EndpointID, RequestID: request.RequestID}, request.Authorization)
					requestErr = authErr
					if requestErr == nil && accountID != h.accountID {
						requestErr = errors.New("R09 target CAS belongs to another account")
					}
				}
			}
		}
		if requestErr == nil {
			select {
			case h.arrivals <- struct{}{}:
			case <-ctx.Done():
				observed.err = ctx.Err()
				h.results <- observed
				return nil, ctx.Err()
			}
			select {
			case <-h.release:
			case <-ctx.Done():
				observed.err = ctx.Err()
				h.results <- observed
				return nil, ctx.Err()
			}
		}
	}
	var raw []byte
	var transportErr error
	if client, ok := h.HttpClient.(ContextHttpClient); ok {
		raw, transportErr = client.SendPostRequestContext(ctx, url, body)
	} else {
		raw, transportErr = h.HttpClient.SendPostRequest(url, body)
	}
	if first {
		observed.err = requestErr
		if observed.err == nil && transportErr != nil {
			observed.err = transportErr
			var response *HTTPResponseError
			if errors.As(transportErr, &response) {
				decoded := decodeDKVSApplicationResponse(response.Body, nil)
				var refusal *DKVSError
				if errors.As(decoded, &refusal) {
					observed.err = decoded
				}
			}
		}
		if observed.err == nil {
			var reply dkvsWriteResultData
			observed.err = decodeDKVSApplicationResponse(raw, &reply)
			if observed.err == nil {
				checked := coreACKCheckReply(request, reply, h.endpoint)
				observed.err = checked.err
				if observed.err == nil && reply.Data.Applied != 1 {
					observed.err = errors.New("R09 first CAS did not commit exactly one new module record")
				}
				if observed.err == nil {
					observed.err = dkvsindexer.VerifySignature(reply.Data.Records[0])
				}
			}
		}
		h.results <- observed
	}
	return raw, transportErr
}

func coreWalletName(manager *Manager, id int64) string {
	for _, entry := range manager.GetWalletCatalog() {
		if entry.ID == id {
			return entry.Name
		}
	}
	return ""
}

// This wrapper injects a lost Bitcoin RPC outcome without changing the real
// CoreNode account-state/blob CAS or the controlled chain's transaction view.
type coreE2EUnknownBroadcastEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
}

func (*coreE2EUnknownBroadcastEvidence) Broadcast([]byte) (string, error) {
	return "", context.DeadlineExceeded
}

// Observe the exact signed transaction at the real controlled backend without
// opening an artificial window for concurrent same-root account mutations.
type coreE2EObservedBroadcast struct {
	rgb11wallet.BitcoinEvidenceProvider
	inspect func([]byte) error
}

func (e *coreE2EObservedBroadcast) Broadcast(raw []byte) (string, error) {
	if e.inspect != nil {
		if err := e.inspect(raw); err != nil {
			return "", err
		}
	}
	return e.BitcoinEvidenceProvider.Broadcast(raw)
}
