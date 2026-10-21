package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// coreRGB11SameRootThreeDeviceE2E models one computer and two phones as
// independent Managers/DBs over the same paid account and CoreNode binding.
// It deliberately alternates writers through the real account state/blob CAS.
func coreRGB11SameRootThreeDeviceE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	seed *Manager, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	ctx := context.Background()
	deviceA := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, deviceA)
	issued, err := deviceA.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EM", Name: "E2E same-root three-device asset", Amounts: []uint64{1000},
	})
	coreRequire(t, "issue same-root multi-device NIA", err)
	coreCheckpoint(t, "same_root_issue_authoritative", deviceA, cfg, chain, senderMaterial)

	// The invoice receiver needs the contract before it can persist an OOB
	// receive request. This is the standard RGB contract file, not a test codec.
	exported, err := deviceA.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export same-root multi-device contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode same-root multi-device contract", err)
	_, err = receiver.ImportRGB11ContractFile(ctx, contract)
	coreRequire(t, "import standard contract on OOB receiver", err)
	endpoint, err := receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish same-root multi-device Direct endpoint", err)
	coreAssert(t, endpoint != nil && !endpoint.Temporary, "same-root test needs a paid Direct endpoint")

	deviceB := coreRestoreE2E(t, cfg, chain, senderMaterial)
	deviceC := coreRestoreE2E(t, cfg, chain, senderMaterial)
	seedRoot, err := seed.RootAccountID()
	coreRequire(t, "read seed same-root identity", err)
	for index, device := range []*Manager{deviceA, deviceB, deviceC} {
		coreSynchronizeRestoredAccount(t, device)
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

	beforeTransfers := chain.broadcastCount()

	firstInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create invoice for cancel-wins ordering", err)
	firstPrepared, err := deviceA.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: firstInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare first shared-input operation", err)
	coreAssert(t, chain.broadcastCount() == beforeTransfers,
		"prepared first device broadcast before any delivery/ACK")
	coreAssertRGB11Reservation(t, deviceA, firstPrepared.State.TransferID, true)

	competitorInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create independent competing invoice", err)
	_, err = deviceB.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: competitorInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreAssert(t, err != nil, "second same-root device prepared a conflicting input")
	_, err = deviceC.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: competitorInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreAssert(t, err != nil, "third same-root device prepared a conflicting input")
	coreAssert(t, chain.broadcastCount() == beforeTransfers,
		"same-input preparation race broadcast a transaction")

	// A phone adopts the prepared transfer through the real account CAS, cancels
	// it, and releases the reservation before the computer tries its stale ID.
	coreRequire(t, "second device adopts prepared transfer", deviceB.SyncAccountManagementState(ctx))
	coreRequire(t, "second device wins cancel before broadcast",
		deviceB.CancelRGB11OutOfBandTransfer(firstPrepared.State.TransferID))
	coreAssertRGB11Reservation(t, deviceB, firstPrepared.State.TransferID, false)
	if _, err := deviceA.BroadcastRGB11OutOfBand([]string{firstPrepared.State.TransferID}); err == nil {
		t.Fatal("stale computer broadcast an intent canceled by another device")
	}
	coreAssert(t, chain.broadcastCount() == beforeTransfers,
		"stale broadcast after cancellation reached the Bitcoin backend")
	coreAssertRGB11Reservation(t, deviceA, firstPrepared.State.TransferID, false)
	coreAssertRGB11Balances(t, deviceB, receiver, issued.AssetName, 1000, 0)

	// Cancellation restores the source proof for a new direct transfer. A real
	// receiver ACK then wins the second ordering and authorizes exactly one
	// broadcast. During that backend call, device B changes account metadata and
	// commits it to the same real CoreNode; device A's final closure sync must
	// adopt that selected revision without changing the transaction intent.
	directPrepared, _, err := deviceA.PrepareConfiguredRGB11AddressTransfer(ctx,
		RGB11AddressSendRequest{ReceiverAddress: receiver.GetWallet().GetAddress(),
			AssetName: issued.AssetName, AmountRaw: "400", FeeRate: 2, MinConfirmations: 1},
		dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare direct transfer after cross-device cancellation", err)
	coreAssert(t, overlapsRGB11Outpoints(firstPrepared.State.InputOutPoints,
		directPrepared.State.InputOutPoints), "cancel did not free the source input for a new intent")
	directIntentBaseline, err := deviceA.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
	coreRequire(t, "capture direct immutable broadcast intent", err)
	directIntentBaseline = cloneRGB11BroadcastIntent(directIntentBaseline)
	delivered, err := deviceA.DeliverAndBroadcastConfiguredRGB11AddressTransfer(
		directPrepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver direct consignment before ACK", err)
	coreAssert(t, delivered.AwaitingACK && !delivered.Broadcast,
		"direct delivery crossed its positive-ACK barrier")
	coreAssert(t, chain.broadcastCount() == beforeTransfers,
		"direct delivery broadcast before receiver ACK")
	accepted, err := receiver.SyncConfiguredRGB11AddressMailbox(ctx,
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "receiver accepts and ACKs same-root direct transfer", err)
	coreAssert(t, accepted.Received == 1 && accepted.Invalid == 0,
		"receiver did not durably accept one direct consignment")
	coreAssertRGB11ACKRecoveryPublished(t, receiver, directPrepared)

	const rebasedWalletName = "RGB11 same-root CAS rebase E2E"
	backendEntered := make(chan struct{})
	backendRelease := make(chan struct{})
	var backendReleaseOnce sync.Once
	releaseBackend := func() { backendReleaseOnce.Do(func() { close(backendRelease) }) }
	pausedEvidence := &coreE2EInFlightBroadcastEvidence{
		BitcoinEvidenceProvider: chain,
		entered:                 backendEntered,
		release:                 backendRelease,
		beforeBroadcast: func() error {
			// Device C commits a metadata edit after B's broadcast-intent CAS but
			// before B reaches the local chain backend. This keeps B's same signed
			// raw transaction in flight across a newer account revision.
			if err := deviceC.UpdateWalletName(deviceC.GetAccountManagementStatus().RootWalletID, rebasedWalletName); err != nil {
				return err
			}
			if err := deviceC.SyncAccountManagementState(ctx); err != nil {
				return err
			}
			pending, loadErr := deviceC.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
			if loadErr != nil {
				return loadErr
			}
			if pending.State.WitnessTxID != directPrepared.TxID ||
				pending.State.Status != rgb11StatusBroadcastAttempted ||
				!sameRGB11BroadcastIntent(directIntentBaseline, pending) {
				return errors.New("selected account revision lost or changed the in-flight broadcast intent")
			}
			return nil
		},
	}
	previousBEvidence := deviceB.rgbManager.evidence
	deviceB.rgbManager.evidence = pausedEvidence
	defer func() {
		releaseBackend()
		deviceB.rgbManager.evidence = previousBEvidence
	}()
	type mailboxSyncAttempt struct {
		result *RGB11AddressMailboxSyncResult
		err    error
	}
	deviceBResult := make(chan mailboxSyncAttempt, 1)
	go func() {
		result, syncErr := deviceB.SyncConfiguredRGB11AddressMailbox(ctx,
			dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		deviceBResult <- mailboxSyncAttempt{result: result, err: syncErr}
	}()
	select {
	case <-backendEntered:
	case early := <-deviceBResult:
		t.Fatalf("device B did not reach the post-CAS backend boundary: result=%+v err=%v", early.result, early.err)
	case <-time.After(60 * time.Second):
		t.Fatal("device B did not reach the post-CAS backend boundary")
	}

	// A's explicit backend error cannot revoke B's already-authorized call. The
	// raw transaction remains the same and its input lock must survive until the
	// in-flight backend outcome is known.
	beforeDirectBroadcast := chain.broadcastCount()
	previousAEvidence := deviceA.rgbManager.evidence
	deviceA.rgbManager.evidence = &coreE2ERejectedBroadcastEvidence{
		BitcoinEvidenceProvider: previousAEvidence,
	}
	_, rejectedErr := deviceA.BroadcastRGB11AddressTransfer(directPrepared.State.TransferID)
	deviceA.rgbManager.evidence = previousAEvidence
	coreAssert(t, errors.Is(rejectedErr, ErrRGB11BroadcastResultUnknown),
		"device A did not preserve the signed intent after its backend rejected the in-flight transaction")
	coreAssert(t, chain.broadcastCount() == beforeDirectBroadcast,
		"device A's rejected backend call unexpectedly reached the controlled chain")
	uncertainA, err := deviceA.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
	coreRequire(t, "read device A pending after its rejected backend call", err)
	coreAssert(t, uncertainA.State.WitnessTxID == directPrepared.TxID &&
		uncertainA.State.Status == rgb11StatusBroadcastAttempted &&
		bytes.Equal(uncertainA.SignedTx, directIntentBaseline.SignedTx),
		"device A's backend error replaced or dropped the original signed transaction")
	coreAssertRGB11Reservation(t, deviceA, directPrepared.State.TransferID, true)
	competing, _, competingErr := deviceA.PrepareConfiguredRGB11AddressTransfer(ctx,
		RGB11AddressSendRequest{ReceiverAddress: receiver.GetWallet().GetAddress(),
			AssetName: issued.AssetName, AmountRaw: "1", FeeRate: 2, MinConfirmations: 1},
		dkvsindexer.RecordVerificationOptions{})
	coreAssert(t, competingErr != nil && competing == nil,
		"device A released the source input for a second transfer while device B's old raw remained in flight")
	coreAssertRGB11Reservation(t, deviceA, directPrepared.State.TransferID, true)
	if err := deviceC.CancelExpiredRGB11Transfer(directPrepared.State.TransferID); err == nil {
		t.Fatal("device C canceled the positively ACKed transaction while device B was still in the backend")
	}
	coreAssertRGB11Reservation(t, deviceC, directPrepared.State.TransferID, true)

	releaseBackend()
	deviceBOutcome := <-deviceBResult
	deviceB.rgbManager.evidence = previousBEvidence
	coreRequire(t, "device B resumes the same broadcast after A's rejection", deviceBOutcome.err)
	coreAssert(t, deviceBOutcome.result != nil && deviceBOutcome.result.ACKs >= 1,
		"device B did not consume the receiver ACK before resuming the original broadcast")
	coreAssert(t, chain.broadcastCount() == beforeDirectBroadcast+1,
		"resumed in-flight transaction was not accepted exactly once")
	txid, err := deviceA.BroadcastRGB11AddressTransfer(directPrepared.State.TransferID)
	coreRequire(t, "adopt and retry direct transfer after B's in-flight broadcast", err)
	coreAssert(t, txid == directPrepared.TxID && chain.broadcastCount() == beforeDirectBroadcast+1,
		"idempotent retry changed the original transaction or broadcast it twice")
	coreAssert(t, coreWalletName(deviceA, deviceA.GetAccountManagementStatus().RootWalletID) == rebasedWalletName,
		"sender device did not adopt metadata selected during its operation barrier")
	currentIntent, err := deviceA.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
	coreRequire(t, "read sender intent after metadata rebase", err)
	coreAssert(t, sameRGB11BroadcastIntent(directIntentBaseline, currentIntent),
		"metadata adoption changed the original immutable broadcast intent")

	// Device B's cancellation attempt is now stale and must preserve the same
	// txid and reservation. The cancel API itself performs the account sync.
	coreRequire(t, "second device observes the broadcast-selected account revision",
		deviceB.SyncAccountManagementState(ctx))
	if err := deviceB.CancelExpiredRGB11Transfer(directPrepared.State.TransferID); err == nil {
		t.Fatal("second device canceled a positively ACKed, broadcast direct transaction")
	}
	coreAssertRGB11Reservation(t, deviceB, directPrepared.State.TransferID, true)
	blockedDirect, err := deviceB.rgbManager.projectionStore.LoadPendingTransfer(directPrepared.State.TransferID)
	coreRequire(t, "read direct intent after rejected cancellation", err)
	coreAssert(t, blockedDirect.State.WitnessTxID == directPrepared.TxID &&
		blockedDirect.State.Status == "broadcast" && blockedDirect.State.AckStatus == "accepted",
		"rejected cancellation lost or rewrote the broadcast transaction identity")
	coreAssertRGB11Reservation(t, deviceB, directPrepared.State.TransferID, true)

	// Confirm and reconcile the direct transaction before the next send. Its
	// RGB change output is otherwise still at zero confirmations and correctly
	// unavailable to a MinConfirmations=1 spend.
	chain.confirm(txid)
	_, err = deviceA.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct transfer before next spend", err)
	_, err = receiver.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct receipt before next receive", err)
	coreAssertRGB11Balances(t, deviceA, receiver, issued.AssetName, 600, 400)
	coreAssert(t, chain.broadcastCount() == beforeTransfers+1, "direct settlement did not preserve exactly one effective broadcast")

	// An ambiguous OOB response still exercises the opposite ordering on the
	// same cancel API: the broadcast intent is durable before the backend call,
	// so a stale device cannot cancel or release inputs while outcome is unknown.
	secondInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create invoice for broadcast-wins ordering", err)
	secondPrepared, err := deviceA.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: secondInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare broadcast-wins OOB transfer", err)
	_, err = receiver.PrepareRGB11Consignment(ctx, secondInvoice.RequestID,
		[]byte(secondPrepared.RecipientConsignment))
	coreRequire(t, "receiver stages OOB proof before broadcast", err)
	beforeOOBBroadcast := chain.broadcastCount()
	realEvidence := deviceA.rgbManager.evidence
	deviceA.rgbManager.evidence = &coreE2EUnknownBroadcastEvidence{
		BitcoinEvidenceProvider: realEvidence,
	}
	unknownTxID, unknownErr := deviceA.BroadcastRGB11OutOfBand([]string{secondPrepared.State.TransferID})
	deviceA.rgbManager.evidence = realEvidence
	coreAssert(t, errors.Is(unknownErr, ErrRGB11BroadcastResultUnknown) &&
		unknownTxID == secondPrepared.TxID && chain.broadcastCount() == beforeOOBBroadcast,
		"unknown OOB broadcast outcome lost its txid, broadcast early, or became a normal failure")
	unknownPending, err := deviceA.rgbManager.projectionStore.LoadPendingTransfer(secondPrepared.State.TransferID)
	coreRequire(t, "read OOB intent after ambiguous broadcast", err)
	coreAssert(t, unknownPending.State.Status == rgb11StatusBroadcastAttempted &&
		rgb11TransferKeepsInputsLocked(&unknownPending.State),
		"unknown OOB broadcast did not retain the irreversible intent and input lock")
	coreRequire(t, "third device observes broadcast before cancellation",
		deviceC.SyncAccountManagementState(ctx))
	if err := deviceC.CancelRGB11OutOfBandTransfer(secondPrepared.State.TransferID); err == nil {
		t.Fatal("third device canceled an OOB transaction after broadcast became uncertain")
	}
	coreAssertRGB11Reservation(t, deviceC, secondPrepared.State.TransferID, true)
	secondTxID, err := deviceA.BroadcastRGB11OutOfBand([]string{secondPrepared.State.TransferID})
	coreRequire(t, "retry original OOB transaction after unknown response", err)
	coreAssert(t, secondTxID == secondPrepared.TxID && chain.broadcastCount() == beforeOOBBroadcast+1,
		"unknown OOB retry changed or duplicated the prepared transaction")
	_, err = receiver.AcceptRGB11Consignment(ctx, secondInvoice.RequestID,
		[]byte(secondPrepared.RecipientConsignment))
	coreRequire(t, "receiver accepts broadcast OOB consignment", err)

	chain.confirm(secondTxID)
	_, err = deviceA.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct transfer on computer", err)
	_, err = receiver.RefreshRGB11State(ctx)
	coreRequire(t, "settle direct and OOB receipts", err)
	coreRequire(t, "cold third device adopts settled sender history", deviceC.SyncAccountManagementState(ctx))
	_, err = deviceC.RefreshRGB11State(ctx)
	coreRequire(t, "refresh cold third-device ownership", err)
	remaining, err := deviceC.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read third-device balance after settlement", err)
	coreAssert(t, coreRGB11BalanceEquals(remaining, 599),
		"cold third device did not recover the exact remaining sender allocation")
	coldReceiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, coldReceiver)
	_, err = coldReceiver.RefreshRGB11State(ctx)
	coreRequire(t, "refresh cold receiver after ACK and broadcast history", err)
	priorReceived, err := coldReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold receiver's prior accepted allocation", err)
	coreAssert(t, coreRGB11BalanceEquals(priorReceived, 401),
		"cold receiver did not recover the accepted direct and OOB allocations")

	beforeFinalSend := chain.broadcastCount()
	finalPrepared := coreSendRGB11Amount(t, chain, deviceC, coldReceiver, issued.AssetName, "599",
		beforeFinalSend, 0, 1000, nil)
	coreAssert(t, finalPrepared != nil && finalPrepared.TxID != "",
		"cold third device did not create a new signed transaction")
	spent, err := deviceC.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read third-device balance after its recovered spend", err)
	received, err := coldReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read recipient after third-device recovered spend", err)
	coreAssert(t, coreRGB11BalanceIsZero(spent) && coreRGB11BalanceEquals(received, 1000),
		"same-root device recovery and re-spend did not preserve asset conservation")
	coreRequire(t, "synchronize original recipient after cold-device send", receiver.SyncAccountManagementState(ctx))
	_, err = receiver.RefreshRGB11State(ctx)
	coreRequire(t, "refresh original recipient after cold-device send", err)
	originalReceiverBalance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read original recipient after cold-device send", err)
	coreAssert(t, originalReceiverBalance.Value.Uint64() == 1000,
		"cold receiver's acknowledged send did not converge to the original recipient device")
	coreRequire(t, "synchronize remaining same-root devices after final spend", deviceB.SyncAccountManagementState(ctx))
	coreRequire(t, "synchronize original seed manager after multi-device lifecycle",
		seed.SyncAccountManagementState(ctx))
	coreAssert(t, chain.broadcastCount() == beforeTransfers+3,
		"cancel, sync, or recovery caused an unexpected extra broadcast")

	for _, device := range []*Manager{deviceA, deviceB, deviceC} {
		coreAssert(t, coreWalletName(device, device.GetAccountManagementStatus().RootWalletID) == rebasedWalletName,
			"same-root metadata did not converge across all recovered devices")
	}
	currentSeedRoot, err := seed.RootAccountID()
	coreRequire(t, "read seed root after multi-device lifecycle", err)
	coreAssert(t, currentSeedRoot == seedRoot, "seed device changed managed root during multi-device recovery")
}

// coreRGB11SameRootCASLoserE2E isolates the real account-CAS loser lifecycle
// from the broader three-device flow. It starts three distinct root-wallet
// databases at one account revision, makes two private signed prepares reach
// the real CoreNode CAS together, and records whether the losing device can
// explicitly cancel its never-delivered intent and continue.
func coreRGB11SameRootCASLoserE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	seed, receiver *Manager, senderMaterial *coreRecoveryMaterial) {
	t.Helper()
	ctx := context.Background()
	deviceA := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, deviceA)
	rootID, err := seed.RootAccountID()
	coreRequire(t, "read R09 root wallet identity", err)
	deviceRoot, err := deviceA.RootAccountID()
	coreRequire(t, "read first R09 device root identity", err)
	coreAssert(t, deviceRoot == rootID, "first R09 device restored a different root wallet")
	chain.fund(t, deviceA.GetWallet().GetAddress(), 6)
	issued, err := deviceA.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ER", Name: "E2E real-CAS loser asset", Amounts: []uint64{1000},
	})
	coreRequire(t, "issue isolated R09 root-wallet NIA", err)
	coreCheckpoint(t, "R09_same_root_issue_authoritative", deviceA, cfg, chain, senderMaterial)

	deviceB := coreRestoreE2E(t, cfg, chain, senderMaterial)
	deviceC := coreRestoreE2E(t, cfg, chain, senderMaterial)
	devices := []*Manager{deviceA, deviceB, deviceC}
	for index, device := range devices {
		coreSynchronizeRestoredAccount(t, device)
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

	baselineRevision := deviceA.GetAccountManagementStatus().ManagedDataRevision
	coreAssert(t, baselineRevision != 0 &&
		deviceB.GetAccountManagementStatus().ManagedDataRevision == baselineRevision &&
		deviceC.GetAccountManagementStatus().ManagedDataRevision == baselineRevision,
		"three R09 root devices did not start from one account-CAS baseline")
	invoices := make([]string, 2)
	for index := range invoices {
		invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
			AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreRequire(t, "create independent R09 out-of-band witness invoice", err)
		invoices[index] = invoice.Invoice
	}

	beforeBroadcast := chain.broadcastCount()
	type prepareAttempt struct {
		device   *Manager
		invoice  string
		prepared *RGB11PreparedTransfer
		err      error
	}
	casArrivals := make(chan struct{}, 2)
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
	defer func() {
		releaseCAS()
		restoreTransports()
	}()
	for _, device := range []*Manager{deviceB, deviceC} {
		store, err := device.accountDKVSStore()
		coreRequire(t, "open R09 account client for real CAS race", err)
		transport := store.client.Http
		priorHTTP[device] = transport
		gate := &coreE2EDirtyCASBarrierHTTP{
			HttpClient: transport, owner: device, arrivals: casArrivals, release: casRelease,
		}
		device.http = gate
		device.dkvs.mu.Lock()
		for _, client := range device.dkvs.clients {
			client.Http = gate
		}
		device.dkvs.mu.Unlock()
	}
	startRace := make(chan struct{})
	raceResults := make(chan prepareAttempt, 2)
	for index, device := range []*Manager{deviceB, deviceC} {
		attempt := prepareAttempt{device: device, invoice: invoices[index]}
		go func() {
			<-startRace
			prepared, prepareErr := attempt.device.PrepareRGB11Transfer(ctx, RGB11SendRequest{
				Invoice: attempt.invoice, FeeRate: 2, MinConfirmations: 1,
			})
			attempt.prepared, attempt.err = prepared, prepareErr
			raceResults <- attempt
		}()
	}
	close(startRace)
	for index := 0; index < 2; index++ {
		select {
		case <-casArrivals:
		case <-time.After(60 * time.Second):
			releaseCAS()
			t.Fatal("core-e2e: both R09 local prepares did not reach the real account CAS barrier")
		}
	}
	releaseCAS()
	firstAttempt, secondAttempt := <-raceResults, <-raceResults
	restoreTransports()
	var winner *RGB11PreparedTransfer
	var winnerDevice *Manager
	var loser *prepareAttempt
	for _, attempt := range []prepareAttempt{firstAttempt, secondAttempt} {
		if attempt.err == nil && attempt.prepared != nil {
			if winner != nil {
				t.Fatal("core-e2e: two same-baseline R09 devices committed conflicting spends")
			}
			winner, winnerDevice = attempt.prepared, attempt.device
		} else if attempt.prepared != nil {
			t.Fatal("core-e2e: failed R09 prepare returned a public prepared transfer")
		} else {
			losingAttempt := attempt
			loser = &losingAttempt
		}
	}
	coreAssert(t, winner != nil && loser != nil &&
		((firstAttempt.err == nil) != (secondAttempt.err == nil)),
		"real R09 account CAS did not select exactly one same-baseline prepare")
	if winner == nil || loser == nil || winner.State == nil {
		t.Fatal("core-e2e: real R09 CAS race did not produce a winner and loser")
	}
	coreAssert(t, chain.broadcastCount() == beforeBroadcast,
		"R09 private prepares broadcast before delivery or ACK")
	winnerPending, err := winnerDevice.rgbManager.projectionStore.LoadPendingTransfer(winner.State.TransferID)
	coreRequire(t, "load successful R09 prepare's private signed transaction", err)
	coreAssert(t, len(winnerPending.SignedTx) != 0 &&
		winnerPending.State.WitnessTxID == winner.State.WitnessTxID,
		"successful R09 CAS winner did not retain its signed private pending")
	coreAssertRGB11Reservation(t, winnerDevice, winner.State.TransferID, true)

	loserState, err := loser.device.GetRGB11State()
	coreRequire(t, "inspect actual R09 CAS-loser private state", err)
	var losingTransfer *rgb11wallet.TransferState
	for _, transfer := range loserState.Transfers {
		if transfer != nil && transfer.Invoice == loser.invoice && transfer.Direction == "send" {
			losingTransfer = transfer
			break
		}
	}
	coreAssert(t, losingTransfer != nil && losingTransfer.Status == "prepared" && losingTransfer.WitnessTxID != "",
		"R09 CAS loser did not persist its private intent before the real account CAS")
	if losingTransfer == nil {
		t.Fatal("core-e2e: R09 CAS failed before the losing local intent existed")
	}
	losingPending, err := loser.device.rgbManager.projectionStore.LoadPendingTransfer(losingTransfer.TransferID)
	coreRequire(t, "load exact private R09 losing transaction", err)
	coreAssert(t, len(losingPending.SignedTx) != 0 && len(losingPending.RecipientConsignment) != 0 &&
		losingPending.State.WitnessTxID == losingTransfer.WitnessTxID,
		"R09 failed CAS did not retain the exact private signed pending")
	coreAssertRGB11Reservation(t, loser.device, losingTransfer.TransferID, true)

	remoteBeforeWinnerCancel := coreManagedRemoteRecords(t, deviceA, cfg)
	if err := loser.device.CancelRGB11OutOfBandTransfer(losingTransfer.TransferID); err == nil {
		t.Fatal("private loser cancellation released inputs still selected by an active remote winner")
	}
	coreAssertRGB11Reservation(t, loser.device, losingTransfer.TransferID, true)
	coreAssertManagedRemoteEqual(t, "R09 private cancel while remote winner is active", remoteBeforeWinnerCancel,
		coreManagedRemoteRecords(t, deviceA, cfg))

	// Use a third independent database to adopt and explicitly cancel the
	// selected winner. This leaves the authoritative input free while the losing
	// device still holds its never-exposed private signed intent.
	coreRequire(t, "third R09 device adopts selected winner", deviceA.SyncAccountManagementState(ctx))
	coreRequire(t, "third R09 device cancels selected winner", deviceA.CancelRGB11OutOfBandTransfer(winner.State.TransferID))
	coreAssertRGB11Reservation(t, deviceA, winner.State.TransferID, false)
	remoteAfterWinnerCancel := coreManagedRemoteRecords(t, deviceA, cfg)

	// The initial Sync is expected to stop at the selected account payload,
	// because it omits the local-only intent. The explicit public Cancel must
	// still provide a safe way to discard an intent which has never been shared.
	syncErr := loser.device.SyncAccountManagementState(ctx)
	currentPending, loadErr := loser.device.rgbManager.projectionStore.LoadPendingTransfer(losingTransfer.TransferID)
	coreRequire(t, "reload R09 losing private pending after account sync", loadErr)
	coreAssert(t, bytes.Equal(currentPending.SignedTx, losingPending.SignedTx) &&
		currentPending.State.WitnessTxID == losingPending.State.WitnessTxID,
		"R09 failed sync changed the losing transaction identity")
	coreAssertRGB11Reservation(t, loser.device, losingTransfer.TransferID, true)
	cancelErr := loser.device.CancelRGB11OutOfBandTransfer(losingTransfer.TransferID)
	if cancelErr != nil {
		coreAssert(t, syncErr != nil, "R09 selected-state sync succeeded but explicit cancellation failed")
		retained, loadErr := loser.device.rgbManager.projectionStore.LoadPendingTransfer(losingTransfer.TransferID)
		coreRequire(t, "reload R09 pending after failed explicit cancellation", loadErr)
		coreAssert(t, bytes.Equal(retained.SignedTx, losingPending.SignedTx) &&
			retained.State.WitnessTxID == losingPending.State.WitnessTxID,
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
	}
	coreRequire(t, "parent sender adopts clean R09 account after loser attempt",
		seed.SyncAccountManagementState(ctx))
}

// coreRGB11StableOnlyColdRestoreE2E puts both a valid stale RGB active cache
// and a corrupt cache with a valid old-style application ID in the real
// CoreNode mailbox. Two new databases must recover only the current stable
// account bundle; one then spends the recovered allocation through Direct.
func coreRGB11StableOnlyColdRestoreE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	ctx := context.Background()
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
	coreRequire(t, "cancel never-shared stale-cache transaction",
		source.CancelRGB11OutOfBandTransfer(oldPrepared.State.TransferID))
	cancelled, err := source.rgbManager.projectionStore.LoadPendingTransfer(oldPrepared.State.TransferID)
	coreRequire(t, "read canceled stale-cache transaction", err)
	coreAssert(t, cancelled.State.Status == "rejected" && cancelled.State.AckStatus == "rejected" &&
		cancelled.State.RejectReason == RGB11RejectReasonUser && cancelled.State.WitnessTxID == oldPrepared.TxID &&
		cancelled.State.ConsignmentHash == oldPrepared.State.ConsignmentHash &&
		equalRGB11StringSet(cancelled.State.InputOutPoints, oldPrepared.State.InputOutPoints),
		"source did not durably cancel the original stale-cache transaction")
	coreAssertRGB11Reservation(t, source, oldPrepared.State.TransferID, false)

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
	stableBeforeCache := coreRemoteStableRGB(t, source, cfg)
	localBeforeCache := coreLocalRGBBundleDigests(t, source)
	remoteRecordsBeforeCache := coreManagedRemoteRecords(t, source, cfg)

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
	coldA := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coldB := coreRestoreE2E(t, cfg, chain, senderMaterial)
	for _, cold := range []*Manager{coldA, coldB} {
		if source.db == cold.db || coldA.db == coldB.db {
			t.Fatal("core-e2e: stable-only cache recovery did not use independent databases")
		}
		beforeColdSync := chain.broadcastCount()
		coreRequire(t, "cold same-root cache recovery sync", cold.SyncAccountManagementState(ctx))
		_, err = cold.RefreshRGB11State(ctx)
		coreRequire(t, "freshly reconcile cold same-root cache recovery", err)
		coreAssert(t, coreDigest(coreCanonicalCatalog(source)) == coreDigest(coreCanonicalCatalog(cold)),
			"stable-only cold recovery changed the account catalog")
		coreAssertMapEqual(t, "stable-only cold recovery bundle", localBeforeCache,
			coreLocalRGBBundleDigests(t, cold))
		coreAssertMapEqual(t, "stable-only cold recovery authoritative bundle", stableBeforeCache,
			coreRemoteStableRGB(t, cold, cfg))
		coreAssert(t, coreDigest(coreRGBSemantic(t, source)) == coreDigest(coreRGBSemantic(t, cold)),
			"valid or corrupt legacy active cache changed recovered ownership, spendability, phase or lock")
		staleRecovered, err := cold.rgbManager.projectionStore.LoadPendingTransfer(oldPrepared.State.TransferID)
		coreRequire(t, "load stale transaction after cold recovery", err)
		coreAssert(t, staleRecovered.State.Status == "rejected" && staleRecovered.State.AckStatus == "rejected" &&
			staleRecovered.State.RejectReason == RGB11RejectReasonUser &&
			staleRecovered.State.WitnessTxID == oldPrepared.TxID &&
			staleRecovered.State.ConsignmentHash == oldPrepared.State.ConsignmentHash &&
			equalRGB11StringSet(staleRecovered.State.InputOutPoints, oldPrepared.State.InputOutPoints),
			"cold recovery resurrected a stale active cache over the selected canceled phase")
		coreAssertRGB11Reservation(t, cold, oldPrepared.State.TransferID, false)
		coreAssert(t, chain.broadcastCount() == beforeColdSync,
			"cold stable-only cache recovery broadcast a transaction")
	}
	// The first independent cold database proves the recovered stable output is
	// actually spendable after it ignores both mailbox entries.
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
}

func coreAssertRGB11Reservation(t *testing.T, manager *Manager, transferID string, wantLocked bool) {
	t.Helper()
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	coreRequire(t, "load same-root transfer reservation", err)
	coreAssert(t, len(pending.State.InputOutPoints) > 0,
		"transfer closure omitted the original source input identity")
	locks := manager.utxoLockerL1.GetLockedUtxoList()
	for _, outpoint := range pending.State.InputOutPoints {
		lock := locks[outpoint]
		if wantLocked {
			coreAssert(t, lock != nil && lock.ReservationID == pending.ReservationID,
				"pending transfer lost its original input reservation")
		} else {
			coreAssert(t, lock == nil || lock.ReservationID != pending.ReservationID,
				"cancelled transfer still owns an input reservation")
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

// coreE2EDirtyCASBarrierHTTP pauses only dirty account-bundle CAS requests,
// then passes them through to the real CoreNode. This makes both independent
// local prepares reach the authoritative CAS with their signed private
// pendings already persisted, without substituting a fake CAS implementation.
type coreE2EDirtyCASBarrierHTTP struct {
	HttpClient
	owner    *Manager
	arrivals chan<- struct{}
	release  <-chan struct{}
	once     sync.Once
}

func (h *coreE2EDirtyCASBarrierHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	if h != nil && h.owner != nil && url != nil &&
		strings.HasSuffix(url.Path, "/v3/dkvs/records/batch-cas") {
		h.owner.mutex.RLock()
		dirty := h.owner.accountProfile != nil && h.owner.accountProfile.ManagedDataDirty
		h.owner.mutex.RUnlock()
		if dirty {
			h.once.Do(func() {
				h.arrivals <- struct{}{}
				<-h.release
			})
		}
	}
	return h.HttpClient.SendPostRequest(url, body)
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

// This wrapper pauses only the local controlled Bitcoin broadcast boundary.
// All account state/blob CAS calls remain real requests to the configured
// CoreNode; the wrapped evidence provider is the existing CoreModules fixture.
type coreE2EInFlightBroadcastEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	beforeBroadcast func() error
	entered         chan struct{}
	release         <-chan struct{}
	enterErr        error
	once            sync.Once
}

func (e *coreE2EInFlightBroadcastEvidence) Broadcast(raw []byte) (string, error) {
	e.once.Do(func() {
		if e.beforeBroadcast != nil {
			e.enterErr = e.beforeBroadcast()
		}
		if e.enterErr == nil && e.entered != nil {
			close(e.entered)
		}
	})
	if e.enterErr != nil {
		return "", e.enterErr
	}
	if e.release != nil {
		<-e.release
	}
	return e.BitcoinEvidenceProvider.Broadcast(raw)
}

type coreE2ERejectedBroadcastEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
}

func (*coreE2ERejectedBroadcastEvidence) Broadcast([]byte) (string, error) {
	return "", errors.New("deterministic local backend rejection while peer is in flight")
}
