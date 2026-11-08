package wallet

import rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"

// RGB11 projection readiness is deliberately RAM-only. A restarted manager
// starts with no verified scope state, so restored ownership is unavailable
// until fresh Bitcoin evidence has produced and committed a complete candidate.
func (p *rgb11Manager) markRGB11ProjectionUnverified() {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ProjectionVerified = false
		state.ReadyCarriers = nil
		state.ProofFaults = nil
	})
}

// markRGB11ProjectionUnavailable keeps the last durable projection intact but
// prevents it from being treated as spendable after fresh verification fails.
func (p *rgb11Manager) markRGB11ProjectionUnavailable() {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ProjectionVerified = false
		state.ReadyCarriers = nil
		state.ProofFaults = nil
	})
}

// markRGB11ProjectionVerified may be called only after the complete current
// ownership candidate and its required protections have committed successfully.
// It marks a fresh frontier; stable confirmation eligibility is checked separately.
func (p *rgb11Manager) markRGB11ProjectionVerified() {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ProjectionVerified = true
		state.ReadyCarriers = nil
		state.ProofFaults = nil
	})
}

func (p *rgb11Manager) rgb11CarrierAvailable(point string) bool {
	state := p.rgb11ScopeState()
	return state.ProjectionVerified || state.ReadyCarriers[point]
}

// Called in the reservation commit completion; maps are immutable thereafter.
func (p *rgb11Manager) publishRGB11Readiness(complete bool, ready map[string]bool, faults []rgb11wallet.ProofFault) {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ProjectionVerified = complete
		state.ReadyCarriers = ready
		state.ProofFaults = append([]rgb11wallet.ProofFault(nil), faults...)
	})
}

func (p *rgb11Manager) rgb11ProjectionAvailable() bool {
	if p == nil || p.scopeStates == nil {
		return false
	}
	scope := p.rgb11ScopeKey()
	if scope == "" {
		return false
	}
	return p.scopeStates.load(scope).ProjectionVerified
}

// rgb11ProjectionStableConfirmed requires a fresh current frontier and the
// existing per-transfer MinConfirmations for every current proof. It adds no
// backup or stable-finality threshold. The read gate keeps proof and transfer
// requirements in one reservation generation. Callers must not already hold
// this gate.
func (p *rgb11Manager) rgb11ProjectionStableConfirmed() bool {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		!p.rgb11ProjectionAvailable() || p.rgb11ReservationOwner() == nil {
		return false
	}
	release := p.beginRGB11ReservationRead()
	defer release()
	if !p.rgb11ProjectionAvailable() {
		return false
	}
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return false
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return false
	}
	requirements := rgb11ProofConfirmationRequirements(transfers)
	for _, proof := range proofs {
		if !rgb11ProofIsCurrent(proof) {
			continue
		}
		if !rgb11ProofIsAvailable(proof, requirements) {
			return false
		}
	}
	return true
}

// Retain only readiness already established in RAM. A cold/restored scope
// cannot acquire readiness merely because the remaining records decode.
func (p *rgb11Manager) noteRGB11ProofFaults(view *rgb11wallet.LocalProjection) {
	if view == nil || len(view.Faults) == 0 {
		return
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(view.Records)
	if err != nil {
		return
	}
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		ready := make(map[string]bool)
		for point, ok := range state.ReadyCarriers {
			if ok && !view.Points[point] {
				ready[point] = true
			}
		}
		if state.ProjectionVerified {
			for _, proof := range proofs {
				ready[proof.OutPoint] = true
			}
		}
		state.ProjectionVerified = false
		state.ReadyCarriers = ready
		faults := append([]rgb11wallet.ProofFault(nil), state.ProofFaults...)
		seen := make(map[string]bool)
		for _, f := range faults {
			seen[f.OutPoint] = true
		}
		for _, f := range view.Faults {
			if !seen[f.OutPoint] {
				faults = append(faults, f)
				seen[f.OutPoint] = true
			}
		}
		state.ProofFaults = faults
	})
}
