package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

// NativeChannelSource is an authenticated, local recovery record. It is never
// a Bitcoin-indexer balance: every use revalidates the complete native history
// and the actual Bitcoin carrier against current chain evidence.
type NativeChannelSource struct {
	Version        uint32    `json:"version"`
	ChannelID      string    `json:"channel_id"`
	Output         *TxOutput `json:"output"`
	PublicProof    []byte    `json:"public_proof"`
	DescendTxIDs   []string  `json:"descend_txids,omitempty"`
	PrivateWitness []byte    `json:"private_witness,omitempty"`
}

// Serializes immutable-source/monotonic-receipt checks with their local writes.
// No network work is performed while this mutex is held.
var nativeChannelInventoryMu sync.Mutex

func nativeChannelInventoryPrefix(channel string) []byte {
	h := sha256.Sum256([]byte(channel))
	return []byte(GetDBKeyPrefix() + "native-channel-source/" + _chain + "/" + hex.EncodeToString(h[:]) + "/")
}

func nativeChannelInventoryKey(channel, outpoint string) []byte {
	return append(nativeChannelInventoryPrefix(channel), []byte(outpoint)...)
}

func (p *Manager) validateNativeChannelSource(ctx context.Context, local common.Wallet,
	channelID string, source *NativeChannelSource, mode nativeproof.Mode) (*nativeproof.Result, error) {
	if source == nil || source.Version != 1 || source.ChannelID != channelID || source.Output == nil ||
		len(source.Output.Assets) != 1 || source.Output.Assets[0].Name.Protocol != rgb11wallet.Protocol ||
		source.Output.Assets[0].BindingSat != 0 || len(source.PublicProof) == 0 || local == nil {
		return nil, fmt.Errorf("invalid native channel source")
	}
	witness, _, err := p.nativeChannelWitness(local, channelID)
	if err != nil {
		return nil, err
	}
	script, err := utils.WitnessScriptHash(witness)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(script, source.Output.OutValue.PkScript) {
		return nil, fmt.Errorf("native source does not belong to fixed channel owner")
	}
	rgb, err := p.stpRGB11Scope(local)
	if err != nil {
		return nil, err
	}
	result, err := nativeproof.Validate(source.PublicProof, nativeproof.Source{
		Outpoint: source.Output.OutPointStr, Value: source.Output.Value(),
		PkScript: script, Assets: source.Output.Assets, BitcoinNetwork: GetChainParam().Name,
	}, rgb11wallet.NativeBitcoinResolver(ctx, rgb.evidence), mode)
	if err != nil {
		return nil, err
	}
	if result.Value != source.Output.Value() {
		return nil, fmt.Errorf("native source must preserve its complete Bitcoin carrier")
	}
	// A source can only be treated as a single-asset carrier when none of its
	// exact consumed ancestors or terminal carriers is known to contain another
	// native asset. Checking just the final output loses this information after
	// a transfer consumes a mixed carrier.
	points := make(map[string]bool)
	for _, state := range result.History.HistoricalStates {
		point := wire.OutPoint{Hash: state.Outpoint.TxID, Index: state.Outpoint.Vout}
		points[point.String()] = true
	}
	proofs, err := rgb.projectionStore.ListProofs()
	if err != nil {
		return nil, err
	}
	for _, proof := range proofs {
		if proof != nil && points[proof.OutPoint] && proof.AssetName != result.Asset.Name {
			return nil, fmt.Errorf("native channel history contains a known co-asset")
		}
	}
	terminalPoint, err := wire.NewOutPointFromString(source.Output.OutPointStr)
	if err != nil {
		return nil, err
	}
	for point := range points {
		historicalPoint, err := wire.NewOutPointFromString(point)
		if err != nil {
			return nil, err
		}
		if mode == nativeproof.Prepared && historicalPoint.Hash == terminalPoint.Hash {
			// The signed candidate has not been broadcast. Indexed co-assets
			// can only exist on its consumed ancestors; those are checked.
			continue
		}
		output, err := p.l1IndexerClient.GetTxOutput(point)
		if err != nil {
			// Historical RGB validation proves the exact consuming witness.
			// A UTXO-only indexer may report a spent output using its own error
			// vocabulary. Accept absence only after independently observing that
			// exact Bitcoin spend, never on a missing row or RPC failure alone.
			spent, evidenceErr := rgb.evidence.GetOutspend(point)
			if evidenceErr == nil && spent != nil && spent.Spent &&
				rgb11KnownObservedChannelSpend(rgb.evidence, point, spent.SpendingTx) {
				continue
			}
			return nil, fmt.Errorf("native history co-asset evidence unavailable: %w", err)
		}
		if output != nil && len(output.Assets) != 0 {
			return nil, fmt.Errorf("native channel history contains indexed co-assets")
		}
	}
	return result, nil
}

// RegisterNativeChannelSettlementSource records material only after full
// current-chain validation. A source's proof and carrier cannot be replaced;
// settlement receipts may only be appended, retaining their exact prefix.
func (p *Manager) RegisterNativeChannelSettlementSource(ctx context.Context, local common.Wallet,
	channelID string, source *NativeChannelSource) error {
	if _, err := p.validateNativeChannelSource(ctx, local, channelID, source, nativeproof.Current); err != nil {
		return err
	}
	if len(source.PrivateWitness) != 0 || len(source.DescendTxIDs) != 0 {
		if err := p.ValidateNativeChannelReceipt(ctx, local, channelID, source); err != nil {
			return err
		}
	}
	if err := p.saveNativeChannelSource(source); err != nil {
		return err
	}
	_, err := runRGB11ManagedOperationWithManager(p, ctx, rgb11ManagedOperationContinue,
		func() (*rgb11Manager, error) { return p.stpRGB11Scope(local) },
		func(scope *rgb11Manager) (*RGB11ImportResult, error) {
			return scope.importObservedNativePoolTransfer(ctx, source)
		})
	return err
}

func (p *Manager) saveNativeChannelSource(source *NativeChannelSource) error {
	if p == nil || p.GetDB() == nil {
		return fmt.Errorf("native channel inventory database unavailable")
	}
	seen := make(map[string]bool)
	for _, id := range source.DescendTxIDs {
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != id || seen[id] {
			return fmt.Errorf("invalid or duplicate native settlement receipt")
		}
		seen[id] = true
	}
	nativeChannelInventoryMu.Lock()
	defer nativeChannelInventoryMu.Unlock()
	key := nativeChannelInventoryKey(source.ChannelID, source.Output.OutPointStr)
	raw, err := p.GetDB().Read(key)
	if err == nil {
		var existing NativeChannelSource
		if json.Unmarshal(raw, &existing) != nil || existing.Output == nil {
			return fmt.Errorf("invalid persisted native channel source")
		}
		oldSource, newSource := existing, *source
		oldSource.DescendTxIDs, newSource.DescendTxIDs = nil, nil
		oldRaw, _ := json.Marshal(oldSource)
		newRaw, _ := json.Marshal(newSource)
		if !bytes.Equal(oldRaw, newRaw) || len(source.DescendTxIDs) < len(existing.DescendTxIDs) {
			return fmt.Errorf("native channel source is immutable")
		}
		for i, id := range existing.DescendTxIDs {
			if source.DescendTxIDs[i] != id {
				return fmt.Errorf("native settlement receipts must retain their exact prefix")
			}
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	raw, err = json.Marshal(source)
	if err != nil {
		return err
	}
	return p.GetDB().Write(key, raw)
}

func (p *Manager) loadNativeChannelSource(channelID, outpoint string) (*NativeChannelSource, error) {
	raw, err := p.GetDB().Read(nativeChannelInventoryKey(channelID, outpoint))
	if err != nil {
		return nil, err
	}
	var source NativeChannelSource
	if err := json.Unmarshal(raw, &source); err != nil || source.Version != 1 ||
		source.ChannelID != channelID || source.Output == nil || source.Output.OutPointStr != outpoint {
		return nil, fmt.Errorf("invalid persisted native channel source")
	}
	return &source, nil
}

func (p *Manager) ListNativeChannelSources(ctx context.Context, local common.Wallet,
	channelID string, asset *indexer.AssetName) ([]*TxOutput, error) {
	if asset == nil || asset.Protocol != rgb11wallet.Protocol || local == nil {
		return nil, fmt.Errorf("invalid native channel inventory query")
	}
	var records []*NativeChannelSource
	err := p.GetDB().BatchRead(nativeChannelInventoryPrefix(channelID), false, func(_, raw []byte) error {
		var record NativeChannelSource
		if err := json.Unmarshal(raw, &record); err != nil || record.Output == nil || len(record.Output.Assets) != 1 {
			return fmt.Errorf("invalid native channel inventory record")
		}
		if record.Output.Assets[0].Name == *asset {
			records = append(records, &record)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	rgb, err := p.stpRGB11Scope(local)
	if err != nil {
		return nil, err
	}
	result := make([]*TxOutput, 0, len(records))
	for _, record := range records {
		spent, err := rgb.evidence.GetOutspend(record.Output.OutPointStr)
		if err != nil || spent == nil {
			return nil, fmt.Errorf("native carrier status unavailable: %w", err)
		}
		if spent.Spent {
			continue
		}
		if _, err := p.validateNativeChannelSource(ctx, local, channelID, record, nativeproof.Current); err != nil {
			return nil, err
		}
		if len(record.PrivateWitness) != 0 || len(record.DescendTxIDs) != 0 {
			if err := p.ValidateNativeChannelReceipt(ctx, local, channelID, record); err != nil {
				return nil, err
			}
		}
		result = append(result, record.Output.Clone())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].OutPointStr < result[j].OutPointStr })
	return result, nil
}
