package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
)

const (
	rgb11L1MonitorCheckpointVersion = uint32(1)
	rgb11L1VolatileBlockCount       = int64(6)
	rgb11L1MonitorMetadataKey       = "l1-monitor-v1"
)

type RGB11L1ChainPoint struct {
	Height int64  `json:"height"`
	Hash   string `json:"hash"`
}

// RGB11L1ReorgEvent describes the old and new L1 views observed by the
// wallet-local RGB monitor. A nil common ancestor means the fork is deeper
// than the six volatile blocks plus their stable anchor retained locally.
type RGB11L1ReorgEvent struct {
	CommonAncestor *RGB11L1ChainPoint `json:"common_ancestor,omitempty"`
	OldTip         RGB11L1ChainPoint  `json:"old_tip"`
	NewTip         RGB11L1ChainPoint  `json:"new_tip"`
	Deep           bool               `json:"deep"`
}

type rgb11L1MonitorCheckpoint struct {
	Version uint32              `json:"version"`
	Tip     RGB11L1ChainPoint   `json:"tip"`
	Blocks  []RGB11L1ChainPoint `json:"blocks"`
}

func validateRGB11L1MonitorCheckpoint(checkpoint *rgb11L1MonitorCheckpoint) error {
	if checkpoint == nil || checkpoint.Version != rgb11L1MonitorCheckpointVersion ||
		checkpoint.Tip.Height < 0 || checkpoint.Tip.Hash == "" || len(checkpoint.Blocks) == 0 ||
		len(checkpoint.Blocks) > int(rgb11L1VolatileBlockCount+1) {
		return fmt.Errorf("invalid RGB11 L1 monitor checkpoint")
	}
	for index, block := range checkpoint.Blocks {
		if block.Height < 0 || block.Hash == "" {
			return fmt.Errorf("invalid RGB11 L1 monitor block")
		}
		if index > 0 && block.Height != checkpoint.Blocks[index-1].Height+1 {
			return fmt.Errorf("non-contiguous RGB11 L1 monitor checkpoint")
		}
	}
	last := checkpoint.Blocks[len(checkpoint.Blocks)-1]
	if last != checkpoint.Tip {
		return fmt.Errorf("RGB11 L1 monitor tip does not match its block window")
	}
	return nil
}

func encodeRGB11L1MonitorCheckpoint(checkpoint *rgb11L1MonitorCheckpoint) ([]byte, error) {
	if err := validateRGB11L1MonitorCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	return json.Marshal(checkpoint)
}

func decodeRGB11L1MonitorCheckpoint(encoded []byte) (*rgb11L1MonitorCheckpoint, error) {
	var checkpoint rgb11L1MonitorCheckpoint
	if len(encoded) == 0 || json.Unmarshal(encoded, &checkpoint) != nil {
		return nil, fmt.Errorf("decode RGB11 L1 monitor checkpoint")
	}
	if err := validateRGB11L1MonitorCheckpoint(&checkpoint); err != nil {
		return nil, err
	}
	return &checkpoint, nil
}

func buildRGB11L1MonitorCheckpoint(tip RGB11L1ChainPoint,
	hashAt func(int64) (string, error)) (*rgb11L1MonitorCheckpoint, error) {

	if tip.Height < 0 || tip.Hash == "" || hashAt == nil {
		return nil, fmt.Errorf("invalid RGB11 L1 tip")
	}
	start := tip.Height - rgb11L1VolatileBlockCount
	if start < 0 {
		start = 0
	}
	blocks := make([]RGB11L1ChainPoint, 0, tip.Height-start+1)
	for height := start; height <= tip.Height; height++ {
		hash := tip.Hash
		if height != tip.Height {
			var err error
			hash, err = hashAt(height)
			if err != nil {
				return nil, fmt.Errorf("get RGB11 L1 block hash %d: %w", height, err)
			}
		}
		if hash == "" {
			return nil, fmt.Errorf("empty RGB11 L1 block hash at %d", height)
		}
		blocks = append(blocks, RGB11L1ChainPoint{Height: height, Hash: hash})
	}
	latestHash, err := hashAt(tip.Height)
	if err != nil {
		return nil, fmt.Errorf("verify RGB11 L1 tip hash %d: %w", tip.Height, err)
	}
	if latestHash != tip.Hash {
		return nil, fmt.Errorf("RGB11 L1 tip changed while building checkpoint")
	}
	return &rgb11L1MonitorCheckpoint{
		Version: rgb11L1MonitorCheckpointVersion,
		Tip:     tip,
		Blocks:  blocks,
	}, nil
}

func detectRGB11L1Reorg(previous *rgb11L1MonitorCheckpoint, current RGB11L1ChainPoint,
	hashAt func(int64) (string, error)) (*RGB11L1ReorgEvent, error) {

	if err := validateRGB11L1MonitorCheckpoint(previous); err != nil {
		return nil, err
	}
	if current.Height < 0 || current.Hash == "" || hashAt == nil {
		return nil, fmt.Errorf("invalid current RGB11 L1 tip")
	}
	if previous.Tip == current {
		return nil, nil
	}

	var common *RGB11L1ChainPoint
	for index := len(previous.Blocks) - 1; index >= 0; index-- {
		block := previous.Blocks[index]
		if block.Height > current.Height {
			continue
		}
		hash, err := hashAt(block.Height)
		if err != nil {
			return nil, fmt.Errorf("compare RGB11 L1 block hash %d: %w", block.Height, err)
		}
		if hash == block.Hash {
			copy := block
			common = &copy
			break
		}
	}
	if common != nil && *common == previous.Tip {
		return nil, nil
	}
	return &RGB11L1ReorgEvent{
		CommonAncestor: common,
		OldTip:         previous.Tip,
		NewTip:         current,
		Deep:           common == nil,
	}, nil
}

func (p *rgb11Manager) loadRGB11L1MonitorCheckpoint() (*rgb11L1MonitorCheckpoint, error) {
	if p == nil || p.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	encoded, err := p.projectionStore.LoadLocalMetadata(rgb11L1MonitorMetadataKey)
	if err != nil {
		return nil, err
	}
	return decodeRGB11L1MonitorCheckpoint(encoded)
}

func (p *rgb11Manager) saveRGB11L1MonitorCheckpoint(checkpoint *rgb11L1MonitorCheckpoint) error {
	if p == nil || p.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	encoded, err := encodeRGB11L1MonitorCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	return p.projectionStore.SaveLocalMetadata(rgb11L1MonitorMetadataKey, encoded)
}

// handleL1Reorg deliberately reuses the single RGB chain reconciliation path.
// The monitor checkpoint is committed only after this operation succeeds, so a
// crash or transient evidence failure causes the same event to be retried.
func (p *rgb11Manager) handleL1Reorg(ctx context.Context,
	event *RGB11L1ReorgEvent) (*RGB11RefreshResult, error) {

	if p == nil || event == nil || event.OldTip.Hash == "" || event.NewTip.Hash == "" {
		return nil, ErrRGB11Inconsistent
	}
	p.setRGB11ReconciliationState("reorging")
	result, err := p.RefreshRGB11State(ctx)
	if err != nil {
		p.setRGB11ReconciliationState("error")
		p.notifyRGB11ChainUpdate()
		return result, err
	}
	p.setRGB11ReconciliationState("idle")
	p.notifyRGB11ChainUpdate()
	return result, nil
}

func (p *Manager) handleRGB11L1MonitorTick(ctx context.Context) error {
	if p == nil || p.rgbManager == nil || p.l1IndexerClient == nil {
		return nil
	}
	// Capture a fixed wallet/account scope while account switching is excluded,
	// then release the global scope lock before any remote chain query. The
	// scoped manager owns separate projection/engine store instances pinned to
	// the captured scope, so a concurrent wallet/account switch cannot redirect
	// this tick's checkpoint or reconciliation writes into the new scope.
	releaseRGB11Operation := p.beginRGB11Operation()
	if p.wallet == nil || p.rgbManager.projectionStore == nil {
		releaseRGB11Operation()
		return nil
	}
	account, err := p.rgbManager.fixedRGB11ScopeAccount()
	evidence := p.rgbManager.evidence
	releaseRGB11Operation()
	if err != nil {
		return err
	}
	scoped, err := p.newScopedRGB11Manager(account)
	if err != nil {
		return err
	}
	scoped.evidence = evidence
	return scoped.handleL1MonitorTick(ctx)
}

func (p *rgb11Manager) handleL1MonitorTick(ctx context.Context) error {
	if p == nil || p.Manager == nil || p.projectionStore == nil || p.l1IndexerClient == nil {
		return nil
	}
	height := int64(p.l1IndexerClient.GetSyncHeight())
	if height < 0 {
		return nil
	}
	hashAt := func(height int64) (string, error) {
		return p.l1IndexerClient.GetBlockHash(int(height))
	}
	tipHash, err := hashAt(height)
	if err != nil {
		return fmt.Errorf("get RGB11 L1 tip hash at %d: %w", height, err)
	}
	current := RGB11L1ChainPoint{Height: height, Hash: tipHash}

	previous, err := p.loadRGB11L1MonitorCheckpoint()
	if err != nil {
		if !errors.Is(err, indexer.ErrKeyNotFound) {
			// A corrupt device-local checkpoint cannot establish a common ancestor.
			// Reconcile fail-closed, then replace it with a fresh chain window.
			event := &RGB11L1ReorgEvent{OldTip: current, NewTip: current, Deep: true}
			if _, reconcileErr := p.handleL1Reorg(ctx, event); reconcileErr != nil {
				return errors.Join(err, reconcileErr)
			}
		}
		checkpoint, buildErr := buildRGB11L1MonitorCheckpoint(current, hashAt)
		if buildErr != nil {
			return buildErr
		}
		return p.saveRGB11L1MonitorCheckpoint(checkpoint)
	}

	event, err := detectRGB11L1Reorg(previous, current, hashAt)
	if err != nil {
		return err
	}
	if event != nil {
		result, reconcileErr := p.handleL1Reorg(ctx, event)
		if reconcileErr != nil {
			return reconcileErr
		}
		Log.Warningf("RGB11 L1 reorg handled old=%d:%s new=%d:%s deep=%v reorged=%d conflicted=%d",
			event.OldTip.Height, event.OldTip.Hash, event.NewTip.Height, event.NewTip.Hash,
			event.Deep, result.Reorged, result.Conflicted)
	}

	checkpoint, err := buildRGB11L1MonitorCheckpoint(current, hashAt)
	if err != nil {
		return err
	}
	return p.saveRGB11L1MonitorCheckpoint(checkpoint)
}
