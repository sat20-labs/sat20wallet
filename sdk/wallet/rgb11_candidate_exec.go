package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A candidate operation owns the complete local read/build/commit interval.
// Network evidence stays outside the short reservation commit gate. A stale
// local read is safe to rebuild only before any candidate has been published.
type rgb11CandidateKind uint8

const (
	rgb11AcceptCandidate rgb11CandidateKind = iota
	rgb11PrepareCandidate
	rgb11RefreshCandidate
)

type rgb11CandidateOp struct {
	kind            rgb11CandidateKind
	requestID       string
	raw             []byte
	expectedTxID    string
	expectedVout    *uint32
	localTransferID string
}

type rgb11CandidateResult struct {
	current   *RGB11WalletSnapshot
	candidate *RGB11WalletSnapshot
	resolved  *RGB11WalletSnapshot
	receipt   *rgb11wallet.ValidationReceipt
	refresh   *RGB11RefreshResult
}

func (p *rgb11Manager) runRGB11Candidate(ctx context.Context,
	op rgb11CandidateOp) (*rgb11CandidateResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engineStore == nil ||
		p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Keep the original operation and its selected wallet fixed across attempts.
	op.raw = append([]byte(nil), op.raw...)
	if op.expectedVout != nil {
		vout := *op.expectedVout
		op.expectedVout = &vout
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	scope := p.rgb11ScopeKey()
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		actualID, err := p.RGB11WalletID()
		if err != nil {
			return nil, err
		}
		if actualID != walletID || p.rgb11ScopeKey() != scope {
			return nil, errAccountSnapshotChanged
		}
		current, err := p.readRGB11Local(walletID)
		if err != nil {
			return nil, err
		}
		result := &rgb11CandidateResult{current: current}
		var first *rgb11ResolvedRecoveryProjection
		switch op.kind {
		case rgb11AcceptCandidate:
			result.candidate, result.receipt, _, err = p.buildRGB11AcceptedConsignmentCandidateWithID(
				ctx, current, op.requestID, op.raw, op.expectedTxID, op.expectedVout, op.localTransferID)
		case rgb11PrepareCandidate:
			result.candidate, result.receipt, err = p.buildRGB11PreparedReceiveCandidate(
				ctx, current, op.requestID, op.raw, op.expectedTxID, op.expectedVout, op.localTransferID)
			if err == nil {
				if owner := p.accountManagementOwner(); owner != nil {
					var scopeID string
					scopeID, err = p.rgb11AccountManagedScopeID()
					if err == nil {
						_, err = owner.preflightRGB11TerminalRecovery(scopeID, result.candidate)
					}
				}
			}
		case rgb11RefreshCandidate:
			first, err = p.resolveRGB11Local(ctx, current)
			if err == nil {
				var view *rgb11wallet.LocalProjection
				view, err = rgb11wallet.SplitLocalProjection(current.ProjectionRecords, first.faults...)
				if err == nil {
					work := *current
					work.ProjectionRecords = view.Records
					result.candidate, result.refresh, err = p.buildRGB11RefreshCandidate(ctx, &work, time.Now().Unix())
					if err == nil {
						result.candidate.ProjectionRecords, err = view.Merge(result.candidate.ProjectionRecords)
					}
				}
			}
		default:
			return nil, ErrRGB11Inconsistent
		}
		if err != nil {
			if op.kind == rgb11RefreshCandidate {
				if !p.revokeRGB11At(current) {
					continue
				}
				return result, fmt.Errorf("build RGB11 refresh candidate: %w", err)
			}
			return nil, err
		}
		if first != nil && len(first.faults) == 0 && result.candidate != nil && current.Version == result.candidate.Version &&
			!rgb11SnapshotChanged(current, result.candidate) {
			// Only an identical refresh candidate can reuse this attempt's
			// resolution. Publication still rechecks chain evidence and the
			// local baseline before atomically committing the fresh lock plan.
			result.resolved, err = p.publishRGB11CandidateSnapshot(ctx, current, first)
		} else {
			result.resolved, err = p.applyRGB11CandidateSnapshot(ctx, current, result.candidate)
		}
		if errors.Is(err, errRGB11StaleLocal) {
			// Neither the result nor evidence/lock plan from this attempt is usable.
			continue
		}
		if err != nil {
			if op.kind == rgb11RefreshCandidate {
				return result, fmt.Errorf("apply RGB11 refresh candidate: %w", err)
			}
			return nil, err
		}
		return result, nil
	}
	return nil, fmt.Errorf("RGB11 local candidate changed during three attempts: %w", errRGB11StaleLocal)
}
