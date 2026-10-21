package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreInvoiceTransferE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	proxy := &rgb11ProxyTestServer{}
	proxyHTTP := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
	defer proxyHTTP.Close()
	proxyEndpoint := "rpc://" + strings.TrimPrefix(proxyHTTP.URL, "http://")

	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EO", Name: "E2E invoice asset", Amounts: []uint64{1000},
	})
	coreRequire(t, "issue invoice-transfer NIA", err)
	coreCheckpoint(t, "01_issued_sender", sender, cfg, chain, senderMaterial)
	exported, err := sender.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export canonical contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode standard contract", err)
	coreAssert(t, bytes.HasPrefix(contract, []byte("RGB\x00CON")), "contract export is not standard RGB CON")
	_, err = receiver.ImportRGB11ContractFile(context.Background(), contract)
	coreRequire(t, "receiver import standard contract", err)
	balance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "import-only balance", err)
	coreAssert(t, balance.Sign() == 0, "contract import incorrectly created ownership")
	coreRequire(t, "synchronize receiver before invoice barrier fault",
		receiver.SyncAccountManagementState(context.Background()))
	// An invoice is a public commitment only after the account bundle CAS is
	// confirmed. Fail just the dirty bundle CAS after allowing the real CoreNode
	// baseline reads; the local request/reveal must remain resumable while the
	// public result and authoritative remote revision stay unchanged.
	remoteBeforeInvoiceFault := coreManagedRemoteRecords(t, receiver, cfg)
	profileBeforeInvoiceFault := coreCaptureManagedProfile(receiver)
	coreAssert(t, !profileBeforeInvoiceFault.dirty,
		"invoice CAS fault did not start from a confirmed clean account baseline")
	walletID, err := receiver.rgbManager.RGB11WalletID()
	coreRequire(t, "identify receiver scope before invoice barrier fault", err)
	snapshotBeforeInvoiceFault, _, err := receiver.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "snapshot receiver before invoice barrier fault", err)
	engineKeysBeforeFault := make(map[string]struct{}, len(snapshotBeforeInvoiceFault.EngineRecords))
	projectionKeysBeforeFault := make(map[string]struct{}, len(snapshotBeforeInvoiceFault.ProjectionRecords))
	for _, record := range snapshotBeforeInvoiceFault.EngineRecords {
		engineKeysBeforeFault[record.Key] = struct{}{}
	}
	for _, record := range snapshotBeforeInvoiceFault.ProjectionRecords {
		projectionKeysBeforeFault[record.Key] = struct{}{}
	}
	store, err := receiver.accountDKVSStore()
	coreRequire(t, "open receiver's real account DKVS client for invoice CAS fault", err)
	oldHTTP := store.client.Http
	fault := &coreRejectDirtyManagedCASHTTP{HttpClient: oldHTTP, owner: receiver}
	store.client.Http = fault
	blockedInvoice, blockedInvoiceErr := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{proxyEndpoint}, ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	store.client.Http = oldHTTP
	coreAssert(t, blockedInvoiceErr != nil && blockedInvoice == nil,
		"invoice escaped while the real account recovery CAS was rejected")
	fault.mu.Lock()
	rejectedInvoiceCAS := fault.rejected
	fault.mu.Unlock()
	coreAssert(t, rejectedInvoiceCAS > 0, "invoice fault did not reach the dirty account-data CAS")
	coreAssertManagedRemoteEqual(t, "rejected invoice barrier", remoteBeforeInvoiceFault,
		coreManagedRemoteRecords(t, receiver, cfg))
	profileAfterInvoiceFault := coreCaptureManagedProfile(receiver)
	coreAssert(t, profileAfterInvoiceFault.dirty &&
		profileAfterInvoiceFault.revision == profileBeforeInvoiceFault.revision &&
		profileAfterInvoiceFault.hash == profileBeforeInvoiceFault.hash,
		"failed invoice CAS advanced or cleared the last-confirmed local account revision")
	snapshotAfterInvoiceFault, _, err := receiver.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "snapshot locally retained invoice request", err)
	newReceiveRequests, newReceiveKeys := 0, 0
	for _, record := range snapshotAfterInvoiceFault.EngineRecords {
		if _, existed := engineKeysBeforeFault[record.Key]; existed {
			continue
		}
		request, decodeErr := corewallet.DecodeReceiveRequest(record.Value)
		coreRequire(t, "decode locally retained receive request", decodeErr)
		coreAssert(t, request.RequestID != "" && request.Invoice != "",
			"failed invoice barrier left no resumable receive identity")
		newReceiveRequests++
	}
	for _, record := range snapshotAfterInvoiceFault.ProjectionRecords {
		if strings.HasPrefix(record.Key, "receive-key-") {
			if _, existed := projectionKeysBeforeFault[record.Key]; !existed {
				newReceiveKeys++
			}
		}
	}
	coreAssert(t, newReceiveRequests == 1 && newReceiveKeys == 1,
		"failed invoice barrier did not retain exactly its private request and witness receive key")
	// This fixture has no background transport worker. Recover the original
	// durable outbox before asking the SDK to create another public invoice.
	coreReplayManagedOutbox(t, receiver)
	coreAssertMapEqual(t, "replayed invoice barrier local/confirmed recovery",
		coreLocalRGBBundleDigests(t, receiver), coreRemoteStableRGB(t, receiver, cfg))
	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{proxyEndpoint}, ContractID: issued.ContractID,
		AmountRaw: "400", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create standard-proxy witness invoice", err)
	receiver = corePendingCheckpoint(t, "02_invoice_receiver", receiver, cfg, chain, receiverMaterial)
	before := chain.broadcastCount()
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare native invoice transfer", err)
	preparedJSON, err := json.Marshal(prepared)
	coreRequire(t, "serialize public standard-proxy prepare result", err)
	coreAssert(t, !bytes.Contains(preparedJSON, []byte(`"signed_psbt"`)),
		"public CoreModules Prepare result exposed a broadcastable signed PSBT")
	coreAssert(t, chain.broadcastCount() == before, "prepare unexpectedly broadcast")
	sender = corePendingCheckpoint(t, "03_prepared_sender", sender, cfg, chain, senderMaterial)
	// Inject a CoreNode outage before the external proxy delivery. This keeps
	// the real account CAS and proxy fixture in place while proving that an
	// unavailable recovery barrier cannot leak the consignment or broadcast.
	store, err = sender.accountDKVSStore()
	coreRequire(t, "open sender's real account DKVS client for outage injection", err)
	oldHTTP = store.client.Http
	store.client.Http = coreOfflineHTTP{HttpClient: oldHTTP}
	blocked, blockedErr := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	store.client.Http = oldHTTP
	coreAssert(t, blockedErr != nil && blocked == nil,
		"proxy delivery proceeded without a confirmed account recovery barrier")
	coreAssert(t, chain.broadcastCount() == before,
		"CoreNode outage before proxy delivery caused a Bitcoin broadcast")
	proxy.mu.Lock()
	blockedProxyDelivery := proxy.txID != "" || len(proxy.consignment) != 0
	proxy.mu.Unlock()
	coreAssert(t, !blockedProxyDelivery,
		"CoreNode outage before proxy delivery exposed the consignment to the receiver")
	preparedPending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read prepared transfer after rejected offline delivery", err)
	coreAssert(t, preparedPending.State.Status == "prepared" && preparedPending.State.WitnessTxID == prepared.TxID,
		"rejected offline delivery discarded or changed the resumable prepared transaction")

	t.Run("malformed_consignment_cannot_create_ownership", func(t *testing.T) {
		_, err := receiver.ValidateRGB11Consignment(context.Background(), []byte("not an RGB consignment"))
		coreAssert(t, err != nil, "malformed consignment accepted")
		balance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
		coreRequire(t, "balance after invalid payload", err)
		coreAssert(t, balance.Sign() == 0, "invalid consignment changed ownership")
	})
	proxy.mu.Lock()
	proxy.postHTTPStatus = http.StatusBadGateway // store the consignment, then lose the successful HTTP response
	proxy.mu.Unlock()
	delivered, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	coreRequire(t, "publish standard-proxy consignment before ACK", err)
	coreAssert(t, delivered.AwaitingACK && !delivered.Broadcast && chain.broadcastCount() == before,
		"standard-proxy delivery crossed the positive-ACK broadcast barrier")
	proxy.mu.Lock()
	postedTxID, postedConsignment := proxy.txID, append([]byte(nil), proxy.consignment...)
	proxy.postHTTPStatus = 0
	proxy.mu.Unlock()
	coreAssert(t, postedTxID == prepared.TxID && bytes.HasPrefix(postedConsignment, []byte("RGB\x00TFR")),
		"standard proxy did not receive the prepared RGB transfer and txid")
	receiver = corePendingCheckpoint(t, "04_proxy_consignment_waiting_receiver", receiver, cfg, chain, receiverMaterial)
	// A receiver must not publish a positive Standard ACK until its accepted
	// consignment, receive request, key and reservation are in the confirmed
	// account bundle. Reject the real dirty CAS after the local accept, then
	// retry the same request ID once the CoreNode accepts writes again.
	remoteBeforeStandardACKFault := coreManagedRemoteRecords(t, receiver, cfg)
	profileBeforeStandardACKFault := coreCaptureManagedProfile(receiver)
	localBeforeStandardACKFault := coreLocalRGBBundleDigests(t, receiver)
	store, err = receiver.accountDKVSStore()
	coreRequire(t, "open receiver account client for Standard ACK barrier fault", err)
	oldHTTP = store.client.Http
	fault = &coreRejectDirtyManagedCASHTTP{HttpClient: oldHTTP, owner: receiver}
	store.client.Http = fault
	blockedReceive, blockedReceiveErr := receiver.ReceiveRGB11ProxyConsignment(
		context.Background(), invoice.RequestID)
	store.client.Http = oldHTTP
	coreAssert(t, blockedReceiveErr != nil &&
		(blockedReceive == nil || !blockedReceive.AckPosted),
		"Standard receiver acknowledged before its account recovery CAS was confirmed")
	fault.mu.Lock()
	rejectedStandardACKCAS := fault.rejected
	fault.mu.Unlock()
	coreAssert(t, rejectedStandardACKCAS > 0,
		"Standard ACK fault did not reach the dirty account-data CAS")
	proxy.mu.Lock()
	standardACKEscaped := proxy.ack != nil && *proxy.ack
	proxy.mu.Unlock()
	coreAssert(t, !standardACKEscaped && chain.broadcastCount() == before,
		"Standard ACK barrier failure reached the proxy or Bitcoin backend")
	coreAssertManagedRemoteEqual(t, "rejected Standard receiver ACK barrier",
		remoteBeforeStandardACKFault, coreManagedRemoteRecords(t, receiver, cfg))
	profileAfterStandardACKFault := coreCaptureManagedProfile(receiver)
	coreAssert(t, profileAfterStandardACKFault.dirty &&
		profileAfterStandardACKFault.revision == profileBeforeStandardACKFault.revision &&
		profileAfterStandardACKFault.hash == profileBeforeStandardACKFault.hash,
		"failed Standard ACK barrier advanced the last-confirmed receiver account revision")
	localAfterStandardACKFault := coreLocalRGBBundleDigests(t, receiver)
	coreAssert(t, coreDigest(localAfterStandardACKFault) != coreDigest(localBeforeStandardACKFault),
		"failed Standard ACK barrier discarded its locally accepted receive material")
	requestAfterStandardACKFault, err := receiver.rgbManager.engine.LoadReceive(invoice.RequestID)
	coreRequire(t, "reload retained Standard receive request after rejected ACK", err)
	// Standard Proxy delivers the strict TFR file; the prepared send hashes
	// its armor. Bind the receiver to the exact bytes actually delivered.
	standardObjectHash := rgb11ObjectHash(postedConsignment)
	coreAssert(t, requestAfterStandardACKFault.TransferID == prepared.State.TransferID &&
		requestAfterStandardACKFault.ObjectHash == standardObjectHash,
		"failed Standard ACK barrier lost the original accepted request or consignment binding")
	retainedStandardReceive, err := receiver.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
	coreRequire(t, "load retained Standard receive transition after rejected ACK", err)
	coreAssert(t, retainedStandardReceive.Status == "awaiting_broadcast" &&
		retainedStandardReceive.WitnessTxID == prepared.TxID &&
		retainedStandardReceive.ConsignmentHash == standardObjectHash,
		"failed Standard ACK barrier changed or discarded the original receive transition")

	proxy.mu.Lock()
	proxy.ackPostHTTPStatus = http.StatusBadGateway // persist the retry ACK, then lose its response
	proxy.mu.Unlock()
	coreReplayManagedOutbox(t, receiver)
	proxyReceived, err := receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
	coreRequire(t, "receiver prepares and ACKs standard-proxy consignment", err)
	proxy.mu.Lock()
	proxy.ackPostHTTPStatus = 0
	ackStored := proxy.ack != nil && *proxy.ack
	proxy.mu.Unlock()
	coreAssert(t, proxyReceived.AckPosted && proxyReceived.AwaitingBroadcast &&
		proxyReceived.TxID == prepared.TxID && chain.broadcastCount() == before && ackStored,
		"standard-proxy receiver ACK did not remain ahead of Bitcoin broadcast")
	coreAssertRGB11ACKRecoveryPublished(t, receiver, prepared)
	receiver = corePendingCheckpoint(t, "05_proxy_positive_ACK_cold_recovery", receiver, cfg, chain, receiverMaterial)
	balance, err = receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "pre-broadcast balance", err)
	coreAssert(t, balance.Sign() == 0, "receiver credited an unbroadcast witness")

	// The sender must durably record the positive ACK and broadcast intent
	// before calling the Bitcoin backend. A real account-CAS failure at that
	// boundary must leave the exact signed transaction and input lock resumable.
	remoteBeforeStandardBroadcastFault := coreManagedRemoteRecords(t, sender, cfg)
	profileBeforeStandardBroadcastFault := coreCaptureManagedProfile(sender)
	store, err = sender.accountDKVSStore()
	coreRequire(t, "open sender account client for Standard broadcast barrier fault", err)
	oldHTTP = store.client.Http
	fault = &coreRejectDirtyManagedCASHTTP{HttpClient: oldHTTP, owner: sender}
	store.client.Http = fault
	blockedDelivery, blockedDeliveryErr := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	store.client.Http = oldHTTP
	coreAssert(t, blockedDeliveryErr != nil &&
		(blockedDelivery == nil || !blockedDelivery.Broadcast),
		"Standard sender reported broadcast success before its account recovery CAS was confirmed")
	fault.mu.Lock()
	rejectedStandardBroadcastCAS := fault.rejected
	fault.mu.Unlock()
	coreAssert(t, rejectedStandardBroadcastCAS > 0,
		"Standard broadcast fault did not reach the dirty account-data CAS")
	coreAssert(t, chain.broadcastCount() == before,
		"Standard broadcast barrier failure reached the Bitcoin backend")
	coreAssertManagedRemoteEqual(t, "rejected Standard sender broadcast barrier",
		remoteBeforeStandardBroadcastFault, coreManagedRemoteRecords(t, sender, cfg))
	profileAfterStandardBroadcastFault := coreCaptureManagedProfile(sender)
	coreAssert(t, profileAfterStandardBroadcastFault.dirty &&
		profileAfterStandardBroadcastFault.revision == profileBeforeStandardBroadcastFault.revision &&
		profileAfterStandardBroadcastFault.hash == profileBeforeStandardBroadcastFault.hash,
		"failed Standard broadcast barrier advanced the last-confirmed sender account revision")
	retainedStandardPending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "reload retained Standard sender transaction after rejected broadcast", err)
	coreAssert(t, retainedStandardPending.State.WitnessTxID == prepared.TxID &&
		retainedStandardPending.State.AckStatus == "accepted" &&
		rgb11TransferKeepsInputsLocked(&retainedStandardPending.State),
		"failed Standard broadcast barrier lost the accepted transaction identity or input lock")
	coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, true)

	coreReplayManagedOutbox(t, sender)
	delivered, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	coreRequire(t, "sender consumes positive proxy ACK and broadcasts", err)
	txid := delivered.TxID
	coreAssert(t, delivered.Broadcast && !delivered.AwaitingACK && txid == prepared.TxID &&
		chain.broadcastCount() == before+1, "proxy ACK did not authorize exactly the original transaction")
	sender = corePendingCheckpoint(t, "06_broadcast_sender", sender, cfg, chain, senderMaterial)

	chain.confirm(txid)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "settle invoice sender", err)
	proxyReceived, err = receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
	coreRequire(t, "receiver accepts confirmed proxy consignment", err)
	coreAssert(t, proxyReceived.Receipt != nil && !proxyReceived.AwaitingBroadcast &&
		proxyReceived.TxID == prepared.TxID, "proxy receive did not settle the accepted transfer")
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "settle invoice receiver", err)
	coreCheckpoint(t, "07_settled_sender", sender, cfg, chain, senderMaterial)
	coreCheckpoint(t, "08_settled_receiver", receiver, cfg, chain, receiverMaterial)
	left, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "remaining invoice amount", err)
	right, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "received invoice amount", err)
	coreAssert(t, left.Value.Uint64() == 600 && right.Value.Uint64() == 400, "invoice transfer violated asset conservation")

	// Reopen the witness recipient from its account recovery package and spend
	// part of that received allocation through the ACK-gated Direct path.
	beforeColdRestore := chain.broadcastCount()
	coldWitnessReceiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, coldWitnessReceiver)
	_, err = coldWitnessReceiver.RefreshRGB11State(context.Background())
	coreRequire(t, "freshly reconcile cold witness receiver", err)
	witnessRecovered, err := coldWitnessReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold witness allocation before re-spend", err)
	coreAssert(t, witnessRecovered.Value.Uint64() == 400,
		"cold recovery did not retain the settled witness-invoice allocation")
	_, err = sender.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish sender endpoint for witness re-spend", err)
	coreAssert(t, chain.broadcastCount() == beforeColdRestore,
		"cold witness recovery or endpoint publication broadcast a transaction")
	coreSendRGB11AddressAmountAndSettle(t, chain, coldWitnessReceiver, sender,
		issued.AssetName, "100", beforeColdRestore)
	left, err = sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "sender balance after witness cold re-spend", err)
	right, err = coldWitnessReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "cold witness receiver balance after re-spend", err)
	coreAssert(t, left.Value.Uint64() == 700 && right.Value.Uint64() == 300,
		"cold witness re-spend did not preserve the expected allocations")
	receiver = coldWitnessReceiver

	// Complete the requested Standard -> Direct -> Standard sequence. This
	// second standard hop uses the existing proxy fixture over HTTP while
	// recovery continues to use the real CoreNode account CAS.
	returnProxy := &rgb11ProxyTestServer{}
	returnHTTP := httptest.NewServer(http.HandlerFunc(returnProxy.serveHTTP))
	defer returnHTTP.Close()
	returnEndpoint := "rpc://" + strings.TrimPrefix(returnHTTP.URL, "http://")
	returnInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{returnEndpoint}, ContractID: issued.ContractID,
		AmountRaw: "100", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create Standard invoice after Direct hop", err)
	receiver = corePendingCheckpoint(t, "08b_standard_return_invoice_receiver", receiver, cfg, chain, receiverMaterial)
	beforeReturn := chain.broadcastCount()
	returnPrepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: returnInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare Standard transfer after Direct hop", err)
	coreAssert(t, chain.broadcastCount() == beforeReturn,
		"Standard return prepare broadcast before proxy delivery and positive ACK")
	sender = corePendingCheckpoint(t, "08c_standard_return_prepared_sender", sender, cfg, chain, senderMaterial)
	returnDelivery, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{returnPrepared.State.TransferID})
	coreRequire(t, "deliver Standard return consignment", err)
	coreAssert(t, returnDelivery.AwaitingACK && !returnDelivery.Broadcast &&
		chain.broadcastCount() == beforeReturn,
		"Standard return delivery crossed the positive-ACK barrier")
	_, err = receiver.ReceiveRGB11ProxyConsignment(context.Background(), returnInvoice.RequestID)
	coreRequire(t, "receiver validates and ACKs Standard return", err)
	coreAssertRGB11ACKRecoveryPublished(t, receiver, returnPrepared)
	receiver = corePendingCheckpoint(t, "08d_standard_return_ACK_receiver", receiver, cfg, chain, receiverMaterial)
	returnDelivery, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{returnPrepared.State.TransferID})
	coreRequire(t, "sender consumes Standard return ACK", err)
	coreAssert(t, returnDelivery.Broadcast && returnDelivery.TxID == returnPrepared.TxID &&
		chain.broadcastCount() == beforeReturn+1,
		"Standard return ACK did not authorize exactly the prepared transaction")
	chain.confirm(returnPrepared.TxID)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "settle Standard return sender", err)
	returnReceived, err := receiver.ReceiveRGB11ProxyConsignment(context.Background(), returnInvoice.RequestID)
	coreRequire(t, "receiver settles Standard return", err)
	coreAssert(t, returnReceived.Receipt != nil && returnReceived.TxID == returnPrepared.TxID,
		"Standard return receive did not settle the acknowledged transaction")
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "settle Standard return receiver", err)
	left, err = sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "sender balance after Standard -> Direct -> Standard", err)
	right, err = receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "receiver balance after Standard -> Direct -> Standard", err)
	coreAssert(t, left.Value.Uint64() == 600 && right.Value.Uint64() == 400,
		"Standard -> Direct -> Standard violated asset conservation")

	// Blind invoice recovery must retain the hidden seal and engine receive
	// records across invoice return and prepared receive, then yield a spendable
	// allocation after broadcast/acceptance.
	blindInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "blind", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "200", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create blind OOB invoice for recovery and re-spend", err)
	receiver = corePendingCheckpoint(t, "09_blind_invoice_receiver", receiver, cfg, chain, receiverMaterial)
	beforeBlind := chain.broadcastCount()
	blindPrepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: blindInvoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare blind invoice transfer", err)
	coreAssert(t, chain.broadcastCount() == beforeBlind, "blind invoice prepare unexpectedly broadcast")
	sender = corePendingCheckpoint(t, "10_blind_prepared_sender", sender, cfg, chain, senderMaterial)
	_, err = receiver.PrepareRGB11Consignment(context.Background(), blindInvoice.RequestID,
		[]byte(blindPrepared.RecipientConsignment))
	coreRequire(t, "cold receiver validates blind OOB consignment", err)
	receiver = corePendingCheckpoint(t, "11_blind_receiver_prepared", receiver, cfg, chain, receiverMaterial)
	blindTxID, err := sender.BroadcastRGB11OutOfBand([]string{blindPrepared.State.TransferID})
	coreRequire(t, "broadcast blind OOB invoice transaction", err)
	coreAssert(t, blindTxID == blindPrepared.TxID && chain.broadcastCount() == beforeBlind+1,
		"blind OOB invoice broadcast changed or duplicated the prepared transaction")
	sender = corePendingCheckpoint(t, "12_blind_sender_broadcast", sender, cfg, chain, senderMaterial)
	_, err = receiver.AcceptRGB11Consignment(context.Background(), blindInvoice.RequestID,
		[]byte(blindPrepared.RecipientConsignment))
	coreRequire(t, "accept broadcast blind OOB invoice transfer", err)
	receiver = corePendingCheckpoint(t, "13_blind_receiver_accepted", receiver, cfg, chain, receiverMaterial)
	chain.confirm(blindTxID)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "settle blind invoice sender", err)
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "settle blind invoice receiver", err)
	left, err = sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "sender balance after blind invoice", err)
	right, err = receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "receiver balance after blind invoice", err)
	coreAssert(t, left.Value.Uint64() == 400 && right.Value.Uint64() == 600,
		"blind invoice transfer violated asset conservation")

	// A second independent cold restore proves the accepted blind allocation is
	// not only displayed but can fund a real, acknowledged follow-up transfer.
	coldBlindReceiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, coldBlindReceiver)
	_, err = coldBlindReceiver.RefreshRGB11State(context.Background())
	coreRequire(t, "freshly reconcile cold blind receiver", err)
	blindRecovered, err := coldBlindReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold blind allocation before re-spend", err)
	coreAssert(t, blindRecovered.Value.Uint64() == 600,
		"cold recovery did not retain the settled blind-invoice allocation")
	beforeBlindRespend := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, coldBlindReceiver, sender,
		issued.AssetName, "100", beforeBlindRespend)
	left, err = sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "sender balance after blind cold re-spend", err)
	right, err = coldBlindReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "cold blind receiver balance after re-spend", err)
	coreAssert(t, left.Value.Uint64() == 500 && right.Value.Uint64() == 500,
		"cold blind re-spend did not preserve the expected allocations")
}

func coreSendRGB11AddressAmountAndSettle(t *testing.T, chain *coreE2EChain,
	source, destination *Manager, assetName indexer.AssetName, amount string, beforeBroadcast int) {
	t.Helper()
	prepared, _, err := source.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: destination.GetWallet().GetAddress(), AssetName: assetName,
		AmountRaw: amount, FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare cold-recovered allocation re-spend", err)
	coreAssert(t, chain.broadcastCount() == beforeBroadcast,
		"cold-recovered allocation prepare broadcast before delivery/ACK")
	delivery, err := source.DeliverAndBroadcastConfiguredRGB11AddressTransfer(
		prepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver cold-recovered allocation to Direct mailbox", err)
	coreAssert(t, delivery.AwaitingACK && !delivery.Broadcast,
		"cold-recovered allocation crossed the Direct ACK barrier")
	coreWaitRGB11MailboxACK(t, chain, destination, prepared, beforeBroadcast)
	coreAssertRGB11ACKRecoveryPublished(t, destination, prepared)
	coreWaitRGB11MailboxBroadcast(t, chain, source, prepared, beforeBroadcast)
	chain.confirm(prepared.TxID)
	_, err = source.RefreshRGB11State(context.Background())
	coreRequire(t, "settle cold-recovered spend source", err)
	_, err = destination.RefreshRGB11State(context.Background())
	coreRequire(t, "settle cold-recovered spend destination", err)
}

// coreRejectDirtyManagedCASHTTP keeps reads and clean baseline requests on the
// real CoreNode, then fails a CAS only after the local account bundle becomes
// dirty. This exercises the post-mutation publication barrier without a
// replacement CAS server or a modified CoreModules fixture.
type coreRejectDirtyManagedCASHTTP struct {
	HttpClient
	owner        *Manager
	mu           sync.Mutex
	rejected     int
	beforeReject func()
	rejectWhen   func() bool
}

func (h *coreRejectDirtyManagedCASHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	if h != nil && h.owner != nil && url != nil &&
		strings.HasSuffix(url.Path, "/v3/dkvs/records/batch-cas") {
		h.owner.mutex.RLock()
		dirty := h.owner.accountProfile != nil && h.owner.accountProfile.ManagedDataDirty
		h.owner.mutex.RUnlock()
		if h.rejectWhen != nil {
			dirty = h.rejectWhen()
		}
		if dirty {
			h.mu.Lock()
			first := h.rejected == 0
			h.rejected++
			h.mu.Unlock()
			if first && h.beforeReject != nil {
				h.beforeReject()
			}
			return nil, &HTTPResponseError{
				StatusCode: http.StatusServiceUnavailable,
				Body:       []byte("intentional CoreModules account-data CAS outage"),
			}
		}
	}
	return h.HttpClient.SendPostRequest(url, body)
}

func coreDirectTransferE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	originalSender := sender
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ED", Name: "E2E direct asset", Amounts: []uint64{1200},
	})
	coreRequire(t, "issue Direct NIA", err)
	coreCheckpoint(t, "01_issued_sender", sender, cfg, chain, senderMaterial)
	endpoint, err := receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish Direct receive capability", err)
	coreAssert(t, !endpoint.Temporary, "paid Direct capability downgraded to temporary")
	before := chain.broadcastCount()
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: receiver.GetWallet().GetAddress(), AssetName: issued.AssetName,
		AmountRaw: "500", FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare Direct transfer", err)
	sender = corePendingCheckpoint(t, "02_prepared_sender", sender, cfg, chain, senderMaterial)
	result, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(prepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver Direct consignment via real CoreNode", err)
	coreAssert(t, result.AwaitingACK && !result.Broadcast && chain.broadcastCount() == before, "Direct broadcast crossed receiver ACK boundary")
	sender = corePendingCheckpoint(t, "03_delivered_waiting_ACK_sender", sender, cfg, chain, senderMaterial)

	// The accepted Direct consignment must be committed to the receiver's
	// stable recovery bundle before a positive mailbox ACK is sent. Reject the
	// real recovery CAS after the exact candidate is applied, then retry the
	// same mailbox ID. This barrier exports the candidate before the later ACK
	// mutation sets the profile dirty flag. Compare the first rejected boundary.
	var remoteBeforeDirectACKFault map[string]coreManagedRecordFingerprint
	var profileBeforeDirectACKFault coreManagedProfileFingerprint
	localBeforeDirectACKFault := coreLocalRGBBundleDigests(t, receiver)
	receiverStore, err := receiver.accountDKVSStore()
	coreRequire(t, "open receiver account client for Direct ACK barrier fault", err)
	oldReceiverHTTP := receiverStore.client.Http
	receiverACKFault := &coreRejectDirtyManagedCASHTTP{HttpClient: oldReceiverHTTP, owner: receiver}
	receiverACKFault.rejectWhen = func() bool {
		state, err := receiver.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
		return err == nil && state.Direction == "receive" && state.AckStatus == "persisted" &&
			state.Status == "awaiting_broadcast" && state.WitnessTxID == prepared.TxID
	}
	receiverACKFault.beforeReject = func() {
		remoteBeforeDirectACKFault = coreManagedRemoteRecords(t, receiver, cfg)
		profileBeforeDirectACKFault = coreCaptureManagedProfile(receiver)
	}
	receiverStore.client.Http = receiverACKFault
	blockedDirectACK, blockedDirectACKErr := receiver.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	receiverStore.client.Http = oldReceiverHTTP
	coreAssert(t, blockedDirectACKErr != nil || (blockedDirectACK != nil &&
		blockedDirectACK.Received == 0 && blockedDirectACK.Invalid > 0),
		"Direct receiver did not report its rejected ACK recovery barrier")
	receiverACKFault.mu.Lock()
	rejectedDirectACKCAS := receiverACKFault.rejected
	receiverACKFault.mu.Unlock()
	coreAssert(t, rejectedDirectACKCAS > 0,
		"Direct ACK fault did not reach the accepted receive recovery CAS")
	coreAssert(t, chain.broadcastCount() == before,
		"Direct receiver ACK barrier failure reached the Bitcoin backend")
	coreAssertManagedRemoteEqual(t, "rejected Direct receiver ACK barrier",
		remoteBeforeDirectACKFault, coreManagedRemoteRecords(t, receiver, cfg))
	profileAfterDirectACKFault := coreCaptureManagedProfile(receiver)
	coreAssert(t, profileAfterDirectACKFault.dirty == profileBeforeDirectACKFault.dirty &&
		profileAfterDirectACKFault.revision == profileBeforeDirectACKFault.revision &&
		profileAfterDirectACKFault.hash == profileBeforeDirectACKFault.hash,
		"failed Direct ACK barrier advanced the last-confirmed receiver account revision")
	localAfterDirectACKFault := coreLocalRGBBundleDigests(t, receiver)
	coreAssert(t, coreDigest(localAfterDirectACKFault) != coreDigest(localBeforeDirectACKFault),
		"failed Direct ACK barrier discarded its locally accepted receive material")
	coreAssert(t, coreDigest(localAfterDirectACKFault) != coreDigest(coreRemoteStableRGB(t, receiver, cfg)),
		"failed Direct ACK barrier unexpectedly confirmed its locally accepted receive material")
	directReceiveState, err := receiver.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
	coreRequire(t, "load retained Direct receive state after rejected ACK", err)
	coreAssert(t, directReceiveState.Direction == "receive" &&
		directReceiveState.Status == "awaiting_broadcast" &&
		directReceiveState.AckStatus == "persisted" &&
		directReceiveState.WitnessTxID == prepared.TxID,
		"failed Direct ACK barrier lost its exact accepted receive transition")
	directRequestID, err := receiver.rgbManager.projectionStore.LoadPreparedReceive(prepared.State.TransferID)
	coreRequire(t, "load retained Direct prepared receive request", err)
	directRequest, err := receiver.rgbManager.engine.LoadReceive(directRequestID)
	coreRequire(t, "reload retained Direct receive request", err)
	coreAssert(t, directRequest.TransferID == prepared.State.TransferID &&
		directRequest.ObjectHash == prepared.State.ConsignmentHash,
		"failed Direct ACK barrier lost the original request or consignment binding")
	ackMessageID, err := rgb11AddressMessageID(prepared.State.TransferID)
	coreRequire(t, "derive Direct ACK mailbox identity", err)
	senderRoot, err := sender.accountManagementRootWallet()
	coreRequire(t, "resolve sender root to verify no Direct ACK escaped", err)
	senderMailbox, err := sender.freshDirectMessages(senderRoot)
	coreRequire(t, "read sender mailbox before Direct ACK retry", err)
	for _, message := range senderMailbox {
		if message != nil && message.Payload != nil &&
			message.Payload.Kind == AccountMessageKindRGB11ACK &&
			message.Payload.ApplicationID == ackMessageID {
			t.Fatal("core-e2e: Direct positive ACK escaped before receiver recovery CAS")
		}
	}

	// These deterministic managers do not start background workers. Replay the
	// original signed outbox through the existing worker stage before retrying
	// the public mailbox operation that owns the recovery barrier.
	plan, err := receiver.currentAccountManagedOutboxPlan()
	coreRequire(t, "read rejected Direct receive outbox", err)
	coreAssert(t, plan.Pending, "rejected Direct receive CAS lost its original outbox")
	var submittedDirectACK bool
	var remainingDirectACK int
	err = receiver.dkvs.runTransport(func() error {
		client, err := receiver.dkvs.primaryClient()
		if err != nil {
			return err
		}
		store := newDKVSReplicaStore(receiver.db)
		submittedDirectACK, err = receiver.flushDKVSOutbox(client, store)
		if err != nil {
			return err
		}
		entries, err := store.LoadOutbox(client.replicaNamespace)
		remainingDirectACK = len(entries)
		return err
	})
	coreRequire(t, "replay original Direct receive outbox", err)
	coreAssert(t, submittedDirectACK && remainingDirectACK == 0,
		"original Direct receive outbox did not commit completely")
	coreWaitRGB11MailboxACK(t, chain, receiver, prepared, before)
	coreAssertRGB11ACKRecoveryPublished(t, receiver, prepared)
	receiver = corePendingCheckpoint(t, "04_receiver_ACK_committed", receiver, cfg, chain, receiverMaterial)
	coreAssert(t, chain.broadcastCount() == before, "passive receiver recovery broadcast sender transaction")
	// The sender must publish its ACK-accepted/broadcast-attempted state before
	// crossing the Bitcoin backend. A rejected dirty CAS retains the same txid
	// and reservation, and a later retry resumes that exact operation.
	var remoteBeforeDirectBroadcastFault map[string]coreManagedRecordFingerprint
	var profileBeforeDirectBroadcastFault coreManagedProfileFingerprint
	senderStore, err := sender.accountDKVSStore()
	coreRequire(t, "open sender account client for Direct broadcast barrier fault", err)
	oldSenderHTTP := senderStore.client.Http
	senderRootView, err := sender.captureRGB11NetworkScope(true)
	coreRequire(t, "capture Direct sender root scope for broadcast barrier", err)
	senderBroadcastFault := &coreRejectDirtyManagedCASHTTP{HttpClient: oldSenderHTTP, owner: sender}
	senderBroadcastFault.rejectWhen = func() bool {
		pending, err := senderRootView.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
		return err == nil && pending.State.Direction == "send" &&
			pending.State.Status == rgb11StatusBroadcastAttempted &&
			pending.State.AckStatus == "accepted" && pending.State.DeliveryAcknowledged &&
			pending.State.WitnessTxID == prepared.TxID
	}
	senderBroadcastFault.beforeReject = func() {
		remoteBeforeDirectBroadcastFault = coreManagedRemoteRecords(t, sender, cfg)
		profileBeforeDirectBroadcastFault = coreCaptureManagedProfile(sender)
	}
	senderStore.client.Http = senderBroadcastFault
	_, blockedDirectBroadcastErr := sender.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	senderStore.client.Http = oldSenderHTTP
	coreAssert(t, blockedDirectBroadcastErr != nil,
		"Direct sender reported broadcast success before its account recovery CAS was confirmed")
	senderBroadcastFault.mu.Lock()
	rejectedDirectBroadcastCAS := senderBroadcastFault.rejected
	senderBroadcastFault.mu.Unlock()
	coreAssert(t, rejectedDirectBroadcastCAS > 0,
		"Direct sender broadcast fault did not reach the exact broadcast-intent recovery CAS")
	coreAssert(t, chain.broadcastCount() == before,
		"Direct sender broadcast barrier failure reached the Bitcoin backend")
	coreAssertManagedRemoteEqual(t, "rejected Direct sender broadcast barrier",
		remoteBeforeDirectBroadcastFault, coreManagedRemoteRecords(t, sender, cfg))
	profileAfterDirectBroadcastFault := coreCaptureManagedProfile(sender)
	coreAssert(t, profileAfterDirectBroadcastFault.dirty == profileBeforeDirectBroadcastFault.dirty &&
		profileAfterDirectBroadcastFault.revision == profileBeforeDirectBroadcastFault.revision &&
		profileAfterDirectBroadcastFault.hash == profileBeforeDirectBroadcastFault.hash,
		"failed Direct broadcast barrier advanced the last-confirmed sender account revision")
	retainedDirectPending, err := senderRootView.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "load retained Direct sender transaction after rejected broadcast", err)
	coreAssert(t, retainedDirectPending.State.WitnessTxID == prepared.TxID &&
		retainedDirectPending.State.AckStatus == "accepted" &&
		rgb11TransferKeepsInputsLocked(&retainedDirectPending.State),
		"failed Direct broadcast barrier lost the accepted transaction identity or input lock")
	coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, true)
	coreAssert(t, coreDigest(coreLocalRGBBundleDigests(t, sender)) != coreDigest(coreRemoteStableRGB(t, sender, cfg)),
		"failed Direct broadcast barrier unexpectedly confirmed its ACK-accepted state")

	plan, err = sender.currentAccountManagedOutboxPlan()
	coreRequire(t, "read rejected Direct sender outbox", err)
	coreAssert(t, plan.Pending, "rejected Direct broadcast CAS lost its original outbox")
	var submittedDirectBroadcast bool
	var remainingDirectBroadcast int
	err = sender.dkvs.runTransport(func() error {
		client, err := sender.dkvs.primaryClient()
		if err != nil {
			return err
		}
		store := newDKVSReplicaStore(sender.db)
		submittedDirectBroadcast, err = sender.flushDKVSOutbox(client, store)
		if err != nil {
			return err
		}
		entries, err := store.LoadOutbox(client.replicaNamespace)
		remainingDirectBroadcast = len(entries)
		return err
	})
	coreRequire(t, "replay original Direct sender outbox", err)
	coreAssert(t, submittedDirectBroadcast && remainingDirectBroadcast == 0,
		"original Direct sender outbox did not commit completely")
	coreWaitRGB11MailboxBroadcast(t, chain, sender, prepared, before)
	sender = corePendingCheckpoint(t, "05_ACK_and_broadcast_sender", sender, cfg, chain, senderMaterial)
	_, err = receiver.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "receiver discovers broadcast", err)
	receiver = corePendingCheckpoint(t, "06_broadcast_receiver", receiver, cfg, chain, receiverMaterial)

	chain.confirm(prepared.TxID)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "settle Direct sender", err)
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "settle Direct receiver", err)
	coreCheckpoint(t, "07_settled_sender", sender, cfg, chain, senderMaterial)
	coreCheckpoint(t, "08_settled_receiver", receiver, cfg, chain, receiverMaterial)
	left, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "remaining Direct amount", err)
	right, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "received Direct amount", err)
	coreAssert(t, left.Value.Uint64() == 700 && right.Value.Uint64() == 500, "Direct transfer violated asset conservation")
	for i := 0; i < 2; i++ {
		_, err = sender.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		coreRequire(t, "replayed sender mailbox", err)
		_, err = receiver.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		coreRequire(t, "replayed receiver mailbox", err)
	}
	coreAssert(t, chain.broadcastCount() == before+1, "mailbox replay rebroadcast the settled transaction")

	// The parent still owns the prepared device. Its normal account sync must
	// adopt the confirmed cold writer's complete recovery closure.
	coreRequire(t, "verify cold sender confirmed closure before original-device sync",
		sender.verifyCurrentRGB11RecoveryClosure())
	originalPending, err := originalSender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read original prepared pending for recovery diagnosis", err)
	selectedPending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read cold settled pending for recovery diagnosis", err)
	coreAssert(t, originalPending != nil && selectedPending != nil,
		"original/cold sender lost the shared Direct pending identity")
	coreRequire(t, "synchronize original prepared sender after cold Direct settlement",
		originalSender.SyncAccountManagementState(context.Background()))
	coreAssertMapEqual(t, "original sender converged to confirmed Direct recovery closure",
		coreLocalRGBBundleDigests(t, originalSender), coreRemoteStableRGB(t, sender, cfg))
	coreAssert(t, chain.broadcastCount() == before+1,
		"original-device synchronization rebroadcast the settled Direct transaction")
}
