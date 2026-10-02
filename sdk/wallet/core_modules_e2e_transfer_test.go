package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func coreInvoiceTransferE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
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
	senderStable := coreCaptureStableReference(t, sender, cfg)
	receiverStable := coreCaptureStableReference(t, receiver, cfg)

	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "400", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create traditional invoice", err)
	corePendingCheckpoint(t, "02_invoice_receiver", receiver, cfg, chain, receiverMaterial, receiverStable)
	before := chain.broadcastCount()
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare native invoice transfer", err)
	coreAssert(t, chain.broadcastCount() == before, "prepare unexpectedly broadcast")
	corePendingCheckpoint(t, "03_prepared_sender", sender, cfg, chain, senderMaterial, senderStable)

	t.Run("malformed_consignment_cannot_create_ownership", func(t *testing.T) {
		_, err := receiver.ValidateRGB11Consignment(context.Background(), []byte("not an RGB consignment"))
		coreAssert(t, err != nil, "malformed consignment accepted")
		balance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
		coreRequire(t, "balance after invalid payload", err)
		coreAssert(t, balance.Sign() == 0, "invalid consignment changed ownership")
	})
	_, err = receiver.PrepareRGB11Consignment(context.Background(), invoice.RequestID, []byte(prepared.RecipientConsignment))
	coreRequire(t, "receiver validates before broadcast", err)
	corePendingCheckpoint(t, "04_receiver_validated_awaiting_broadcast", receiver, cfg, chain, receiverMaterial, receiverStable)
	balance, err = receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "pre-broadcast balance", err)
	coreAssert(t, balance.Sign() == 0, "receiver credited an unbroadcast witness")

	txid, err := sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	coreRequire(t, "broadcast signed invoice transaction", err)
	coreAssert(t, txid == prepared.TxID && chain.broadcastCount() == before+1, "wrong or duplicate invoice broadcast")
	corePendingCheckpoint(t, "05_broadcast_sender", sender, cfg, chain, senderMaterial, senderStable)
	_, err = receiver.AcceptRGB11Consignment(context.Background(), invoice.RequestID, []byte(prepared.RecipientConsignment))
	coreRequire(t, "accept actually broadcast invoice transfer", err)
	corePendingCheckpoint(t, "06_mempool_receiver", receiver, cfg, chain, receiverMaterial, receiverStable)

	chain.confirm(txid)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "settle invoice sender", err)
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "settle invoice receiver", err)
	coreCheckpoint(t, "07_settled_sender", sender, cfg, chain, senderMaterial)
	coreCheckpoint(t, "08_settled_receiver", receiver, cfg, chain, receiverMaterial)
	left, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "remaining invoice amount", err)
	right, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "received invoice amount", err)
	coreAssert(t, left.Value.Uint64() == 600 && right.Value.Uint64() == 400, "invoice transfer violated asset conservation")
}

func coreDirectTransferE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain,
	sender, receiver *Manager, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ED", Name: "E2E direct asset", Amounts: []uint64{1200},
	})
	coreRequire(t, "issue Direct NIA", err)
	coreCheckpoint(t, "01_issued_sender", sender, cfg, chain, senderMaterial)
	endpoint, err := receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish Direct receive capability", err)
	coreAssert(t, !endpoint.Temporary, "paid Direct capability downgraded to temporary")
	senderStable := coreCaptureStableReference(t, sender, cfg)
	receiverStable := coreCaptureStableReference(t, receiver, cfg)
	before := chain.broadcastCount()
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: receiver.GetWallet().GetAddress(), AssetName: issued.AssetName,
		AmountRaw: "500", FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare Direct transfer", err)
	corePendingCheckpoint(t, "02_prepared_sender", sender, cfg, chain, senderMaterial, senderStable)
	result, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(prepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver Direct consignment via real CoreNode", err)
	coreAssert(t, result.AwaitingACK && !result.Broadcast && chain.broadcastCount() == before, "Direct broadcast crossed receiver ACK boundary")
	corePendingCheckpoint(t, "03_delivered_waiting_ACK_sender", sender, cfg, chain, senderMaterial, senderStable)

	syncResult, err := receiver.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "receiver validates Direct and publishes ACK", err)
	coreAssert(t, syncResult.Received == 1 && syncResult.Invalid == 0, "Direct mailbox did not accept exactly one transfer")
	corePendingCheckpoint(t, "04_receiver_ACK_committed", receiver, cfg, chain, receiverMaterial, receiverStable)
	coreAssert(t, chain.broadcastCount() == before, "passive receiver recovery broadcast sender transaction")
	_, err = sender.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "sender consumes ACK and broadcasts", err)
	coreAssert(t, chain.broadcastCount() == before+1, "Direct ACK must cause one broadcast")
	corePendingCheckpoint(t, "05_ACK_and_broadcast_sender", sender, cfg, chain, senderMaterial, senderStable)
	_, err = receiver.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
	coreRequire(t, "receiver discovers broadcast", err)
	corePendingCheckpoint(t, "06_broadcast_receiver", receiver, cfg, chain, receiverMaterial, receiverStable)

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
}
