package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// coreRGBRecoveryCapacityBoundaryE2E exercises real cross-scope recovery
// material through the connected CoreModules/DKVS harness. Capacity noise is
// only a deterministic boundary fault-injection provider; the RGB material,
// ACK, proof, and transaction use the real native path.
func coreRGBRecoveryCapacityBoundaryE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, sender, receiver *Manager,
	senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	_ = receiverMaterial // The second scope is created on this receiver during the case.
	t.Run("R21_full_bundle_prepare_and_cross_scope_invoice_capacity", func(t *testing.T) {
		proxy := &rgb11ProxyTestServer{}
		proxyHTTP := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
		defer proxyHTTP.Close()
		proxyEndpoint := "rpc://" + strings.TrimPrefix(proxyHTTP.URL, "http://")

		issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
			Schema: "NIA", Ticker: "E2EC", Name: "E2E capacity proof asset", Amounts: []uint64{777},
		})
		coreRequire(t, "issue capacity-boundary RGB11 asset", err)
		coreCheckpoint(t, "R21_nonempty_RGB11_baseline", sender, cfg, chain, senderMaterial)
		coreLogRGB11AccountPackageSize(t, "R21_real_nonempty_baseline", sender)

		exported, err := sender.ExportRGB11Contract(issued.ContractID)
		coreRequire(t, "export capacity-boundary contract", err)
		contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
		coreRequire(t, "decode capacity-boundary contract", err)
		_, err = receiver.ImportRGB11ContractFile(context.Background(), contract)
		coreRequire(t, "import contract into receiver root account", err)

		rootID := receiver.GetAccountManagementStatus().RootWalletID
		coreRequire(t, "create receiver account scope 1", receiver.EnsureAccount(rootID, 1,
			"Capacity receive scope", "did:capacity:receive"))
		coreRequire(t, "publish receiver's expanded account catalog",
			receiver.SyncAccountManagementState(context.Background()))
		coreRequire(t, "select receiver account scope 1", receiver.SwitchAccount(1))
		_, err = receiver.ImportRGB11ContractFile(context.Background(), contract)
		coreRequire(t, "import contract into receiver account scope 1", err)
		coreRequire(t, "select receiver root account", receiver.SwitchAccount(0))
		capacityInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
			AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreRequire(t, "create receiver invoice for capacity-bound prepare", err)
		coreAssert(t, capacityInvoice != nil, "receiver capacity invoice was not created")

		coreRequire(t, "select receiver account scope 1 for standard receive", receiver.SwitchAccount(1))
		receiveInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: RGB11ProxyTransport,
			TransportEndpoints: []string{proxyEndpoint}, ContractID: issued.ContractID,
			AmountRaw: "100", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreRequire(t, "create Standard witness invoice in receiver scope 1", err)
		coreAssert(t, receiveInvoice != nil && receiveInvoice.RequestID != "" && receiveInvoice.Invoice != "",
			"receiver scope 1 did not create a durable Standard witness invoice")

		beforeBroadcast := chain.broadcastCount()
		prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
			Invoice: receiveInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
		})
		coreRequire(t, "prepare cross-scope Standard witness transfer", err)
		coreAssert(t, prepared != nil && prepared.State != nil && len(prepared.State.InputOutPoints) != 0,
			"cross-scope transfer omitted its real input/operation identity")
		coreAssert(t, chain.broadcastCount() == beforeBroadcast,
			"cross-scope prepare broadcast before receiver accepted the consignment")
		delivery, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(
			context.Background(), []string{prepared.State.TransferID})
		coreRequire(t, "deliver cross-scope Standard witness consignment", err)
		coreAssert(t, delivery.AwaitingACK && !delivery.Broadcast,
			"cross-scope delivery crossed the positive ACK barrier")
		received, err := receiver.ReceiveRGB11ProxyConsignment(context.Background(), receiveInvoice.RequestID)
		coreRequire(t, "receiver accepts cross-scope Standard consignment and publishes ACK", err)
		coreAssert(t, received.AckPosted && received.AwaitingBroadcast && received.TxID == prepared.TxID,
			"receiver did not durably ACK exactly one cross-scope consignment before broadcast")
		coreAssertRGB11ACKRecoveryPublished(t, receiver, prepared)
		coreAssert(t, received.Receipt != nil, "Proxy ACK omitted the exact received consignment receipt")
		receiveHash := received.Receipt.ConsignmentHash
		coreAssert(t, chain.broadcastCount() == beforeBroadcast,
			"receiver ACK caused an early transaction broadcast")
		delivery, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(
			context.Background(), []string{prepared.State.TransferID})
		coreRequire(t, "sender adopts the positive ACK and broadcasts once", err)
		coreAssert(t, delivery.Broadcast && delivery.TxID == prepared.TxID,
			"sender did not broadcast the acknowledged original transaction")
		coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
			"positive ACK did not authorize exactly one original transaction")
		chain.confirm(prepared.TxID)
		_, err = sender.RefreshRGB11State(context.Background())
		coreRequire(t, "settle sender side before prepare capacity case", err)
		coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
			"sender refresh unexpectedly rebroadcast a transaction")

		// R21: an already-published nonempty RGB history cannot sign another
		// transfer whose final full-account bundle is at the exact wire limit.
		senderRGBBefore := coreLocalRGBBundleDigests(t, sender)
		cleanupSenderCapacity := coreInstallCapacityProviderAtWireBoundary(t, sender, cfg)
		senderAtBoundary := coreManagedRemoteRecords(t, sender, cfg)
		senderProfileAtBoundary := coreCaptureManagedProfile(sender)
		coreAssert(t, !senderProfileAtBoundary.dirty && senderProfileAtBoundary.revision > 0 &&
			senderProfileAtBoundary.hash != "", "sender capacity baseline is not a confirmed account package")
		senderSemanticBefore := coreDigest(coreRGBSemantic(t, sender))
		senderBroadcasts := chain.broadcastCount()
		_, createInvoiceErr := sender.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
			AmountRaw:   "1",
			WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreAssertCapacityRejected(t, createInvoiceErr,
			"new invoice at the exact account capacity boundary")
		_, prepareErr := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
			Invoice: capacityInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
		})
		coreAssertCapacityRejected(t, prepareErr,
			"new send prepare at the exact account capacity boundary")
		coreAssert(t, chain.broadcastCount() == senderBroadcasts,
			"capacity-rejected invoice/prepare caused a transaction broadcast")
		coreAssert(t, coreDigest(coreRGBSemantic(t, sender)) == senderSemanticBefore,
			"capacity-rejected invoice/prepare changed sender proof, task, invoice, or lock state")
		coreAssertMapEqual(t, "sender local recovery package after R21 rejection",
			senderRGBBefore, coreLocalRGBBundleDigests(t, sender))
		coreAssertManagedRemoteEqual(t, "sender remote records after R21 rejection",
			senderAtBoundary, coreManagedRemoteRecords(t, sender, cfg))
		coreAssertManagedProfileUnchanged(t, "sender account metadata after R21 rejection",
			senderProfileAtBoundary, coreCaptureManagedProfile(sender))
		if err := cleanupSenderCapacity(); err != nil {
			t.Fatalf("remove sender capacity fault provider: %v", err)
		}

		// The receiver's scope 1 contains the accepted request and prepared
		// consignment. Sizing a new invoice in scope 0 must include this known
		// future receipt/proof/output/contract-reference closure.
		receiverCatalog, err := receiver.accountManagedDataCatalog()
		coreRequire(t, "enumerate receiver scopes for R21/R22", err)
		rootID = receiver.GetAccountManagementStatus().RootWalletID
		var rootScope0, rootScope1 AccountManagedDataScope
		for _, scope := range receiverCatalog.Scopes {
			if scope.WalletID == rootID && scope.AccountIndex == 0 {
				rootScope0 = scope
			}
			if scope.WalletID == rootID && scope.AccountIndex == 1 {
				rootScope1 = scope
			}
		}
		coreAssert(t, rootScope0.ID() != "" && rootScope1.ID() != "" && rootScope0.ID() != rootScope1.ID(),
			"receiver did not retain two distinct root-account scopes")
		receiverProvider := &rgb11AccountManagedDataProvider{owner: receiver}
		receiverAccounts, err := receiverProvider.accountsByScope()
		coreRequire(t, "enumerate receiver local RGB11 scopes", err)
		targetScopeManager, err := receiver.newScopedRGB11Manager(receiverAccounts[rootScope0.ID()])
		coreRequire(t, "open receiver target scope 0", err)
		targetWalletID, err := targetScopeManager.RGB11WalletID()
		coreRequire(t, "resolve receiver scope 0 wallet", err)
		targetSnapshot, _, err := targetScopeManager.exportRGB11WalletSnapshot(targetWalletID)
		coreRequire(t, "export target scope 0 candidate baseline", err)
		withoutPreviewBundle, err := coreNextManagedBundle(receiver, receiverCatalog)
		coreRequire(t, "build receiver no-preview baseline bundle", err)
		withoutPreview, err := coreMeasureManagedBundle(t, receiver, withoutPreviewBundle)
		coreRequire(t, "measure receiver no-preview baseline", err)
		withPreview, err := receiver.preflightRGB11TerminalRecovery(rootScope0.ID(), targetSnapshot)
		coreRequire(t, "size cross-scope known receive terminal closure", err)
		// The high-entropy capacity provider disables outer AMD compression.
		// Its exact ADE growth is therefore the canonical AMD byte delta.
		previewDelta := withPreview.AMDBytes - withoutPreview.AMDBytes
		coreAssert(t, previewDelta > 0,
			"cross-scope accepted receive did not add measurable future proof/receipt/output bytes")
		coreAssert(t, withoutPreview.DKB1Bytes < swire.MaxDKVSBlobValueSize,
			"receiver ACK baseline is already beyond the DKB1 limit")
		t.Logf("core-e2e: cross-scope capacity preview baseline_RPK_raw=%d baseline_RPK_deflate=%d baseline_AMD=%d baseline_ADE=%d baseline_DKB1=%d preview_RPK_raw=%d preview_RPK_deflate=%d preview_AMD=%d preview_ADE=%d preview_DKB1=%d preview_delta_DKB1=%d",
			withoutPreview.RPKRawBytes, withoutPreview.RPKEncodedBytes, withoutPreview.AMDBytes,
			withoutPreview.ADEBytes, withoutPreview.DKB1Bytes, withPreview.RPKRawBytes,
			withPreview.RPKEncodedBytes, withPreview.AMDBytes, withPreview.ADEBytes,
			withPreview.DKB1Bytes, previewDelta)

		targetADE := swire.MaxDKVSBlobValueSize - 8 - previewDelta/2
		if targetADE <= 0 || targetADE >= swire.MaxDKVSBlobValueSize-8 {
			t.Fatalf("invalid cross-scope capacity target ADE=%d preview delta=%d", targetADE, previewDelta)
		}
		cleanupReceiverCapacity := coreInstallCapacityProviderAtEnvelopeTarget(t, receiver, cfg,
			targetADE, withoutPreviewBundle)
		receiverAtBoundary := coreManagedRemoteRecords(t, receiver, cfg)
		receiverProfileAtBoundary := coreCaptureManagedProfile(receiver)
		coreAssert(t, receiverProfileAtBoundary.envelopeBytes == targetADE,
			"receiver stable ACK baseline did not reach calibrated ADE boundary")
		coreAssert(t, !receiverProfileAtBoundary.dirty && receiverProfileAtBoundary.revision > 0 &&
			receiverProfileAtBoundary.hash != "", "receiver capacity baseline is not a confirmed ACK package")
		capacityInvoicesBefore := coreLocalRGBBundleDigests(t, receiver)
		coreRequire(t, "select receiver target scope 0", receiver.SwitchAccount(0))
		_, invoiceErr := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
			AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreAssertCapacityRejected(t, invoiceErr,
			"new scope-0 invoice while scope-1 ACK has known terminal recovery growth")
		coreAssertMapEqual(t, "receiver local recovery package after R21 invoice rejection",
			capacityInvoicesBefore, coreLocalRGBBundleDigests(t, receiver))
		coreAssertManagedRemoteEqual(t, "receiver remote ACK bundle after R21 invoice rejection",
			receiverAtBoundary, coreManagedRemoteRecords(t, receiver, cfg))
		coreAssertManagedProfileUnchanged(t, "receiver account metadata after R21 invoice rejection",
			receiverProfileAtBoundary, coreCaptureManagedProfile(receiver))

		// R22: the confirmed receive proof is materialized locally, but its
		// final account CAS is forced over the limit by later provider growth.
		// The last accepted ACK bundle and revision remain authoritative.
		coreRequire(t, "return to receiver scope 1 for chain reconciliation", receiver.SwitchAccount(1))
		beforeLateProofBroadcasts := chain.broadcastCount()
		_, refreshErr := receiver.RefreshRGB11State(context.Background())
		coreAssertCapacityRejected(t, refreshErr, "late receive proof publication after ACK")
		coreAssert(t, chain.broadcastCount() == beforeLateProofBroadcasts,
			"late proof capacity failure caused an additional broadcast")
		lateState, err := receiver.GetRGB11State()
		coreRequire(t, "read local receive proof after R22 capacity rejection", err)
		foundLocalProof, foundReceipt := false, false
		for _, proof := range lateState.Proofs {
			if proof != nil && proof.ConsignmentHash == receiveHash && rgb11ProofIsCurrent(proof) {
				foundLocalProof = true
			}
		}
		localPayloads, err := receiverProvider.Export(receiverCatalog)
		coreRequire(t, "export local receiver closure after R22 rejection", err)
		for _, payload := range localPayloads {
			packageValue, decodeErr := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
			coreRequire(t, "decode local receiver closure after R22 rejection", decodeErr)
			for _, record := range packageValue.ProjectionRecords {
				if record.Key == "validation-"+receiveHash {
					foundReceipt = true
				}
			}
		}
		coreAssert(t, foundLocalProof && foundReceipt,
			"R22 overflow discarded the locally validated receive proof/receipt")
		receiverProfileAfter := coreCaptureManagedProfile(receiver)
		coreAssert(t, receiverProfileAfter.dirty &&
			receiverProfileAfter.revision == receiverProfileAtBoundary.revision &&
			receiverProfileAfter.hash == receiverProfileAtBoundary.hash &&
			bytes.Equal(receiverProfileAfter.envelope, receiverProfileAtBoundary.envelope),
			"R22 overflow cleared dirty state or advanced/replaced the last confirmed account package")
		coreAssertManagedRemoteEqual(t, "remote records after R22 late proof rejection",
			receiverAtBoundary, coreManagedRemoteRecords(t, receiver, cfg))
		if err := cleanupReceiverCapacity(); err != nil {
			t.Fatalf("remove receiver capacity fault provider: %v", err)
		}
		coreVerifyPaidRecords(t, receiver, cfg)
	})
	t.Run("R21_blind_proxy_terminal_preview_blocks_positive_ack", func(t *testing.T) {
		coreRGB11BlindCapacityBoundaryE2E(t, cfg, chain, senderMaterial, receiverMaterial)
	})
}

func coreRGB11BlindCapacityBoundaryE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	proxy := &rgb11ProxyTestServer{}
	proxyHTTP := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
	defer proxyHTTP.Close()
	proxyEndpoint := "rpc://" + strings.TrimPrefix(proxyHTTP.URL, "http://")

	sender := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, sender)
	receiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, receiver)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EBL", Name: "E2E blind capacity asset", Amounts: []uint64{777},
	})
	coreRequire(t, "issue blind-capacity RGB11 asset", err)
	coreCheckpoint(t, "R21_blind_capacity_nonempty_sender", sender, cfg, chain, senderMaterial)
	exported, err := sender.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export blind-capacity contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode blind-capacity contract", err)
	_, err = receiver.ImportRGB11ContractFile(context.Background(), contract)
	coreRequire(t, "import blind-capacity contract", err)

	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "blind", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{proxyEndpoint}, ContractID: issued.ContractID,
		AmountRaw: "100", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create Standard blind proxy invoice", err)
	coreAssert(t, invoice != nil && invoice.RequestID != "" && invoice.Invoice != "",
		"Standard blind invoice did not become available after stable publication")
	invoiceValue, err := invoicing.Parse(invoice.Invoice)
	coreRequire(t, "parse Standard blind proxy invoice", err)
	coreAssert(t, invoiceValue.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal,
		"capacity fixture did not create a blinded-seal beneficiary")

	beforeBroadcast := chain.broadcastCount()
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare sender transfer to blind invoice", err)
	coreAssert(t, prepared != nil && prepared.State != nil && prepared.State.TransferID != "" &&
		prepared.TxID != "", "blind proxy prepare omitted the real operation identity")
	delivery, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	coreRequire(t, "deliver blind consignment before broadcast", err)
	coreAssert(t, delivery.AwaitingACK && !delivery.Broadcast && chain.broadcastCount() == beforeBroadcast,
		"blind transaction broadcast before the receiver ACK")

	proxy.mu.Lock()
	raw := append([]byte(nil), proxy.consignment...)
	remoteTxID := proxy.txID
	remoteVout := proxy.vout
	proxy.mu.Unlock()
	coreAssert(t, len(raw) != 0 && remoteTxID == prepared.TxID,
		"proxy did not retain the exact prepared blind consignment and witness identity")

	catalog, err := receiver.accountManagedDataCatalog()
	coreRequire(t, "enumerate blind receiver account scopes", err)
	rootID := receiver.GetAccountManagementStatus().RootWalletID
	var targetScope AccountManagedDataScope
	for _, scope := range catalog.Scopes {
		if scope.WalletID == rootID && scope.AccountIndex == 0 {
			targetScope = scope
			break
		}
	}
	coreAssert(t, targetScope.ID() != "", "blind receiver root scope is not catalogued")
	walletID, err := receiver.rgbManager.RGB11WalletID()
	coreRequire(t, "resolve blind receiver RGB11 wallet", err)
	snapshot, _, err := receiver.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "export invoice-only blind receiver snapshot", err)
	candidate, _, err := receiver.rgbManager.buildRGB11PreparedReceiveCandidate(
		context.Background(), snapshot, invoice.RequestID, raw, remoteTxID, remoteVout, "")
	coreRequire(t, "construct exact blind prepared-receive candidate for sizing", err)
	request, err := rgb11ReceiveRequestFromSnapshot(candidate.EngineRecords, invoice.RequestID)
	coreRequire(t, "read blinded request reveal from candidate", err)
	coreAssert(t, request.Mode == corewallet.ReceiveBlind && request.Seal.TxID != nil,
		"prepared candidate omitted its persisted blind receive reveal")
	transfers, err := rgb11wallet.TransfersFromProjectionSnapshot(candidate.ProjectionRecords)
	coreRequire(t, "read blind prepared receive lifecycle for sizing", err)
	var receiveState *rgb11wallet.TransferState
	for _, state := range transfers {
		if state != nil && state.Direction == "receive" && state.ReceiveRequestID == invoice.RequestID {
			receiveState = state
			break
		}
	}
	coreAssert(t, receiveState != nil && receiveState.WitnessTxID == prepared.TxID &&
		len(receiveState.OutputOutPoints) == 1,
		"prepared blind receive did not retain its transition witness and one carrier")
	carrierTxID := strings.SplitN(receiveState.OutputOutPoints[0], ":", 2)[0]
	coreAssert(t, carrierTxID != "" && carrierTxID != receiveState.WitnessTxID,
		"fixture did not separate blinded carrier producer from RGB transition witness")
	coreAssertRGB11PreparedReceiveCapacityReceiptShape(t, receiver, candidate,
		receiveState, request, raw)

	preparedBundle, err := coreManagedBundleWithRGB11Snapshot(t, receiver, catalog,
		targetScope.ID(), candidate)
	coreRequire(t, "build prepared-only full account bundle for blind capacity", err)
	preparedSize, err := coreMeasureManagedBundle(t, receiver, preparedBundle)
	coreRequire(t, "measure prepared-only blind account bundle", err)
	currentBundle, err := coreNextManagedBundle(receiver, catalog)
	coreRequire(t, "build invoice-only full bundle for blind capacity fault", err)
	currentSize, err := coreMeasureManagedBundle(t, receiver, currentBundle)
	coreRequire(t, "measure invoice-only full bundle for blind capacity", err)
	terminalSize, err := receiver.preflightRGB11TerminalRecovery(targetScope.ID(), candidate)
	coreRequire(t, "measure blind terminal recovery closure", err)
	coreAssert(t, preparedSize.DKB1Bytes < terminalSize.DKB1Bytes,
		"blind receive terminal proof closure did not exceed its prepared-only package")
	coreAssert(t, terminalSize.DKB1Bytes <= swire.MaxDKVSBlobValueSize,
		"blind terminal closure does not fit without the explicit capacity fault injection")
	coreAssert(t, preparedSize.DKB1Bytes < swire.MaxDKVSBlobValueSize,
		"blind prepared-only bundle is already over the stable blob limit")
	// Capacity noise makes the outer envelope uncompressed; use AMD deltas
	// rather than compression-sensitive ADE sizes of the unfilled bundles.
	preparedGrowth := preparedSize.AMDBytes - currentSize.AMDBytes
	terminalGrowth := terminalSize.AMDBytes - currentSize.AMDBytes
	coreAssert(t, preparedGrowth > 0 && terminalGrowth > preparedGrowth,
		fmt.Sprintf("blind ADE sizes: current=%d prepared=%d terminal=%d", currentSize.ADEBytes, preparedSize.ADEBytes, terminalSize.ADEBytes))
	maxADE := swire.MaxDKVSBlobValueSize - 8
	// Preserve the prepared-only candidate under the limit, but put its known
	// terminal closure one byte over the final DKB1 wire limit. The ADE itself
	// remains below its independent 1 MiB ceiling in this boundary case.
	targetADE := maxADE - terminalGrowth + 1
	coreAssert(t, targetADE >= currentSize.ADEBytes && targetADE < maxADE,
		"blind capacity boundary cannot preserve the actual invoice-only baseline")
	coreAssert(t, targetADE+preparedGrowth < maxADE && targetADE+terminalGrowth == maxADE+1 &&
		targetADE+terminalGrowth+8 == swire.MaxDKVSBlobValueSize+1,
		"blind capacity boundary does not isolate terminal growth above the final ADE limit")
	coreLogRGB11AccountPackageSize(t, "R21_blind_invoice_stable_before_capacity_fault", receiver)
	preparedEncoded := mustEncodeRecoveryPackage(t, candidate)
	t.Logf("core-e2e: R21 blind-capacity carrier_equals_witness=false prepared_RPK_raw=%d prepared_RPK_deflate=%d prepared_AMD=%d prepared_ADE=%d prepared_DKB1=%d terminal_RPK_raw=%d terminal_RPK_deflate=%d terminal_AMD=%d terminal_ADE=%d terminal_DKB1=%d target_ADE=%d preview_ADE_growth=%d final_DKB1_overage=1",
		coreRecoveryPackageRawSize(t, preparedEncoded),
		len(preparedEncoded), preparedSize.AMDBytes, preparedSize.ADEBytes,
		preparedSize.DKB1Bytes, terminalSize.RPKRawBytes, terminalSize.RPKEncodedBytes,
		terminalSize.AMDBytes, terminalSize.ADEBytes, terminalSize.DKB1Bytes, targetADE, terminalGrowth)

	cleanup := coreInstallCapacityProviderAtEnvelopeTarget(t, receiver, cfg, targetADE, currentBundle)
	remoteBefore := coreManagedRemoteRecords(t, receiver, cfg)
	profileBefore := coreCaptureManagedProfile(receiver)
	coreAssert(t, profileBefore.envelopeBytes == targetADE && !profileBefore.dirty &&
		profileBefore.revision > 0 && profileBefore.hash != "",
		"blind capacity fault baseline was not a confirmed envelope below the wire ceiling")
	beforeAttemptBroadcasts := chain.broadcastCount()
	_, receiveErr := receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
	coreAssertCapacityRejected(t, receiveErr, "blind receive terminal closure before positive ACK")
	coreAssert(t, chain.broadcastCount() == beforeAttemptBroadcasts,
		"blind receive capacity failure caused a transaction broadcast")
	proxy.mu.Lock()
	ackHistory := append([]bool(nil), proxy.ackHistory...)
	proxy.mu.Unlock()
	positiveAck, negativeAck := false, false
	for _, ack := range ackHistory {
		if ack {
			positiveAck = true
		} else {
			negativeAck = true
		}
	}
	coreAssert(t, !positiveAck && negativeAck,
		"blind receive capacity failure did not produce a negative-only ACK decision")
	coreAssertManagedRemoteEqual(t, "remote stable package after blind capacity rejection",
		remoteBefore, coreManagedRemoteRecords(t, receiver, cfg))
	coreAssertManagedProfileUnchanged(t, "receiver profile after blind capacity rejection",
		profileBefore, coreCaptureManagedProfile(receiver))
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read sender pending operation after receiver capacity rejection", err)
	coreAssert(t, pending != nil && pending.State.WitnessTxID == prepared.TxID &&
		len(pending.RecipientConsignment) != 0 && len(pending.LocalConsignment) != 0 &&
		len(pending.SignedTx) != 0,
		"capacity rejection discarded sender's unbroadcast recovery materials")
	if err := cleanup(); err != nil {
		t.Fatalf("remove blind capacity fault provider: %v", err)
	}
}

func coreAssertRGB11PreparedReceiveCapacityReceiptShape(t *testing.T, manager *Manager,
	snapshot *rgb11wallet.RGB11WalletSnapshot, state *rgb11wallet.TransferState,
	request *corewallet.ReceiveRequest, raw []byte) {
	t.Helper()
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	prepared, err := rgb11wallet.ValidatePreparedWith(context.Background(), validator, raw,
		manager.rgbManager.evidence)
	coreRequire(t, "validate prepared blind receipt shape", err)
	invoice, err := invoicing.Parse(request.Invoice)
	coreRequire(t, "parse blind receipt-shape invoice", err)
	matched, err := manager.rgbManager.matchRGB11ReceiveAllocation(prepared.Receipt, request,
		invoice, prepared.Outputs, "", nil)
	coreRequire(t, "match exact blind allocation for receipt shape", err)
	material, err := manager.rgbManager.rgb11TerminalCapacityMaterial(context.Background(), validator, raw, state.WitnessTxID, nil)
	coreRequire(t, "validate prepared blind historical receipt shape", err)
	history := material.History
	coreAssert(t, history != nil && history.MaterialReceipt != nil,
		"blind history report omitted its material receipt")

	// Seed a structurally valid prior receipt with one authenticated historical
	// allocation absent from the prepared receipt. The old preview copied the
	// prepared receipt and collided with this already-bound base; the selector
	// must preserve the base and append the newly matched allocation.
	baseAllocations := make([]rgb11wallet.ValidatedAllocation, 0,
		1)
	for _, allocation := range history.MaterialReceipt.Allocations {
		if allocation.OperationID == matched.Allocation.OperationID &&
			allocation.AssignmentType == matched.Allocation.AssignmentType &&
			allocation.AssignmentIndex == matched.Allocation.AssignmentIndex {
			continue
		}
		inLegacy := false
		for _, legacyAllocation := range prepared.Receipt.Allocations {
			if allocation.OperationID == legacyAllocation.OperationID &&
				allocation.AssignmentType == legacyAllocation.AssignmentType &&
				allocation.AssignmentIndex == legacyAllocation.AssignmentIndex {
				inLegacy = true
				break
			}
		}
		if inLegacy {
			continue
		}
		baseAllocations = append(baseAllocations, allocation)
		break
	}
	coreAssert(t, len(baseAllocations) == 1,
		"blind fixture lacks a historical allocation to exercise a distinct receipt shape")
	baseReceipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, nil, baseAllocations)
	coreRequire(t, "select prior blind receipt shape", err)
	seededRecords, err := rgb11wallet.AddValidatedConsignmentProjectionSnapshot(
		snapshot.ProjectionRecords, raw, baseReceipt, nil)
	coreRequire(t, "seed prior authenticated receipt in sizing-only snapshot", err)
	shapeSnapshot := *snapshot
	shapeSnapshot.ProjectionRecords = seededRecords

	legacyReceipt := *prepared.Receipt
	legacyReceipt.Status = "valid"
	legacyHash, err := legacyReceipt.Hash()
	coreRequire(t, "hash legacy prepared receipt shape", err)
	expectedReceipt, err := rgb11wallet.SelectHistoricalValidationReceipt(history, baseReceipt,
		[]rgb11wallet.ValidatedAllocation{matched.Allocation})
	coreRequire(t, "select accepted blind receipt shape", err)
	expectedHash, err := expectedReceipt.Hash()
	coreRequire(t, "hash accepted blind receipt shape", err)
	coreAssert(t, legacyHash != expectedHash,
		"fixture does not distinguish the legacy prepared receipt from the selected terminal receipt")
	_, legacyErr := rgb11wallet.AddValidatedConsignmentProjectionSnapshot(
		seededRecords, raw, &legacyReceipt, nil)
	coreAssert(t, errors.Is(legacyErr, rgb11wallet.ErrValidationReceipt),
		"legacy prepared receipt did not reproduce the existing-base shape conflict")

	walletID, err := manager.rgbManager.RGB11WalletID()
	coreRequire(t, "resolve wallet before receipt-shape preview", err)
	before, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "export wallet before receipt-shape preview", err)
	preview, err := manager.rgbManager.addRGB11PreparedReceiveCapacityPreview(
		context.Background(), &shapeSnapshot, state, raw)
	coreRequire(t, "build selected receipt shape for capacity preview", err)
	actualReceipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(preview.ProjectionRecords)
	coreRequire(t, "read selected receipt shape from capacity preview", err)
	actualReceipt := actualReceipts[state.ConsignmentHash]
	coreAssert(t, actualReceipt != nil && len(actualReceipt.Allocations) == len(expectedReceipt.Allocations),
		"capacity preview did not preserve the base receipt and add the exact accepted allocation")
	actualHash, err := actualReceipt.Hash()
	coreRequire(t, "hash capacity preview receipt shape", err)
	coreAssert(t, actualHash == expectedHash,
		"capacity preview receipt differs from the selected accepted receipt")
	after, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "export wallet after receipt-shape preview", err)
	beforeEncoded := mustEncodeRecoveryPackage(t, before)
	afterEncoded := mustEncodeRecoveryPackage(t, after)
	coreAssert(t, bytes.Equal(beforeEncoded, afterEncoded),
		"sizing-only receipt preview persisted wallet state")
	t.Logf("core-e2e: selected_receive_receipt_shape legacy_allocations=%d selected_allocations=%d legacy_differs=true preview_matches_selected=true sizing_only_persisted=false",
		len(legacyReceipt.Allocations), len(expectedReceipt.Allocations))
}

func coreManagedBundleWithRGB11Snapshot(t *testing.T, manager *Manager,
	catalog AccountManagedDataCatalog, scope string,
	snapshot *rgb11wallet.RGB11WalletSnapshot) (account.ManagedDataBundle, error) {
	t.Helper()
	bundle, err := coreNextManagedBundle(manager, catalog)
	if err != nil {
		return account.ManagedDataBundle{}, err
	}
	packageValue, err := rgb11wallet.RecoveryPackageFromSnapshot(snapshot, 0)
	if err != nil {
		return account.ManagedDataBundle{}, err
	}
	encoded, _, err := rgb11wallet.EncodeRecoveryPackageWithSize(packageValue)
	if err != nil {
		return account.ManagedDataBundle{}, err
	}
	replaced := false
	for index := range bundle.Items {
		item := &bundle.Items[index]
		if item.Provider != rgb11AccountManagedProviderID || item.Scope != scope {
			continue
		}
		if replaced {
			return account.ManagedDataBundle{}, errors.New("duplicate RGB11 item for blind capacity scope")
		}
		item.Payload = append([]byte(nil), encoded...)
		replaced = true
	}
	if !replaced {
		bundle.Items = append(bundle.Items, account.ManagedDataItem{
			Provider: rgb11AccountManagedProviderID, Scope: scope, Payload: append([]byte(nil), encoded...),
		})
	}
	return account.NormalizeManagedDataBundle(bundle)
}

func mustEncodeRecoveryPackage(t *testing.T, snapshot *rgb11wallet.RGB11WalletSnapshot) []byte {
	t.Helper()
	packageValue, err := rgb11wallet.RecoveryPackageFromSnapshot(snapshot, 0)
	coreRequire(t, "build recovery package for byte measurement", err)
	encoded, _, err := rgb11wallet.EncodeRecoveryPackageWithSize(packageValue)
	coreRequire(t, "encode recovery package for byte measurement", err)
	return encoded
}

type coreManagedRecordFingerprint struct {
	seq    uint64
	hash   string
	digest string
	bytes  int
}

type coreManagedProfileFingerprint struct {
	dirty         bool
	revision      uint64
	hash          string
	envelope      []byte
	envelopeBytes int
}

func coreManagedRemoteRecords(t *testing.T, manager *Manager,
	cfg coreE2EConfig) map[string]coreManagedRecordFingerprint {
	t.Helper()
	root, err := manager.accountManagementRootWallet()
	coreRequire(t, "resolve account-managed root for capacity snapshot", err)
	stateKey, err := manager.accountManagedStateKey(root)
	coreRequire(t, "derive account-managed state key", err)
	blobKey, err := manager.accountManagedDataBlobKey(root)
	coreRequire(t, "derive account-managed blob key", err)
	wrapperKey, err := accountRootWrapperKey(root)
	coreRequire(t, "derive account-root wrapper key", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	result := make(map[string]coreManagedRecordFingerprint, 3)
	for _, key := range []string{stateKey, blobKey, wrapperKey} {
		record, err := client.GetRecordDirect(key)
		coreRequire(t, "read authoritative record for capacity snapshot", err)
		result[string(key)] = coreManagedRecordFingerprint{
			seq: record.Seq, hash: dkvsindexer.RecordHash(record).String(),
			digest: coreDigest(record.Value), bytes: len(record.Value),
		}
	}
	return result
}

func coreAssertManagedRemoteEqual(t *testing.T, label string,
	want, got map[string]coreManagedRecordFingerprint) {
	t.Helper()
	if !coreManagedRemoteRecordsEqual(want, got) {
		t.Fatalf("core-e2e: %s changed an authoritative managed record", label)
	}
}

func coreManagedRemoteRecordsEqual(want, got map[string]coreManagedRecordFingerprint) bool {
	return maps.Equal(want, got)
}

func coreAssertMapEqual(t *testing.T, label string, want, got map[string]string) {
	t.Helper()
	if coreDigest(want) != coreDigest(got) {
		t.Fatalf("core-e2e: %s changed", label)
	}
}

func coreCaptureManagedProfile(manager *Manager) coreManagedProfileFingerprint {
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	if manager.accountProfile == nil {
		return coreManagedProfileFingerprint{}
	}
	envelope := append([]byte(nil), manager.accountProfile.ManagedDataEnvelope...)
	return coreManagedProfileFingerprint{
		dirty:    manager.accountProfile.ManagedDataDirty,
		revision: manager.accountProfile.ManagedDataRevision,
		hash:     manager.accountProfile.ManagedDataHash,
		envelope: envelope, envelopeBytes: len(envelope),
	}
}

func coreLocalRGBBundleDigests(t *testing.T, manager *Manager) map[string]string {
	t.Helper()
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "enumerate account scopes for local capacity snapshot", err)
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	payloads, err := provider.Export(catalog)
	coreRequire(t, "export local RGB recovery material for capacity snapshot", err)
	result := make(map[string]string, len(payloads))
	for _, payload := range payloads {
		result[payload.Scope] = coreDigest(payload.Payload)
	}
	return result
}

func coreNextManagedBundle(manager *Manager,
	catalog AccountManagedDataCatalog) (account.ManagedDataBundle, error) {
	manager.mutex.RLock()
	if manager.accountProfile == nil {
		manager.mutex.RUnlock()
		return account.ManagedDataBundle{}, errors.New("account profile unavailable")
	}
	revision := manager.accountProfile.ManagedDataRevision
	manager.mutex.RUnlock()
	if revision == ^uint64(0) {
		return account.ManagedDataBundle{}, errors.New("account-managed revision overflow")
	}
	if revision == 0 {
		revision = 1
	} else {
		revision++
	}
	bundle, _, err := manager.exportAccountManagedData(catalog, revision)
	return bundle, err
}

func coreCurrentManagedBundle(manager *Manager,
	catalog AccountManagedDataCatalog) (account.ManagedDataBundle, error) {
	manager.mutex.RLock()
	if manager.accountProfile == nil {
		manager.mutex.RUnlock()
		return account.ManagedDataBundle{}, errors.New("account profile unavailable")
	}
	revision := manager.accountProfile.ManagedDataRevision
	manager.mutex.RUnlock()
	bundle, _, err := manager.exportAccountManagedData(catalog, revision)
	return bundle, err
}

func coreLogRGB11AccountPackageSize(t *testing.T, label string, manager *Manager) {
	t.Helper()
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "enumerate account scopes for RGB11 size sample", err)
	bundle, err := coreCurrentManagedBundle(manager, catalog)
	coreRequire(t, "export full account bundle for RGB11 size sample", err)
	size, err := coreMeasureManagedBundle(t, manager, bundle)
	coreRequire(t, "measure full account bundle for RGB11 size sample", err)
	provider := &rgb11AccountManagedDataProvider{owner: manager}
	payloads, err := provider.Export(catalog)
	coreRequire(t, "export RGB11 packages for account size sample", err)
	rpkRaw, rpkEncoded := 0, 0
	for _, payload := range payloads {
		rpkRaw += coreRecoveryPackageRawSize(t, payload.Payload)
		rpkEncoded += len(payload.Payload)
	}
	t.Logf("core-e2e: %s RPK_raw=%d RPK_deflate=%d AMD=%d ADE=%d DKB1=%d", label,
		rpkRaw, rpkEncoded, size.AMDBytes, size.ADEBytes, size.DKB1Bytes)
}

func coreMeasureManagedBundle(t *testing.T, manager *Manager,
	bundle account.ManagedDataBundle) (rgb11RecoveryBundleSize, error) {
	t.Helper()
	var size rgb11RecoveryBundleSize
	manager.mutex.RLock()
	if manager.accountProfile == nil {
		manager.mutex.RUnlock()
		return size, errors.New("account profile unavailable")
	}
	accountID := manager.accountProfile.AccountID
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	plain, err := account.EncodeManagedDataBundle(bundle)
	if err != nil {
		return size, err
	}
	size.AMDBytes = len(plain)
	defer zeroBytes(plain)
	envelope, _, err := account.SealManagedDataBundleWithInfo(secret, accountID, bundle, nil)
	if err != nil {
		return size, err
	}
	size.ADEBytes = len(envelope)
	defer zeroBytes(envelope)
	blob, err := EncodeDKVSBlobValue(envelope, nil)
	if err != nil {
		return size, err
	}
	size.DKB1Bytes = len(blob)
	defer zeroBytes(blob)
	return size, nil
}

func coreInstallCapacityProviderAtWireBoundary(t *testing.T, manager *Manager,
	cfg coreE2EConfig) func() error {
	t.Helper()
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "enumerate scopes before capacity provider injection", err)
	base, err := coreNextManagedBundle(manager, catalog)
	coreRequire(t, "export full account baseline before capacity injection", err)
	return coreInstallCapacityProviderAtEnvelopeTarget(t, manager, cfg,
		swire.MaxDKVSBlobValueSize-8, base)
}

func coreInstallCapacityProviderAtEnvelopeTarget(t *testing.T, manager *Manager,
	cfg coreE2EConfig, wantEnvelopeBytes int,
	base account.ManagedDataBundle) func() error {
	t.Helper()
	manager.mutex.RLock()
	if manager.accountProfile == nil || len(manager.accountSecret) != 32 {
		manager.mutex.RUnlock()
		t.Fatal("core-e2e: account secret unavailable for capacity fixture")
	}
	accountID := manager.accountProfile.AccountID
	secret := append([]byte(nil), manager.accountSecret...)
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	catalog, err := manager.accountManagedDataCatalog()
	coreRequire(t, "enumerate scope for capacity provider", err)
	if len(catalog.Scopes) == 0 {
		t.Fatal("core-e2e: account capacity fixture has no wallet scopes")
	}
	payload := accountManagedPayloadForEnvelopeSize(t, secret, accountID, base,
		"zz-capacity", catalog.Scopes[0].ID(), wantEnvelopeBytes)
	provider := &accountManagedCapacityProvider{scope: catalog.Scopes[0].ID(), payload: payload}
	manager.managedDataMu.RLock()
	_, exists := manager.managedDataProviders[provider.ID()]
	manager.managedDataMu.RUnlock()
	if exists {
		zeroBytes(payload)
		t.Fatal("core-e2e: capacity test provider is already registered")
	}
	if err := manager.RegisterAccountManagedDataProvider(provider); err != nil {
		zeroBytes(payload)
		t.Fatal(err)
	}
	cleaned := false
	cleanup := func() error {
		if cleaned {
			return nil
		}
		cleaned = true
		manager.managedDataMu.Lock()
		delete(manager.managedDataProviders, provider.ID())
		manager.managedDataMu.Unlock()
		zeroBytes(payload)
		if err := manager.markAccountManagedDataDirtyDeferred(provider.ID()); err != nil {
			return err
		}
		return manager.SyncAccountManagementState(context.Background())
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Errorf("remove capacity test provider and restore compact bundle: %v", err)
		}
	})
	if err := manager.markAccountManagedDataDirtyDeferred(provider.ID()); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("publish capacity test baseline: %v", err)
	}
	profile := coreCaptureManagedProfile(manager)
	if profile.envelopeBytes != wantEnvelopeBytes {
		t.Fatalf("capacity fixture ADE=%d want=%d", profile.envelopeBytes, wantEnvelopeBytes)
	}
	coreLogRGB11AccountPackageSize(t, "capacity_injected_account_bundle", manager)
	remote := coreManagedRemoteRecords(t, manager, cfg)
	root, err := manager.accountManagementRootWallet()
	coreRequire(t, "read root after capacity baseline", err)
	blobKey, err := manager.accountManagedDataBlobKey(root)
	coreRequire(t, "derive blob key after capacity baseline", err)
	blob, ok := remote[string(blobKey)]
	encodedBlob, err := EncodeDKVSBlobValue(profile.envelope, nil)
	coreRequire(t, "encode actual target ADE as DKB1", err)
	if !ok || blob.bytes != len(encodedBlob) || blob.bytes > swire.MaxDKVSBlobValueSize {
		t.Fatalf("capacity fixture DKB1=%d want=%d limit=%d", blob.bytes, len(encodedBlob), swire.MaxDKVSBlobValueSize)
	}
	return cleanup
}

func coreAssertCapacityRejected(t *testing.T, err error, action string) {
	t.Helper()
	if err == nil || (!errors.Is(err, dkvsindexer.ErrRecordTooLarge) &&
		!strings.Contains(strings.ToLower(err.Error()), "limit") &&
		!strings.Contains(strings.ToLower(err.Error()), "bytes")) {
		t.Fatalf("core-e2e: %s error=%v; want explicit account-data capacity rejection", action, err)
	}
}

func coreAssertManagedProfileUnchanged(t *testing.T, label string,
	before, after coreManagedProfileFingerprint) {
	t.Helper()
	if before.dirty != after.dirty || before.revision != after.revision ||
		before.hash != after.hash || !bytes.Equal(before.envelope, after.envelope) {
		t.Fatalf("core-e2e: %s changed account profile metadata", label)
	}
}
