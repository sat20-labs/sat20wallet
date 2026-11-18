package wallet

import (
	"errors"
	"fmt"
	"slices"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Only the spent, unconfirmed carrier with no validated RGB consumer has
// this cause. Retain the actual spender identity, not just its error message.
type rgb11MempoolSpend struct{ point, spender string }

func (e *rgb11MempoolSpend) Error() string {
	return fmt.Sprintf("RGB11 allocation %s has an unvalidated mempool spender", e.point)
}

func rgb11BackupFaults(faults []rgb11wallet.ProofFault) error {
	causes := []error{ErrRGB11Inconsistent}
	for _, fault := range faults {
		causes = append(causes, fault)
	}
	return fmt.Errorf("complete RGB11 backup pending: %w", errors.Join(causes...))
}

// A selected import compares complete normalized records and its exact
// diagnostic generation. Lock images are private to this apply/rollback:
// the original fault carriers plus just the rows this apply actually changed.
type rgb11ImportState struct {
	snapshot *RGB11WalletSnapshot
	faults   []rgb11wallet.ProofFault
	locks    map[string]*LockedUtxo
}

type rgb11ImportSwap struct {
	from, to *rgb11ImportState
	undo     bool
}

func (s *rgb11ImportState) matches(faults []rgb11wallet.ProofFault) bool {
	return slices.EqualFunc(s.faults, faults, func(old, live rgb11wallet.ProofFault) bool {
		cause, ok := old.Cause.(*rgb11MempoolSpend)
		return ok && cause != nil && live.Cause == cause &&
			live.OutPoint == old.OutPoint && live.AssetName == old.AssetName
	})
}

func (p *rgb11Manager) readRGB11SelectedBase(walletID string) (*rgb11ImportState, error) {
	if p == nil || p.rgbManager == nil || p.engineStore == nil || p.projectionStore == nil ||
		p.wallet == nil || walletID == "" {
		return nil, ErrRGB11Inconsistent
	}
	release := p.beginRGB11ReservationRead()
	defer release()
	snapshot, _, err := p.exportRGB11WalletSnapshotLocked(walletID)
	if err != nil {
		return nil, err
	}
	// Per-store export already validates both stores; also require all
	// cross-store references. Selected merge checks the full recovery closure.
	if err := rgb11wallet.ValidateWalletSnapshot(snapshot); err != nil {
		return nil, err
	}
	state := &rgb11ImportState{snapshot: snapshot, faults: slices.Clone(p.rgb11ScopeState().ProofFaults)}
	if len(state.faults) == 0 {
		return state, nil
	}
	state.locks = make(map[string]*LockedUtxo, len(state.faults))
	for _, f := range state.faults {
		cause, ok := f.Cause.(*rgb11MempoolSpend)
		if !ok || cause == nil || cause.point != f.OutPoint || cause.spender == "" {
			return nil, rgb11BackupFaults(state.faults)
		}
		state.locks[f.OutPoint] = nil
	}
	if err := p.utxoLockerL1.readRGB11FaultLocks(state.locks); err != nil {
		return nil, err
	}
	return state, nil
}

// Corrupt proof bytes remain in the database. This diagnostic cache retains
// localized validation failures while allowing unrelated valid carriers to work.
func (p *rgb11Manager) noteRGB11ProofFaults(view *rgb11wallet.LocalProjection) {
	if view == nil || len(view.Faults) == 0 {
		return
	}
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		faults := append([]rgb11wallet.ProofFault(nil), state.ProofFaults...)
		seen := make(map[string]bool, len(faults))
		for _, fault := range faults {
			seen[fault.OutPoint] = true
		}
		for _, fault := range view.Faults {
			if !seen[fault.OutPoint] {
				faults = append(faults, fault)
				seen[fault.OutPoint] = true
			}
		}
		state.ProofFaults = faults
	})
}
