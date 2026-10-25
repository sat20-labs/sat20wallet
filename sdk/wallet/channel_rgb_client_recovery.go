package wallet

import (
	"bytes"
	"fmt"
	"reflect"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

const rgbClientRecoveryBranch = "rgb-client-recovery"

func hasSTPRGBClientRecoveryContext(channel *Channel, commitment *ChannelCommitment) bool {
	return channel != nil && len(channel.AssetFunding["rgb11"]) != 0 ||
		commitment != nil && len(commitment.AssetProofs["rgb11"]) != 0
}

func cloneRGBClientRecovery(source *CommitmentTx) *CommitmentTx {
	if source == nil {
		return nil
	}
	copy := *source
	if source.CommitTx != nil {
		copy.CommitTx = source.CommitTx.Copy()
	}
	if source.DeAnchorTx != nil {
		copy.DeAnchorTx = source.DeAnchorTx.Copy()
	}
	copy.PrevTxs, copy.NextTxs = cloneSTPBitcoinChain(source.PrevTxs), cloneSTPBitcoinChain(source.NextTxs)
	copy.AssetProofs, copy.RecoveryProofs = cloneSTPAssetProofs(source.AssetProofs), cloneSTPAssetProofs(source.RecoveryProofs)
	cloneSigs := func(source [][]byte) [][]byte {
		if source == nil {
			return nil
		}
		result := make([][]byte, len(source))
		for i := range source {
			result[i] = append([]byte(nil), source[i]...)
		}
		return result
	}
	copy.CommitSig, copy.DeAnchorSig = cloneSigs(source.CommitSig), cloneSigs(source.DeAnchorSig)
	return &copy
}

func equalRGBClientRecovery(a, b *CommitmentTx) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !equalSTPBitcoinTx(a.CommitTx, b.CommitTx) || !equalSTPBitcoinChain(a.PrevTxs, b.PrevTxs) ||
		!equalSTPBitcoinChain(a.NextTxs, b.NextTxs) || !equalSTPAssetProofs(a.AssetProofs, b.AssetProofs) ||
		!equalSTPAssetProofs(a.RecoveryProofs, b.RecoveryProofs) || !reflect.DeepEqual(a.CommitSig, b.CommitSig) ||
		!reflect.DeepEqual(a.DeAnchorSig, b.DeAnchorSig) {
		return false
	}
	if a.DeAnchorTx == nil || b.DeAnchorTx == nil {
		return a.DeAnchorTx == b.DeAnchorTx
	}
	x, xe := EncodeMsgTx_SatsNet(a.DeAnchorTx)
	y, ye := EncodeMsgTx_SatsNet(b.DeAnchorTx)
	return xe == nil && ye == nil && x == y
}

// Only the explicitly planned RGB client allocations and an identified plain
// fee output can enter this optional package. No legacy protocol sweep is run.
func (p *Manager) createRGBClientRecovery(channel *Channel, parent *wire.MsgTx, plans []stpPlannedAsset,
	feeIndexes []uint32, proofs map[string][]byte, feeRate int64) (*CommitmentTx, error) {
	return p.createRGBClientRecoveryWithView(channel, parent, plans, feeIndexes, proofs, feeRate, nil)
}

func (p *Manager) createRGBClientRecoveryWithView(channel *Channel, parent *wire.MsgTx, plans []stpPlannedAsset,
	feeIndexes []uint32, proofs map[string][]byte, feeRate int64, view *stpRGBEvidenceView) (*CommitmentTx, error) {
	var rgb *stpPlannedAsset
	for i := range plans {
		if plans[i].protocol.Protocol() == "rgb11" {
			rgb = &plans[i]
			break
		}
	}
	if rgb == nil || len(proofs["rgb11"]) == 0 {
		return nil, nil
	}
	inputs := make([]*TxOutput, 0)
	selected := make(map[int]bool)
	for _, allocation := range rgb.plan.RemoteOutputs {
		index := locateCommitAssetOutput(parent, allocation)
		if allocation == nil || allocation.Amt == nil || allocation.Amt.Sign() <= 0 || index < 0 || index >= len(parent.TxOut) ||
			!bytes.Equal(parent.TxOut[index].PkScript, channel.GetChannelPkScript()) || selected[index] {
			return nil, fmt.Errorf("invalid RGB client recovery allocation")
		}
		for _, other := range plans {
			if other.protocol.Protocol() == "rgb11" {
				continue
			}
			for _, constraint := range other.plan.Layout {
				if constraint.Output == parent.TxOut[index] {
					return nil, fmt.Errorf("RGB recovery allocation shares a legacy carrier")
				}
			}
		}
		selected[index] = true
		input := indexer.GenerateTxOutput(parent, index)
		input.Assets = indexer.TxAssets{{Name: allocation.AssetName.AssetName, Amount: *allocation.Amt.Clone(), BindingSat: 0}}
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	consumed := make(map[uint32]bool)
	for _, item := range plans {
		for _, next := range item.plan.NextTxs {
			if next == nil {
				return nil, fmt.Errorf("nil commitment successor")
			}
			for _, tx := range []*wire.MsgTx{next.CommitTx, next.RevealTx} {
				if tx == nil {
					continue
				}
				for _, in := range tx.TxIn {
					if in.PreviousOutPoint.Hash == parent.TxHash() {
						consumed[in.PreviousOutPoint.Index] = true
					}
				}
			}
		}
	}
	var feeInputs []*TxOutput
	for _, index := range feeIndexes {
		if int(index) >= len(parent.TxOut) || selected[int(index)] || consumed[index] {
			continue
		}
		output := parent.TxOut[index]
		if output == nil || !bytes.Equal(output.PkScript, channel.GetChannelPkScript()) {
			continue
		}
		for _, item := range plans {
			for _, constraint := range item.plan.Layout {
				if constraint.Output == output {
					return nil, fmt.Errorf("RGB recovery fee reserve is an asset carrier")
				}
			}
		}
		if len(feeInputs) != 0 {
			return nil, fmt.Errorf("ambiguous RGB recovery fee reserves")
		}
		feeInputs = append(feeInputs, indexer.GenerateTxOutput(parent, int(index)))
	}
	if len(feeInputs) == 0 {
		return nil, fmt.Errorf("RGB client recovery has no explicit plain fee reserve")
	}
	client := channel.LocalChanCfg.PaymentKey
	if !channel.IsInitiator {
		client = channel.RemoteChanCfg.PaymentKey
	}
	recipient, err := GetP2TRpkScript(client)
	if err != nil {
		return nil, err
	}
	candidate := NewChannelCommitment()
	candidate.CommitTx, candidate.AssetProofs = parent, map[string][]byte{"rgb11": append([]byte(nil), proofs["rgb11"]...)}
	plan, err := (rgb11STPProtocol{view: view}).BuildChannelRecovery(p, STPAssetRecoveryRequest{
		Channel: channel, Commitment: candidate, Operation: STPOpForceClose, Branch: rgbClientRecoveryBranch,
		RecipientScript: recipient, CommitmentScript: channel.RedeemScript, ScriptType: SCRIPT_TYPE_CHANNEL,
		Inputs: inputs, FeeInputs: feeInputs, FeeRate: feeRate,
	})
	if err != nil {
		return nil, err
	}
	if plan == nil || plan.NoNativeAssets || plan.Tx == nil || len(plan.NativeProofs["rgb11"]) == 0 {
		return nil, fmt.Errorf("incomplete RGB client recovery")
	}
	return &CommitmentTx{CommitTx: plan.Tx, AssetProofs: cloneSTPAssetProofs(plan.NativeProofs)}, nil
}

// This A1 package is deliberately one RGB transition. Reject an expanded
// chain rather than silently signing successors for which no signature slot
// or independently constructed proof is defined yet.
func rgbClientRecoveryFetcher(channel *Channel, parent *wire.MsgTx, recovery *CommitmentTx) (*txscript.MultiPrevOutFetcher, error) {
	if channel == nil || parent == nil || recovery == nil || recovery.CommitTx == nil ||
		len(recovery.PrevTxs) != 0 || len(recovery.NextTxs) != 0 || len(recovery.AssetProofs) != 1 || len(recovery.AssetProofs["rgb11"]) == 0 {
		return nil, fmt.Errorf("invalid RGB client recovery package")
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, parent)
	seen := make(map[wire.OutPoint]bool)
	if len(recovery.CommitTx.TxIn) == 0 {
		return nil, fmt.Errorf("empty RGB client recovery")
	}
	for _, input := range recovery.CommitTx.TxIn {
		if input == nil || input.PreviousOutPoint.Hash != parent.TxHash() || seen[input.PreviousOutPoint] {
			return nil, fmt.Errorf("RGB client recovery is detached from its parent")
		}
		seen[input.PreviousOutPoint] = true
		previous := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		if previous == nil || !bytes.Equal(previous.PkScript, channel.GetChannelPkScript()) {
			return nil, fmt.Errorf("RGB client recovery changed its channel spending branch")
		}
	}
	return fetcher, nil
}

func partialSignRGBClientRecovery(channel *Channel, parent *wire.MsgTx, recovery *CommitmentTx) ([][][]byte, [][]byte, error) {
	if recovery == nil {
		return nil, nil, nil
	}
	fetcher, err := rgbClientRecoveryFetcher(channel, parent, recovery)
	if err != nil {
		return nil, nil, err
	}
	sigs, err := PartialSignTxWithWallet(channel.LocalWallet(), recovery.CommitTx, fetcher, channel.RedeemScript, false,
		channel.GetRemotePubKey().SerializeCompressed())
	return nil, sigs, err
}

func (p *Manager) signAndVerifyRGBClientRecovery(channel *Channel, parent *wire.MsgTx, recovery *CommitmentTx, prevSigs [][][]byte, sigs [][]byte) error {
	if recovery == nil {
		if len(prevSigs) != 0 || len(sigs) != 0 {
			return fmt.Errorf("unexpected RGB recovery signatures")
		}
		return nil
	}
	if len(prevSigs) != 0 {
		return fmt.Errorf("unexpected RGB recovery predecessor signatures")
	}
	fetcher, err := rgbClientRecoveryFetcher(channel, parent, recovery)
	if err != nil {
		return err
	}
	if len(sigs) != len(recovery.CommitTx.TxIn) {
		return fmt.Errorf("incomplete RGB recovery signatures")
	}
	if _, err := FinalSignTxWithWallet(channel.LocalWallet(), recovery.CommitTx, fetcher, channel.RedeemScript, false,
		channel.GetRemotePubKey().SerializeCompressed(), sigs); err != nil {
		return err
	}
	return VerifySignedTx(recovery.CommitTx, fetcher)
}

func encodeRGBClientRecovery(channel *Channel, commitment *ChannelCommitment) ([]string, error) {
	if commitment == nil || commitment.RGBClientRecovery == nil {
		return nil, nil
	}
	if !hasSTPRGBClientRecoveryContext(channel, commitment) {
		return nil, fmt.Errorf("RGB recovery on a non-RGB commitment")
	}
	fetcher, err := rgbClientRecoveryFetcher(channel, commitment.CommitTx, commitment.RGBClientRecovery)
	if err != nil {
		return nil, err
	}
	if err := VerifySignedTx(commitment.RGBClientRecovery.CommitTx, fetcher); err != nil {
		return nil, err
	}
	raw, err := EncodeMsgTx(commitment.RGBClientRecovery.CommitTx)
	if err != nil {
		return nil, err
	}
	return []string{raw}, nil
}

func validateRGBClientRecovery(channel *Channel, encoded []string) (*CommitmentTx, error) {
	if channel == nil || channel.RemoteCommitment == nil {
		return nil, fmt.Errorf("missing RGB recovery peer context")
	}
	commitment := channel.RemoteCommitment
	if commitment.RGBClientRecovery == nil {
		if len(encoded) != 0 {
			return nil, fmt.Errorf("unexpected RGB recovery package")
		}
		return nil, nil
	}
	if commitment.RGBClientRecovery.CommitTx == nil {
		return nil, fmt.Errorf("RGB recovery is missing its independently built transaction")
	}
	if !hasSTPRGBClientRecoveryContext(channel, commitment) || len(encoded) != 1 {
		return nil, fmt.Errorf("invalid RGB recovery package count")
	}
	copy := cloneRGBClientRecovery(commitment.RGBClientRecovery)
	tx, err := DecodeMsgTx(encoded[0])
	if err != nil || tx.TxHash() != copy.CommitTx.TxHash() {
		return nil, fmt.Errorf("RGB recovery changed its independently built transaction")
	}
	copy.CommitTx = tx
	fetcher, err := rgbClientRecoveryFetcher(channel, commitment.CommitTx, copy)
	if err != nil {
		return nil, err
	}
	if err := VerifySignedTx(tx, fetcher); err != nil {
		return nil, fmt.Errorf("RGB recovery authorization: %w", err)
	}
	return copy, nil
}

// This validates existing bytes only. ReceiveRevocation is the activation gate:
// it has proved the old secret and signed punishment before calling this helper.
// The watchtower repeats the same binding before persisting the immutable record.
func (p *Manager) verifiedPunishRGBClientRecovery(channel *Channel, view *stpRGBEvidenceView) (*wire.MsgTx, error) {
	if channel == nil || channel.RemoteCommitment == nil {
		return nil, fmt.Errorf("missing revoked RGB commitment snapshot")
	}
	old := channel.RemoteCommitment
	if old.RGBClientRecovery == nil {
		return nil, nil
	}
	if !channel.IsInitiator || old.CommitTx == nil || len(old.AssetProofs["rgb11"]) == 0 {
		return nil, fmt.Errorf("RGB punishment recovery is not bound to its old server commitment")
	}
	recovery := old.RGBClientRecovery
	fetcher, err := rgbClientRecoveryFetcher(channel, old.CommitTx, recovery)
	if err != nil {
		return nil, err
	}
	if err := VerifySignedTx(recovery.CommitTx, fetcher); err != nil {
		return nil, fmt.Errorf("revoked RGB client recovery is not fully signed: %w", err)
	}
	protocol := rgb11STPProtocol{view: view}
	if _, err := protocol.VerifyChannelProof(p, channel, recovery.CommitTx, recovery.AssetProofs["rgb11"], STPOpPunish); err != nil {
		return nil, fmt.Errorf("revoked RGB client recovery proof: %w", err)
	}
	if err := completeSTPRGBView(view); err != nil {
		return nil, err
	}
	return recovery.CommitTx, nil
}
