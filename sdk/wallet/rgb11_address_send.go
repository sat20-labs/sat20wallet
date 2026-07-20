package wallet

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/invoicing"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const RGB11AddressTransport = "address-dkvs"

var rgb11AddressMessageDomain = []byte("SAT20-RGB11-ADDRESS-MESSAGE-V1")

type RGB11AddressSendRequest struct {
	ReceiverAddress  string            `json:"receiver_address"`
	AssetName        indexer.AssetName `json:"asset_name"`
	AmountRaw        string            `json:"amount_raw"`
	FeeRate          int64             `json:"fee_rate"`
	MinConfirmations uint8             `json:"min_confirmations"`
	Expiry           int64             `json:"expiry,omitempty"`
}

func randomRGB11TmpKey() (string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return dkvsindexer.TmpKey(hex.EncodeToString(entropy[:]))
}

// rgb11AddressMessageID maps the canonical RGB transfer identifier to the
// fixed-size, lower-case DKVS message segment. The canonical transfer ID remains
// inside the encrypted Consignment and is never replaced by this transport ID.
func rgb11AddressMessageID(transferID string) (string, error) {
	transferID = strings.TrimSpace(transferID)
	if transferID == "" {
		return "", ErrRGB11AddressMailbox
	}
	input := make([]byte, 0, len(rgb11AddressMessageDomain)+len(transferID))
	input = append(input, rgb11AddressMessageDomain...)
	input = append(input, transferID...)
	sum := sha256.Sum256(input)
	return hex.EncodeToString(sum[:]), nil
}

func synthesizeRGB11AddressInvoice(endpoint *RGB11AddressEndpoint, asset indexer.AssetName,
	amount uint64, expiry int64) (string, error) {
	if endpoint == nil || endpoint.AccountID == "" || len(endpoint.PkScript) == 0 || amount == 0 {
		return "", ErrRGB11TraditionalReceiveRequired
	}
	officialID, err := rgb11wallet.OfficialAssetID(asset)
	if err != nil {
		return "", err
	}
	contractID, err := consensus.ParseContractID(officialID)
	if err != nil {
		return "", err
	}
	xonly, err := hex.DecodeString(endpoint.AccountID)
	if err != nil || len(xonly) != 32 {
		return "", ErrRGB11TraditionalReceiveRequired
	}
	var internal [32]byte
	copy(internal[:], xonly)
	beneficiary, err := invoicing.NewWitnessBeneficiary(
		rgb11InvoiceNetwork(GetChainParam()), endpoint.PkScript, &internal,
	)
	if err != nil {
		return "", err
	}
	relayKey, err := randomRGB11TmpKey()
	if err != nil {
		return "", err
	}
	ackKey, err := randomRGB11TmpKey()
	if err != nil {
		return "", err
	}
	invoice := invoicing.Invoice{
		Contract:    &contractID,
		Assignment:  &invoicing.InvoiceState{Kind: invoicing.StateAmount, Amount: invoicing.Amount(amount)},
		Beneficiary: beneficiary,
		Expiry:      &expiry,
		UnknownQuery: []invoicing.QueryParam{
			{Key: "sat20_recipient", Value: hex.EncodeToString(endpoint.CompressedPubKey)},
			{Key: "sat20_relay", Value: relayKey},
			{Key: "sat20_ack", Value: ackKey},
		},
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		return "", err
	}
	return invoice.String(), nil
}

// PrepareRGB11AddressTransfer resolves the receiver's account capability and
// synthesizes a private witness invoice solely to reuse the audited RGB11
// transition builder. The synthesized invoice is removed from persisted public
// lifecycle state immediately after preparation.
func (p *Manager) PrepareRGB11AddressTransfer(ctx context.Context, client *SatsNetDKVSClient,
	request RGB11AddressSendRequest, verify dkvsindexer.RecordVerificationOptions) (
	*RGB11PreparedTransfer, *RGB11AddressEndpoint, error,
) {
	if p == nil || client == nil || request.ReceiverAddress == "" ||
		request.AssetName.Protocol != rgb11wallet.Protocol {
		return nil, nil, ErrRGB11TraditionalReceiveRequired
	}
	if verify.Now == 0 {
		verify.Now = uint64(time.Now().UnixMilli())
	}
	endpoint, err := p.ResolveRGB11AddressEndpoint(client, request.ReceiverAddress, verify)
	if err != nil {
		return nil, nil, err
	}
	if endpoint.CapabilityFlags&RGB11ReceiveCapabilityAny == 0 {
		return nil, nil, ErrRGB11TraditionalReceiveRequired
	}
	amount, err := strconv.ParseUint(request.AmountRaw, 10, 64)
	if err != nil || amount == 0 {
		return nil, nil, fmt.Errorf("invalid RGB11 amount")
	}
	if request.Expiry == 0 {
		request.Expiry = time.Now().Add(24 * time.Hour).Unix()
	}
	invoice, err := synthesizeRGB11AddressInvoice(endpoint, request.AssetName, amount, request.Expiry)
	if err != nil {
		return nil, nil, err
	}
	prepared, err := p.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice:          invoice,
		FeeRate:          request.FeeRate,
		MinConfirmations: request.MinConfirmations,
	})
	if err != nil {
		return nil, nil, err
	}
	if prepared == nil || prepared.State == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		return nil, nil, err
	}
	senderAccountID, err := dkvsAccountID(p.wallet)
	if err != nil {
		return nil, nil, err
	}
	messageID, err := rgb11AddressMessageID(pending.State.TransferID)
	if err != nil {
		return nil, nil, err
	}
	deliveryKey, err := dkvsindexer.MailMsgKey(endpoint.AccountID, senderAccountID, messageID)
	if err != nil {
		return nil, nil, err
	}
	ackKey, err := dkvsindexer.MailMsgKey(senderAccountID, endpoint.AccountID, messageID)
	if err != nil {
		return nil, nil, err
	}
	pending.State.AddressMode = true
	pending.State.AddressMessageID = messageID
	pending.State.TransportMode = RGB11AddressTransport
	pending.State.SenderAccountID = senderAccountID
	pending.State.ReceiverAccountID = endpoint.AccountID
	pending.State.ReceiverAddress = endpoint.Address
	pending.State.ReceiveCapabilityKey = endpoint.CapabilityRecordKey
	pending.State.ReceiveCapabilityHash = endpoint.CapabilityRecordHash
	pending.State.DeliveryRecordKey = deliveryKey
	pending.State.RelayRecordKey = deliveryKey
	pending.State.AckRecordKey = ackKey
	pending.State.RecipientID = endpoint.AccountID
	pending.State.Invoice = ""
	pending.State.SyntheticInvoiceRemoved = true
	pending.State.AckStatus = "awaiting-persistence"
	if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
		return nil, nil, err
	}
	// Snapshot export strips RecipientConsignment for address-mode transfers,
	// so this synchronization advances canonical state without backing up the
	// transient delivery cache.
	p.autoBackupRGB11AfterMutation()
	prepared.State = &pending.State
	prepared.States = []*rgb11wallet.TransferState{&pending.State}
	return prepared, endpoint, nil
}
