package wallet

import (
	"context"
	"errors"
	indexer "github.com/sat20-labs/indexer/common"
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
	RGB11ContractExportResult     = rgb11wallet.RGB11ContractExportResult
	RGB11NameRegistration         = rgb11wallet.RGB11Registration
	RGB11ImportResult             = rgb11wallet.RGB11ImportResult
	RGB11RejectListProvider       = rgb11wallet.RGB11RejectListProvider
	RGB11RejectListViolation      = rgb11wallet.RGB11RejectListViolation
	RGB11InvoiceRequest           = rgb11wallet.RGB11InvoiceRequest
	RGB11SendRequest              = rgb11wallet.RGB11SendRequest
	RGB11PreparedTransfer         = rgb11wallet.RGB11PreparedTransfer
	RGB11PreparedTransferPackage  = rgb11wallet.RGB11PreparedTransferPackage
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

// beginRGB11Operation protects a local capture only. Release it before any
// network access; remote RGB work uses a fixed manager view instead.
func (p *Manager) beginRGB11Operation() func() {
	p.rgbManagedExecutionMu.Lock()
	p.rgbOperationMu.RLock()
	return func() {
		p.rgbOperationMu.RUnlock()
		p.rgbManagedExecutionMu.Unlock()
	}
}

func (p *Manager) beginRGB11ReadObservation() func() {
	p.rgbOperationMu.RLock()
	return p.rgbOperationMu.RUnlock
}

// beginExclusiveRGB11Operation protects local lifecycle changes. Network
// operations use captured scopes and validate intent at their own boundaries.
func (p *Manager) beginExclusiveRGB11Operation() func() {
	p.rgbOperationMu.Lock()
	return p.rgbOperationMu.Unlock
}

func (p *Manager) beginRGB11ScopeChange() func() {
	p.rgbOperationMu.Lock()
	return p.rgbOperationMu.Unlock
}

func (p *Manager) synchronizedRGB11Manager() (*rgb11Manager, error) {
	if p == nil || p.rgbManager == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := p.checkAccountManagedDataImport(); err != nil {
		return nil, err
	}
	return p.rgbManager, nil
}

// captureRGB11NetworkScope takes a fixed view while lifecycle changes are
// excluded locally. The returned stores and wallet do not follow UI selection;
// no lifecycle lock may remain held while that view uses a remote provider.
func (p *Manager) captureRGB11NetworkScope(root bool) (*rgb11Manager, error) {
	if p == nil || p.rgbManager == nil {
		return nil, ErrRGB11Inconsistent
	}
	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	p.rgbOperationMu.RLock()
	defer p.rgbOperationMu.RUnlock()
	if root {
		return p.rootRGB11Manager()
	}
	p.mutex.RLock()
	account, err := p.rgbManager.fixedRGB11ScopeAccount()
	p.mutex.RUnlock()
	if err != nil {
		return nil, err
	}
	return p.newScopedRGB11Manager(account)
}

// A background/root view already owns an immutable selection. The selected
// UI manager must capture one before beginning a lock-free network operation.
func (p *rgb11Manager) fixedRGB11NetworkScope() (*rgb11Manager, error) {
	if p == nil || p.Manager == nil {
		return nil, ErrRGB11Inconsistent
	}
	if p.accountOwner != nil && p.accountOwner != p.Manager {
		return p, nil
	}
	return p.Manager.captureRGB11NetworkScope(false)
}

// Publish only the derived status for this captured scope. A UI selection
// made during remote work must never inherit another account's status.
func (p *rgb11Manager) publishRGB11ConsistencyStatus() {
	if p == nil || p.consistencyStatus == "" || p.accountOwner == nil || p.accountOwner == p.Manager {
		return
	}
	owner := p.accountOwner
	owner.rgbOperationMu.Lock()
	defer owner.rgbOperationMu.Unlock()
	owner.mutex.RLock()
	defer owner.mutex.RUnlock()
	if owner.rgbManager != nil && owner.status != nil &&
		owner.status.CurrentWallet == p.status.CurrentWallet && owner.status.CurrentAccount == p.status.CurrentAccount {
		owner.rgbManager.consistencyStatus = p.consistencyStatus
	}
}

func (p *Manager) AcceptRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	return runRootRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11AddressACK, error) {
			return manager.AcceptRGB11AddressACK(record, verify)
		})
}

func (p *Manager) AcceptRGB11Consignment(ctx context.Context, requestID string, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*rgb11wallet.ValidationReceipt, error) {
			return manager.AcceptRGB11Consignment(ctx, requestID, raw)
		})
}

func (p *Manager) BroadcastRGB11AddressTransfer(transferID string) (string, error) {
	return runRootRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (string, error) {
			return manager.BroadcastRGB11AddressTransfer(transferID)
		})
}

func (p *Manager) BroadcastRGB11OutOfBand(transferIDs []string) (string, error) {
	return runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (string, error) {
			return manager.BroadcastRGB11OutOfBand(transferIDs)
		})
}

func (p *Manager) DeliverAndBroadcastRGB11ProxyTransfer(ctx context.Context,
	transferIDs []string) (*RGB11ProxyDeliveryResult, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11ProxyDeliveryResult, error) {
			return manager.DeliverAndBroadcastRGB11ProxyTransfer(ctx, transferIDs)
		})
}

func (p *Manager) CancelRGB11OutOfBandTransfer(transferID string) error {
	_, err := runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (struct{}, error) {
			return struct{}{}, manager.CancelRGB11OutOfBandTransfer(transferID)
		})
	if errors.Is(err, dkvsindexer.ErrWriteConflict) {
		if cancelErr := p.cancelUnpublishedRGB11Transfer(transferID); cancelErr != nil {
			return errors.Join(err, cancelErr)
		}
		return nil
	}
	return err
}

func (p *Manager) CancelExpiredRGB11Transfer(transferID string) error {
	_, err := runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (struct{}, error) {
			return struct{}{}, manager.CancelExpiredRGB11Transfer(transferID)
		})
	return err
}

func (p *Manager) PrepareRGB11Consignment(ctx context.Context, requestID string,
	raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*rgb11wallet.ValidationReceipt, error) {
			return manager.PrepareRGB11Consignment(ctx, requestID, raw)
		})
}

func (p *Manager) CreateRGB11Invoice(request RGB11InvoiceRequest) (*corewallet.ReceiveRequest, error) {
	return runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*corewallet.ReceiveRequest, error) {
			return manager.CreateRGB11Invoice(request)
		})
}

func (p *Manager) DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID string,
	options RGB11AddressDeliveryOptions) (*RGB11AddressDeliveryResult, error) {
	return runRootRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11AddressDeliveryResult, error) {
			return manager.DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID, options)
		})
}

func (p *Manager) EnableConfiguredRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	return runRootRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*RGB11AddressEndpoint, error) {
			if !p.rgb11ManagerIsRoot(manager) {
				return nil, ErrRGB11DirectRootRequired
			}
			return manager.EnableConfiguredRGB11AddressReceive(options)
		})
}

// enableRootRGB11AddressReceive is the internal lifecycle helper used while
// bringing up account services. Its caller already owns that lifecycle; keep
// it out of runAccountOperation to avoid recursively entering the account gate.
func (p *Manager) enableRootRGB11AddressReceive(options RGB11ReceiveCapabilityOptions) (*RGB11AddressEndpoint, error) {
	manager, err := p.captureRGB11NetworkScope(true)
	if err != nil {
		return nil, err
	}
	return manager.EnableConfiguredRGB11AddressReceive(options)
}

func (p *Manager) GetRGB11AssetBalance(name *indexer.AssetName) (*Decimal, error) {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.GetRGB11AssetBalance(name)
}

func (p *Manager) GetRGB11ConsistencyStatus() string {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	if err := p.checkAccountManagedDataImport(); err != nil {
		return "warning"
	}
	return p.rgbManager.GetRGB11ConsistencyStatus()
}

func (p *Manager) GetRGB11ReceiveRequest(requestID string) (*corewallet.ReceiveRequest, error) {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.GetRGB11ReceiveRequest(requestID)
}

func (p *Manager) GetRGB11State() (*RGB11State, error) {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.GetRGB11State()
}

func (p *Manager) ImportRGB11Contract(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*RGB11ImportResult, error) {
			return manager.ImportRGB11Contract(ctx, raw)
		})
}

func (p *Manager) ImportRGB11ContractFile(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*RGB11ImportResult, error) {
			return manager.ImportRGB11ContractFile(ctx, raw)
		})
}

// ExportRGB11Contract returns the canonical RGB\0CON file for a contract
// already validated in the active wallet scope. It does not mutate wallet data.
func (p *Manager) ExportRGB11Contract(contractID string) (*RGB11ContractExportResult, error) {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ExportRGB11Contract(contractID)
}

func (p *Manager) IssueRGB11Asset(ctx context.Context, request RGB11IssueRequest) (*RGB11IssueResult, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*RGB11IssueResult, error) {
			return manager.IssueRGB11Asset(ctx, request)
		})
}

func (p *Manager) ListRGB11Outputs() ([]*TxOutput, error) {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ListRGB11Outputs()
}

func (p *Manager) PrepareConfiguredRGB11AddressTransfer(ctx context.Context, request RGB11AddressSendRequest,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11PreparedTransfer, *RGB11AddressEndpoint, error) {
	type result struct {
		transfer *RGB11PreparedTransfer
		endpoint *RGB11AddressEndpoint
	}
	value, err := runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (result, error) {
			if !p.rgb11ManagerIsRoot(manager) {
				return result{}, ErrRGB11TraditionalReceiveRequired
			}
			transfer, endpoint, err := manager.PrepareConfiguredRGB11AddressTransfer(ctx, request, verify)
			return result{transfer: transfer, endpoint: endpoint}, err
		})
	return value.transfer, value.endpoint, err
}

func (p *Manager) PrepareRGB11Transfer(ctx context.Context, request RGB11SendRequest) (*RGB11PreparedTransfer, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*RGB11PreparedTransfer, error) {
			return manager.PrepareRGB11Transfer(ctx, request)
		})
}

func (p *Manager) ProjectRGB11Allocation(outpoint string, asset *indexer.AssetInfo, proof *rgb11wallet.AllocationProof) error {
	_, err := runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (struct{}, error) {
			return struct{}{}, manager.projectRGB11Allocation(outpoint, asset, proof)
		})
	return err
}

// ResumeRGB11PreparedTransfer reloads an existing transfer package without
// preparing, signing, reserving or mutating it.
func (p *Manager) ResumeRGB11PreparedTransfer(transferID string) (*RGB11PreparedTransferPackage, error) {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	return manager.ResumeRGB11PreparedTransfer(transferID)
}

func (p *Manager) FetchRGB11ProxyAck(ctx context.Context,
	transferID string) (*RGB11ProxyAckResult, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11ProxyAckResult, error) {
			return manager.FetchRGB11ProxyAck(ctx, transferID)
		})
}

func (p *Manager) RGB11WalletID() (string, error) {
	releaseRGB11Observation := p.beginRGB11ReadObservation()
	defer releaseRGB11Observation()
	return p.rgbManager.RGB11WalletID()
}

func (p *Manager) RebuildRGB11Locks() error {
	_, err := runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationRefresh,
		func(manager *rgb11Manager) (struct{}, error) {
			return struct{}{}, manager.RebuildRGB11Locks()
		})
	return err
}

func (p *Manager) RefreshRGB11AddressACK(record *swire.DKVSRecord,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressACK, error) {
	return runRootRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11AddressACK, error) {
			return manager.RefreshRGB11AddressACK(record, verify)
		})
}

func (p *Manager) RefreshRGB11State(ctx context.Context) (*RGB11RefreshResult, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationRefresh,
		func(manager *rgb11Manager) (*RGB11RefreshResult, error) {
			result, err := manager.refreshRGB11State(ctx)
			manager.wakeRGB11ChainReconciliation()
			return result, err
		})
}

func (p *Manager) ReceiveRGB11ProxyConsignment(ctx context.Context,
	requestID string) (*RGB11ProxyReceiveResult, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11ProxyReceiveResult, error) {
			return manager.ReceiveRGB11ProxyConsignment(ctx, requestID)
		})
}

func (p *Manager) RegisterRGB11TickerInfo(info *indexer.TickerInfo) error {
	_, err := runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (struct{}, error) {
			return struct{}{}, manager.RegisterRGB11TickerInfo(info)
		})
	return err
}

func (p *Manager) ResolveConfiguredRGB11AddressEndpoint(address string,
	verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	manager, err := p.captureRGB11NetworkScope(true)
	if err != nil {
		return nil, err
	}
	return manager.ResolveConfiguredRGB11AddressEndpoint(address, verify)
}

func (p *Manager) SyncConfiguredRGB11AddressMailbox(ctx context.Context,
	verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*RGB11AddressMailboxSyncResult, error) {
	return runRootRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11AddressMailboxSyncResult, error) {
			if !p.rgb11ManagerIsRoot(manager) {
				return nil, ErrRGB11DirectRootRequired
			}
			return manager.SyncConfiguredRGB11AddressMailbox(ctx, verify, ackOptions)
		})
}

func (p *Manager) syncRootRGB11AddressMailbox(ctx context.Context,
	verify dkvsindexer.RecordVerificationOptions,
	ackOptions RGB11AddressDeliveryOptions) (*RGB11AddressMailboxSyncResult, error) {
	if p == nil {
		return nil, ErrRGB11Inconsistent
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Generic DKVS polling also reaches this private background adapter. A
	// wallet without RGB recovery must not consume RGB messages, but its
	// ordinary mailbox synchronization remains usable.
	if !p.accountManagedRecoveryConfigured() {
		return &RGB11AddressMailboxSyncResult{}, nil
	}
	return runRootRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue,
		func(manager *rgb11Manager) (*RGB11AddressMailboxSyncResult, error) {
			return manager.SyncConfiguredRGB11AddressMailbox(ctx, verify, ackOptions)
		})
}

func (p *Manager) ValidateRGB11Consignment(ctx context.Context, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	manager, err := p.captureRGB11NetworkScope(false)
	if err != nil {
		return nil, err
	}
	return manager.ValidateRGB11Consignment(ctx, raw)
}

// getL1TxOutput is the single non-RGB transaction-builder integration point.
func (p *Manager) getL1TxOutput(outpoint string) (*TxOutput, error) {
	return p.rgbManager.getL1TxOutput(outpoint)
}

// SetRGB11LocalAssetName changes SDK-local metadata only. Contract identity,
// balances, transfer proofs and SatoshiNet registered names are never renamed.
func (p *Manager) SetRGB11LocalAssetName(contractID, name string) error {
	_, err := runRGB11ManagedOperation(p, context.Background(), rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (struct{}, error) {
			return struct{}{}, manager.SetRGB11LocalAssetName(contractID, name)
		})
	return err
}
