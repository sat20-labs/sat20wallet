package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// rehydratePendingPaymentRuntime restores only post-negotiation payments from
// local records. The caller holds channelIdentityMu after unlocking the catalog.
// Validate the whole channel group before installation: installChannelLocked
// rebinds all its reservations, including earlier payments awaiting confirmation.
func (p *Manager) rehydratePendingPaymentRuntime() error {
	groups := make(map[string]map[int64]*PaymentReservation)
	for id, resv := range p.GetPaymentReservations() {
		if resv == nil || resv.Channel != nil || p.GetChannel(resv.ChannelId) != nil {
			continue
		}
		if groups[resv.ChannelId] == nil {
			groups[resv.ChannelId] = make(map[int64]*PaymentReservation)
		}
		groups[resv.ChannelId][id] = resv
	}
	var recoveryErrors []error
	for channelID, originals := range groups {
		replacements := make(map[int64]*PaymentReservation, len(originals))
		ids := make([]int64, 0, len(originals))
		for id := range originals {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var channel *Channel
		var recoveryErr error
		spent := make(map[swire.OutPoint]bool)
		heights := make(map[int]bool)
		for _, id := range ids {
			original := originals[id]
			loaded, err := LoadReservation(p.db, p, RESV_TYPE_PAYMENT, id)
			if err != nil {
				recoveryErr = fmt.Errorf("reload payment %d: %w", id, err)
				break
			}
			replacement, ok := loaded.(*PaymentReservation)
			if !ok || replacement.Id != id || replacement.ChannelId != channelID ||
				replacement.WalletId != original.WalletId || replacement.Status != original.Status {
				recoveryErr = fmt.Errorf("payment %d persisted identity or phase mismatch", id)
				break
			}
			if channel == nil {
				channel, err = p.loadPendingPaymentChannel(replacement)
				if err != nil {
					recoveryErr = err
					break
				}
			}
			height, err := validatePendingPayment(channel, replacement)
			if err != nil {
				recoveryErr = fmt.Errorf("payment %d: %w", id, err)
				break
			}
			if heights[height] {
				recoveryErr = fmt.Errorf("payment %d has conflicting channel generation", id)
				break
			}
			heights[height] = true
			for _, input := range replacement.PaymentTx.TxIn {
				if spent[input.PreviousOutPoint] {
					recoveryErr = fmt.Errorf("payment %d conflicts with another pending payment input", id)
					break
				}
				spent[input.PreviousOutPoint] = true
			}
			if recoveryErr != nil {
				break
			}
			replacements[id] = replacement
		}
		if recoveryErr != nil {
			recoveryErrors = append(recoveryErrors, fmt.Errorf("restore payment channel %s: %w", channelID, recoveryErr))
			continue
		}

		p.mutex.Lock()
		changed := p.channelMap[channelID] != nil
		for id, original := range originals {
			if p.paymentChannelMap[id] != original || p.resvMap[id] != original || original.Channel != nil ||
				original.Status != replacements[id].Status {
				changed = true
			}
		}
		if changed {
			p.mutex.Unlock()
			continue
		}
		conflict := false
		for id, other := range p.resvMap {
			if replacements[id] == nil && other != nil &&
				reservationMatchesChannelContext(other, channelID, other.GetWalletId()) {
				conflict = true
				break
			}
		}
		for _, other := range p.localActionPerformMap {
			if action, ok := other.(*LocalActionPerformData); ok && action != nil &&
				action.Status > RS_CLOSED && action.Status != RS_PERFORM_ACTION_COMPLETED && action.Action == LOCAL_ACTION_LOCK_WITH_EXPAND {
				if param, ok := action.ActionParam.(*LocalActionParam_Expand); ok && param != nil && param.ChannelId == channelID {
					conflict = true
				}
			}
		}
		if conflict {
			p.mutex.Unlock()
			recoveryErrors = append(recoveryErrors, fmt.Errorf("payment channel %s has another unfinished operation", channelID))
			continue
		}
		for _, replacement := range replacements {
			replacement.Channel = channel
			p.addResvLocked(replacement)
		}
		p.installChannelLocked(channel)
		p.mutex.Unlock()
		if tower := p.GetWatchTower(); tower != nil {
			tower.CleanCurrentRemoteCommitTx(channel)
		}
	}
	return errors.Join(recoveryErrors...)
}

func (p *Manager) loadPendingPaymentChannel(resv *PaymentReservation) (*Channel, error) {
	wallet := resv.LocalWallet()
	if resv.ChannelId == "" || resv.Id <= 0 || resv.WalletId.Id == 0 || wallet == nil || wallet.GetWalletId() != resv.WalletId {
		return nil, fmt.Errorf("invalid payment channel or local wallet identity")
	}
	p.mutex.RLock()
	info := p.walletInfoMap[resv.WalletId.Id]
	accountExists := info != nil && uint64(resv.WalletId.SubAccountId) < uint64(info.Accounts)
	p.mutex.RUnlock()
	if !accountExists {
		return nil, fmt.Errorf("payment wallet account is unavailable")
	}
	stored, err := p.LoadChannelInDB(resv.ChannelId)
	if err != nil {
		return nil, err
	}
	if stored.ChannelId != resv.ChannelId || stored.Status != CS_READY ||
		stored.LocalWalletId != resv.WalletId.Id || stored.LocalChanCfg.WalletId != resv.WalletId.SubAccountId {
		return nil, fmt.Errorf("payment channel identity, READY state or wallet/account mismatch")
	}
	if stored.LocalChanCfg.PaymentKey == nil || stored.RemoteChanCfg.PaymentKey == nil ||
		wallet.GetPaymentPubKey() == nil || !stored.LocalChanCfg.PaymentKey.IsEqual(wallet.GetPaymentPubKey()) {
		return nil, fmt.Errorf("payment channel local payment key mismatch")
	}
	expectedID, err := GetP2WSHaddress(stored.LocalChanCfg.PaymentKey.SerializeCompressed(), stored.RemoteChanCfg.PaymentKey.SerializeCompressed())
	if err != nil || expectedID != stored.ChannelId {
		return nil, fmt.Errorf("payment keys do not match channel id")
	}
	// Do not run native proof validation here: RGB evidence may require network
	// access. Inspect only the local next-generation obligation. A normal,
	// unrevoked journal does not prevent restoring an ordinary payment.
	context := &Channel{ChannelInDB: *stored}
	raw, err := p.db.Read(stpNativeCommitmentKey(context, "local"))
	if err == nil {
		var record stpNativeCommitmentRecord
		if err := DecodeFromBytes(raw, &record); err != nil {
			return nil, err
		}
		if record.Version != 1 || record.ChannelID != stored.ChannelId ||
			record.Height != uint64(stored.CommitHeight)+1 || record.Role != "local" {
			return nil, fmt.Errorf("native payment recovery journal has inconsistent context")
		}
		if record.RevokedPrevious {
			return nil, fmt.Errorf("payment channel has a pending native revocation obligation")
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	if _, err := p.db.Read(stpNativePreparedRecoveryKey(context)); err == nil {
		// A malformed preparation must also block ordinary READY installation.
		return nil, fmt.Errorf("payment channel has a pending native prepared recovery")
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	return &Channel{ChannelInDB: *stored, manager: p, localWallet: wallet,
		PeerRPC: p.GetPeerNodeClient(stored)}, nil
}

func validatePendingPayment(channel *Channel, resv *PaymentReservation) (int, error) {
	if (resv.Status != RS_PAYMENT_STARTED && resv.Status != RS_PAYMENT_BROADCASTED) || !resv.NeedSendLockTx ||
		resv.PaymentTx == nil || len(resv.PaymentTx.TxIn) == 0 || len(resv.PaymentTx.TxOut) == 0 {
		return 0, fmt.Errorf("payment has no complete post-negotiation transaction")
	}
	if resv.Id <= 0 || resv.WalletId != channel.LocalWallet().GetWalletId() || resv.LocalWallet() == nil ||
		resv.LocalWallet().GetWalletId() != resv.WalletId {
		return 0, fmt.Errorf("payment wallet or persisted generation mismatch")
	}
	tx := resv.PaymentTx
	// TxID and signature hashing serialize every input/output, so validate the
	// shape before either can dereference a damaged persisted transaction.
	for _, input := range tx.TxIn {
		if input == nil || len(input.Witness) == 0 {
			return 0, fmt.Errorf("payment input or witness is missing")
		}
	}
	for _, output := range tx.TxOut {
		if output == nil || output.Value < 0 {
			return 0, fmt.Errorf("payment output is missing or invalid")
		}
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	prevouts := make(map[swire.OutPoint]bool)
	for _, outputs := range [][]*TxOutput_SatsNet{resv.Utxos, resv.Fees} {
		for _, output := range outputs {
			if output == nil || output.OutValue.Value < 0 {
				return 0, fmt.Errorf("payment prevout is missing or invalid")
			}
			point, err := swire.NewOutPointFromString(output.OutPointStr)
			if err != nil || point == nil || prevouts[*point] {
				return 0, fmt.Errorf("payment prevout identity is invalid or duplicated")
			}
			prevouts[*point] = true
			fetcher.AddPrevOut(*point, &output.OutValue)
		}
	}
	for _, output := range resv.Utxos {
		if resv.IsUnlock {
			if !bytes.Equal(output.OutValue.PkScript, channel.GetChannelPkScript()) {
				return 0, fmt.Errorf("unlock prevout does not belong to channel")
			}
		} else if err := ValidateSatsNetInputFromInitiator(channel, resv.IsInitiator == channel.IsInitiator, output, "payment"); err != nil {
			return 0, err
		}
	}
	for _, output := range resv.Fees {
		if err := ValidateSatsNetInputFromInitiator(channel, resv.IsInitiator == channel.IsInitiator, output, "payment fee"); err != nil {
			return 0, err
		}
	}
	if len(prevouts) != len(tx.TxIn) {
		return 0, fmt.Errorf("payment prevouts do not cover transaction inputs")
	}
	for _, input := range tx.TxIn {
		if !prevouts[input.PreviousOutPoint] {
			return 0, fmt.Errorf("payment input prevout is missing")
		}
	}
	height := -1
	for _, output := range tx.TxOut {
		kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err != nil || kind != sindexer.CONTENT_TYPE_CHANNELID {
			continue
		}
		if height != -1 || !strings.HasPrefix(string(data), channel.ChannelId+"-") {
			return 0, fmt.Errorf("payment channel metadata is inconsistent")
		}
		height, err = strconv.Atoi(strings.TrimPrefix(string(data), channel.ChannelId+"-"))
		if err != nil || height < 0 || height >= channel.CommitHeight {
			return 0, fmt.Errorf("payment commitment generation mismatch")
		}
	}
	if height < 0 {
		return 0, fmt.Errorf("payment channel metadata is missing")
	}
	// Historical reservations need not equal the latest payment. Use the
	// signed generation, not wall-clock ordering of reservation IDs.
	if height == channel.CommitHeight-1 {
		if resv.Id != channel.UpdateTime || tx.TxID() != channel.LastPaymentTxId {
			return 0, fmt.Errorf("current payment transaction does not match channel generation")
		}
	} else if resv.Id == channel.UpdateTime {
		return 0, fmt.Errorf("current payment has an older commitment generation")
	}
	if err := VerifySignedTx_SatsNet(tx, fetcher); err != nil {
		return 0, fmt.Errorf("verify persisted payment: %w", err)
	}
	return height, nil
}
