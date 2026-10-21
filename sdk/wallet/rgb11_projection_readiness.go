package wallet

// RGB11 projection readiness is deliberately RAM-only. A restarted manager
// starts with no verified scope state, so restored ownership is unavailable
// until fresh Bitcoin evidence has produced and committed a complete candidate.
func (p *rgb11Manager) markRGB11ProjectionUnverified() {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ProjectionVerified = false
	})
}

// markRGB11ProjectionUnavailable keeps the last durable projection intact but
// prevents it from being treated as spendable after fresh verification fails.
func (p *rgb11Manager) markRGB11ProjectionUnavailable() {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ProjectionVerified = false
	})
}

// markRGB11ProjectionVerified may be called only after the complete current
// ownership candidate and its required protections have committed successfully.
// It marks a fresh frontier; stable confirmation eligibility is checked separately.
func (p *rgb11Manager) markRGB11ProjectionVerified() {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.ProjectionVerified = true
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
