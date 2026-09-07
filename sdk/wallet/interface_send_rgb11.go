package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// isL1SendInputProtected is shared by ordinary asset and fee selectors,
// including channel sends. Bitcoin indexers cannot identify RGB allocations;
// a missing derived lock must never turn a locally known carrier into fee BTC.
func (p *Manager) isL1SendInputProtected(outpoint string) bool {
	if p.utxoLockerL1.IsLocked(outpoint) {
		return true
	}
	return p.isL1RGBInputProtected(outpoint)
}

// Explicit inputs may legitimately carry a non-RGB reservation (for example
// a channel sweep), but may never be used to inscribe over RGB state.
func (p *Manager) isL1RGBInputProtected(outpoint string) bool {
	if p.utxoLockerL1.IsLocked(outpoint) {
		lock := p.utxoLockerL1.GetLockedUtxoList()[outpoint]
		if lock != nil && (lock.Reason == rgb11wallet.LockReasonRGB || lock.Reason == rgb11wallet.LockReasonPending) {
			return true
		}
	}
	if p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return false
	}
	output, err := p.rgbManager.projectionStore.LoadOutput(outpoint)
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return false
	}
	if err != nil || output == nil {
		Log.Errorf("cannot resolve local RGB state for send input %s: %v", outpoint, err)
		return true
	}
	for _, asset := range output.Assets {
		if asset.Name.Protocol == rgb11wallet.Protocol {
			return true
		}
	}
	return false
}

// RGB11SendPendingError retains the durable transfer identity when delivery,
// receiver availability or the caller's deadline prevents a synchronous send.
// ResumeRGB11Send continues this transaction without selecting new inputs.
type RGB11SendPendingError struct {
	TransferID string
	TxID       string
	Err        error
}

func (e *RGB11SendPendingError) Error() string {
	return fmt.Sprintf("RGB11 send pending: transfer=%s txid=%s: %v", e.TransferID, e.TxID, e.Err)
}
func (e *RGB11SendPendingError) Unwrap() error { return e.Err }

func parseRGB11SendAmount(raw string) (uint64, error) {
	amount, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || amount == 0 {
		return 0, fmt.Errorf("invalid RGB11 amount %q", raw)
	}
	return amount, nil
}

func (p *Manager) sendRGB11Assets(localWallet common.Wallet, dest []*SendAssetInfo, name *indexer.AssetName,
	feeRate int64, memo []byte) (*wire.MsgTx, int64, error) {
	return p.sendRGB11AssetsWithChannel(localWallet, dest, name, feeRate, memo, nil)
}

func (p *Manager) sendRGB11AssetsWithChannel(localWallet common.Wallet, dest []*SendAssetInfo, name *indexer.AssetName,
	feeRate int64, memo []byte, channel *rgb11ChannelSendContext) (*wire.MsgTx, int64, error) {
	if localWallet == nil || p.wallet == nil || localWallet.GetAddress() != p.wallet.GetAddress() {
		return nil, 0, ErrRGB11DirectRootRequired
	}
	if len(memo) != 0 {
		return nil, 0, fmt.Errorf("RGB11 witness transaction already contains its commitment; memo is unsupported")
	}
	if len(dest) == 0 || len(dest) > 32 {
		return nil, 0, fmt.Errorf("RGB11 send requires 1 to 32 outputs")
	}
	info := p.getTickerInfo(name)
	if info == nil {
		return nil, 0, fmt.Errorf("unknown RGB11 ticker %s", name.String())
	}
	requests := make([]RGB11AddressSendRequest, len(dest))
	for i, d := range dest {
		if d == nil || d.AssetName == nil || *d.AssetName != *name || d.AssetAmt == nil || d.AssetAmt.Precision != info.Divisibility {
			return nil, 0, fmt.Errorf("invalid RGB11 recipient %d", i)
		}
		amount, err := decimalUint64(d.AssetAmt)
		if err != nil || amount == 0 {
			return nil, 0, fmt.Errorf("invalid RGB11 recipient amount %d", i)
		}
		if d.Value != 0 && d.Value != rgb11CarrierValue {
			return nil, 0, fmt.Errorf("RGB11 recipient value must be %d sats", rgb11CarrierValue)
		}
		requests[i] = RGB11AddressSendRequest{ReceiverAddress: d.Address, AssetName: *name, AmountRaw: strconv.FormatUint(amount, 10), FeeRate: feeRate, MinConfirmations: 1}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prepared, err := runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew, func(manager *rgb11Manager) (*RGB11PreparedTransfer, error) {
		if !p.rgb11ManagerIsRoot(manager) {
			return nil, ErrRGB11DirectRootRequired
		}
		manager.channelSend = channel
		defer func() { manager.channelSend = nil }()
		return manager.prepareRGB11AddressBatch(ctx, requests, dkvsindexer.RecordVerificationOptions{})
	})
	if err != nil {
		if prepared != nil && prepared.State != nil {
			return nil, 0, &RGB11SendPendingError{TransferID: prepared.State.TransferID, TxID: prepared.TxID, Err: err}
		}
		return nil, 0, err
	}
	return p.resumeRGB11Send(ctx, prepared.State.TransferID)
}

// ResumeRGB11Send completes SAT20 direct delivery and waits for all recipient
// ACKs before using the existing durable RGB broadcast boundary.
func (p *Manager) ResumeRGB11Send(ctx context.Context, transferID string) (string, int64, error) {
	tx, fee, err := p.resumeRGB11Send(ctx, transferID)
	if tx == nil {
		return "", fee, err
	}
	return tx.TxID(), fee, err
}

func (p *Manager) resumeRGB11Send(ctx context.Context, transferID string) (*wire.MsgTx, int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	type sendResult struct {
		tx   *wire.MsgTx
		fee  int64
		done bool
	}
	var txID string
	for {
		result, err := runRootRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue, func(manager *rgb11Manager) (sendResult, error) {
			var result sendResult
			first, err := manager.projectionStore.LoadPendingTransfer(transferID)
			if err != nil {
				return result, err
			}
			txID = first.State.WitnessTxID
			batch, err := manager.loadRGB11AddressBatch(first)
			if err != nil {
				return result, err
			}
			tx := wire.NewMsgTx(2)
			if err := tx.Deserialize(bytes.NewReader(first.SignedTx)); err != nil {
				return result, err
			}
			var fee int64
			if len(first.SignedPSBT) != 0 {
				packet, err := psbt.NewFromRawBytes(bytes.NewReader(first.SignedPSBT), false)
				if err != nil {
					return result, err
				}
				for _, input := range packet.Inputs {
					if input.WitnessUtxo == nil {
						return result, ErrRGB11Inconsistent
					}
					fee += input.WitnessUtxo.Value
				}
			} else {
				// Recovery snapshots deliberately omit the derivable PSBT.
				for _, input := range tx.TxIn {
					// Spent outputs may no longer exist in the UTXO endpoint.
					// Their value remains recoverable from the immutable parent tx.
					if raw, err := manager.evidence.GetRawTx(input.PreviousOutPoint.Hash.String()); err == nil {
						parent := wire.NewMsgTx(2)
						if err := parent.Deserialize(bytes.NewReader(raw)); err != nil {
							return result, err
						}
						if parent.TxHash() != input.PreviousOutPoint.Hash || uint64(input.PreviousOutPoint.Index) >= uint64(len(parent.TxOut)) {
							return result, ErrRGB11Inconsistent
						}
						fee += parent.TxOut[input.PreviousOutPoint.Index].Value
						continue
					}
					previous, err := manager.l1IndexerClient.GetTxOutput(input.PreviousOutPoint.String())
					if err != nil {
						return result, err
					}
					if previous == nil {
						return result, ErrRGB11Inconsistent
					}
					fee += previous.OutValue.Value
				}
			}
			for _, output := range tx.TxOut {
				fee -= output.Value
			}
			if fee < 0 {
				return result, ErrRGB11Inconsistent
			}
			result.tx, result.fee = tx, fee
			if rgb11BroadcastCompleteStatus(first.State.Status) {
				if first.ChannelSend != nil && !first.ChannelSend.Signed {
					if err := manager.recoverRGB11ChannelWitness(batch); err != nil {
						return result, err
					}
					if err := result.tx.Deserialize(bytes.NewReader(first.SignedTx)); err != nil {
						return result, err
					}
				}
				result.done = true
				return result, nil
			}
			store, err := manager.configuredRGB11Store()
			if err != nil {
				return result, err
			}
			for _, item := range batch {
				if item.State.Status == "prepared" {
					if _, err := manager.deliverRGB11AddressTransferStore(store, item.State.TransferID, RGB11AddressDeliveryOptions{}); err != nil {
						return result, err
					}
				}
			}
			if _, err := manager.SyncConfiguredRGB11AddressMailbox(ctx, dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{}); err != nil {
				return result, err
			}
			_, err = manager.BroadcastRGB11AddressTransfer(transferID)
			if errors.Is(err, ErrRGB11AddressDeliveryRequired) {
				return result, nil
			}
			if err != nil {
				return result, err
			}
			if first.ChannelSend != nil {
				updated, err := manager.projectionStore.LoadPendingTransfer(transferID)
				if err != nil {
					return result, err
				}
				if updated.ChannelSend == nil || !updated.ChannelSend.Signed {
					return result, ErrRGB11Inconsistent
				}
				if err := result.tx.Deserialize(bytes.NewReader(updated.SignedTx)); err != nil {
					return result, err
				}
			}
			result.done = true
			return result, nil
		})
		if err != nil {
			return nil, result.fee, &RGB11SendPendingError{TransferID: transferID, TxID: txID, Err: err}
		}
		if result.done {
			return result.tx, result.fee, nil
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, result.fee, &RGB11SendPendingError{TransferID: transferID, TxID: txID, Err: ctx.Err()}
		case <-timer.C:
		}
	}
}

// L2 ticker facts come from the L2 chain. Do not place them in the shared L1
// ticker cache: an L2 balance does not establish ownership of Bitcoin RGB
// allocations or substitute for a validated L1 consignment.
func (p *Manager) GetSendTickerInfo_SatsNet(name *indexer.AssetName) *indexer.TickerInfo {
	return p.getSendTickerInfo_SatsNet(name)
}

func (p *Manager) getSendTickerInfo_SatsNet(name *indexer.AssetName) *indexer.TickerInfo {
	if name == nil {
		return nil
	}
	if name.Protocol != rgb11wallet.Protocol {
		return p.getTickerInfo(name)
	}
	if p.l2IndexerClient == nil {
		return nil
	}
	info := p.l2IndexerClient.GetTickInfo(name)
	if info == nil || info.AssetName != *name || info.N != 0 {
		return nil
	}
	return info
}
