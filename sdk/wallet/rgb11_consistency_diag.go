//go:build rgb11discard

package wallet

import (
	"context"
	"errors"
	"fmt"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type RGB11ConsistencyIssue struct {
	Severity   string `json:"severity"`
	Check      string `json:"check"`
	TransferID string `json:"transfer_id,omitempty"`
	OutPoint   string `json:"outpoint,omitempty"`
	AssetName  string `json:"asset_name,omitempty"`
	Error      string `json:"error"`
}

type RGB11ConsistencyDiagnostic struct {
	CurrentStatus string                  `json:"current_status"`
	OutputCount   int                     `json:"output_count"`
	ProofCount    int                     `json:"proof_count"`
	TransferCount int                     `json:"transfer_count"`
	ExpectedSpend map[string]string       `json:"expected_spend"`
	Issues        []RGB11ConsistencyIssue `json:"issues"`
}

// UnlockRGB11Diag selects the existing wallet scope without rebuilding locks.
// It is maintenance-only and refuses the fallback-wallet path that may persist
// a changed current wallet selection.
func (p *Manager) UnlockRGB11Diag(password string) (int64, error) {
	if p == nil {
		return -1, ErrRGB11Inconsistent
	}
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.wallet != nil {
		info := p.walletInfoMap[p.status.CurrentWallet]
		if info == nil {
			return -1, ErrRGB11Inconsistent
		}
		secret, err := p.loadWalletSecretBytes(info, password)
		zeroBytes(secret)
		if err != nil {
			return -1, errors.New("password is incorrect")
		}
		return p.status.CurrentWallet, nil
	}
	if p.status == nil || p.status.CurrentWallet == 0 || p.walletInfoMap[p.status.CurrentWallet] == nil {
		return -1, errors.New("current wallet selection is unavailable")
	}
	locker := p.utxoLockerL1
	previousStatus := ""
	if p.rgbManager != nil {
		previousStatus = p.rgbManager.consistencyStatus
	}
	p.utxoLockerL1 = nil
	id, err := p.unlockWallet(password)
	p.utxoLockerL1 = locker
	if p.rgbManager != nil {
		p.rgbManager.consistencyStatus = previousStatus
	}
	return id, err
}

func (p *Manager) DiagnoseRGB11Consistency(ctx context.Context) (*RGB11ConsistencyDiagnostic, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil ||
		p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	store := p.rgbManager.projectionStore
	outputs, err := store.ListOutputs()
	if err != nil {
		return nil, err
	}
	proofs, err := store.ListProofs()
	if err != nil {
		return nil, err
	}
	transfers, err := store.ListTransfers()
	if err != nil {
		return nil, err
	}
	diagnostic := &RGB11ConsistencyDiagnostic{
		CurrentStatus: p.GetRGB11ConsistencyStatus(), OutputCount: len(outputs),
		ProofCount: len(proofs), TransferCount: len(transfers),
		ExpectedSpend: make(map[string]string), Issues: make([]RGB11ConsistencyIssue, 0),
	}
	seen := make(map[string]bool)
	add := func(issue RGB11ConsistencyIssue) {
		key := issue.Check + "\x00" + issue.TransferID + "\x00" + issue.OutPoint + "\x00" + issue.AssetName
		if !seen[key] {
			seen[key] = true
			diagnostic.Issues = append(diagnostic.Issues, issue)
		}
	}

	for _, state := range transfers {
		if !rgb11TransferHasExpectedSpend(state) {
			continue
		}
		pending, loadErr := store.LoadPendingTransfer(state.TransferID)
		if loadErr != nil {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "send_journal",
				TransferID: state.TransferID, Error: loadErr.Error()})
			continue
		}
		if validateErr := validateRGB11PendingTransaction(pending); validateErr != nil {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "send_journal",
				TransferID: state.TransferID, Error: validateErr.Error()})
			continue
		}
		for _, outpoint := range state.InputOutPoints {
			if other := diagnostic.ExpectedSpend[outpoint]; other != "" && other != state.WitnessTxID {
				add(RGB11ConsistencyIssue{Severity: "broken", Check: "duplicate_input",
					TransferID: state.TransferID, OutPoint: outpoint,
					Error: fmt.Sprintf("also expected by %s", other)})
				continue
			}
			diagnostic.ExpectedSpend[outpoint] = state.WitnessTxID
		}
		if len(pending.LocalConsignment) == 0 {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "local_consignment",
				TransferID: state.TransferID, Error: "local consignment is absent"})
			continue
		}
		validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
		if _, validateErr := rgb11wallet.ValidateWith(ctx, validator,
			pending.LocalConsignment, p.rgbManager.evidence); validateErr != nil {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "local_consignment",
				TransferID: state.TransferID, Error: validateErr.Error()})
		}
	}

	proofIndex := make(map[string]*rgb11wallet.AllocationProof, len(proofs))
	for _, proof := range proofs {
		if proof == nil {
			continue
		}
		proofIndex[proof.OutPoint+"|"+proof.AssetName.String()] = proof
		if proof.Status == "inconsistent" {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "proof_status",
				OutPoint: proof.OutPoint, AssetName: proof.AssetName.String(), Error: "proof is inconsistent"})
		}
		if consistentErr := store.AssertConsistent(proof.OutPoint, proof.AssetName); consistentErr != nil {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "proof_projection",
				OutPoint: proof.OutPoint, AssetName: proof.AssetName.String(), Error: consistentErr.Error()})
		}
		outspend, spendErr := p.rgbManager.evidence.GetOutspend(proof.OutPoint)
		if spendErr != nil {
			add(RGB11ConsistencyIssue{Severity: "warning", Check: "spend_evidence",
				OutPoint: proof.OutPoint, AssetName: proof.AssetName.String(), Error: spendErr.Error()})
			continue
		}
		if outspend == nil || !outspend.Spent {
			continue
		}
		expected := diagnostic.ExpectedSpend[proof.OutPoint]
		if expected == "" {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "unexpected_spend",
				OutPoint: proof.OutPoint, AssetName: proof.AssetName.String(), Error: "no local send journal owns spender"})
		} else if outspend.SpendingTx == "" || outspend.SpendingTx == "unknown" {
			add(RGB11ConsistencyIssue{Severity: "warning", Check: "unresolved_spend",
				OutPoint: proof.OutPoint, AssetName: proof.AssetName.String(), Error: "spending tx is unknown"})
		} else if outspend.SpendingTx != expected {
			add(RGB11ConsistencyIssue{Severity: "broken", Check: "conflicting_spend",
				OutPoint: proof.OutPoint, AssetName: proof.AssetName.String(),
				Error: fmt.Sprintf("expected %s got %s", expected, outspend.SpendingTx)})
		}
	}
	for _, output := range outputs {
		if output == nil {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.Protocol != rgb11wallet.Protocol {
				continue
			}
			key := output.OutPointStr + "|" + asset.Name.String()
			if proofIndex[key] == nil {
				add(RGB11ConsistencyIssue{Severity: "broken", Check: "missing_proof",
					OutPoint: output.OutPointStr, AssetName: asset.Name.String(), Error: "output has no proof"})
			}
		}
	}
	return diagnostic, nil
}
