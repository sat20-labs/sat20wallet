package wallet

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

// STPAssetChannelProtocol is the optional evidence boundary for native assets.
// Ordinary UTXO protocols need no sidecar. Proof verification returns a
// transaction-local output view only, never imports ordinary wallet balances.
// Bitcoin script authorization remains the responsibility of STP's signers.
type STPAssetChannelProtocol interface {
	VerifyChannelProof(*Manager, *Channel, *wire.MsgTx, []byte, STPAssetOperation) ([]*TxOutput, error)
	BuildChannelRecovery(*Manager, STPAssetRecoveryRequest) (*STPAssetRecoveryPlan, error)
}

// STPAssetFundingProtocol carries proof inventory through the existing funding
// messages. ValidateChannelFunding returns the normalized, merged inventory to
// persist; it must reject hidden known co-carried assets and changed carriers.
type STPAssetFundingProtocol interface {
	PrepareChannelFunding(*Manager, *Channel, *AssetName, []*TxOutput) ([]byte, error)
	ValidateChannelFunding(*Manager, *Channel, *AssetName, []*TxOutput, []byte) ([]byte, error)
}

// STPAssetExpansionFunding authenticates an already confirmed channel carrier.
// Expansion has no new Bitcoin transaction on which BindSplice could attach
// evidence, so the protocol must explicitly bind its full allocation here.
type STPAssetExpansionFunding interface {
	BuildExpansionAnchorProof(*Manager, *Channel, *AssetName, *TxOutput) ([]byte, error)
}

// Native metadata is derived from authenticated genesis material. An indexer's
// absence of a client-validated asset must not be filled with guessed metadata.
type STPAssetFundingMetadata interface {
	TickerFromFunding(*Manager, *indexer.AssetName, []byte) (*indexer.TickerInfo, error)
}

func (p *Manager) GetSTPFundingTickerInfo(asset *indexer.AssetName, funding map[string][]byte) (*indexer.TickerInfo, error) {
	if asset == nil {
		return nil, fmt.Errorf("missing funding asset")
	}
	protocol, err := STPAssetProtocolFor(asset, STPOpSpliceIn)
	if err != nil {
		return nil, err
	}
	if native, ok := protocol.(STPAssetFundingMetadata); ok && len(funding[asset.Protocol]) != 0 {
		info, err := native.TickerFromFunding(p, asset, funding[asset.Protocol])
		if err != nil {
			return nil, err
		}
		if info == nil || info.AssetName != *asset {
			return nil, fmt.Errorf("native funding metadata identity mismatch")
		}
		return info, nil
	}
	info := p.getTickerInfo(asset)
	if info == nil {
		return nil, fmt.Errorf("can't get ticker %s info", asset.String())
	}
	return info, nil
}

// STPAssetHiddenInputVerifier lets client-validated protocols reject a locally
// known native carrier mislabelled as a Bitcoin-only input. Public indexers do
// not establish the absence of an undisclosed native allocation.
type STPAssetHiddenInputVerifier interface {
	VerifyUndisclosedChannelInputs(*Manager, *wire.MsgTx) error
}

// STPAssetJournalProtocol verifies a native lifecycle obligation even when a
// full asset exit leaves the next commitment with Bitcoin-only outputs.
type STPAssetJournalProtocol interface {
	VerifyChannelJournalContext(*Manager, *Channel) error
}

// STPAssetSettlementProtocol imports only outputs independently established by
// the protocol's ordinary current-chain validator. Candidate package validation
// itself is never a wallet ownership receipt.
type STPAssetSettlementProtocol interface {
	ImportChannelSettlement(*Manager, *wire.MsgTx, []byte) error
}

// Prepared funding can leave ordinary wallet change in a confirmed ancestor
// even when the peer publishes the commitment before finishing its RPC reply.
type STPAssetPreparedFundingSettlement interface {
	ImportPreparedChannelFunding(*Manager, *Channel, []*wire.MsgTx) error
}

type STPAssetRecoveryRequest struct {
	Channel          *Channel
	Commitment       *ChannelCommitment
	Operation        STPAssetOperation
	Branch           string
	OutputIndexes    []int
	RecipientScript  []byte
	CommitmentScript []byte
	ScriptType       int
	CSVDelay         uint32
	Height           uint32
	FeeRate          int64
	Inputs           []*TxOutput
	FeeInputs        []*TxOutput
	PrevTxs          []*wire.MsgTx
}

type STPAssetRecoveryPlan struct {
	Tx           *wire.MsgTx
	PrevFetcher  txscript.PrevOutputFetcher
	Fee          int64
	NativeProofs map[string][]byte
	// NoNativeAssets is returned only after validating that none of the
	// selected inputs carries this protocol's state. STP may then use its
	// ordinary Bitcoin recovery constructor for those inputs.
	NoNativeAssets bool
}

func (p *Manager) buildSTPChannelRecovery(req STPAssetRecoveryRequest) (*STPAssetRecoveryPlan, error) {
	if req.Commitment == nil {
		return nil, fmt.Errorf("missing recovery commitment")
	}
	var result *STPAssetRecoveryPlan
	for _, protocol := range stpAssetProtocols.ordered {
		builder, native := protocol.(STPAssetChannelProtocol)
		if !native {
			continue
		}
		proof := req.Commitment.AssetProofs[protocol.Protocol()]
		if len(proof) == 0 {
			for _, balances := range []map[AssetName]*Decimal{req.Commitment.LocalBalance, req.Commitment.RemoteBalance} {
				for asset, amount := range balances {
					if asset.Protocol == protocol.Protocol() && amount.Sign() != 0 {
						return nil, fmt.Errorf("native recovery proof is missing")
					}
				}
			}
			continue
		}
		plan, err := builder.BuildChannelRecovery(p, req)
		if err != nil {
			return nil, err
		}
		if plan == nil {
			return nil, fmt.Errorf("missing native recovery plan")
		}
		if plan.NoNativeAssets {
			continue
		}
		if result != nil {
			return nil, fmt.Errorf("multiple native recovery plans require a combined transaction")
		}
		if plan.Tx == nil || plan.PrevFetcher == nil || len(plan.NativeProofs[protocol.Protocol()]) == 0 {
			return nil, fmt.Errorf("incomplete native recovery plan")
		}
		result = plan
	}
	if result != nil {
		req.Commitment.RecoveryProofs = cloneSTPAssetProofs(result.NativeProofs)
	}
	return result, nil
}

// A reservation's legacy DataInDB does not contain its complete transient
// RevocationInfo. Native assets therefore retain the complete signed candidate
// and private funding context in one independent durable value before a
// signature leaves this node or an older commitment secret is disclosed.
type stpNativeCommitmentRecord struct {
	Version         uint32
	ChannelID       string
	Height          uint64
	Role            string
	AssetFunding    map[string][]byte
	AssetEntropy    [32]byte
	Commitment      *ChannelCommitment
	Channel         *ChannelInDB
	RevokedPrevious bool
}

func stpNativeCommitmentKey(channel *Channel, role string) []byte {
	return []byte(fmt.Sprintf("%sstp-native-commitment-%s-%d-%s", GetDBKeyPrefix(), channel.ChannelId, channel.CommitHeight+1, role))
}

func needsSTPNativeCommitmentJournal(channel *Channel, commitment *ChannelCommitment) bool {
	return (channel != nil && len(channel.AssetFunding) != 0) || (commitment != nil && len(commitment.AssetProofs) != 0)
}

func (p *Manager) verifySTPNativeJournalContext(channel *Channel, commitment *ChannelCommitment) error {
	if channel == nil || commitment == nil {
		return fmt.Errorf("missing native journal context")
	}
	for protocol, inventory := range channel.AssetFunding {
		if len(inventory) == 0 {
			return fmt.Errorf("missing %s native channel inventory", protocol)
		}
		if verifier, ok := stpAssetProtocols.byName[protocol].(STPAssetJournalProtocol); ok {
			if err := verifier.VerifyChannelJournalContext(p, channel); err != nil {
				return err
			}
		} else if len(commitment.AssetProofs[protocol]) == 0 {
			return fmt.Errorf("missing %s native lifecycle verifier", protocol)
		}
	}
	return nil
}

func (p *Manager) persistSTPNativeCommitment(channel *Channel, commitment *ChannelCommitment, role string) error {
	key, value, err := p.prepareSTPNativeCommitmentWrite(channel, commitment, role)
	if err != nil || len(key) == 0 {
		return err
	}
	return p.db.Write(key, value)
}

// Prepare a journal value without publishing it. Callers updating more than
// one recovery snapshot can preserve the same validation and revocation rules
// while committing all of their values in a single database transaction.
func (p *Manager) prepareSTPNativeCommitmentWrite(channel *Channel, commitment *ChannelCommitment, role string) ([]byte, []byte, error) {
	if !needsSTPNativeCommitmentJournal(channel, commitment) {
		return nil, nil, nil
	}
	if p.db == nil || channel == nil || channel.CommitHeight < 0 || commitment == nil || commitment.CommitTx == nil ||
		commitment.DeAnchorTx == nil || (role != "local" && role != "remote") {
		return nil, nil, fmt.Errorf("native channel commitment has no complete durable signing context")
	}
	if err := p.verifySTPNativeJournalContext(channel, commitment); err != nil {
		return nil, nil, err
	}
	record := stpNativeCommitmentRecord{Version: 1, ChannelID: channel.ChannelId,
		Height: uint64(channel.CommitHeight) + 1, Role: role, AssetFunding: cloneSTPAssetProofs(channel.AssetFunding),
		AssetEntropy: channel.AssetEntropy, Commitment: commitment, Channel: &channel.ChannelInDB}
	previous, err := p.db.Read(stpNativeCommitmentKey(channel, role))
	if err == nil {
		var stored stpNativeCommitmentRecord
		if err := DecodeFromBytes(previous, &stored); err != nil {
			return nil, nil, err
		}
		if stored.RevokedPrevious {
			if role != "local" {
				return nil, nil, fmt.Errorf("invalid native revocation journal role")
			}
			if err := p.verifySTPNativeCommitmentPersisted(channel, commitment); err != nil {
				return nil, nil, fmt.Errorf("cannot replace a native commitment after revocation: %w", err)
			}
			record.RevokedPrevious = true
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil, err
	}
	raw, err := EncodeToBytes(&record)
	if err != nil {
		return nil, nil, err
	}
	return stpNativeCommitmentKey(channel, role), raw, nil
}

func (p *Manager) verifySTPNativeCommitmentPersisted(channel *Channel, commitment *ChannelCommitment) error {
	if p.db == nil || channel == nil || commitment == nil || commitment.CommitTx == nil || commitment.DeAnchorTx == nil {
		return fmt.Errorf("native commitment must be durable before revocation")
	}
	raw, err := p.db.Read(stpNativeCommitmentKey(channel, "local"))
	if err != nil {
		return fmt.Errorf("native commitment journal is unavailable: %w", err)
	}
	var record stpNativeCommitmentRecord
	if err := DecodeFromBytes(raw, &record); err != nil {
		return err
	}
	if record.Version != 1 || record.ChannelID != channel.ChannelId || record.Height != uint64(channel.CommitHeight)+1 ||
		record.Role != "local" || record.AssetEntropy != channel.AssetEntropy ||
		!equalSTPAssetProofs(record.AssetFunding, channel.AssetFunding) || record.Commitment == nil ||
		record.Channel == nil || !equalSTPBitcoinChain(record.Channel.NativeParents, channel.NativeParents) ||
		!reflect.DeepEqual(record.Channel.NativePrevouts, channel.NativePrevouts) ||
		record.Commitment.CommitTx == nil || record.Commitment.DeAnchorTx == nil ||
		!equalSTPAssetProofs(record.Commitment.AssetProofs, commitment.AssetProofs) {
		return fmt.Errorf("native commitment journal differs from the verified candidate")
	}
	storedTx, err := EncodeMsgTx(record.Commitment.CommitTx)
	if err != nil {
		return err
	}
	currentTx, err := EncodeMsgTx(commitment.CommitTx)
	if err != nil || storedTx != currentTx {
		return fmt.Errorf("native commitment signatures are not durable")
	}
	storedDeanchor, err := EncodeMsgTx_SatsNet(record.Commitment.DeAnchorTx)
	if err != nil {
		return err
	}
	currentDeanchor, err := EncodeMsgTx_SatsNet(commitment.DeAnchorTx)
	if err != nil || storedDeanchor != currentDeanchor {
		return fmt.Errorf("native commitment deanchor signatures are not durable")
	}
	// The whole recovery package is a signing obligation. Comparing only the
	// main transaction would permit a changed or missing signed other-sweep,
	// next transaction, or proof chain after the old secret was released.
	if !equalSTPCommitmentPackage(record.Commitment, commitment) {
		return fmt.Errorf("native commitment recovery package is not durable")
	}
	return nil
}

func (p *Manager) markSTPNativeRevocationReady(channel *Channel) error {
	if err := p.verifySTPNativeCommitmentPersisted(channel, channel.LocalCommitment); err != nil {
		return err
	}
	key := stpNativeCommitmentKey(channel, "local")
	raw, err := p.db.Read(key)
	if err != nil {
		return err
	}
	var record stpNativeCommitmentRecord
	if err := DecodeFromBytes(raw, &record); err != nil {
		return err
	}
	if record.Channel == nil {
		return fmt.Errorf("native revocation journal has no recoverable funding snapshot")
	}
	record.RevokedPrevious = true
	raw, err = EncodeToBytes(&record)
	if err != nil {
		return err
	}
	return p.db.Write(key, raw)
}

// LoadSTPNativeRevocationRecovery returns a fully authorized newer local candidate
// when this process may already have disclosed the previous commitment secret
// but the main channel record still has the old height. The caller must not
// resume signing with the old channel or mark this recovered snapshot READY;
// its pending reservation must reconcile, or this candidate can be force-closed.
// A normal fully persisted channel has no pending record at height + 1.
func (p *Manager) loadSTPNativeRevocationRecovery(stored *ChannelInDB) (*ChannelInDB, error) {
	if stored == nil || p.db == nil || stored.CommitHeight < 0 {
		return nil, fmt.Errorf("missing native revocation recovery context")
	}
	channel := &Channel{ChannelInDB: *stored}
	raw, err := p.db.Read(stpNativeCommitmentKey(channel, "local"))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record stpNativeCommitmentRecord
	if err := DecodeFromBytes(raw, &record); err != nil {
		return nil, err
	}
	if record.Version != 1 || record.ChannelID != stored.ChannelId || record.Height != uint64(stored.CommitHeight)+1 || record.Role != "local" {
		return nil, fmt.Errorf("native revocation recovery journal has inconsistent context")
	}
	if !record.RevokedPrevious {
		return nil, nil
	}
	if record.Channel == nil || record.Channel.ChannelId != stored.ChannelId || record.Channel.CommitHeight != stored.CommitHeight ||
		record.Commitment == nil || record.Commitment.CommitTx == nil || record.Commitment.DeAnchorTx == nil ||
		!needsSTPNativeCommitmentJournal(&Channel{ChannelInDB: *record.Channel}, record.Commitment) || record.Channel.AssetEntropy != record.AssetEntropy ||
		!equalSTPAssetProofs(record.Channel.AssetFunding, record.AssetFunding) {
		return nil, fmt.Errorf("native revocation recovery journal is incomplete")
	}
	candidate := &Channel{ChannelInDB: *record.Channel}
	candidate.LocalCommitment = record.Commitment
	if err := verifySTPNativeParents(candidate); err != nil {
		return nil, err
	}
	if stored.AssetEntropy != ([32]byte{}) && stored.AssetEntropy != candidate.AssetEntropy {
		return nil, fmt.Errorf("native recovery changed the channel entropy")
	}
	if err := p.verifySTPNativeJournalContext(candidate, candidate.LocalCommitment); err != nil {
		return nil, err
	}
	if err := p.verifySTPNativeCommitmentPersisted(candidate, candidate.LocalCommitment); err != nil {
		return nil, err
	}
	if err := p.verifySTPChannelProofs(candidate, candidate.LocalCommitment.CommitTx, candidate.LocalCommitment.AssetProofs, STPOpForceClose); err != nil {
		return nil, err
	}
	if err := verifySTPNativeRecoveryAuthorization(candidate); err != nil {
		return nil, fmt.Errorf("native recovery candidate Bitcoin authorization: %w", err)
	}
	candidate.CommitHeight++
	candidate.Status = CS_NATIVE_RECOVERY
	return &candidate.ChannelInDB, nil
}

// Normal channel storage keeps the local witness and peer signatures separate.
// Verify their complete authorization on a copy, after checking the immutable
// journal. Neither the persisted package nor the returned exportable instance
// is changed, and recovery never needs a wallet key or a newly made signature.
func verifySTPNativeRecoveryAuthorization(channel *Channel) error {
	commitment := channel.LocalCommitment
	if commitment == nil || commitment.CommitTx == nil || channel.ChanPoint == nil {
		return fmt.Errorf("missing native recovery signing context")
	}
	fetcher := channel.GetCommitmentPrefetchor()
	for _, previous := range commitment.PrevTxs {
		if previous == nil {
			return fmt.Errorf("missing native commitment predecessor")
		}
		for _, input := range previous.TxIn {
			if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
				return fmt.Errorf("native commitment predecessor is missing a verified prevout")
			}
		}
		if err := VerifySignedTx(previous, fetcher); err != nil {
			return fmt.Errorf("native commitment predecessor authorization: %w", err)
		}
		AddTxOutputsToPrevFetcher(fetcher, previous)
	}
	for _, input := range commitment.CommitTx.TxIn {
		if input == nil || fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
			return fmt.Errorf("native recovery is missing a verified prevout")
		}
	}
	tx := commitment.CommitTx.Copy()
	if len(commitment.CommitSig) != 0 {
		if len(commitment.CommitSig) != len(tx.TxIn) || channel.GetLocalPubKey() == nil || channel.GetRemotePubKey() == nil {
			return fmt.Errorf("native recovery has incomplete persisted peer signatures")
		}
		local, peer := channel.GetLocalPubKey().SerializeCompressed(), channel.GetRemotePubKey().SerializeCompressed()
		_, script, err := GetP2WSHscript(local, peer)
		if err != nil {
			return err
		}
		for _, input := range tx.TxIn {
			previous := fetcher.FetchPrevOutput(input.PreviousOutPoint)
			if previous == nil {
				return fmt.Errorf("native recovery is missing prevout %s", input.PreviousOutPoint)
			}
			if bytes.Equal(previous.PkScript, script) && len(input.Witness) != 0 && len(input.Witness) != 4 {
				return fmt.Errorf("native recovery has a malformed local witness")
			}
		}
		if err := FinalTxWithPeerSig(channel.RedeemScript, local, peer, tx, fetcher, commitment.CommitSig); err != nil {
			return err
		}
	}
	return VerifySignedTx(tx, fetcher)
}

func cloneSTPAssetProofs(proofs map[string][]byte) map[string][]byte {
	if len(proofs) == 0 {
		return nil
	}
	copy := make(map[string][]byte, len(proofs))
	for protocol, proof := range proofs {
		copy[protocol] = append([]byte(nil), proof...)
	}
	return copy
}

func stpOutputIndex(tx *wire.MsgTx, output *wire.TxOut) (int, error) {
	for i, candidate := range tx.TxOut {
		if candidate == output {
			return i, nil
		}
	}
	return 0, fmt.Errorf("protocol removed an STP-owned output")
}

func equalSTPAssetProofs(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for protocol, proof := range a {
		if !bytes.Equal(proof, b[protocol]) {
			return false
		}
	}
	return true
}

func equalSTPBitcoinTx(a, b *wire.MsgTx) bool {
	if a == nil || b == nil {
		return a == b
	}
	var x, y bytes.Buffer
	if a.Serialize(&x) != nil || b.Serialize(&y) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

func equalSTPBitcoinChain(a, b []*wire.MsgTx) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalSTPBitcoinTx(a[i], b[i]) {
			return false
		}
	}
	return true
}

func cloneSTPBitcoinChain(txs []*wire.MsgTx) []*wire.MsgTx {
	if len(txs) == 0 {
		return nil
	}
	result := make([]*wire.MsgTx, len(txs))
	for i, tx := range txs {
		if tx != nil {
			result[i] = tx.Copy()
		}
	}
	return result
}

func cloneSTPPrevouts(outputs map[string]*wire.TxOut) map[string]*wire.TxOut {
	if len(outputs) == 0 {
		return nil
	}
	result := make(map[string]*wire.TxOut, len(outputs))
	for key, output := range outputs {
		if output != nil {
			result[key] = wire.NewTxOut(output.Value, append([]byte(nil), output.PkScript...))
		}
	}
	return result
}

func retainSTPNativeParent(channel *Channel, tx *wire.MsgTx, fetcher txscript.PrevOutputFetcher) error {
	if len(channel.AssetFunding) == 0 {
		return nil
	}
	if channel.NativePrevouts == nil {
		channel.NativePrevouts = make(map[string]*wire.TxOut)
	}
	for _, input := range tx.TxIn {
		output := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		if output == nil {
			return fmt.Errorf("missing native funding parent input %s", input.PreviousOutPoint)
		}
		channel.NativePrevouts[input.PreviousOutPoint.String()] = wire.NewTxOut(output.Value, append([]byte(nil), output.PkScript...))
	}
	for _, parent := range channel.NativeParents {
		if parent.TxHash() == tx.TxHash() {
			return nil
		}
	}
	channel.NativeParents = append(channel.NativeParents, tx.Copy())
	return nil
}

func acceptSTPSignedNativeParents(channel *Channel, signed []*wire.MsgTx) error {
	if channel == nil {
		return fmt.Errorf("missing channel for native parents")
	}
	if len(channel.NativeParents) == 0 {
		return nil
	}
	for _, tx := range signed {
		// Legacy expansion includes a nil placeholder when no additional
		// inscription transaction is needed. Required native parents are
		// checked independently below, including every input signature.
		if tx == nil {
			continue
		}
		for i, candidate := range channel.NativeParents {
			if candidate.TxHash() == tx.TxHash() {
				channel.NativeParents[i] = tx.Copy()
			}
		}
	}
	return verifySTPNativeParents(channel)
}

func verifySTPNativeParents(channel *Channel) error {
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, tx := range channel.NativeParents {
		for _, input := range tx.TxIn {
			out := channel.NativePrevouts[input.PreviousOutPoint.String()]
			if out == nil {
				return fmt.Errorf("native parent missing previous output %s", input.PreviousOutPoint)
			}
			fetcher.AddPrevOut(input.PreviousOutPoint, out)
		}
		if err := VerifySignedTx(tx, fetcher); err != nil {
			return fmt.Errorf("native funding parent signatures: %w", err)
		}
		for i := range tx.TxOut {
			output := indexer.GenerateTxOutput(tx, i)
			fetcher.AddPrevOut(*output.OutPoint(), output.TxOut())
		}
	}
	return nil
}

func equalSTPCommitmentPackage(a, b *ChannelCommitment) bool {
	if a == nil || b == nil {
		return a == b
	}
	if !equalSTPBitcoinTx(a.CommitTx, b.CommitTx) ||
		!equalSTPBitcoinChain(a.PrevTxs, b.PrevTxs) ||
		!equalSTPBitcoinChain(a.NextTxs, b.NextTxs) ||
		!equalSTPAssetProofs(a.AssetProofs, b.AssetProofs) ||
		!equalSTPAssetProofs(a.RecoveryProofs, b.RecoveryProofs) ||
		!reflect.DeepEqual(a.RecoveryFeeIndexes, b.RecoveryFeeIndexes) ||
		!reflect.DeepEqual(a.CommitSig, b.CommitSig) ||
		!reflect.DeepEqual(a.DeAnchorSig, b.DeAnchorSig) || len(a.Others) != len(b.Others) {
		return false
	}
	for i, x := range a.Others {
		y := b.Others[i]
		if x == nil || y == nil {
			if x != y {
				return false
			}
			continue
		}
		if !equalSTPBitcoinTx(x.CommitTx, y.CommitTx) ||
			!equalSTPBitcoinChain(x.PrevTxs, y.PrevTxs) ||
			!equalSTPBitcoinChain(x.NextTxs, y.NextTxs) ||
			!equalSTPAssetProofs(x.AssetProofs, y.AssetProofs) ||
			!equalSTPAssetProofs(x.RecoveryProofs, y.RecoveryProofs) ||
			!reflect.DeepEqual(x.CommitSig, y.CommitSig) ||
			!reflect.DeepEqual(x.DeAnchorSig, y.DeAnchorSig) {
			return false
		}
		if x.DeAnchorTx == nil || y.DeAnchorTx == nil {
			if x.DeAnchorTx != y.DeAnchorTx {
				return false
			}
		} else {
			xs, xe := EncodeMsgTx_SatsNet(x.DeAnchorTx)
			ys, ye := EncodeMsgTx_SatsNet(y.DeAnchorTx)
			if xe != nil || ye != nil || xs != ys {
				return false
			}
		}
	}
	return true
}

// PrepareSTPChannelFunding proves selected native carriers before any signed
// request is sent. Inputs receive only the asset view established by that proof.
func (p *Manager) PrepareSTPChannelFunding(channel *Channel, asset *AssetName, inputs []*TxOutput) (map[string][]byte, error) {
	if channel == nil || asset == nil {
		return nil, fmt.Errorf("missing channel funding context")
	}
	protocol, err := STPAssetProtocolFor(&asset.AssetName, STPOpSpliceIn)
	if err != nil {
		return nil, err
	}
	funder, native := protocol.(STPAssetFundingProtocol)
	if !native {
		return nil, nil
	}
	if err := p.requireSTPNativePersistence(); err != nil {
		return nil, err
	}
	candidate, candidateInputs := stpFundingValidationContext(channel, inputs)
	if candidate.AssetEntropy == ([32]byte{}) {
		if _, err := rand.Read(candidate.AssetEntropy[:]); err != nil {
			return nil, err
		}
	}
	proof, err := funder.PrepareChannelFunding(p, candidate, asset, candidateInputs)
	if err != nil {
		return nil, err
	}
	if len(proof) == 0 {
		return nil, fmt.Errorf("missing %s funding proof", protocol.Protocol())
	}
	proof, err = funder.ValidateChannelFunding(p, candidate, asset, candidateInputs, proof)
	if err != nil {
		return nil, err
	}
	if len(proof) == 0 {
		return nil, fmt.Errorf("missing validated %s funding proof", protocol.Protocol())
	}
	if channel.AssetFunding == nil {
		channel.AssetFunding = make(map[string][]byte)
	}
	channel.AssetFunding[protocol.Protocol()] = append([]byte(nil), proof...)
	channel.AssetEntropy = candidate.AssetEntropy
	applySTPValidatedFundingInputs(inputs, candidateInputs)
	return cloneSTPAssetProofs(channel.AssetFunding), nil
}

// Validation may enrich the selected inputs with proven asset allocations.
// Keep those annotations and the new entropy private until every check succeeds.
// The remainder of the channel is read-only protocol context.
func stpFundingValidationContext(channel *Channel, inputs []*TxOutput) (*Channel, []*TxOutput) {
	candidate := &Channel{
		ChannelInDB: channel.ChannelInDB,
		PeerRPC:     channel.PeerRPC,
		manager:     channel.manager,
		localWallet: channel.localWallet,
	}
	candidate.AssetFunding = cloneSTPAssetProofs(channel.AssetFunding)
	cloned := make([]*TxOutput, len(inputs))
	for i, input := range inputs {
		if input != nil {
			cloned[i] = input.Clone()
		}
	}
	return candidate, cloned
}

func applySTPValidatedFundingInputs(inputs, validated []*TxOutput) {
	for i, input := range inputs {
		if input != nil && validated[i] != nil {
			input.Assets = validated[i].Assets
			input.SatBindingMap = validated[i].SatBindingMap
		}
	}
}

// AcceptSTPChannelFunding checks the peer's evidence against independently read
// Bitcoin outputs. Peers cannot supply invented indexer balances in this path.
func (p *Manager) AcceptSTPChannelFunding(channel *Channel, asset *AssetName, inputs []*TxOutput, proofs map[string][]byte, entropy [32]byte) error {
	if channel == nil || asset == nil {
		return fmt.Errorf("missing channel funding context")
	}
	protocol, err := STPAssetProtocolFor(&asset.AssetName, STPOpSpliceIn)
	if err != nil {
		return err
	}
	funder, native := protocol.(STPAssetFundingProtocol)
	if !native {
		if len(proofs) != 0 {
			return fmt.Errorf("unexpected native funding evidence")
		}
		return nil
	}
	if err := p.requireSTPNativePersistence(); err != nil {
		return err
	}
	if entropy == ([32]byte{}) || (channel.AssetEntropy != ([32]byte{}) && channel.AssetEntropy != entropy) {
		return fmt.Errorf("invalid native channel entropy")
	}
	for name, value := range proofs {
		if name != protocol.Protocol() && !bytes.Equal(channel.AssetFunding[name], value) {
			return fmt.Errorf("unexpected %s funding evidence", name)
		}
	}
	candidate, candidateInputs := stpFundingValidationContext(channel, inputs)
	candidate.AssetEntropy = entropy
	proof, err := funder.ValidateChannelFunding(p, candidate, asset, candidateInputs, proofs[protocol.Protocol()])
	if err != nil {
		return err
	}
	if len(proof) == 0 {
		return fmt.Errorf("missing %s funding proof", protocol.Protocol())
	}
	if metadata, ok := protocol.(STPAssetFundingMetadata); ok {
		info, err := metadata.TickerFromFunding(p, &asset.AssetName, proof)
		if err != nil {
			return err
		}
		if info == nil || info.AssetName != asset.AssetName {
			return fmt.Errorf("native funding metadata identity mismatch")
		}
		if err := saveTickerInfo(p.db, info); err != nil {
			return err
		}
		p.mutex.Lock()
		if p.tickerInfoMap == nil {
			p.tickerInfoMap = make(map[string]*indexer.TickerInfo)
		}
		p.tickerInfoMap[info.AssetName.String()] = info
		p.mutex.Unlock()
	}
	if channel.AssetFunding == nil {
		channel.AssetFunding = make(map[string][]byte)
	}
	channel.AssetFunding[protocol.Protocol()] = append([]byte(nil), proof...)
	channel.AssetEntropy = entropy
	applySTPValidatedFundingInputs(inputs, candidateInputs)
	return nil
}

func (p *Manager) verifySTPChannelProofs(channel *Channel, tx *wire.MsgTx, proofs map[string][]byte, operation STPAssetOperation) error {
	if channel == nil || tx == nil {
		return fmt.Errorf("missing channel proof context")
	}
	for _, protocol := range stpAssetProtocols.ordered {
		proof := proofs[protocol.Protocol()]
		verifier, native := protocol.(STPAssetChannelProtocol)
		if !native {
			if len(proof) != 0 {
				return fmt.Errorf("unexpected %s channel proof", protocol.Protocol())
			}
			continue
		}
		if len(proof) != 0 {
			if _, err := verifier.VerifyChannelProof(p, channel, tx, proof, operation); err != nil {
				return err
			}
		} else if channel.HasProtocolAsset(protocol.Protocol()) {
			return fmt.Errorf("missing %s channel transaction proof", protocol.Protocol())
		} else if len(channel.AssetFunding[protocol.Protocol()]) != 0 {
			// A fully exited protocol may leave an empty inventory and its
			// final lifecycle proof. Verify that obligation before accepting
			// a Bitcoin-only successor commitment.
			journal, ok := protocol.(STPAssetJournalProtocol)
			if !ok {
				return fmt.Errorf("missing %s native lifecycle verifier", protocol.Protocol())
			}
			if err := journal.VerifyChannelJournalContext(p, channel); err != nil {
				return err
			}
		}
		if hidden, ok := protocol.(STPAssetHiddenInputVerifier); ok && len(proof) == 0 {
			if err := hidden.VerifyUndisclosedChannelInputs(p, tx); err != nil {
				return err
			}
		}
	}
	for protocol := range proofs {
		if _, ok := stpAssetProtocols.byName[protocol]; !ok {
			return fmt.Errorf("unknown channel proof protocol %s", protocol)
		}
	}
	return nil
}

// VerifySTPChannelProofs is also used by the serving node before co-signing or
// broadcasting an externally requested channel transaction.
func (p *Manager) VerifySTPChannelProofs(channel *Channel, tx *wire.MsgTx, proofs map[string][]byte, operation STPAssetOperation) error {
	return p.verifySTPChannelProofs(channel, tx, proofs, operation)
}

func (p *Manager) ImportSTPChannelSettlement(tx *wire.MsgTx, proofs map[string][]byte) error {
	keys := make([]string, 0, len(proofs))
	for name := range proofs {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		importer, ok := stpAssetProtocols.byName[name].(STPAssetSettlementProtocol)
		if !ok {
			return fmt.Errorf("no %s channel settlement verifier", name)
		}
		if err := importer.ImportChannelSettlement(p, tx, proofs[name]); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) isSTPNativeInputProtected(outpoint string) bool {
	if p == nil || p.db == nil {
		return false
	}
	protected := false
	check := func(channel *ChannelInDB) {
		if channel == nil || len(channel.AssetFunding) == 0 {
			return
		}
		for name, outputs := range channel.FundingUtxos {
			if _, native := stpAssetProtocols.byName[name.Protocol].(STPAssetChannelProtocol); !native {
				continue
			}
			for _, output := range outputs {
				if output.OutPointStr == outpoint {
					protected = true
				}
			}
		}
		for _, parent := range channel.NativeParents {
			if parent == nil {
				continue
			}
			// A signed funding transaction reserves its inputs. Its change and
			// withdrawal outputs become independently spendable after settlement;
			// protecting all parent outputs would lock those funds indefinitely.
			for _, input := range parent.TxIn {
				if input.PreviousOutPoint.String() == outpoint {
					protected = true
				}
			}
		}
	}
	if err := p.db.BatchRead([]byte(GetDBKeyPrefix()+DB_KEY_CHANNEL), false, func(_, raw []byte) error {
		var channel ChannelInDB
		if err := DecodeFromBytes(raw, &channel); err != nil {
			return err
		}
		check(&channel)
		return nil
	}); err != nil {
		return true
	}
	if protected {
		return true
	}
	// A peer may already hold a signature even though the main channel record
	// has not advanced. Its durable candidate protects the same carriers.
	if err := p.db.BatchRead([]byte(GetDBKeyPrefix()+"stp-native-commitment-"), false, func(_, raw []byte) error {
		var record stpNativeCommitmentRecord
		if err := DecodeFromBytes(raw, &record); err != nil {
			return err
		}
		check(record.Channel)
		return nil
	}); err != nil {
		return true
	}
	return protected
}
