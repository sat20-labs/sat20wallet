package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// This quote is a reviewable unsigned ordinary wallet transfer, not a funding
// reservation, a promise about the old balance, or permission to spend it.
type closedSingleUTXOTopupQuote struct {
	QuoteID       string            `json:"quoteId"`
	WalletID      int64             `json:"walletId"`
	AccountIndex  uint32            `json:"accountIndex"`
	Network       string            `json:"network"`
	PaymentKey    string            `json:"paymentKey"`
	PeerKey       string            `json:"peerKey"`
	ChannelID     string            `json:"channelId"`
	OldParentID   string            `json:"oldParentId"`
	Amount        int64             `json:"amount"`
	WalletFee     int64             `json:"walletFee"`
	RequiredInput int64             `json:"requiredInput"`
	FeeRate       int64             `json:"feeRate"`
	FeeConfig     *ChannelFeeConfig `json:"feeConfig"`
	UnsignedTx    string            `json:"unsignedTx"`
}

// Only this JSON string is nested in the existing recovery owner's ActionParam.
// No new database key, gob type, signer or transaction owner is introduced.
type closedSingleUTXOTopupRecord struct {
	Quote     string `json:"quote"`
	SignedRaw string `json:"signedRaw"`
	TxID      string `json:"txId"`
	Vout      uint32 `json:"vout"`
	Phase     string `json:"phase"`
}

type closedSingleUTXOTopupContext struct {
	wallet  common.Wallet
	client  NodeRPCClient
	l1      IndexerRPCClient
	l2      IndexerRPCClient
	locker  *UtxoLocker
	peer    []byte
	network string
	mode    string
}

func (p *Manager) captureClosedSingleUTXOTopup() (*closedSingleUTXOTopupContext, error) {
	if p == nil {
		return nil, fmt.Errorf("wallet manager is unavailable")
	}
	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.wallet == nil || p.serverNode == nil || p.serverNode.client == nil || p.serverNode.Pubkey == nil {
		return nil, fmt.Errorf("wallet or server is unavailable; no funds were moved")
	}
	c := &closedSingleUTXOTopupContext{wallet: p.wallet.Clone(), client: p.serverNode.client, locker: p.utxoLockerL1, peer: append([]byte(nil), p.serverNode.Pubkey.SerializeCompressed()...)}
	if p.cfg != nil {
		c.network = p.cfg.Chain
		c.mode = p.cfg.Mode
	}
	if manager := p.l1IndexerClient; manager != nil {
		manager.mutex.RLock()
		c.l1 = manager.active
		if c.l1 == nil && len(manager.indexers) != 0 {
			c.l1 = manager.indexers[0]
		}
		manager.mutex.RUnlock()
	}
	if manager := p.l2IndexerClient; manager != nil {
		manager.mutex.RLock()
		c.l2 = manager.active
		if c.l2 == nil && len(manager.indexers) != 0 {
			c.l2 = manager.indexers[0]
		}
		manager.mutex.RUnlock()
	}
	if c.wallet == nil || c.wallet.GetPaymentPubKey() == nil || c.wallet.GetNodePubKey() == nil {
		return nil, fmt.Errorf("wallet signer is unavailable")
	}
	return c, nil
}

// Caller holds channelIdentityMu while checking and using the captured signer.
func (p *Manager) closedSingleUTXOIdentityMatches(c *closedSingleUTXOTopupContext) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	network := ""
	if p.cfg != nil {
		network = p.cfg.Chain
	}
	return p.wallet != nil && p.serverNode != nil && p.serverNode.Pubkey != nil &&
		p.wallet.GetWalletId() == c.wallet.GetWalletId() && p.wallet.GetPaymentPubKey() != nil &&
		p.wallet.GetPaymentPubKey().IsEqual(c.wallet.GetPaymentPubKey()) &&
		bytes.Equal(p.serverNode.Pubkey.SerializeCompressed(), c.peer) && network == c.network && p.utxoLockerL1 == c.locker
}

func (p *Manager) probeClosedSingleUTXOTopup(c *closedSingleUTXOTopupContext, channelID, offlineHint string) error {
	req := &wwire.PingReq{PingRequest: wwire.PingRequest{MsgHeader: wwire.NewMsgHeader(), PubKey: c.wallet.GetPaymentPubKey().SerializeCompressed(), NodeId: c.wallet.GetNodePubKey().SerializeCompressed()}}
	req.Mode = c.mode
	raw, err := json.Marshal(req.PingRequest)
	if err != nil {
		return err
	}
	// Authentication only. A quote never signs a funding or transfer transaction.
	req.Sig, err = c.wallet.SignMessageWithIndex(raw, 0)
	if err != nil {
		return err
	}
	response, err := c.client.SendPingReq(req)
	if err != nil || response == nil || response.Code < 0 || response.PingResponse == nil {
		return fmt.Errorf("%s", offlineHint)
	}
	if response.NextAction != "pong" && (response.NextAction != "rebuild" || response.ActionParam != channelID) {
		return fmt.Errorf("server response does not authorize recovery for this exact payment account")
	}
	p.channelIdentityMu.RLock()
	matches := p.closedSingleUTXOIdentityMatches(c)
	p.channelIdentityMu.RUnlock()
	if !matches {
		return fmt.Errorf("wallet, account, peer or network changed; request a new quote")
	}
	return nil
}

func (p *Manager) closedSingleUTXOQuoteChannel(c *closedSingleUTXOTopupContext, channelID string) (*ChannelInDB, error) {
	if p.db == nil {
		return nil, fmt.Errorf("local closed-channel state is unavailable; a seed cannot recreate it")
	}
	if active := p.GetActiveChannelWithId(channelID); active != nil {
		return nil, fmt.Errorf("this channel is still active; peer unavailability is not a confirmed close")
	}
	stored, err := p.LoadChannelInDB(channelID)
	if err != nil {
		return nil, err
	}
	if stored.ChannelId != channelID || stored.Status > CS_CLOSED || stored.CommitHeight < 0 || len(stored.Contract) != 0 || !stored.IsInitiator ||
		stored.LocalWalletId != c.wallet.GetId() || stored.LocalChanCfg.WalletId != c.wallet.GetSubAccount() || stored.LocalChanCfg.PaymentKey == nil ||
		!stored.LocalChanCfg.PaymentKey.IsEqual(c.wallet.GetPaymentPubKey()) || stored.RemoteChanCfg.PaymentKey == nil ||
		!bytes.Equal(stored.RemoteChanCfg.PaymentKey.SerializeCompressed(), c.peer) || stored.RemoteCommitment == nil || stored.RemoteCommitment.CommitTx == nil {
		return nil, fmt.Errorf("exact local closed channel and old parent are required; a seed cannot recreate them")
	}
	if record, err := p.loadSTPNativeRevocationRecovery(stored); err != nil {
		return nil, err
	} else if record != nil {
		return nil, fmt.Errorf("revoked commitment recovery is pending")
	}
	if record, err := p.readSTPNativePreparedRecovery(&Channel{ChannelInDB: *stored}); err != nil {
		return nil, err
	} else if record != nil {
		return nil, fmt.Errorf("prepared commitment recovery is pending")
	}
	return stored, nil
}

func closedSingleUTXOQuoteID(q closedSingleUTXOTopupQuote) string {
	q.QuoteID = ""
	raw, _ := json.Marshal(q)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func parseClosedSingleUTXOQuote(raw string) (*closedSingleUTXOTopupQuote, error) {
	var q closedSingleUTXOTopupQuote
	if json.Unmarshal([]byte(raw), &q) != nil || q.ChannelID == "" || q.OldParentID == "" || q.PaymentKey == "" || q.PeerKey == "" ||
		q.FeeConfig == nil || q.Amount <= 0 || q.WalletFee < 0 || q.FeeRate <= 0 || q.Amount > math.MaxInt64-q.WalletFee ||
		q.RequiredInput != q.Amount+q.WalletFee || q.UnsignedTx == "" || q.QuoteID != closedSingleUTXOQuoteID(q) {
		return nil, fmt.Errorf("invalid or incomplete single-UTXO funding quote")
	}
	return &q, nil
}

func (p *Manager) quoteClosedSingleUTXOTopup(c *closedSingleUTXOTopupContext, feeRate int64) (*closedSingleUTXOTopupQuote, *wire.MsgTx, *txscript.MultiPrevOutFetcher, error) {
	channelID, err := GetP2WSHaddress(c.peer, c.wallet.GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		return nil, nil, nil, err
	}
	stored, err := p.closedSingleUTXOQuoteChannel(c, channelID)
	if err != nil {
		return nil, nil, nil, err
	}
	hasOwner := false
	var savedOwner *LocalActionPerformData
	var savedQuote *closedSingleUTXOTopupQuote
	var savedRecord *closedSingleUTXOTopupRecord
	for _, item := range p.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery || !closedIntentWalletMatches(owner, c.wallet) {
			continue
		}
		owner.RLock()
		intent, parseErr := loadClosedChannelIntent(owner)
		terminal := owner.Status == closedIntentReopened || owner.Status == closedIntentWithdrawn
		owner.RUnlock()
		if parseErr != nil {
			return nil, nil, nil, parseErr
		}
		if intent.ChannelID != channelID || terminal {
			continue
		}
		hasOwner = true
		if intent.TopupJSON != "" {
			var record closedSingleUTXOTopupRecord
			if json.Unmarshal([]byte(intent.TopupJSON), &record) != nil {
				return nil, nil, nil, fmt.Errorf("invalid original top-up record")
			}
			if record.Phase != "approved" && record.Phase != "signed" && record.Phase != "broadcast_inflight" && record.Phase != "broadcast" {
				return nil, nil, nil, closedChannelRecoveryPending(owner, "existing_topup:"+record.TxID, nil)
			}
			original, parseErr := parseClosedSingleUTXOQuote(record.Quote)
			if parseErr != nil || original.ChannelID != channelID || original.OldParentID != stored.RemoteCommitment.CommitTx.TxID() ||
				original.WalletID != c.wallet.GetId() || original.AccountIndex != c.wallet.GetSubAccount() || original.Network != c.network ||
				original.PaymentKey != hex.EncodeToString(c.wallet.GetPaymentPubKey().SerializeCompressed()) || original.PeerKey != hex.EncodeToString(c.peer) || original.FeeRate != feeRate {
				return nil, nil, nil, fmt.Errorf("prepared top-up identity or approved fee rate changed; retain its original transaction")
			}
			transactionRaw := record.SignedRaw
			if record.Phase == "approved" {
				transactionRaw = original.UnsignedTx
			}
			signed, decodeErr := DecodeMsgTx(transactionRaw)
			if decodeErr != nil {
				return nil, nil, nil, decodeErr
			}
			if err := validateClosedSingleUTXOTopupRaw(original, &record, signed); err != nil {
				return nil, nil, nil, err
			}
			savedOwner, savedQuote, savedRecord = owner, original, &record
		} else if closedSingleUTXOPreparedOwnerMayAdopt(owner, intent) {
			savedOwner = owner
		}
		if intent.OldParentID != stored.RemoteCommitment.CommitTx.TxID() || intent.Choice != "reopen" || intent.Request != nil ||
			(intent.Step != "prepared" && intent.Step != "open_rejected") {
			return nil, nil, nil, closedChannelRecoveryPending(owner, intent.Step, nil)
		}
	}

	// Only this exact account-bound top-up or definitely never-dispatched
	// prepared owner may exclude itself. Other unresolved lifecycles still block.
	if savedOwner != nil {
		p.channelIdentityMu.RLock()
		matches := p.closedSingleUTXOIdentityMatches(c)
		lockPending := matches && p.hasPendingLockWithExpand(channelID)
		p.channelIdentityMu.RUnlock()
		if !matches || lockPending {
			return nil, nil, nil, fmt.Errorf("account changed or channel lock-with-expand is already in progress")
		}
		if err := p.rejectClosedChannelIntentConflicts(savedOwner, c.wallet); err != nil {
			return nil, nil, nil, err
		}
	} else if err := p.rejectUnfinishedChannelLifecycleForWallet(channelID, c.wallet); err != nil {
		return nil, nil, nil, err
	}
	offlineHint, err := p.closedSingleUTXOOfflineHint(c, stored)
	if err != nil {
		return nil, nil, nil, err
	}
	if hasOwner {
		offlineHint = "server is offline; the original reopen intent or prepared top-up remains pending; no alternative withdrawal was chosen"
	}
	if err = p.probeClosedSingleUTXOTopup(c, channelID, offlineHint); err != nil {
		return nil, nil, nil, err
	}

	if feeRate <= 0 {
		return nil, nil, nil, fmt.Errorf("a positive explicit fee rate is required")
	}
	feeClient, ok := c.client.(ChannelOpenFeeRPCClient)
	if !ok {
		return nil, nil, nil, fmt.Errorf("server does not provide a channel fee quote")
	}
	fees, err := feeClient.GetChannelOpenFeeReq()
	if err != nil {
		return nil, nil, nil, err
	}
	if fees == nil || !fees.Valid || fees.OpenFee == nil {
		return nil, nil, nil, fmt.Errorf("server returned invalid channel fees")
	}
	feeCfg := NewFromOpenChannelFee(fees.OpenFee)
	if feeCfg.ManageFee < 0 || feeCfg.MortgageFee < 0 || feeCfg.CommitmentFee < 0 || feeCfg.MinReserveSats < 0 ||
		feeCfg.ManageFee > math.MaxInt64-feeCfg.MortgageFee || feeCfg.ManageFee+feeCfg.MortgageFee > math.MaxInt64-feeCfg.CommitmentFee-MIN_AVAILABLE_VALUE || feeCfg.FeeToDAO() < 0 {
		return nil, nil, nil, fmt.Errorf("server returned invalid channel fee amounts")
	}
	amount := feeCfg.MinCapacity()
	if fees.MinCapacity > amount {
		amount = fees.MinCapacity
	}
	if c.l1 == nil || c.locker == nil {
		return nil, nil, nil, fmt.Errorf("Bitcoin account is unavailable")
	}
	if savedQuote != nil {
		if savedQuote.Amount < amount {
			return nil, nil, nil, fmt.Errorf("server now requires a larger output; retain the original prepared transaction pending review")
		}
		transactionRaw := savedRecord.SignedRaw
		if savedRecord.Phase == "approved" {
			transactionRaw = savedQuote.UnsignedTx
		}
		tx, err := DecodeMsgTx(transactionRaw)
		if err != nil {
			return nil, nil, nil, err
		}
		if err = validateClosedSingleUTXOTopupRaw(savedQuote, savedRecord, tx); err != nil {
			return nil, nil, nil, err
		}
		fetcher := txscript.NewMultiPrevOutFetcher(nil)
		for _, input := range tx.TxIn {
			output, readErr := c.l1.GetTxOutput(input.PreviousOutPoint.String())
			if readErr != nil || output == nil {
				return nil, nil, nil, fmt.Errorf("prepared top-up input is unavailable")
			}
			fetcher.AddPrevOut(input.PreviousOutPoint, &output.OutValue)
		}
		if err = p.revalidateClosedSingleUTXOInputs(c, tx, fetcher); err != nil {
			return nil, nil, nil, err
		}
		if savedRecord.Phase != "approved" {
			if err = VerifySignedTx(tx, fetcher); err != nil {
				return nil, nil, nil, err
			}
		}
		p.channelIdentityMu.RLock()
		matches := p.closedSingleUTXOIdentityMatches(c)
		for _, input := range tx.TxIn {
			if p.isL1RGBInputProtected(input.PreviousOutPoint.String()) {
				matches = false
			}
		}
		p.channelIdentityMu.RUnlock()
		if !matches {
			return nil, nil, nil, fmt.Errorf("prepared top-up account or asset protection changed")
		}
		unsigned, err := DecodeMsgTx(savedQuote.UnsignedTx)
		return savedQuote, unsigned, fetcher, err
	}
	dest := []*SendAssetInfo{{Address: channelID, AssetName: &indexer.ASSET_PLAIN_SAT, AssetAmt: indexer.NewDefaultDecimal(0), Value: amount}}
	tx, fetcher, fee, err := p.BuildBatchSendTxV3_btc(c.wallet.GetAddress(), nil, dest, feeRate, nil, true, false, false)
	if err != nil {
		return nil, nil, nil, err
	}
	if fee < 0 || amount > math.MaxInt64-fee {
		return nil, nil, nil, fmt.Errorf("invalid ordinary wallet transfer fee")
	}
	unsigned, err := EncodeMsgTx(tx)
	if err != nil {
		return nil, nil, nil, err
	}
	id := c.wallet.GetWalletId()
	q := &closedSingleUTXOTopupQuote{WalletID: id.Id, AccountIndex: id.SubAccountId, Network: c.network, PaymentKey: hex.EncodeToString(c.wallet.GetPaymentPubKey().SerializeCompressed()), PeerKey: hex.EncodeToString(c.peer), ChannelID: channelID, OldParentID: stored.RemoteCommitment.CommitTx.TxID(), Amount: amount, WalletFee: fee, RequiredInput: amount + fee, FeeRate: feeRate, FeeConfig: feeCfg, UnsignedTx: unsigned}
	q.QuoteID = closedSingleUTXOQuoteID(*q)
	p.channelIdentityMu.RLock()
	matches := p.closedSingleUTXOIdentityMatches(c)
	p.channelIdentityMu.RUnlock()
	if !matches {
		return nil, nil, nil, fmt.Errorf("wallet, account, peer or network changed; request a new quote")
	}
	return q, tx, fetcher, nil
}

// A failed ordinary pre-Open read may leave this original owner. This is
// not an exception for unknown/rejected requests, drain or funding children.
func closedSingleUTXOPreparedOwnerMayAdopt(owner *LocalActionPerformData, intent *closedChannelIntent) bool {
	if owner == nil || intent == nil {
		return false
	}
	owner.RLock()
	defer owner.RUnlock()
	current, err := loadClosedChannelIntent(owner)
	return err == nil && current.source == intent.source && owner.Status == RS_PERFORM_ACTION_STARTED && len(owner.ActionResvs) == 0 &&
		intent.Choice == "reopen" && intent.Step == "prepared" && intent.TopupJSON == "" &&
		intent.Request == nil && len(intent.RequestSignature) == 0 && intent.Accept == nil && intent.FundingCreated == nil && intent.FundingSigned == nil && intent.FundingBroadcasted == nil &&
		len(intent.FundingSnapshot) == 0 && len(intent.FundingInputsSnapshot) == 0 && len(intent.ChannelSnapshot) == 0 && len(intent.DrainSnapshot) == 0 && intent.L2DrainTxID == ""
}

func (p *Manager) QuoteClosedChannelSingleUTXOTopup(feeRate int64) (string, error) {
	c, err := p.captureClosedSingleUTXOTopup()
	if err != nil {
		return "", err
	}
	q, _, _, err := p.quoteClosedSingleUTXOTopup(c, feeRate)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(q)
	return string(raw), err
}

func closedSingleUTXOTopupResult(record *closedSingleUTXOTopupRecord) string {
	raw, _ := json.Marshal(struct {
		TxID  string `json:"txId"`
		Phase string `json:"phase"`
	}{record.TxID, record.Phase})
	return string(raw)
}

func (p *Manager) ConfirmClosedChannelSingleUTXOTopup(rawQuote string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("wallet manager is unavailable")
	}
	approved, err := parseClosedSingleUTXOQuote(rawQuote)
	if err != nil {
		return "", err
	}
	p.l1SendMu.Lock()
	defer p.l1SendMu.Unlock()
	c, err := p.captureClosedSingleUTXOTopup()
	if err != nil {
		return "", err
	}
	id := c.wallet.GetWalletId()
	if approved.WalletID != id.Id || approved.AccountIndex != id.SubAccountId || approved.Network != c.network ||
		approved.PaymentKey != hex.EncodeToString(c.wallet.GetPaymentPubKey().SerializeCompressed()) || approved.PeerKey != hex.EncodeToString(c.peer) {
		return "", fmt.Errorf("funding approval belongs to a different wallet, account, peer or network")
	}
	// Repeated confirmation resumes only the exact already-approved signed raw.
	for _, item := range p.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != localActionClosedChannelRecovery || !closedIntentWalletMatches(owner, c.wallet) {
			continue
		}
		owner.RLock()
		intent, loadErr := loadClosedChannelIntent(owner)
		owner.RUnlock()
		if loadErr != nil {
			return "", loadErr
		}
		if intent.ChannelID != approved.ChannelID || intent.OldParentID != approved.OldParentID || intent.TopupJSON == "" {
			continue
		}
		var record closedSingleUTXOTopupRecord
		if json.Unmarshal([]byte(intent.TopupJSON), &record) != nil || record.Quote != rawQuote {
			return "", fmt.Errorf("another exact top-up is already pending; do not add BTC again")
		}
		if record.Phase == "confirmed" {
			return closedSingleUTXOTopupResult(&record), nil
		}
		if record.Phase == "approved" {
			// No signed bytes have ever been durably published or sent. The
			// common path revalidates this exact stored unsigned plan only.
			break
		}
		if intent.Choice != "reopen" || (record.Phase != "signed" && record.Phase != "broadcast_inflight" && record.Phase != "broadcast") {
			return "", fmt.Errorf("original top-up choice or phase is invalid")
		}
		signed, decodeErr := DecodeMsgTx(record.SignedRaw)
		if decodeErr != nil {
			return "", decodeErr
		}
		if err := validateClosedSingleUTXOTopupRaw(approved, &record, signed); err != nil {
			return "", err
		}
		if c.l1 == nil {
			return "", closedChannelRecoveryPending(owner, "topup_evidence", nil)
		}
		if observed, lookupErr := c.l1.GetRawTx(record.TxID); lookupErr == nil {
			if observed != record.SignedRaw {
				return "", closedChannelRecoveryPending(owner, "topup_exact_raw", fmt.Errorf("observed transaction differs from the approved signed bytes"))
			}
			return closedSingleUTXOTopupResult(&record), nil
		}
		// Missing/uncertain delivery may be retried only by explicit confirmation
		// of these same bytes, after fresh account, chain and online revalidation.
		revalidated, _, _, recheckErr := p.quoteClosedSingleUTXOTopup(c, approved.FeeRate)
		if recheckErr != nil {
			return "", recheckErr
		}
		current, _ := json.Marshal(revalidated)
		if string(current) != rawQuote {
			return "", fmt.Errorf("original prepared quote changed; no transaction was sent")
		}
		return p.broadcastClosedSingleUTXOTopup(c, owner, intent, &record)
	}
	q, tx, fetcher, err := p.quoteClosedSingleUTXOTopup(c, approved.FeeRate)
	if err != nil {
		return "", err
	}
	current, _ := json.Marshal(q)
	if string(current) != rawQuote {
		return "", fmt.Errorf("quote, selected inputs, amount or fees changed; review and confirm a new quote")
	}
	approvedRecord, _ := json.Marshal(&closedSingleUTXOTopupRecord{Quote: rawQuote, Phase: "approved"})
	owner, intent, err := p.closedReopenIntentForWalletWithInitialTopup(c.wallet, q.ChannelID, q.OldParentID, string(approvedRecord))
	if err != nil {
		return "", err
	}
	if err = p.rejectClosedChannelIntentConflicts(owner, c.wallet); err != nil {
		return "", err
	}
	if intent.OldParentID != q.OldParentID || intent.Request != nil || intent.TopupJSON != string(approvedRecord) || (intent.Step != "prepared" && intent.Step != "open_rejected") {
		return "", closedChannelRecoveryPending(owner, intent.Step, nil)
	}
	if err = p.revalidateClosedSingleUTXOInputs(c, tx, fetcher); err != nil {
		return "", err
	}
	p.channelIdentityMu.RLock()
	if !p.closedSingleUTXOIdentityMatches(c) {
		p.channelIdentityMu.RUnlock()
		return "", fmt.Errorf("account changed before signing; approval cancelled")
	}
	for _, input := range tx.TxIn {
		if p.isL1SendInputProtected(input.PreviousOutPoint.String()) {
			p.channelIdentityMu.RUnlock()
			return "", fmt.Errorf("quoted wallet input is now protected; request a new quote")
		}
	}
	signed, err := SignTxWithWallet(c.wallet, tx, fetcher)
	p.channelIdentityMu.RUnlock()
	if err != nil {
		return "", err
	}
	if err = VerifySignedTx(signed, fetcher); err != nil {
		return "", err
	}
	signedRaw, err := EncodeMsgTx(signed)
	if err != nil {
		return "", err
	}
	record := &closedSingleUTXOTopupRecord{Quote: rawQuote, SignedRaw: signedRaw, TxID: signed.TxID(), Vout: 0, Phase: "signed"}
	encoded, _ := json.Marshal(record)
	intent.TopupJSON = string(encoded)
	if err = p.persistClosedChannelIntent(owner, intent, owner.Status); err != nil {
		return "", err
	}
	return p.broadcastClosedSingleUTXOTopup(c, owner, intent, record)
}

func (p *Manager) revalidateClosedSingleUTXOInputs(c *closedSingleUTXOTopupContext, tx *wire.MsgTx, fetcher *txscript.MultiPrevOutFetcher) error {
	sourceScript, err := GetPkScriptFromAddress(c.wallet.GetAddress())
	if err != nil {
		return err
	}
	if !txscript.IsPayToTaproot(sourceScript) {
		return fmt.Errorf("top-up source must be the ordinary P2TR account wallet")
	}
	seen := make(map[string]bool, len(tx.TxIn))
	inputs := make([]string, 0, len(tx.TxIn))
	for _, input := range tx.TxIn {
		outpoint := input.PreviousOutPoint.String()
		if seen[outpoint] {
			return fmt.Errorf("duplicate quoted wallet input")
		}
		seen[outpoint] = true
		actual, readErr := c.l1.GetTxOutput(outpoint)
		expected := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		if readErr != nil || actual == nil || expected == nil || actual.HasAsset() || actual.Value() != expected.Value ||
			!bytes.Equal(actual.OutValue.PkScript, sourceScript) || !bytes.Equal(actual.OutValue.PkScript, expected.PkScript) {
			return fmt.Errorf("quoted wallet input %s changed or is not plain account BTC", outpoint)
		}
		inputs = append(inputs, outpoint)
	}
	existing, err := c.l1.GetExistingUtxos(inputs)
	if err != nil {
		return err
	}
	found := make(map[string]bool, len(existing))
	for _, outpoint := range existing {
		found[outpoint] = true
	}
	for _, outpoint := range inputs {
		if !found[outpoint] {
			return fmt.Errorf("quoted wallet input %s is no longer unspent", outpoint)
		}
	}
	return nil
}

func (p *Manager) broadcastClosedSingleUTXOTopup(c *closedSingleUTXOTopupContext, owner *LocalActionPerformData, intent *closedChannelIntent, record *closedSingleUTXOTopupRecord) (string, error) {
	tx, err := DecodeMsgTx(record.SignedRaw)
	if err != nil || tx == nil || tx.TxID() != record.TxID {
		return "", fmt.Errorf("persisted top-up transaction is invalid")
	}
	q, err := parseClosedSingleUTXOQuote(record.Quote)
	if err != nil {
		return "", err
	}
	if err = validateClosedSingleUTXOTopupRaw(q, record, tx); err != nil {
		return "", err
	}
	inputs := make([]string, 0, len(tx.TxIn))
	for _, input := range tx.TxIn {
		inputs = append(inputs, input.PreviousOutPoint.String())
	}
	reservationID := fmt.Sprintf("closedchannelrecovery:%d:topup", owner.Id)
	if err = c.locker.TryReserve(inputs, "closed-channel-topup", reservationID); err != nil {
		return "", err
	}
	if err = c.l1.TestRawTx_Bitcoin([]string{record.SignedRaw}); err != nil {
		return "", closedChannelRecoveryPending(owner, "topup_signed", err)
	}
	p.channelIdentityMu.RLock()
	matches := p.closedSingleUTXOIdentityMatches(c) && !p.hasPendingLockWithExpand(q.ChannelID)
	if matches {
		if err = p.checkAccountManagedDataImport(); err != nil {
			matches = false
		}
		for _, input := range tx.TxIn {
			if p.isL1RGBInputProtected(input.PreviousOutPoint.String()) {
				matches = false
			}
		}
	}
	p.channelIdentityMu.RUnlock()
	if !matches {
		return "", closedChannelRecoveryPending(owner, "topup_signed", fmt.Errorf("account, pending lifecycle or asset protection changed; the same signed transaction is retained"))
	}
	record.Phase = "broadcast_inflight"
	encoded, _ := json.Marshal(record)
	intent.TopupJSON = string(encoded)
	if err = p.persistClosedChannelIntent(owner, intent, owner.Status); err != nil {
		return "", err
	}
	txid, broadcastErr := c.l1.BroadCastTx(tx)
	if broadcastErr != nil || txid != record.TxID {
		// An uncertain send is not permission to pick new coins or construct a
		// second top-up. The exact signed raw and owned inputs remain durable.
		return closedSingleUTXOTopupResult(record), nil
	}
	record.Phase = "broadcast"
	encoded, _ = json.Marshal(record)
	intent.TopupJSON = string(encoded)
	if err = p.persistClosedChannelIntent(owner, intent, owner.Status); err != nil {
		return closedSingleUTXOTopupResult(record), nil
	}
	p.wakeChannelHeartbeat()
	return closedSingleUTXOTopupResult(record), nil
}

func validateClosedSingleUTXOTopupRaw(q *closedSingleUTXOTopupQuote, record *closedSingleUTXOTopupRecord, tx *wire.MsgTx) error {
	if record.Phase == "approved" && (record.SignedRaw != "" || record.TxID != "") {
		return fmt.Errorf("unsigned approval unexpectedly contains signed or broadcast material")
	}
	if tx == nil || record.Vout != 0 || len(tx.TxOut) == 0 || tx.TxOut[0].Value != q.Amount || (record.Phase != "approved" && tx.TxID() != record.TxID) {
		return fmt.Errorf("persisted top-up output does not match its approved quote")
	}
	script, err := GetPkScriptFromAddress(q.ChannelID)
	if err != nil {
		return err
	}
	if !bytes.Equal(tx.TxOut[0].PkScript, script) {
		return fmt.Errorf("persisted top-up does not pay the exact channel")
	}
	unsigned := tx.Copy()
	for _, input := range unsigned.TxIn {
		input.Witness = nil
	}
	raw, err := EncodeMsgTx(unsigned)
	if err != nil || raw != q.UnsignedTx {
		return fmt.Errorf("persisted top-up changed its approved unsigned transaction")
	}
	return nil
}

// The existing C1 fresh-funding hooks call this before any drain and again
// before making a fresh Open request. No top-up is ever mixed with old inputs.
func (p *Manager) closedChannelSingleUTXOTopupFunding(w common.Wallet, owner *LocalActionPerformData, intent *closedChannelIntent) (*TxOutput, error) {
	if intent.TopupJSON == "" {
		return nil, nil
	}
	if !closedIntentWalletMatches(owner, w) || intent.Choice != "reopen" {
		return nil, fmt.Errorf("top-up recovery account or choice mismatch")
	}
	var record closedSingleUTXOTopupRecord
	if json.Unmarshal([]byte(intent.TopupJSON), &record) != nil {
		return nil, fmt.Errorf("invalid persisted top-up record")
	}
	q, err := parseClosedSingleUTXOQuote(record.Quote)
	if err != nil {
		return nil, err
	}
	id := w.GetWalletId()
	if q.WalletID != id.Id || q.AccountIndex != id.SubAccountId || q.ChannelID != intent.ChannelID || q.OldParentID != intent.OldParentID || q.PaymentKey != hex.EncodeToString(w.GetPaymentPubKey().SerializeCompressed()) {
		return nil, fmt.Errorf("persisted top-up identity does not match its original owner")
	}
	if record.Phase == "approved" {
		return nil, closedChannelRecoveryPending(owner, "topup_approved", nil)
	}
	tx, err := DecodeMsgTx(record.SignedRaw)
	if err != nil {
		return nil, err
	}
	if err = validateClosedSingleUTXOTopupRaw(q, &record, tx); err != nil {
		return nil, err
	}
	c, err := p.captureClosedSingleUTXOTopup()
	if err != nil {
		return nil, err
	}
	if c.wallet.GetWalletId() != w.GetWalletId() || !c.wallet.GetPaymentPubKey().IsEqual(w.GetPaymentPubKey()) ||
		q.PeerKey != hex.EncodeToString(c.peer) || q.Network != c.network || c.l1 == nil || c.l2 == nil {
		return nil, closedChannelRecoveryPending(owner, "topup_confirmation", fmt.Errorf("captured account, peer or network changed"))
	}
	height, err := c.l1.GetTxHeight(record.TxID)
	if err != nil || height <= 0 {
		return nil, closedChannelRecoveryPending(owner, "topup_confirmation", err)
	}
	raw, err := c.l1.GetRawTx(record.TxID)
	if err != nil || raw != record.SignedRaw {
		return nil, closedChannelRecoveryPending(owner, "topup_exact_raw", err)
	}
	outpoint := fmt.Sprintf("%s:%d", record.TxID, record.Vout)
	existing, err := c.l1.GetExistingUtxos([]string{outpoint})
	if err != nil || len(existing) != 1 || existing[0] != outpoint {
		return nil, closedChannelRecoveryPending(owner, "topup_unspent", err)
	}
	output, err := c.l1.GetTxOutput(outpoint)
	if err != nil || output == nil || output.OutPointStr != outpoint || output.HasAsset() || output.Value() != q.Amount || !bytes.Equal(output.OutValue.PkScript, tx.TxOut[0].PkScript) {
		return nil, closedChannelRecoveryPending(owner, "topup_exact_output", err)
	}
	if _, err = c.l2.GetAscendData(outpoint); err == nil {
		return nil, fmt.Errorf("approved top-up output is already ascended")
	} else if !errors.Is(err, indexer.ErrKeyNotFound) && err.Error() != "not found" && err.Error() != fmt.Sprintf("GetAscendData %s not found", outpoint) {
		return nil, closedChannelRecoveryPending(owner, "topup_ascend_state", err)
	}
	p.channelIdentityMu.RLock()
	matches := p.closedSingleUTXOIdentityMatches(c) && !p.isL1RGBInputProtected(outpoint)
	p.channelIdentityMu.RUnlock()
	if !matches {
		return nil, closedChannelRecoveryPending(owner, "topup_account_or_asset_state", nil)
	}
	if record.Phase != "confirmed" {
		record.Phase = "confirmed"
		encoded, _ := json.Marshal(record)
		intent.TopupJSON = string(encoded)
		if err = p.persistClosedChannelIntent(owner, intent, owner.Status); err != nil {
			return nil, err
		}
	}
	return output, nil
}

// Canonical confirmed close and C2's strict package/signature checker only
// determine the offline hint. This never creates a withdrawal choice or signs.
func (p *Manager) closedSingleUTXOOfflineHint(c *closedSingleUTXOTopupContext, stored *ChannelInDB) (string, error) {
	parentID := stored.RemoteCommitment.CommitTx.TxID()
	if c.l1 == nil || !c.l1.IsTxConfirmed(parentID) {
		return "", fmt.Errorf("exact server closing parent is not confirmed; no BTC may be added")
	}
	height, err := c.l1.GetTxHeight(parentID)
	if err != nil || height <= 0 {
		return "", fmt.Errorf("confirmed server closing height is unavailable")
	}
	raw, err := c.l1.GetRawTx(parentID)
	if err != nil {
		return "", err
	}
	parent, err := DecodeMsgTx(raw)
	if err != nil || parent.TxID() != parentID {
		return "", fmt.Errorf("canonical server closing parent does not match local state")
	}
	if nativeForceClose(&ClosingReservation{Channel: &Channel{ChannelInDB: *stored}}) {
		return "server is offline; local RGB/native recovery is not supported through this withdrawal entry; no BTC was moved", nil
	}
	unavailable := "server is offline; no complete verified local exit package is available; no BTC was moved"
	txs, err := closedWithdrawalPackage(parent, stored.RemoteCommitment)
	if err != nil {
		return unavailable, nil
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, parent)
	for i, tx := range txs {
		if i != 0 {
			for _, input := range tx.TxIn {
				if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
					return unavailable, nil
				}
			}
			if err := VerifySignedTx(tx, fetcher); err != nil {
				return unavailable, nil
			}
		}
		AddTxOutputsToPrevFetcher(fetcher, tx)
	}
	return "server is offline; do not add BTC; you may explicitly broadcast your local Others to retrieve the previously closed channel funds", nil
}

func closedChannelSingleUTXOTopupFeeRate(intent *closedChannelIntent) (int64, error) {
	var record closedSingleUTXOTopupRecord
	if intent == nil || json.Unmarshal([]byte(intent.TopupJSON), &record) != nil {
		return 0, fmt.Errorf("invalid original top-up fee approval")
	}
	quote, err := parseClosedSingleUTXOQuote(record.Quote)
	if err != nil {
		return 0, err
	}
	return quote.FeeRate, nil
}
