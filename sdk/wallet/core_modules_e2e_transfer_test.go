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
	corewallet "github.com/sat20-labs/rgb11/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreInvoiceTransferE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	if !coreProxySafetyE2E(t, chain, sender, receiver) {
		return
	}
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
	finishInvoiceBackup := coreRGBBackupOutage(t, receiver, cfg, issued.ContractID, true)
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
	coreRequire(t, "prepare RGB11 invoice transfer", err)
	preparedJSON, err := json.Marshal(prepared)
	coreRequire(t, "serialize public standard-proxy prepare result", err)
	coreAssert(t, !bytes.Contains(preparedJSON, []byte(`"signed_psbt"`)),
		"public CoreModules Prepare result exposed a broadcastable signed PSBT")
	coreAssert(t, chain.broadcastCount() == before, "prepare unexpectedly broadcast")
	sender = corePendingCheckpoint(t, "03_prepared_sender", sender, cfg, chain, senderMaterial)
	// This is the first external confirmation of an unsigned prepared transfer,
	// not completion of an already ACKed or broadcast transaction. A real account
	// outage must reject it without losing the original candidate or reservation.
	store, err := sender.accountDKVSStore()
	coreRequire(t, "open sender's account backup transport", err)
	beforeDelivery, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read unconfirmed proxy candidate before outage", err)
	coreAssert(t, beforeDelivery.State.Status == "prepared" && !beforeDelivery.State.DeliveryAcknowledged &&
		beforeDelivery.State.AckStatus != "accepted" && beforeDelivery.State.AckStatus != "accepted-out-of-band" &&
		(beforeDelivery.ChannelSend == nil || !beforeDelivery.ChannelSend.Signed) && !rgb11PendingHasWitness(beforeDelivery),
		"offline first-confirmation fixture was already acknowledged or signed")
	oldHTTP := store.client.Http
	store.client.Http = coreOfflineHTTP{HttpClient: oldHTTP}
	coreAssert(t, sender.SyncAccountManagementState(context.Background()) != nil,
		"controlled offline transport did not fail a real synchronization")
	queued, queueErr := sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	store.client.Http = oldHTTP
	coreAssert(t, errors.Is(queueErr, ErrDKVSPathNotSynced) && queued == nil,
		"offline account accepted a first proxy confirmation")
	afterRejected, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read original candidate after rejected offline confirmation", err)
	coreAssert(t, coreDigest(beforeDelivery) == coreDigest(afterRejected),
		"offline rejection changed the exact prepared transaction or pending state")
	coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, true)
	proxy.mu.Lock()
	unpublished := proxy.txID == "" && len(proxy.consignment) == 0 && proxy.ack == nil
	proxy.mu.Unlock()
	coreAssert(t, unpublished && chain.broadcastCount() == before,
		"offline rejection posted a consignment, ACK or Bitcoin transaction")
	coreReplayManagedOutbox(t, sender)
	proxy.mu.Lock()
	proxy.postHTTPStatus = http.StatusBadGateway // store proof, then lose the first response
	proxy.mu.Unlock()
	queued, queueErr = sender.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{prepared.State.TransferID})
	coreRequire(t, "confirm the same proxy candidate after account recovery", queueErr)
	coreAssert(t, queued != nil && queued.AwaitingACK && !queued.Broadcast,
		"independent proxy delivery did not retain its ACK boundary")
	coreAssert(t, chain.broadcastCount() == before,
		"proxy delivery without ACK caused a Bitcoin broadcast")
	proxy.mu.Lock()
	postedOriginal := proxy.txID == prepared.TxID && len(proxy.consignment) != 0
	proxy.postHTTPStatus = 0
	proxy.mu.Unlock()
	coreAssert(t, postedOriginal, "recovered confirmation did not post the original proof and transaction")
	preparedPending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "read pending transfer after recovered account delivery", err)
	coreAssert(t, preparedPending.State.WitnessTxID == prepared.TxID &&
		rgb11TransferKeepsInputsLocked(&preparedPending.State),
		"delivery changed the original transaction or released its inputs")

	coreReplayManagedOutbox(t, sender)

	t.Run("malformed_consignment_cannot_create_ownership", func(t *testing.T) {
		_, err := receiver.ValidateRGB11Consignment(context.Background(), []byte("not an RGB consignment"))
		coreAssert(t, err != nil, "malformed consignment accepted")
		balance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
		coreRequire(t, "balance after invalid payload", err)
		coreAssert(t, balance.Sign() == 0, "invalid consignment changed ownership")
	})
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
	finishStandardACK := coreRGBBackupOutage(t, receiver, cfg, issued.ContractID, false)
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
	finishStandardBroadcast := coreRGBBackupOutage(t, sender, cfg, issued.ContractID, false)
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
	timeout        bool
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
			if h.timeout {
				return nil, context.DeadlineExceeded
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
func coreRGBBackupOutage(t *testing.T, manager *Manager, cfg coreE2EConfig, contractID string, timeout bool) func() {
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
	fault := &coreRejectDirtyManagedCASHTTP{HttpClient: oldHTTP, owner: manager, key: before.Key, failed: make(chan struct{}, 1), timeout: timeout}
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
			"failed backup did not retain its exact request as pending")
		if timeout {
			_, sendErr, receiveErr := manager.syncDKVSOnceResult()
			coreRequire(t, "receiver stays online during backup replay", receiveErr)
			coreAssert(t, errors.Is(sendErr, context.DeadlineExceeded), "backup replay must report the POST timeout")
		}
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
		err = manager.CheckAccountWrite(rgb11AccountManagedProviderID, scope)
		if err != nil {
			manager.mutex.RLock()
			accountScope := manager.accountWriteScopeLocked()
			manager.mutex.RUnlock()
			t.Logf("backup outage: worker_already_running=%t account_ready=%t read_ready=%t write_ready=%t restoring=%t conflict=%t",
				running, manager.dkvs.scopeReady(accountScope), manager.dkvs.scopeReady(manager.dkvs.sessionKey(store.client, "read")),
				manager.dkvs.scopeReady(manager.dkvs.sessionKey(store.client, "write")), pending.Restoring, pending.Conflict)
		}
		coreRequire(t, "upload failure keeps local RGB writable", err)
		nextInvoice, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: contractID,
			AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		coreRequire(t, "persist new invoice during failed backup", err)
		coreAssert(t, nextInvoice != nil, "local invoice result is missing during failed backup")
		changed, err := manager.readAccountLocal(accountID, rgb11AccountManagedProviderID, scope)
		coreRequire(t, "read next local generation during failed backup", err)
		coreAssert(t, changed.Generation > pending.Generation && changed.Confirmed == pending.Confirmed,
			"new business did not persist its independent unconfirmed generation")
		updated := coreLocalRGBBundleDigests(t, manager)
		coreAssert(t, coreDigest(updated) != coreDigest(local), "new invoice did not change durable local material")
		local = updated
		entries, err = replica.LoadOutbox(store.client.replicaNamespace)
		coreRequire(t, "read original request after another local edit", err)
		retained := false
		for _, entry := range entries {
			if entry.OriginKey != before.Key {
				continue
			}
			coreAssert(t, entry.RequestID == frozen.RequestID, "another local edit replaced the frozen request")
			stored, err := entry.DecodeMutations()
			coreRequire(t, "read frozen mutation after local edit", err)
			coreAssert(t, len(stored) == 1 && dkvsindexer.RecordHash(stored[0].Record).String() == frozenHash,
				"another local edit changed the original signed record")
			retained = true
		}
		coreAssert(t, retained, "new local edit discarded the unresolved backup request")

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
	result, err := sender.rgbManager.deliverRGBAddress(prepared.State.TransferID)
	coreRequire(t, "deliver Direct consignment via real CoreNode", err)
	coreAssert(t, result.RecordHash != "" && !result.Broadcast && chain.broadcastCount() == before, "delivery-only checkpoint must retain the committed mailbox receipt")
	sender = corePendingCheckpoint(t, "03_mailbox_committed_sender", sender, cfg, chain, senderMaterial)
	// Join this sender before the recipient goes online and publishes its ACK.
	// It resumes only after its own broadcast-backup fault has been installed.
	coreSuspendRGBDevice(t, sender)

	// Direct receive has the same local commit/ACK boundary as Standard.
	localBeforeDirectACKFault := coreLocalRGBBundleDigests(t, receiver)
	finishDirectACK := coreRGBBackupOutage(t, receiver, cfg, issued.ContractID, false)
	directACK, err := receiver.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{})
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
	senderMailbox, err := sender.readAccountMessages(senderRoot, dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "read sender mailbox after local Direct ACK", err)
	ackCount := 0
	for _, message := range senderMailbox {
		if message != nil && message.Kind == AccountMessageKindRGB11ACK &&
			message.ID == ackMessageID {
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
	finishDirectBroadcast := coreRGBBackupOutage(t, sender, cfg, issued.ContractID, false)
	_, err = sender.SyncConfiguredRGB11AddressMailbox(context.Background(),
		dkvsindexer.RecordVerificationOptions{})
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
	_, err = receiver.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{})
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
		_, err = sender.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{})
		coreRequire(t, "replayed sender mailbox", err)
		_, err = receiver.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{})
		coreRequire(t, "replayed receiver mailbox", err)
	}
	coreAssert(t, chain.broadcastCount() == before+1, "mailbox replay rebroadcast the settled transaction")

	// The parent still owns the prepared device. Its normal account sync must
	// adopt the confirmed cold writer's complete recovery closure.
	coreWaitPrivateBackup(t, sender)
	coreRequire(t, "verify cold sender confirmed closure before original-device sync",
		sender.CheckAccountWrite(rgb11AccountManagedProviderID, coreSelectedRGBScope(sender)))
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

// Exercise the PWA cancellation entry and standard proxy receive lifecycle on
// the existing connected accounts. Only the existing Bitcoin evidence boundary
// is faulted; invoices, consignments, locks, ACKs and signatures remain real.
func coreProxySafetyE2E(t *testing.T, chain *coreE2EChain, sender, receiver *Manager) bool {
	controlled, ok := sender.l1IndexerClient.getActiveIndexer().(*coreE2EL1Indexer)
	coreAssert(t, ok && controlled.chain == chain, "proxy safety fixture is not using its controlled Bitcoin indexer")
	// Each case uses a fresh proxy, invoice and reservation for the same public contract.
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "SAFE", Name: "Proxy safety", Amounts: []uint64{10},
	})
	coreRequire(t, "issue proxy safety asset", err)
	coreWaitPrivateBackup(t, sender)

	for _, name := range []string{"proxy_no_ack_cancel", "proxy_expired_cancel", "proxy_expired_nack", "proxy_first_ack_retry", "proxy_prepared_expiry"} {
		if !t.Run(name, func(t *testing.T) {
			proxy := &rgb11ProxyTestServer{}
			server := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
			defer server.Close()
			endpoint := "rpc://" + strings.TrimPrefix(server.URL, "http://")
			expiry := time.Now().Add(time.Hour).Unix()
			if strings.HasPrefix(name, "proxy_expired_") || name == "proxy_prepared_expiry" {
				expiry = time.Now().Add(10 * time.Second).Unix()
			}
			invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: "blind", TransportMode: RGB11ProxyTransport,
				TransportEndpoints: []string{endpoint}, ContractID: issued.ContractID,
				AmountRaw: "1", Expiry: expiry,
			})
			coreRequire(t, "create proxy safety invoice", err)
			prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
				Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
			})
			coreRequire(t, "prepare proxy safety transfer", err)
			before := chain.broadcastCount()
			var sent *RGB11ProxyDeliveryResult
			if name == "proxy_prepared_expiry" {
				coreRequire(t, "tick before unconfirmed proxy expiry",
					sender.handleRGB11L1MonitorTick(context.Background()))
				pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
				coreRequire(t, "read unconfirmed proxy after tick", err)
				proxy.mu.Lock()
				unpublished := len(proxy.consignment) == 0
				proxy.mu.Unlock()
				coreAssert(t, pending.State.Status == "prepared" && unpublished && chain.broadcastCount() == before,
					"background tick advanced an unconfirmed proxy transfer")
			} else {
				sent, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(context.Background(), []string{prepared.State.TransferID})
				coreRequire(t, "deliver without recipient ACK", err)
				coreAssert(t, sent.AwaitingACK && !sent.Broadcast && chain.broadcastCount() == before,
					"unacknowledged proxy delivery broadcast")
			}
			original, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
			coreRequire(t, "read original proxy reservation", err)
			coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, true)
			if name == "proxy_first_ack_retry" {
				reserve, err := receiver.rgbManager.projectionStore.LoadReceiveReservation(invoice.RequestID)
				coreRequire(t, "read blind receive reservation before outage", err)
				fault := errors.New("core-e2e temporary Bitcoin tip outage")
				chain.mu.Lock()
				chain.tipError = fault
				chain.mu.Unlock()
				// Keep the outage active for the entire receive attempt so a
				// background refresh cannot consume a one-shot fault first.
				func() {
					defer func() {
						chain.mu.Lock()
						chain.tipError = nil
						chain.mu.Unlock()
					}()
					_, receiveErr := receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
					coreAssert(t, receiveErr != nil && strings.Contains(receiveErr.Error(), "read RGB11 projection starting tip"),
						"controlled outage did not reach the first receive candidate publication")
				}()
				proxy.mu.Lock()
				noACK := proxy.ack == nil
				proxy.mu.Unlock()
				request, err := receiver.rgbManager.engine.LoadReceive(invoice.RequestID)
				coreRequire(t, "read invoice after transient evidence failure", err)
				coreAssert(t, noACK, "temporary indexer failure posted a terminal proxy NACK")
				coreAssert(t, request.Status == corewallet.ReceivePrepared && request.TransferID == "",
					"temporary indexer failure permanently changed the unused receive request")
				current, err := receiver.rgbManager.projectionStore.LoadReceiveReservation(invoice.RequestID)
				coreRequire(t, "retain blind reservation after transient evidence failure", err)
				lock := receiver.utxoLockerL1.GetLockedUtxoList()[reserve.OutPoint]
				coreAssert(t, current.ReservationID == reserve.ReservationID && lock != nil && lock.ReservationID == reserve.ReservationID,
					"temporary indexer failure released the blind carrier")
				received, err := receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
				coreRequire(t, "retry the same invoice after indexer recovery", err)
				coreAssert(t, received.AckPosted && received.AwaitingBroadcast && chain.broadcastCount() == before,
					"retry failed to retain the ACK-before-broadcast boundary")
				coreRequire(t, "follow positive ACK on L1 monitor tick",
					sender.handleRGB11L1MonitorTick(context.Background()))
				coreAssert(t, chain.broadcastCount() == before+1, "background ACK follow-up did not broadcast once")
				sent, err = sender.DeliverAndBroadcastRGB11ProxyTransfer(context.Background(), []string{prepared.State.TransferID})
				coreRequire(t, "broadcast once after successful receive retry", err)
				coreAssert(t, sent.Broadcast && sent.TxID == prepared.TxID && chain.broadcastCount() == before+1,
					"receive retry changed or duplicated the transaction")
				chain.confirm(sent.TxID)
				_, err = receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
				coreRequire(t, "settle the retried receive", err)
				_, err = sender.RefreshRGB11State(context.Background())
				coreRequire(t, "settle sender after retry", err)
				return
			}
			if strings.HasPrefix(name, "proxy_expired_") || name == "proxy_prepared_expiry" {
				coreAssert(t, time.Now().Unix() < expiry, "proxy fixture expired before delivery")
				time.Sleep(time.Until(time.Unix(expiry, 0)) + 100*time.Millisecond)
			}
			var view *RGBTransferView
			if name == "proxy_expired_nack" {
				_, receiveErr := receiver.ReceiveRGB11ProxyConsignment(context.Background(), invoice.RequestID)
				coreAssert(t, receiveErr != nil, "expired unaccepted invoice was accepted")
				proxy.mu.Lock()
				rejected := proxy.ack != nil && !*proxy.ack
				proxy.mu.Unlock()
				coreAssert(t, rejected, "expired recipient did not publish its rejection")
				ack, ackErr := sender.FetchRGB11ProxyAck(context.Background(), prepared.State.TransferID)
				coreRequire(t, "consume late proxy NACK after invoice expiry", ackErr)
				coreAssert(t, ack.Available && !ack.Accepted, "late NACK was not consumed")
				view, err = sender.rgbCurrentTransfer(prepared.State.TransferID)
				coreRequire(t, "read terminal late-NACK transfer", err)
			} else if name == "proxy_prepared_expiry" {
				coreRequire(t, "expire unconfirmed proxy on existing tick",
					sender.handleRGB11L1MonitorTick(context.Background()))
				proxy.mu.Lock()
				unpublished := len(proxy.consignment) == 0
				proxy.mu.Unlock()
				coreAssert(t, unpublished, "expiry published an unconfirmed proxy consignment")
				view, err = sender.rgbCurrentTransfer(prepared.State.TransferID)
				coreRequire(t, "read expired unconfirmed proxy", err)
			} else if name == "proxy_expired_cancel" {
				// Exercise the real monitor scope loop, including its chain
				// checkpoint. The fixture has never accepted an ACK; a 503
				// alone is not evidence of that fact.
				proxy.mu.Lock()
				noACK := proxy.ack == nil
				proxy.ackGetHTTPStatus = http.StatusServiceUnavailable
				proxy.mu.Unlock()
				coreAssert(t, noACK && original.State.Status == "relayed" &&
					original.State.AckStatus != "accepted" && original.State.AckStatus != "accepted-out-of-band" &&
					!original.State.DeliveryAcknowledged && (original.ChannelSend == nil || !original.ChannelSend.Signed) &&
					!rgb11PendingHasWitness(original),
					"expired cancellation fixture was already acknowledged or signed")
				coreRequire(t, "cancel expired unsigned proxy despite ACK service outage",
					sender.handleRGB11L1MonitorTick(context.Background()))
				checkpoint, checkpointErr := sender.rgbManager.loadRGB11L1MonitorCheckpoint()
				coreRequire(t, "retain chain checkpoint despite proxy outage", checkpointErr)
				coreAssert(t, checkpoint != nil && checkpoint.Tip.Height == 100 && checkpoint.Tip.Hash == strings.Repeat("1", 64),
					"proxy outage prevented the current chain checkpoint")

				view, err = sender.rgbCurrentTransfer(prepared.State.TransferID)
				coreRequire(t, "read expired proxy during ACK service outage", err)
				coreAssert(t, view != nil && (view.Status == "rejected" || view.Status == "cancelled" || view.Status == "expired"),
					"expired unsigned proxy remained pending while ACK service was unavailable")
				coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, false, original)
				coreAssert(t, chain.broadcastCount() == before, "offline expiry cancellation broadcast a transaction")
				proxy.mu.Lock()
				proxy.ackGetHTTPStatus = 0
				proxy.mu.Unlock()
				coreRequire(t, "repeat expiry tick after ACK service recovery",
					sender.handleRGB11L1MonitorTick(context.Background()))
				view, err = sender.rgbCurrentTransfer(prepared.State.TransferID)
				coreRequire(t, "read timed-out proxy transfer", err)
			} else {
				view, err = sender.rgbCurrentTransfer(prepared.State.TransferID)
				coreRequire(t, "read standard proxy cancel action", err)
				coreAssert(t, view.CanCancel, "PWA standard proxy cancel action is hidden")
				view, err = sender.CancelRGBTransfer(context.Background(), prepared.State.TransferID)
				coreRequire(t, "cancel unacknowledged standard proxy via PWA API", err)
			}
			coreAssert(t, view != nil && (view.Status == "rejected" || view.Status == "cancelled" || view.Status == "expired"),
				"proxy cancellation did not report a terminal state")
			coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, false, original)
			_, err = sender.RefreshRGB11State(context.Background())
			coreRequire(t, "refresh after proxy cancellation", err)
			coreAssertRGB11Reservation(t, sender, prepared.State.TransferID, false, original)
			coreAssert(t, chain.broadcastCount() == before, "proxy cancellation broadcast a transaction")
		}) {
			return false
		}
	}
	return true
}
