package wallet

import (
	"context"
	indexer "github.com/sat20-labs/indexer/common"
	corerelay "github.com/sat20-labs/rgb11/relay"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Public RGB11 DTOs live with the protocol package. These aliases preserve the
// Wallet SDK surface without duplicating protocol-specific data structures in
// the outer wallet package.
type (
	RGB11Output                   = rgb11wallet.RGB11Output
	RGB11TickerInfo               = rgb11wallet.RGB11TickerInfo
	RGB11State                    = rgb11wallet.RGB11State
	RGB11IssueRequest             = rgb11wallet.RGB11IssueRequest
	RGB11IssueResult              = rgb11wallet.RGB11IssueResult
	RGB11ImportResult             = rgb11wallet.RGB11ImportResult
	RGB11RejectListProvider       = rgb11wallet.RGB11RejectListProvider
	RGB11RejectListViolation      = rgb11wallet.RGB11RejectListViolation
	RGB11InvoiceRequest           = rgb11wallet.RGB11InvoiceRequest
	RGB11SendRequest              = rgb11wallet.RGB11SendRequest
	RGB11PreparedTransfer         = rgb11wallet.RGB11PreparedTransfer
	RGB11ProxyDeliveryResult      = rgb11wallet.RGB11ProxyDeliveryResult
	RGB11ProxyAckResult           = rgb11wallet.RGB11ProxyAckResult
	RGB11ProxyReceiveResult       = rgb11wallet.RGB11ProxyReceiveResult
	RGB11RefreshResult            = rgb11wallet.RGB11RefreshResult
	RGB11AddressMailboxSyncResult = rgb11wallet.RGB11AddressMailboxSyncResult
	RGB11ReceiveCapability        = rgb11wallet.RGB11ReceiveCapability
	RGB11AddressEndpoint          = rgb11wallet.RGB11AddressEndpoint
	RGB11AddressDeliveryResult    = rgb11wallet.RGB11AddressDeliveryResult
	RGB11AddressACK               = rgb11wallet.RGB11AddressACK
	RGB11AddressSendRequest       = rgb11wallet.RGB11AddressSendRequest
	RGB11WalletSnapshot           = rgb11wallet.RGB11WalletSnapshot
)

// This file is the public RGB11 surface of wallet.Manager. All behavior is
// owned by the dedicated rgb11Manager; keep infrastructure and protocol
// implementation out of the outer wallet manager.

// beginRGB11Operation freezes the selected wallet/account scope for the full
// duration of a public RGB11 operation. Wallet/account switches take the write
// side of this lock, so mutable scoped stores and the signing wallet cannot
// drift halfway through an operation.
func (p *Manager) beginRGB11Operation() func() {
	p.rgbOperationMu.RLock()
	return p.rgbOperationMu.RUnlock
}

func (p *Manager) beginRGB11ScopeChange() func() {
	p.rgbOperationMu.Lock()
	return p.rgbOperationMu.Unlock
}

func (p *Manager) synchronizedRGB11Manager() (*rgb11Manager, error) {
	if p == nil || p.rgbManager == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager, nil
}

func (p *Manager) AcceptRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.AcceptRGB11AddressACK(record, verify)
}

func (p *Manager) AcceptRGB11Consignment(ctx context.Context, requestID string, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.AcceptRGB11Consignment(ctx, requestID, raw)
}

func (p *Manager) AcceptRGB11RelayConsignment(ctx context.Context, requestID string,
	record *corerelay.RelayRecord, raw []byte) (*rgb11wallet.ValidationReceipt, *corerelay.AckRecord, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, nil, err
	}
	return manager.AcceptRGB11RelayConsignment(ctx, requestID, record, raw)
}

func (p *Manager) BroadcastRGB11AddressTransfer(transferID string) (string, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return "", err
	}
	return manager.BroadcastRGB11AddressTransfer(transferID)
}

func (p *Manager) BroadcastRGB11Batch(transferIDs []string, relayRecords []*corerelay.RelayRecord,
	acks []*corerelay.AckRecord) (string, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return "", err
	}
	return manager.BroadcastRGB11Batch(transferIDs, relayRecords, acks)
}

func (p *Manager) BroadcastRGB11OutOfBand(transferIDs []string) (string, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return "", err
	}
	return manager.BroadcastRGB11OutOfBand(transferIDs)
}

func (p *Manager) DeliverAndBroadcastRGB11ProxyTransfer(ctx context.Context,
	transferIDs []string) (*RGB11ProxyDeliveryResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.DeliverAndBroadcastRGB11ProxyTransfer(ctx, transferIDs)
}

func (p *Manager) BroadcastRGB11Transfer(transferID string, relayRecord *corerelay.RelayRecord,
	ack *corerelay.AckRecord) (string, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return "", err
	}
	return manager.BroadcastRGB11Transfer(transferID, relayRecord, ack)
}

func (p *Manager) BuildRGB11RelayRecord(transferID, sourcePeerID string) (*corerelay.RelayRecord, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.BuildRGB11RelayRecord(transferID, sourcePeerID)
}

func (p *Manager) CancelRGB11BatchByNack(transferID string, relayRecord *corerelay.RelayRecord,
	nack *corerelay.AckRecord) error {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return err
	}
	return manager.CancelRGB11BatchByNack(transferID, relayRecord, nack)
}

func (p *Manager) CancelRGB11OutOfBandTransfer(transferID string) error {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return err
	}
	return manager.CancelRGB11OutOfBandTransfer(transferID)
}

func (p *Manager) PrepareRGB11Consignment(ctx context.Context, requestID string,
	raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()

	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.PrepareRGB11Consignment(ctx, requestID, raw)
}

func (p *Manager) CreateRGB11Invoice(request RGB11InvoiceRequest) (*corewallet.ReceiveRequest, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.CreateRGB11Invoice(request)
}

func (p *Manager) DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID, options)
}

func (p *Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.EnableConfiguredRGB11AddressReceive(options)
}

func (p *Manager) FetchRGB11AckRecord(transferID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.AckRecord, *swire.DKVSRecord, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, nil, err
	}
	return manager.FetchRGB11AckRecord(transferID, verifyOpts)
}

func (p *Manager) GetRGB11AssetBalance(name *indexer.AssetName) (*Decimal, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.GetRGB11AssetBalance(name)
}

func (p *Manager) GetRGB11ConsistencyStatus() string {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	return p.rgbManager.GetRGB11ConsistencyStatus()
}

func (p *Manager) GetRGB11ProjectionStore() *rgb11wallet.ProjectionStore {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	return p.rgbManager.GetRGB11ProjectionStore()
}

func (p *Manager) GetRGB11ReceiveRequest(requestID string) (*corewallet.ReceiveRequest, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.GetRGB11ReceiveRequest(requestID)
}

func (p *Manager) GetRGB11State() (*RGB11State, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.GetRGB11State()
}

func (p *Manager) ImportRGB11Contract(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ImportRGB11Contract(ctx, raw)
}

func (p *Manager) ImportRGB11ContractFile(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ImportRGB11ContractFile(ctx, raw)
}

func (p *Manager) IssueRGB11Asset(ctx context.Context, request RGB11IssueRequest) (*RGB11IssueResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.IssueRGB11Asset(ctx, request)
}

func (p *Manager) ListRGB11Outputs() ([]*TxOutput, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ListRGB11Outputs()
}

func (p *Manager) PrepareConfiguredRGB11AddressTransfer(ctx context.Context, request RGB11AddressSendRequest,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11PreparedTransfer, *RGB11AddressEndpoint, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, nil, err
	}
	return manager.PrepareConfiguredRGB11AddressTransfer(ctx, request, verify)
}

func (p *Manager) PrepareRGB11Transfer(ctx context.Context, request RGB11SendRequest) (*RGB11PreparedTransfer, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.PrepareRGB11Transfer(ctx, request)
}

func (p *Manager) ProjectRGB11Allocation(outpoint string, asset *indexer.AssetInfo, proof *rgb11wallet.AllocationProof) error {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return err
	}
	return manager.ProjectRGB11Allocation(outpoint, asset, proof)
}

func (p *Manager) PublishRGB11AckRecord(key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.PublishRGB11AckRecord(key, ack, opts)
}

func (p *Manager) PublishRGB11RelayRecord(transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, nil, err
	}
	return manager.PublishRGB11RelayRecord(transferID, sourcePeerID, opts)
}

func (p *Manager) FetchRGB11ProxyAck(ctx context.Context,
	transferID string) (*RGB11ProxyAckResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.FetchRGB11ProxyAck(ctx, transferID)
}

func (p *Manager) RGB11WalletID() (string, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	return p.rgbManager.RGB11WalletID()
}

func (p *Manager) RebuildRGB11Locks() error {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return err
	}
	return manager.RebuildRGB11Locks()
}

func (p *Manager) RefreshRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.RefreshRGB11AddressACK(record, verify)
}

func (p *Manager) RefreshRGB11State(ctx context.Context) (*RGB11RefreshResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	result, err := manager.RefreshRGB11State(ctx)
	manager.wakeRGB11ChainReconciliation()
	return result, err
}

func (p *Manager) ReceiveRGB11ProxyConsignment(ctx context.Context,
	requestID string) (*RGB11ProxyReceiveResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ReceiveRGB11ProxyConsignment(ctx, requestID)
}

func (p *Manager) RegisterRGB11TickerInfo(info *indexer.TickerInfo) error {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return err
	}
	return manager.RegisterRGB11TickerInfo(info)
}

func (p *Manager) RejectRGB11RelayConsignment(requestID string,
	record *corerelay.RelayRecord) (*corerelay.AckRecord, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.RejectRGB11RelayConsignment(requestID, record)
}

func (p *Manager) ResolveConfiguredRGB11AddressEndpoint(address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ResolveConfiguredRGB11AddressEndpoint(address, verify)
}

func (p *Manager) SyncConfiguredRGB11AddressMailbox(ctx context.Context,
	verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*RGB11AddressMailboxSyncResult, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.SyncConfiguredRGB11AddressMailbox(ctx, verify, ackOptions)
}

func (p *Manager) ValidateRGB11Consignment(ctx context.Context, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	releaseRGB11Operation := p.beginRGB11Operation()
	defer releaseRGB11Operation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ValidateRGB11Consignment(ctx, raw)
}

// getL1TxOutput is the single non-RGB transaction-builder integration point.
func (p *Manager) getL1TxOutput(outpoint string) (*TxOutput, error) {
	return p.rgbManager.getL1TxOutput(outpoint)
}
