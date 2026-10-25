package wallet

import (
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
)

// LoadClosedChannelForReopen reads the last closed state without installing a
// runtime, applying recovery, or replacing an unresolved signing obligation.
// Only a missing channel is a first opening; other read errors must fail closed.
func (p *Manager) LoadClosedChannelForReopen(channelID string) (*ChannelInDB, error) {
	if err := p.rejectUnfinishedChannelLifecycle(channelID); err != nil {
		return nil, err
	}
	stored, err := p.LoadChannelInDB(channelID)
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load previous channel %s: %w", channelID, err)
	}
	if stored.ChannelId != channelID || stored.CommitHeight < 0 || stored.CommitHeight == int(^uint(0)>>1) {
		return nil, fmt.Errorf("previous channel %s has invalid identity or commit height", channelID)
	}
	if stored.Status > CS_CLOSED {
		return nil, fmt.Errorf("channel %s is not closed; resume its pending lifecycle", channelID)
	}
	if recovered, err := p.loadSTPNativeRevocationRecovery(stored); err != nil {
		return nil, err
	} else if recovered != nil {
		return nil, fmt.Errorf("channel %s has an unresolved revoked commitment; recover it before reopening", channelID)
	}
	// The normal prepared-recovery loader skips closed channels. Reopening must
	// still check the verified next-height obligation before replacing that state.
	if prepared, err := p.readSTPNativePreparedRecovery(&Channel{ChannelInDB: *stored}); err != nil {
		return nil, err
	} else if prepared != nil {
		return nil, fmt.Errorf("channel %s has an unresolved prepared commitment; recover it before reopening", channelID)
	}
	return stored, nil
}
