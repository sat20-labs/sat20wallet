package wallet

import (
	"bytes"
	"context"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"sync"

	"github.com/btcsuite/btcd/btcutil/psbt"
	rgb "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// A direct preview is an unsigned, memory-only candidate. Confirmation adopts
// these exact bytes; it never resolves the recipient or selects inputs again.
type rgb11DirectPreview struct {
	mu        sync.Mutex
	manager   *rgb11Manager
	pending   []*rgb.PendingTransfer
	inputs    []string
	change    []string
	saved     bool
	cancelled bool
}

func (p *Manager) previewRGBDirectTransfer(ctx context.Context, request RGB11AddressSendRequest) (*RGBTransferView, error) {
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew,
		func(manager *rgb11Manager) (*RGBTransferView, error) {
			if !p.rgb11ManagerIsRoot(manager) {
				return nil, ErrRGB11TraditionalReceiveRequired
			}
			preview := &rgb11DirectPreview{manager: manager}
			manager.preview = preview
			prepared, _, err := manager.PrepareConfiguredRGB11AddressTransfer(ctx, request, dkvsindexer.RecordVerificationOptions{})
			if err != nil {
				return nil, err
			}
			packet, err := psbt.NewFromRawBytes(bytes.NewReader(preview.pending[0].SignedPSBT), false)
			if err != nil {
				return nil, err
			}
			fee, err := packet.GetTxFee()
			if err != nil {
				return nil, err
			}
			// A preview finishing after a wallet switch must not repopulate the
			// departed wallet's UI session. This is still the preview phase.
			p.rgbOperationMu.RLock()
			defer p.rgbOperationMu.RUnlock()
			p.mutex.RLock()
			selected := p.status != nil && p.wallet != nil &&
				p.status.CurrentWallet == manager.status.CurrentWallet &&
				p.status.CurrentAccount == manager.status.CurrentAccount
			p.mutex.RUnlock()
			if !selected {
				return nil, ErrRGB11OperationScopeChanged
			}
			p.rgbDirectPreviews.Store(prepared.State.TransferID, preview)
			return &RGBTransferView{ID: prepared.State.TransferID, Status: "preview", TxID: prepared.TxID,
				FeeSats: int64(fee), CanConfirm: true, CanCancel: true,
				Recipients:   []RGBRecipientView{{Destination: request.ReceiverAddress, Amount: rgbDisplayAmount(prepared.State.Asset)}},
				WarningCodes: []string{"rgb_address_carrier"}}, nil
		})
}

func (p *Manager) confirmRGBDirectPreview(ctx context.Context, id string, preview *rgb11DirectPreview) (*RGBTransferView, error) {
	preview.mu.Lock()
	defer preview.mu.Unlock()
	if preview.cancelled {
		return nil, ErrRGB11OperationScopeChanged
	}
	manager := preview.manager
	if !preview.saved {
		// Preserve the existing local lifecycle/write gate, not another quote,
		// account lookup, recipient lookup, chain query or input selection.
		if !p.rgbManagedExecutionMu.TryRLock() {
			return nil, ErrAccountManagedDataImportIncomplete
		}
		err := func() error {
			defer p.rgbManagedExecutionMu.RUnlock()
			if err := manager.checkRGB11Write(); err != nil {
				return err
			}
			reservationID := preview.pending[0].ReservationID
			if err := manager.utxoLockerL1.TryReserve(preview.inputs, sdkcommon.UtxoLockRGBPending, reservationID, sdkcommon.UtxoLockRGB); err != nil {
				return err
			}
			reserved := append([]string(nil), preview.inputs...)
			committed := false
			defer func() {
				if !committed {
					_ = manager.utxoLockerL1.ReleaseReservation(reserved, reservationID)
				}
			}()
			if len(preview.change) != 0 {
				if err := manager.utxoLockerL1.TryReserve(preview.change, sdkcommon.UtxoLockRGBPending, reservationID); err != nil {
					return err
				}
				reserved = append(reserved, preview.change...)
			}
			walletID, err := manager.RGB11WalletID()
			if err != nil {
				return err
			}
			// Append the already-built journal to current local storage. Do not
			// overwrite other operations with a snapshot captured at preview time.
			current, err := manager.readRGB11Local(walletID)
			if err != nil {
				return err
			}
			candidate := *current
			for _, pending := range preview.pending {
				candidate.ProjectionRecords, err = rgb.UpsertLocalPending(candidate.ProjectionRecords, pending)
				if err != nil {
					return err
				}
			}
			if err := manager.commitRGB11Local(ctx, current, &candidate); err != nil {
				return err
			}
			committed, preview.saved = true, true
			manager.accountManagementOwner().NotifyAccountChanged()
			return nil
		}()
		if err != nil {
			return nil, err
		}
	}
	// From here the exact candidate is recoverable. The established sender
	// owns delivery, signing, broadcast-intent persistence and reconciliation.
	_, err := manager.DeliverAndBroadcastConfiguredRGB11AddressTransfer(id)
	if err != nil {
		return nil, err
	}
	view, err := p.rgbCurrentTransfer(id)
	if err == nil {
		p.finishRGBLog(id, nil, view)
	}
	p.rgbDirectPreviews.Delete(id)
	return view, err
}

func (p *Manager) clearRGBDirectPreviews() {
	p.rgbDirectPreviews.Range(func(key, value any) bool {
		p.rgbDirectPreviews.Delete(key)
		return true
	})
}
