package wallet

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// ValidateNativeRecoveryOnlyRequest rejects every authorization that could
// publish a parent transaction. Operation handlers must also reject their
// separate splicing/payment signatures and revocation-secret fields.
func ValidateNativeRecoveryOnlyRequest(request *wwire.CommitSigInfo) error {
	if request == nil || !request.RecoveryOnly || request.RecoveryCommitTxID == "" ||
		len(request.CommitSig) != 0 || len(request.CommitDeAnchorSig) != 0 ||
		len(request.CommitPrevTxSig) != 0 || len(request.CommitNextTxSig) != 0 ||
		len(request.NativeOtherTxs) != 0 || len(request.RGBRecoveryTxs) != 0 {
		return fmt.Errorf("recovery preparation contains parent authorization or incomplete context")
	}
	return nil
}

// NativeCommitmentRecoveryRequest filters an independently constructed remote
// commitment down to its dependent-sweep authorization. Returning nil preserves
// the existing request/response flow for protocols without this capability.
func (p *Manager) NativeCommitmentRecoveryRequest(info *RevocationInfo) (*wwire.CommitSigInfo, error) {
	if _, err := rgbReservationView(info); err != nil {
		return nil, err
	}
	if info == nil || info.Channel == nil || info.Channel.RemoteCommitment == nil {
		return nil, fmt.Errorf("missing remote commitment preparation context")
	}
	if !needsSTPNativeOtherExchange(info.Channel, info.Channel.RemoteCommitment) &&
		!hasSTPRGBClientRecoveryContext(info.Channel, info.Channel.RemoteCommitment) {
		return nil, nil
	}
	if info.OldChannel == nil || info.Channel.RemoteCommitment.CommitTx == nil ||
		info.OldChannel.ChannelId != info.Channel.ChannelId || info.OldChannel.CommitHeight != info.Channel.CommitHeight {
		return nil, fmt.Errorf("missing safe previous channel for native recovery preparation")
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return nil, err
	}
	batch.Close()
	source := info.RemoteCommitInfo.CommitSigInfo
	request := &wwire.CommitSigInfo{
		RecoveryOnly: true, RecoveryCommitTxID: info.Channel.RemoteCommitment.CommitTx.TxID(),
		CommitAssetProofs:    cloneSTPAssetProofs(source.CommitAssetProofs),
		CommitOtherPrevTxSig: source.CommitOtherPrevTxSig, CommitOtherTxSig: source.CommitOtherTxSig,
		CommitRGBRecoveryPrevTxSig: source.CommitRGBRecoveryPrevTxSig,
		CommitRGBRecoveryTxSig:     source.CommitRGBRecoveryTxSig,
	}
	return request, ValidateNativeRecoveryOnlyRequest(request)
}

func cloneSTPRecoveryChannel(channel *Channel) (*Channel, error) {
	if channel == nil {
		return nil, fmt.Errorf("missing recovery channel")
	}
	raw, err := EncodeToBytes(&channel.ChannelInDB)
	if err != nil {
		return nil, err
	}
	var stored ChannelInDB
	if err := DecodeFromBytes(raw, &stored); err != nil {
		return nil, err
	}
	return &Channel{ChannelInDB: stored, PeerRPC: channel.PeerRPC, manager: channel.manager, localWallet: channel.localWallet}, nil
}

// PrepareNativeCommitmentRecovery preserves the legacy Other-only API. New
// transport callers use PrepareNativeCommitmentRecoveryResponse to retain the
// separate RGB package instead of mixing it into Others.
func (p *Manager) PrepareNativeCommitmentRecovery(info *RevocationInfo, request *wwire.CommitSigInfo) ([][]string, error) {
	response, err := p.PrepareNativeCommitmentRecoveryResponse(info, request)
	if err != nil {
		return nil, err
	}
	if len(response.RGBRecoveryTxs) != 0 {
		return nil, fmt.Errorf("RGB recovery requires the separate response API")
	}
	return response.NativeOtherTxs, nil
}

// PrepareNativeCommitmentRecoveryResponse signs only independently rebuilt
// dependent packages. The detached candidate never advances live state and
// this response cannot authorize the parent, funding ancestors or revocation.
func (p *Manager) PrepareNativeCommitmentRecoveryResponse(info *RevocationInfo, request *wwire.CommitSigInfo) (*wwire.CommitSigInfo, error) {
	view, err := rgbReservationView(info)
	if err != nil {
		return nil, err
	}
	if err := ValidateNativeRecoveryOnlyRequest(request); err != nil {
		return nil, err
	}
	if info == nil || info.Channel == nil || info.Channel.CommitHeight < 0 || p.GetWallet() == nil ||
		(!needsSTPNativeOtherExchange(info.Channel, info.Channel.LocalCommitment) &&
			!hasSTPRGBClientRecoveryContext(info.Channel, info.Channel.LocalCommitment)) {
		return nil, fmt.Errorf("channel does not support native recovery preparation")
	}
	channel, err := cloneSTPRecoveryChannel(info.Channel)
	if err != nil {
		return nil, err
	}
	wallet := p.GetWallet()
	if view != nil {
		wallet = channel.LocalWallet()
	}
	secret := wallet.GetCommitSecret(channel.PeerNodeId, uint32(channel.CommitHeight+1))
	if secret == nil {
		return nil, fmt.Errorf("missing next commitment construction key")
	}
	var reveal *secp256k1.PrivateKey
	if len(info.RevealPrivKey) != 0 {
		reveal, _ = btcec.PrivKeyFromBytes(info.RevealPrivKey)
	}
	bootstrap := p.GetBootstrapNodePaymentPubKey()
	keys := DeriveCommitmentKeys(secret.PubKey(), 0, bootstrap, reveal, channel)
	tx, _, _, others, err := p.createCommitTxWithRGBView(0, channel, keys, bootstrap, info.FeeRate, view)
	if err != nil {
		return nil, err
	}
	if tx.TxID() != request.RecoveryCommitTxID || !equalSTPAssetProofs(channel.LocalCommitment.AssetProofs, request.CommitAssetProofs) {
		return nil, fmt.Errorf("recovery preparation differs from the independently constructed commitment")
	}
	if err := p.verifySTPChannelProofsWithRGBView(channel, tx, channel.LocalCommitment.AssetProofs, STPOpCommit, view); err != nil {
		return nil, err
	}
	if err := completeSTPRGBView(view); err != nil {
		return nil, err
	}
	if _, err := p.signAndVerifyCommitmentOthers(channel, tx, nil, others, request.CommitOtherPrevTxSig, request.CommitOtherTxSig); err != nil {
		return nil, err
	}
	if err := p.signAndVerifyRGBClientRecovery(channel, tx, channel.LocalCommitment.RGBClientRecovery,
		request.CommitRGBRecoveryPrevTxSig, request.CommitRGBRecoveryTxSig); err != nil {
		return nil, err
	}
	channel.LocalCommitment.CommitTx, channel.LocalCommitment.Others = tx, others
	packages, err := encodeSTPNativeOtherTransactions(channel)
	if err != nil {
		return nil, err
	}
	rgbPackage, err := encodeRGBClientRecovery(channel, channel.LocalCommitment)
	if err != nil {
		return nil, err
	}
	return &wwire.CommitSigInfo{RecoveryOnly: true, RecoveryCommitTxID: request.RecoveryCommitTxID,
		NativeOtherTxs: packages, RGBRecoveryTxs: rgbPackage}, nil
}

type stpNativePreparedRecoveryRecord struct {
	Version    uint32
	ChannelID  string
	Height     uint64
	CommitTxID string
	OldChannel *ChannelInDB
	Candidate  *ChannelInDB
}

func stpNativePreparedRecoveryKey(channel *Channel) []byte {
	return []byte(fmt.Sprintf("%sstp-native-prepared-recovery-%s-%d", GetDBKeyPrefix(), channel.ChannelId, channel.CommitHeight+1))
}

// AcceptNativeCommitmentRecovery is the durable barrier before the normal
// request can release a parent, splicing, deanchor or revocation signature.
func (p *Manager) AcceptNativeCommitmentRecovery(info *RevocationInfo, response *wwire.CommitSigInfo) error {
	view, err := rgbReservationView(info)
	if err != nil {
		return err
	}
	request, err := p.NativeCommitmentRecoveryRequest(info)
	if err != nil {
		return err
	}
	if request == nil || response == nil || !response.RecoveryOnly || response.RecoveryCommitTxID != request.RecoveryCommitTxID ||
		len(response.CommitSig) != 0 || len(response.CommitDeAnchorSig) != 0 || len(response.CommitPrevTxSig) != 0 ||
		len(response.CommitNextTxSig) != 0 || len(response.CommitOtherPrevTxSig) != 0 || len(response.CommitOtherTxSig) != 0 ||
		len(response.CommitRGBRecoveryPrevTxSig) != 0 || len(response.CommitRGBRecoveryTxSig) != 0 ||
		len(response.CommitAssetProofs) != 0 {
		return fmt.Errorf("invalid native recovery preparation response")
	}
	return p.acceptSTPRecoveryTransactionsWithJournalAndRGBView(info.Channel, response.NativeOtherTxs, response.RGBRecoveryTxs,
		func(candidate *Channel) ([]byte, []byte, error) {
			key := stpNativePreparedRecoveryKey(candidate)
			record := stpNativePreparedRecoveryRecord{Version: 1, ChannelID: candidate.ChannelId,
				Height: uint64(candidate.CommitHeight) + 1, CommitTxID: response.RecoveryCommitTxID,
				OldChannel: &info.OldChannel.ChannelInDB, Candidate: &candidate.ChannelInDB}
			previous, err := p.db.Read(key)
			if err == nil {
				var existing stpNativePreparedRecoveryRecord
				if err := DecodeFromBytes(previous, &existing); err != nil {
					return nil, nil, err
				}
				if existing.Version != record.Version || existing.ChannelID != record.ChannelID || existing.Height != record.Height ||
					existing.CommitTxID != record.CommitTxID || existing.Candidate == nil ||
					existing.Candidate.AssetEntropy != candidate.AssetEntropy || !equalSTPAssetProofs(existing.Candidate.AssetFunding, candidate.AssetFunding) ||
					!equalSTPCommitmentPackage(existing.Candidate.RemoteCommitment, candidate.RemoteCommitment) {
					return nil, nil, fmt.Errorf("cannot replace an unresolved native parent authorization")
				}
				return key, previous, nil
			} else if !errors.Is(err, indexer.ErrKeyNotFound) {
				return nil, nil, err
			}
			raw, err := EncodeToBytes(&record)
			return key, raw, err
		}, view)
}

// Validate the RGB package against a deep detached snapshot, then let the
// existing Other-package writer commit the combined candidate and all journal
// refreshes in one batch. No fully signed child is visible on the live channel
// unless that batch succeeds. The legacy Other validation remains unchanged.
func (p *Manager) acceptSTPRecoveryTransactionsWithJournal(channel *Channel, packages [][]string, rgbPackage []string,
	prepare func(*Channel) ([]byte, []byte, error)) error {
	return p.acceptSTPRecoveryTransactionsWithJournalAndRGBView(channel, packages, rgbPackage, prepare, nil)
}

func (p *Manager) acceptSTPRecoveryTransactionsWithJournalAndRGBView(channel *Channel, packages [][]string, rgbPackage []string,
	prepare func(*Channel) ([]byte, []byte, error), view *stpRGBEvidenceView) error {
	if channel == nil || channel.RemoteCommitment == nil {
		return fmt.Errorf("missing remote commitment recovery context")
	}
	if !hasSTPRGBClientRecoveryContext(channel, channel.RemoteCommitment) &&
		channel.RemoteCommitment.RGBClientRecovery == nil && len(rgbPackage) == 0 {
		return p.acceptSTPNativeOtherTransactionsWithJournalAndRGBView(channel, packages, prepare, view)
	}
	candidate, err := cloneSTPRecoveryChannel(channel)
	if err != nil {
		return err
	}
	rgbRecovery, err := validateRGBClientRecovery(candidate, rgbPackage)
	if err != nil {
		return err
	}
	candidate.RemoteCommitment.RGBClientRecovery = rgbRecovery
	if err := p.acceptSTPNativeOtherTransactionsWithJournalAndRGBView(candidate, packages, prepare, view); err != nil {
		return err
	}
	channel.RemoteCommitment.Others = candidate.RemoteCommitment.Others
	channel.RemoteCommitment.RGBClientRecovery = cloneRGBClientRecovery(candidate.RemoteCommitment.RGBClientRecovery)
	return nil
}
