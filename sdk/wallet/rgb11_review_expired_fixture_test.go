package wallet

import (
	"context"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type rgb11ExpiredStatusWait struct {
	rgb11wallet.BitcoinEvidenceProvider
	txid    string
	entered chan struct{}
	release chan struct{}
}

func (e *rgb11ExpiredStatusWait) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.txid {
		select {
		case e.entered <- struct{}{}:
		default:
		}
		<-e.release
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

// Public cancellation needs an activated recovery account and real recovery
// material; the protocol-only expired fixture intentionally has neither.
func newPublicExpiredRGB11CancelFixture(t *testing.T) (*Manager, string, *rgb11ExpiredStatusWait) {
	t.Helper()
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "100", FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	pending.State.Expiry = time.Now().Add(-time.Hour).Unix()
	pending.State.RelayExpiry = pending.State.Expiry
	if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		t.Fatal(err)
	}
	if err := sender.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	evidence.setStatus(prepared.TxID, rgb11wallet.BitcoinTxStatus{TxID: prepared.TxID})
	wait := &rgb11ExpiredStatusWait{BitcoinEvidenceProvider: evidence, txid: prepared.TxID, entered: make(chan struct{}, 1), release: make(chan struct{})}
	sender.rgbManager.evidence = wait
	return sender, prepared.State.TransferID, wait
}
