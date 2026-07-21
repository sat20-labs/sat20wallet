package wallet

import (
	"context"
	indexer "github.com/sat20-labs/indexer/common"
	corerelay "github.com/sat20-labs/rgb11/relay"
	coresync "github.com/sat20-labs/rgb11/sync"
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
	RGB11RefreshResult            = rgb11wallet.RGB11RefreshResult
	RGB11AddressMailboxSyncResult = rgb11wallet.RGB11AddressMailboxSyncResult
	RGB11ReceiveCapability        = rgb11wallet.RGB11ReceiveCapability
	RGB11AddressEndpoint          = rgb11wallet.RGB11AddressEndpoint
	RGB11AddressDeliveryResult    = rgb11wallet.RGB11AddressDeliveryResult
	RGB11AddressACK               = rgb11wallet.RGB11AddressACK
	RGB11AddressSendRequest       = rgb11wallet.RGB11AddressSendRequest
	RGB11AutoBackupPolicy         = rgb11wallet.RGB11AutoBackupPolicy
	RGB11ActivationResult         = rgb11wallet.RGB11ActivationResult
	RGB11WalletSnapshot           = rgb11wallet.RGB11WalletSnapshot
	RGB11EncryptedSnapshot        = rgb11wallet.RGB11EncryptedSnapshot
)

// This file is the public RGB11 surface of wallet.Manager. All behavior is
// owned by the dedicated rgb11Manager; keep infrastructure and protocol
// implementation out of the outer wallet manager.

func (p *Manager) AcceptRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	return p.rgbManager.AcceptRGB11AddressACK(record, verify)
}

func (p *Manager) AcceptRGB11AddressMailbox(ctx context.Context, client *SatsNetDKVSClient,
	record *swire.DKVSRecord, verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*rgb11wallet.ValidationReceipt, *swire.DKVSRecord, error) {
	return p.rgbManager.AcceptRGB11AddressMailbox(ctx, client, record, verify, ackOptions)
}

func (p *Manager) AcceptRGB11Consignment(ctx context.Context, requestID string, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	return p.rgbManager.AcceptRGB11Consignment(ctx, requestID, raw)
}

func (p *Manager) AcceptRGB11RelayConsignment(ctx context.Context, requestID string,
	record *corerelay.RelayRecord, raw []byte) (*rgb11wallet.ValidationReceipt, *corerelay.AckRecord, error) {
	return p.rgbManager.AcceptRGB11RelayConsignment(ctx, requestID, record, raw)
}

func (p *Manager) ActivateRGB11WalletState(verifyOpts dkvsindexer.RecordVerificationOptions) (*RGB11ActivationResult, error) {
	return p.rgbManager.ActivateRGB11WalletState(verifyOpts)
}

func (p *Manager) BackupRGB11WalletState(client *SatsNetDKVSClient, walletID string,
	previous *coresync.WalletHead, opts dkvsindexer.RecordOptions) (*coresync.WalletHead, *swire.DKVSRecord, error) {
	return p.rgbManager.BackupRGB11WalletState(client, walletID, previous, opts)
}

func (p *Manager) BroadcastRGB11AddressTransfer(transferID string) (string, error) {
	return p.rgbManager.BroadcastRGB11AddressTransfer(transferID)
}

func (p *Manager) BroadcastRGB11Batch(transferIDs []string, relayRecords []*corerelay.RelayRecord,
	acks []*corerelay.AckRecord) (string, error) {
	return p.rgbManager.BroadcastRGB11Batch(transferIDs, relayRecords, acks)
}

func (p *Manager) BroadcastRGB11OutOfBand(transferIDs []string) (string, error) {
	return p.rgbManager.BroadcastRGB11OutOfBand(transferIDs)
}

func (p *Manager) BroadcastRGB11Transfer(transferID string, relayRecord *corerelay.RelayRecord,
	ack *corerelay.AckRecord) (string, error) {
	return p.rgbManager.BroadcastRGB11Transfer(transferID, relayRecord, ack)
}

func (p *Manager) BuildRGB11RelayRecord(transferID, sourcePeerID string) (*corerelay.RelayRecord, error) {
	return p.rgbManager.BuildRGB11RelayRecord(transferID, sourcePeerID)
}

func (p *Manager) CancelRGB11BatchByNack(transferID string, relayRecord *corerelay.RelayRecord,
	nack *corerelay.AckRecord) error {
	return p.rgbManager.CancelRGB11BatchByNack(transferID, relayRecord, nack)
}

func (p *Manager) CreateRGB11Invoice(request RGB11InvoiceRequest) (*corewallet.ReceiveRequest, error) {
	return p.rgbManager.CreateRGB11Invoice(request)
}

func (p *Manager) DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	return p.rgbManager.DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID, options)
}

func (p *Manager) DeliverAndBroadcastRGB11AddressTransfer(client *SatsNetDKVSClient, transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	return p.rgbManager.DeliverAndBroadcastRGB11AddressTransfer(client, transferID, options)
}

func (p *Manager) DeliverRGB11AddressTransfer(client *SatsNetDKVSClient, transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	return p.rgbManager.DeliverRGB11AddressTransfer(client, transferID, options)
}

func (p *Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	return p.rgbManager.EnableConfiguredRGB11AddressReceive(options)
}

func (p *Manager) EnableRGB11AddressReceive(client *SatsNetDKVSClient,
	options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	return p.rgbManager.EnableRGB11AddressReceive(client, options)
}

func (p *Manager) FetchRGB11AckRecord(transferID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*corerelay.AckRecord, *swire.DKVSRecord, error) {
	return p.rgbManager.FetchRGB11AckRecord(transferID, verifyOpts)
}

func (p *Manager) GetRGB11AssetBalance(name *indexer.AssetName) (*Decimal, error) {
	return p.rgbManager.GetRGB11AssetBalance(name)
}

func (p *Manager) GetRGB11ConsistencyStatus() string {
	return p.rgbManager.GetRGB11ConsistencyStatus()
}

func (p *Manager) GetRGB11ProjectionStore() *rgb11wallet.ProjectionStore {
	return p.rgbManager.GetRGB11ProjectionStore()
}

func (p *Manager) GetRGB11ReceiveRequest(requestID string) (*corewallet.ReceiveRequest, error) {
	return p.rgbManager.GetRGB11ReceiveRequest(requestID)
}

func (p *Manager) GetRGB11State() (*RGB11State, error) {
	return p.rgbManager.GetRGB11State()
}

func (p *Manager) ImportRGB11Contract(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	return p.rgbManager.ImportRGB11Contract(ctx, raw)
}

func (p *Manager) IssueRGB11Asset(ctx context.Context, request RGB11IssueRequest) (*RGB11IssueResult, error) {
	return p.rgbManager.IssueRGB11Asset(ctx, request)
}

func (p *Manager) ListRGB11Outputs() ([]*TxOutput, error) {
	return p.rgbManager.ListRGB11Outputs()
}

func (p *Manager) PrepareConfiguredRGB11AddressTransfer(ctx context.Context, request RGB11AddressSendRequest,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11PreparedTransfer, *RGB11AddressEndpoint, error) {
	return p.rgbManager.PrepareConfiguredRGB11AddressTransfer(ctx, request, verify)
}

func (p *Manager) PrepareRGB11AddressTransfer(ctx context.Context, client *SatsNetDKVSClient,
	request RGB11AddressSendRequest, verify dkvsindexer.RecordVerificationOptions) (
	*RGB11PreparedTransfer, *RGB11AddressEndpoint, error,
) {
	return p.rgbManager.PrepareRGB11AddressTransfer(ctx, client, request, verify)
}

func (p *Manager) PrepareRGB11Transfer(ctx context.Context, request RGB11SendRequest) (*RGB11PreparedTransfer, error) {
	return p.rgbManager.PrepareRGB11Transfer(ctx, request)
}

func (p *Manager) ProjectRGB11Allocation(outpoint string, asset *indexer.AssetInfo, proof *rgb11wallet.AllocationProof) error {
	return p.rgbManager.ProjectRGB11Allocation(outpoint, asset, proof)
}

func (p *Manager) PublishRGB11AckRecord(key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return p.rgbManager.PublishRGB11AckRecord(key, ack, opts)
}

func (p *Manager) PublishRGB11RelayRecord(transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	return p.rgbManager.PublishRGB11RelayRecord(transferID, sourcePeerID, opts)
}

func (p *Manager) RGB11WalletID() (string, error) {
	return p.rgbManager.RGB11WalletID()
}

func (p *Manager) RebuildRGB11Locks() error {
	return p.rgbManager.RebuildRGB11Locks()
}

func (p *Manager) RefreshRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	return p.rgbManager.RefreshRGB11AddressACK(record, verify)
}

func (p *Manager) RefreshRGB11State(ctx context.Context) (*RGB11RefreshResult, error) {
	return p.rgbManager.RefreshRGB11State(ctx)
}

func (p *Manager) RegisterRGB11TickerInfo(info *indexer.TickerInfo) error {
	return p.rgbManager.RegisterRGB11TickerInfo(info)
}

func (p *Manager) RejectRGB11RelayConsignment(requestID string,
	record *corerelay.RelayRecord) (*corerelay.AckRecord, error) {
	return p.rgbManager.RejectRGB11RelayConsignment(requestID, record)
}

func (p *Manager) RelayRGB11Ack(client *SatsNetDKVSClient, key string, ack *corerelay.AckRecord,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return p.rgbManager.RelayRGB11Ack(client, key, ack, opts)
}

func (p *Manager) RelayRGB11Transfer(client *SatsNetDKVSClient, transferID, sourcePeerID string,
	opts dkvsindexer.RecordOptions) (*corerelay.RelayRecord, *swire.DKVSRecord, error) {
	return p.rgbManager.RelayRGB11Transfer(client, transferID, sourcePeerID, opts)
}

func (p *Manager) ResolveConfiguredRGB11AddressEndpoint(address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	return p.rgbManager.ResolveConfiguredRGB11AddressEndpoint(address, verify)
}

func (p *Manager) ResolveRGB11AddressEndpoint(client *SatsNetDKVSClient, address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	return p.rgbManager.ResolveRGB11AddressEndpoint(client, address, verify)
}

func (p *Manager) RestoreLatestRGB11WalletState(walletID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, error) {
	return p.rgbManager.RestoreLatestRGB11WalletState(walletID, verifyOpts)
}

func (p *Manager) RestoreRGB11WalletState(client *SatsNetDKVSClient, walletID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, error) {
	return p.rgbManager.RestoreRGB11WalletState(client, walletID, verifyOpts)
}

func (p *Manager) SendRGB11AddressACK(client *SatsNetDKVSClient, senderAccountID, messageID string,
	ack RGB11AddressACK, options RGB11AddressDeliveryOptions) (*swire.DKVSRecord, error) {
	return p.rgbManager.SendRGB11AddressACK(client, senderAccountID, messageID, ack, options)
}

func (p *Manager) SyncConfiguredRGB11AddressMailbox(ctx context.Context,
	verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*RGB11AddressMailboxSyncResult, error) {
	return p.rgbManager.SyncConfiguredRGB11AddressMailbox(ctx, verify, ackOptions)
}

func (p *Manager) SyncRGB11WalletState(walletID string, opts dkvsindexer.RecordOptions) (*coresync.WalletHead, error) {
	return p.rgbManager.SyncRGB11WalletState(walletID, opts)
}

func (p *Manager) ValidateRGB11Consignment(ctx context.Context, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	return p.rgbManager.ValidateRGB11Consignment(ctx, raw)
}

// getL1TxOutput is the single non-RGB transaction-builder integration point.
func (p *Manager) getL1TxOutput(outpoint string) (*TxOutput, error) {
	return p.rgbManager.getL1TxOutput(outpoint)
}
