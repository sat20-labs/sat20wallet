package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreInvoiceTransferE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	originalSender, originalReceiver := sender, receiver
	coreTransferDeviceCleanup(t, originalSender, originalReceiver)
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
	// A new invoice is durably local before its independent backup. Keep the
	// real outage active through the worker attempt, then recover before new work.
	walletID, err := receiver.rgbManager.RGB11WalletID()
	coreRequire(t, "identify receiver scope before invoice backup fault", err)
	beforeInvoice, _, err := receiver.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "snapshot receiver before invoice backup fault", err)
	finishInvoiceBackup := coreRGBBackupOutage(t, receiver, cfg, issued.ContractID)
	localInvoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{proxyEndpoint}, ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "persist invoice independently of its backup", err)
	coreAssert(t, localInvoice != nil, "local invoice result is absent")
	afterInvoice, _, err := receiver.rgbManager.exportRGB11WalletSnapshot(walletID)
	coreRequire(t, "snapshot locally persisted invoice", err)
	coreAssert(t, len(afterInvoice.EngineRecords) == len(beforeInvoice.EngineRecords)+1,
		"invoice did not persist exactly one receive request")
	localRequest, err := receiver.rgbManager.engine.LoadReceive(localInvoice.RequestID)
	coreRequire(t, "read exact persisted invoice", err)
	coreAssert(t, localRequest.Invoice == localInvoice.Invoice && len(localRequest.WitnessScript) != 0,
		"local invoice omitted its exact invoice or witness recovery material")
	finishInvoiceBackup()
	coreAssertMapEqual(t, "recovered invoice backup local/confirmed recovery",
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
	store, err := sender.accountDKVSStore()
	coreRequire(t, "open sender's real account DKVS client for outage injection", err)
	oldHTTP := store.client.Http
	store.client.Http = coreOfflineHTTP{HttpClient: oldHTTP}
	coreAssert(t, sender.SyncAccountManagementState(context.Background()) != nil,
		"controlled offline transport did not fail a real synchronization")
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

	coreReplayManagedOutbox(t, sender)

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
	// The positive Standard ACK requires complete local recovery material;
	// remote backup failure is observed separately and cannot undo that ACK.
	localBeforeStandardACKFault := coreLocalRGBBundleDigests(t, receiver)
	finishStandardACK := coreRGBBackupOutage(t, receiver, cfg, issued.ContractID)
	proxy.mu.Lock()
	proxy.ackPostHTTPStatus = http.StatusBadGateway // persist the first ACK, then lose its response
	proxy.mu.Unlock()
	acceptedReceive, err := receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
	coreRequire(t, "locally accept and ACK Standard consignment", err)
	coreAssert(t, acceptedReceive != nil && acceptedReceive.AckPosted && acceptedReceive.AwaitingBroadcast,
		"Standard receive omitted its locally durable ACK")
	proxy.mu.Lock()
	standardACK := proxy.ack != nil && *proxy.ack
	proxy.mu.Unlock()
	coreAssert(t, standardACK && chain.broadcastCount() == before,
		"receiver ACK was missing or broadcast the sender transaction")
	localAfterStandardACKFault := coreLocalRGBBundleDigests(t, receiver)
	coreAssert(t, coreDigest(localAfterStandardACKFault) != coreDigest(localBeforeStandardACKFault),
		"Standard receive did not persist the accepted material")
	finishStandardACK()
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

	// The exact signed transaction and intent are local before broadcast;
	// a subsequent asynchronous backup outage cannot turn that result unknown.
	finishStandardBroadcast := coreRGBBackupOutage(t, sender, cfg, issued.ContractID)
	firstBroadcast, err := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	coreRequire(t, "broadcast the locally committed Standard transaction", err)
	coreAssert(t, firstBroadcast != nil && firstBroadcast.Broadcast && firstBroadcast.TxID == prepared.TxID &&
		chain.broadcastCount() == before+1, "Standard ACK did not authorize exactly the signed transaction")
	retainedStandardPending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "reload exact sender transaction before backup recovery", err)
	coreAssert(t, retainedStandardPending.State.WitnessTxID == prepared.TxID &&
		retainedStandardPending.State.AckStatus == "accepted" &&
		rgb11TransferKeepsInputsLocked(&retainedStandardPending.State),
		"asynchronous backup lost the accepted transaction identity or input lock")
	coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, true)
	finishStandardBroadcast()

	coreReplayManagedOutbox(t, sender)
	delivered, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	coreRequire(t, "sender consumes positive proxy ACK and broadcasts", err)
	txid := delivered.TxID
	coreAssert(t, delivered.Broadcast && !delivered.AwaitingACK && txid == prepared.TxID &&
		chain.broadcastCount() == before+1, "proxy ACK did not authorize exactly the original transaction")
	sender = corePendingCheckpoint(t, "06_broadcast_sender", sender, cfg, chain, senderMaterial)

	coreAssert(t, len(prepared.State.InputOutPoints) > 0, "Standard transfer must consume actual Bitcoin inputs")
	for _, point := range prepared.State.InputOutPoints {
		spend, err := chain.GetOutspend(point)
		coreRequire(t, "read mempool outspend", err)
		coreAssert(t, spend.Spent && spend.SpendingTx == txid, "mempool outspend lost the actual spender")
	}
	chain.confirm(txid)
	for _, point := range prepared.State.InputOutPoints {
		spend, err := chain.GetOutspend(point)
		coreRequire(t, "read confirmed outspend", err)
		coreAssert(t, spend.Spent && spend.SpendingTx == "", "confirmed outspend must hide identity without becoming unspent")
		coreAssert(t, coreConfirmedHistoricalOutput(t, sender, point), "internal confirmed-spend oracle lost historical evidence")
	}
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
	coreWaitPrivateBackup(t, receiver)
	receiver.StopDKVSSync()
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
	coreWaitPrivateBackup(t, receiver)
	receiver.StopDKVSSync()
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
	coreWaitPrivateBackup(t, sender)
	coreWaitPrivateBackup(t, coldBlindReceiver)
}

// Register before any checkpoint creates cold participants. Their Close
// cleanups run first; the outer groups then reuse the original online devices.
func coreTransferDeviceCleanup(t *testing.T, sender, receiver *Manager) {
	t.Helper()
	t.Cleanup(func() {
		senderErr := sender.StartDKVSSync()
		receiverErr := receiver.StartDKVSSync()
		coreRequire(t, "resume original transfer sender", senderErr)
		coreRequire(t, "resume original transfer receiver", receiverErr)
		coreSynchronizeRestoredAccount(t, sender)
		coreSynchronizeRestoredAccount(t, receiver)
		coreWaitPrivateBackup(t, sender)
		coreWaitPrivateBackup(t, receiver)
	})
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
	coreDeliverRGB11OfflineACK(t, chain, source, destination, prepared, beforeBroadcast)
	chain.confirm(prepared.TxID)
	_, err = source.RefreshRGB11State(context.Background())
	coreRequire(t, "settle cold-recovered spend source", err)
	_, err = destination.RefreshRGB11State(context.Background())
	coreRequire(t, "settle cold-recovered spend destination", err)
	coreSyncRGBCheckpoint(t, source, destination)
}

// Reject only the selected RGB module's real asynchronous CAS. All other
// requests continue to use the existing CoreNode and authorization checks.
type coreRejectDirtyManagedCASHTTP struct {
	HttpClient
	owner          *Manager
	mu             sync.Mutex
	rejected       int
	key            string
	failed         chan struct{}
	requestID      string
	recordHash     string
	changedRequest bool
}

// Gate export before it acquires local RGB read gates or enters the transport
// coordinator. Holding a CAS instead would block Direct's later Core binding.
// All actual export/validation/import methods remain the production provider's.
type coreRGBBackupExport struct {
	*rgb11AccountManagedDataProvider
	scope   string
	release chan struct{}
	entered chan struct{}
}

func (p *coreRGBBackupExport) Export(catalog AccountManagedDataCatalog) ([]AccountManagedDataPayload, error) {
	for _, scope := range catalog.Scopes {
		if scope.ID() == p.scope {
			select {
			case p.entered <- struct{}{}:
			default:
			}
			<-p.release
			break
		}
	}
	return p.rgb11AccountManagedDataProvider.Export(catalog)
}

var _ ContextHttpClient = (*coreRejectDirtyManagedCASHTTP)(nil)

func (h *coreRejectDirtyManagedCASHTTP) SendGetRequestContext(ctx context.Context, url *URL) ([]byte, error) {
	return h.HttpClient.(ContextHttpClient).SendGetRequestContext(ctx, url)
}

func (h *coreRejectDirtyManagedCASHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	return h.SendPostRequestContext(context.Background(), url, body)
}

func (h *coreRejectDirtyManagedCASHTTP) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	if h != nil && h.owner != nil && url != nil &&
		strings.HasSuffix(url.Path, "/v3/dkvs/records/batch-cas") {
		var request DKVSBatchCASRequest
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		dirty := false
		recordHash := ""
		for _, mutation := range request.Mutations {
			dirty = dirty || mutation.Record != nil && mutation.Record.Key == h.key
			if mutation.Record != nil && mutation.Record.Key == h.key {
				recordHash = dkvsindexer.RecordHash(mutation.Record).String()
			}
		}
		if dirty {
			h.mu.Lock()
			h.rejected++
			if h.requestID == "" {
				h.requestID = request.RequestID
				h.recordHash = recordHash
			} else if h.requestID != request.RequestID || h.recordHash != recordHash {
				h.changedRequest = true
			}
			h.mu.Unlock()
			select {
			case h.failed <- struct{}{}:
			default:
			}
			return nil, &HTTPResponseError{
				StatusCode: http.StatusServiceUnavailable,
				Body:       []byte("intentional CoreModules account-data CAS outage"),
			}
		}
	}
	return h.HttpClient.(ContextHttpClient).SendPostRequestContext(ctx, url, body)
}

// Keep one real account module outage across the local business operation and
// the existing worker's attempted publication. Neither this fixture nor its
// assertions replace the node, signer, local transaction or backup payload.
func coreRGBBackupOutage(t *testing.T, manager *Manager, cfg coreE2EConfig, contractID string) func() {
	t.Helper()
	// A device can resume through a foreground operation before its automatic
	// receiver starts. Establish real account qualification without polling RGB.
	readyCtx, cancelReady := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReady()
	coreRequire(t, "synchronize account before local business backup fault", manager.SyncAccountManagementState(readyCtx))
	coreRequire(t, "confirm original module baseline before backup fault", manager.WaitAccountManagedDataReady(readyCtx))
	cancelReady()
	scope := coreSelectedRGBScope(manager)
	accountID := manager.GetAccountManagementStatus().AccountID
	before, err := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
	coreRequire(t, "read healthy RGB module before backup outage", err)
	coreAssert(t, before != nil && before.Baseline != nil && before.Generation == before.Confirmed,
		"backup outage requires a confirmed module baseline")
	remote := coreManagedRemoteRecords(t, manager, cfg)
	store, err := manager.accountDKVSStore()
	coreRequire(t, "open actual Core backup transport", err)
	oldHTTP := store.client.Http
	_, contextOK := oldHTTP.(ContextHttpClient)
	coreAssert(t, contextOK, "backup fault requires context-aware Core transport")
	provider, ok := manager.accountProvider(rgb11AccountManagedProviderID).(*rgb11AccountManagedDataProvider)
	coreAssert(t, ok && provider != nil, "backup window requires the actual RGB provider")
	gate := &coreRGBBackupExport{rgb11AccountManagedDataProvider: provider, scope: scope,
		release: make(chan struct{}), entered: make(chan struct{}, 1)}
	manager.managedDataMu.Lock()
	manager.managedDataProviders[rgb11AccountManagedProviderID] = gate
	manager.managedDataMu.Unlock()
	var releaseOnce sync.Once
	releaseExport := func() {
		releaseOnce.Do(func() {
			close(gate.release)
			manager.managedDataMu.Lock()
			manager.managedDataProviders[rgb11AccountManagedProviderID] = provider
			manager.managedDataMu.Unlock()
		})
	}
	fault := &coreRejectDirtyManagedCASHTTP{HttpClient: oldHTTP, owner: manager, key: before.Key, failed: make(chan struct{}, 1)}
	store.client.Http = fault
	t.Cleanup(func() {
		releaseExport()
		store.client.Http = oldHTTP
	})
	return func() {
		// Bound only the worker observation, not the local business operation and
		// its snapshot checks. The export gate itself stays open until cleanup or
		// until the real worker enters this provider after the business commit.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		defer releaseExport()
		manager.dkvs.mu.Lock()
		running := manager.dkvs.stop != nil
		manager.dkvs.mu.Unlock()
		if !running {
			coreRequire(t, "resume this device backup worker after local business", manager.StartDKVSSync())
		}
		select {
		case <-gate.entered:
		case <-ctx.Done():
			t.Fatal("existing background worker did not enter this RGB module export")
		}
		// Release only after observing the real worker call. The normal
		// coalesced notification remains the trigger; this does not fake readiness
		// or bypass the provider/export path.
		releaseExport()
		select {
		case <-fault.failed:
		case <-ctx.Done():
			t.Fatal("existing background worker did not attempt this RGB module backup")
		}
		// Observe completion through the real account coordinator; a channel
		// notification alone occurs before the HTTP failure is persisted.
		backupErr := manager.SyncAccountManagementState(ctx)
		fault.mu.Lock()
		hits := fault.rejected
		requestID, changedRequest := fault.requestID, fault.changedRequest
		recordHash := fault.recordHash
		fault.mu.Unlock()
		var httpFailure *HTTPResponseError
		asHTTP := errors.As(backupErr, &httpFailure)
		httpStatus := 0
		if asHTTP {
			httpStatus = httpFailure.StatusCode
		}
		var pendingRequest *DKVSRequestPendingError
		asPending := errors.As(backupErr, &pendingRequest)
		sameRequest := asPending && requestID != "" && pendingRequest.RequestID == requestID
		isPending := errors.Is(backupErr, ErrDKVSPathNotSynced)
		if !sameRequest || !isPending || hits == 0 || changedRequest {
			t.Logf("core backup failure: type=%T unsynced=%t deadline=%t http=%t status=%d pending=%t same_request=%t changed_request=%t hits=%d",
				backupErr, isPending, errors.Is(backupErr, context.DeadlineExceeded), asHTTP, httpStatus, asPending, sameRequest, changedRequest, hits)
		}
		coreAssert(t, sameRequest && isPending && hits > 0 && !changedRequest,
			"controlled HTTP503 did not retain its exact request as a pending backup")
		pending, err := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
		coreRequire(t, "read durable pending RGB module", err)
		coreAssert(t, pending != nil && pending.Generation > before.Generation && pending.Generation > pending.Confirmed &&
			pending.Baseline != nil && dkvsindexer.RecordHash(pending.Baseline) == dkvsindexer.RecordHash(before.Baseline),
			"failed backup lost local changes or advanced their confirmed baseline")
		replica := newDKVSReplicaStore(manager.db)
		entries, err := replica.LoadOutbox(store.client.replicaNamespace)
		coreRequire(t, "read retained signed backup request", err)
		var frozen *DKVSBatchOutboxEntry
		for _, entry := range entries {
			if entry.OriginDomain == accountKVOutboxDomain && entry.OriginKey == before.Key {
				coreAssert(t, frozen == nil, "backup outage created duplicate module intents")
				frozen = entry
			}
		}
		coreAssert(t, frozen != nil && frozen.RequestID == requestID &&
			(frozen.State == DKVSOutboxPending || frozen.State == DKVSOutboxInflight) &&
			frozen.OriginGeneration > before.Generation && frozen.OriginGeneration <= pending.Generation,
			"response-unknown backup lost its original frozen request")
		mutations, err := frozen.DecodeMutations()
		coreRequire(t, "decode original signed backup request", err)
		coreAssert(t, len(mutations) == 1 && mutations[0].Record != nil && mutations[0].Record.Key == before.Key,
			"retained request changed its exact module identity")
		frozenHash := dkvsindexer.RecordHash(mutations[0].Record).String()
		coreAssert(t, frozenHash == recordHash, "pending backup replaced the actual observed signed record")
		frozenSeq := mutations[0].Record.Seq
		coreAssertManagedRemoteEqual(t, "failed asynchronous RGB backup", remote, coreManagedRemoteRecords(t, manager, cfg))
		local := coreLocalRGBBundleDigests(t, manager)
		coreAssert(t, contractID != "" && errors.Is(manager.CheckAccountWrite(rgb11AccountManagedProviderID, scope), ErrDKVSPathNotSynced),
			"negative control did not establish the actual account write gate failure")
		rejected, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: contractID,
			AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreAssert(t, rejected == nil && errors.Is(err, ErrDKVSPathNotSynced),
			"new business bypassed the actual failed online synchronization")
		coreAssertMapEqual(t, "rejected new business retained local recovery", local, coreLocalRGBBundleDigests(t, manager))
		unchanged, err := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
		coreRequire(t, "read module after rejected new business", err)
		coreAssert(t, unchanged.Generation == pending.Generation,
			"rejected business advanced the durable module generation")
		store.client.Http = oldHTTP
		coreReplayManagedOutbox(t, manager)
		coreWaitPrivateBackup(t, manager)
		confirmed := coreManagedRemoteRecords(t, manager, cfg)
		// One operation can commit another local generation while N is queued.
		// The node enforces seq+1; recovery may already have published that exact
		// newer local closure after completing N, but may never roll back N.
		coreAssert(t, confirmed[before.Key].seq >= frozenSeq &&
			(confirmed[before.Key].seq != frozenSeq || confirmed[before.Key].hash == frozenHash),
			"recovered backup rolled back or replaced the exact accepted sequence")
		entries, err = replica.LoadOutbox(store.client.replicaNamespace)
		coreRequire(t, "read outbox after original backup completes", err)
		for _, entry := range entries {
			coreAssert(t, entry.RequestID != frozen.RequestID && entry.OriginKey != before.Key,
				"recovered module still has an unresolved original request")
		}
		coreAssertMapEqual(t, "backup recovery retained exact local materials", local, coreLocalRGBBundleDigests(t, manager))
		coreAssertMapEqual(t, "backup recovery confirmed complete local materials", local, coreRemoteStableRGB(t, manager, cfg))
	}
}

func coreDirectTransferE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	originalSender, originalReceiver := sender, receiver
	coreTransferDeviceCleanup(t, originalSender, originalReceiver)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ED", Name: "E2E direct asset", Amounts: []uint64{1200},
	})
	coreRequire(t, "issue Direct NIA", err)
	coreCheckpoint(t, "01_issued_sender", sender, cfg, chain, senderMaterial)
	endpoint, err := receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish Direct receive capability", err)
	coreAssert(t, !endpoint.Temporary, "paid Direct capability downgraded to temporary")
	// The recipient device is offline until the sender's delivered, unACKed
	// recovery checkpoint is complete. Its registered public endpoint remains.
	coreSuspendRGBDevice(t, receiver)
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
	// Join this sender before the recipient goes online and publishes its ACK.
	// It resumes only after its own broadcast-backup fault has been installed.
	coreSuspendRGBDevice(t, sender)

	// Direct receive has the same local commit/ACK boundary as Standard.
	localBeforeDirectACKFault := coreLocalRGBBundleDigests(t, receiver)
	finishDirectACK := coreRGBBackupOutage(t, receiver, cfg, issued.ContractID)
	directACK, err := receiver.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "locally accept and ACK Direct consignment", err)
	coreAssert(t, directACK != nil && directACK.Received == 1 && chain.broadcastCount() == before,
		"Direct receive did not commit exactly once ahead of Bitcoin broadcast")
	localAfterDirectACKFault := coreLocalRGBBundleDigests(t, receiver)
	coreAssert(t, coreDigest(localAfterDirectACKFault) != coreDigest(localBeforeDirectACKFault),
		"Direct receive discarded its locally accepted material")
	directReceiveState, err := receiver.rgbManager.projectionStore.LoadTransferState(prepared.State.TransferID)
	coreRequire(t, "load locally durable Direct ACK", err)
	coreAssert(t, directReceiveState.Direction == "receive" && directReceiveState.Status == "awaiting_broadcast" &&
		directReceiveState.AckStatus == "ack-sent" && directReceiveState.DeliveryAcknowledged &&
		directReceiveState.WitnessTxID == prepared.TxID,
		"Direct ACK lost the exact accepted transition")
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
	coreRequire(t, "resolve sender root to read actual Direct ACK", err)
	senderMailbox, err := sender.freshDirectMessages(senderRoot)
	coreRequire(t, "read sender mailbox after local Direct ACK", err)
	ackCount := 0
	for _, message := range senderMailbox {
		if message != nil && message.Payload != nil && message.Payload.Kind == AccountMessageKindRGB11ACK &&
			message.Payload.ApplicationID == ackMessageID {
			ackCount++
		}
	}
	coreAssert(t, ackCount == 1, "Direct receive did not publish exactly one identity-bound ACK")
	finishDirectACK()
	coreWaitRGB11MailboxACK(t, chain, receiver, prepared, before)
	coreAssertRGB11ACKRecoveryPublished(t, receiver, prepared)
	receiver = corePendingCheckpoint(t, "04_receiver_ACK_committed", receiver, cfg, chain, receiverMaterial)
	coreAssert(t, chain.broadcastCount() == before, "passive receiver recovery broadcast sender transaction")
	// Backup runs after the locally recorded ACK and actual broadcast. Keep
	// the same reservation and transaction across its controlled outage.
	senderRootView, err := sender.captureRGB11NetworkScope(true)
	coreRequire(t, "capture Direct sender scope", err)
	finishDirectBroadcast := coreRGBBackupOutage(t, sender, cfg, issued.ContractID)
	_, err = sender.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "consume Direct ACK and broadcast local transaction", err)
	coreAssert(t, chain.broadcastCount() == before+1, "Direct ACK did not broadcast exactly once")
	retainedDirectPending, err := senderRootView.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "load locally retained Direct transaction", err)
	coreAssert(t, retainedDirectPending.State.WitnessTxID == prepared.TxID &&
		retainedDirectPending.State.AckStatus == "accepted" &&
		rgb11TransferKeepsInputsLocked(&retainedDirectPending.State),
		"Direct backup outage lost the accepted identity or input lock")
	coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, true)
	finishDirectBroadcast()
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
	coreWaitPrivateBackup(t, sender)
	coreRequire(t, "verify cold sender confirmed closure before original-device sync",
		sender.verifyCurrentRGB11RecoveryClosure())
	// This cold sender has finished its business. Join it before the original
	// device comes online, so only one device can consume this root's mailbox.
	sender.StopDKVSSync()
	coreRequire(t, "resume original prepared sender for recovery comparison", originalSender.StartDKVSSync())
	coreRequire(t, "synchronize original prepared sender after cold Direct settlement",
		originalSender.SyncAccountManagementState(context.Background()))
	originalPending, err := originalSender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read original prepared pending for recovery diagnosis", err)
	selectedPending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read cold settled pending for recovery diagnosis", err)
	coreAssert(t, originalPending != nil && selectedPending != nil,
		"original/cold sender lost the shared Direct pending identity")
	coreAssertMapEqual(t, "original sender converged to confirmed Direct recovery closure",
		coreLocalRGBBundleDigests(t, originalSender), coreRemoteStableRGB(t, sender, cfg))
	coreAssert(t, chain.broadcastCount() == before+1,
		"original-device synchronization rebroadcast the settled Direct transaction")
	coreWaitPrivateBackup(t, originalSender)
	coreWaitPrivateBackup(t, receiver)
}
