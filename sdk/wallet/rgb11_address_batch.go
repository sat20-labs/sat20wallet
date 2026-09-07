package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func rgb11BatchRecipientID(batchID string, index uint32) string {
	child := sha256.Sum256([]byte(fmt.Sprintf("SAT20-RGB11-BATCH-RECIPIENT-V1:%s:%d", batchID, index)))
	return hex.EncodeToString(child[:])
}

// prepareRGB11AddressBatch reuses the native one-transaction batch builder.
// Each output has its own persistence ACK, including repeated addresses.
func (p *rgb11Manager) prepareRGB11AddressBatch(ctx context.Context, requests []RGB11AddressSendRequest,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11PreparedTransfer, error) {
	if len(requests) == 0 || len(requests) > 32 {
		return nil, fmt.Errorf("RGB11 send requires 1 to 32 outputs")
	}
	endpoints := make([]*RGB11AddressEndpoint, len(requests))
	invoices := make([]string, len(requests))
	sender, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, err
	}
	mailbox, err := mailboxSubscriptionTarget(sender)
	if err != nil {
		return nil, err
	}
	if err := p.accountManagementOwner().SubscribeDKVSPrefix(mailbox); err != nil {
		return nil, err
	}
	for i, request := range requests {
		if request.AssetName != requests[0].AssetName || request.FeeRate != requests[0].FeeRate {
			return nil, fmt.Errorf("RGB11 batch must send one asset at one fee rate")
		}
		endpoint, err := p.ResolveConfiguredRGB11AddressEndpoint(request.ReceiverAddress, verify)
		if err != nil {
			return nil, err
		}
		if endpoint.CapabilityFlags&RGB11ReceiveCapabilityAny == 0 {
			return nil, ErrRGB11TraditionalReceiveRequired
		}
		amount, err := parseRGB11SendAmount(request.AmountRaw)
		if err != nil {
			return nil, err
		}
		expiry := request.Expiry
		if expiry == 0 {
			expiry = time.Now().Add(24 * time.Hour).Unix()
		}
		invoice, err := p.synthesizeRGB11AddressInvoice(endpoint, request.AssetName, amount, expiry)
		if err != nil {
			return nil, err
		}
		endpoints[i], invoices[i] = endpoint, invoice
	}
	prepared, err := p.prepareRGB11Transfer(ctx, RGB11SendRequest{Invoices: invoices, FeeRate: requests[0].FeeRate, MinConfirmations: requests[0].MinConfirmations}, true)
	if err != nil {
		return nil, err
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, len(prepared.States))
	for i, state := range prepared.States {
		pending, err := p.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return prepared, err
		}
		endpoint := endpoints[i]
		messageID, err := rgb11AddressMessageID(state.TransferID)
		if err != nil {
			return prepared, err
		}
		deliveryKey, err := dkvsindexer.MailMsgKey(endpoint.AccountID, sender, messageID)
		if err != nil {
			return prepared, err
		}
		ackKey, err := dkvsindexer.MailMsgKey(sender, endpoint.AccountID, messageID)
		if err != nil {
			return prepared, err
		}
		s := &pending.State
		s.AddressMode = true
		s.AddressMessageID = messageID
		s.TransportMode = RGB11AddressTransport
		s.SenderAccountID = sender
		s.ReceiverAccountID = endpoint.AccountID
		s.ReceiverAddress = endpoint.Address
		s.ReceiveCapabilityKey = endpoint.CapabilityRecordKey
		s.ReceiveCapabilityHash = endpoint.CapabilityRecordHash
		s.DeliveryRecordKey = deliveryKey
		s.RelayRecordKey = deliveryKey
		s.AckRecordKey = ackKey
		s.RecipientID = endpoint.AccountID
		s.Invoice = ""
		s.SyntheticInvoiceRemoved = true
		s.AckStatus = "awaiting-persistence"
		pendingList[i] = pending
		prepared.States[i] = s
	}
	if err := p.projectionStore.SavePendingTransferStates(pendingList); err != nil {
		return prepared, err
	}
	prepared.State = prepared.States[0]
	return prepared, nil
}

// loadRGB11AddressBatch requires the full persisted group. One ACK cannot
// authorize broadcasting inputs shared with another, unacknowledged output.
func (p *rgb11Manager) loadRGB11AddressBatch(first *rgb11wallet.PendingTransfer) ([]*rgb11wallet.PendingTransfer, error) {
	if first == nil || !first.State.AddressMode {
		return nil, ErrRGB11AddressDeliveryRequired
	}
	ids := first.State.BatchTransferIDs
	if len(ids) == 0 {
		ids = []string{first.State.TransferID}
	}
	if len(ids) > 32 || (len(ids) > 1 && (first.State.BatchSize != len(ids) || first.State.BatchID == "")) {
		return nil, ErrRGB11Inconsistent
	}
	list := make([]*rgb11wallet.PendingTransfer, 0, len(ids))
	seen := make(map[string]bool)
	for i, id := range ids {
		if seen[id] {
			return nil, ErrRGB11Inconsistent
		}
		seen[id] = true
		pending, err := p.projectionStore.LoadPendingTransfer(id)
		if err != nil {
			return nil, err
		}
		if !pending.State.AddressMode || pending.State.BatchID != first.State.BatchID || pending.State.WitnessTxID != first.State.WitnessTxID || !bytes.Equal(pending.SignedTx, first.SignedTx) || !sameRGB11ChannelSend(pending.ChannelSend, first.ChannelSend) {
			return nil, ErrRGB11Inconsistent
		}
		if len(ids) > 1 && (id != rgb11BatchRecipientID(first.State.BatchID, uint32(i)) ||
			pending.State.BatchSize != len(ids) || pending.State.RecipientVout != uint32(i+1) ||
			!slices.Equal(pending.State.BatchTransferIDs, ids)) {
			return nil, ErrRGB11Inconsistent
		}
		messageID, err := rgb11AddressMessageID(id)
		if len(ids) > 1 && (err != nil || messageID != pending.State.AddressMessageID) {
			return nil, ErrRGB11Inconsistent
		}
		list = append(list, pending)
	}
	if !seen[first.State.TransferID] {
		return nil, ErrRGB11Inconsistent
	}
	return list, nil
}
