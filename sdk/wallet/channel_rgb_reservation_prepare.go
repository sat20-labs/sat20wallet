package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// A single-carrier expansion adopts authenticated private funding. A fresh
// reopen can recover its exact confirmed server-main carrier from the existing
// closed backup, without projecting it or counting it in the new channel yet.
func (p *Manager) prepareSTPRGBExpansionEvidence(channel *Channel, asset *indexer.AssetName,
	point string, request []byte) (*STPRGBReservationEvidence, error) {
	if channel == nil {
		return nil, fmt.Errorf("missing RGB evidence channel")
	}
	channel.Mutex.RLock()
	facts, inventories, packages, err := captureSTPRGBEvidence(channel, asset, []string{point}, nil, request, nil, nil)
	owner := channel.LocalWallet()
	if facts != nil {
		facts.manager = p
	}
	channel.Mutex.RUnlock()
	if err != nil || facts == nil {
		return facts, err
	}
	closedFunding, err := p.prepareSTPRGBClosedChannelSource(facts, owner, asset, point)
	if err != nil {
		return nil, err
	}
	if len(closedFunding) != 0 {
		inventories = append(inventories, closedFunding)
	}
	if err := p.querySTPRGBEvidence(facts, inventories, packages); err != nil {
		return nil, err
	}
	return facts, nil
}

// PrepareSTPRGBSplicingEvidence queries only RGB facts before the caller takes
// its original business lock. The short read lock copies query dependencies;
// no business mutation, signing, import, or peer request occurs here.
func (p *Manager) PrepareSTPRGBSplicingEvidence(channel *Channel, asset *indexer.AssetName,
	inputs []string, funding map[string][]byte, request []byte, prior *STPRGBReservationEvidence) (*STPRGBReservationEvidence, error) {
	if channel == nil {
		return nil, fmt.Errorf("missing RGB evidence channel")
	}
	channel.Mutex.RLock()
	facts, inventories, packages, err := captureSTPRGBEvidence(channel, asset, inputs, funding, request, prior, nil)
	owner := channel.LocalWallet()
	if facts != nil {
		facts.manager = p
	}
	channel.Mutex.RUnlock()
	if err != nil || facts == nil {
		return facts, err
	}
	// Client-owned sources are local projection objects. Copy their exact
	// material before querying; peer inventories are already request-owned.
	if asset != nil && asset.Protocol == rgb11wallet.Protocol && len(funding[rgb11wallet.Protocol]) == 0 {
		manager, scopeErr := p.stpRGB11Scope(owner)
		if scopeErr != nil {
			return nil, scopeErr
		}
		var sources []rgb11wallet.ChannelFunding
		for _, point := range inputs {
			output, loadErr := manager.projectionStore.LoadOutput(point)
			if loadErr != nil {
				return nil, loadErr
			}
			source, sourceErr := manager.stpRGB11Funding(output, asset)
			if sourceErr != nil {
				return nil, sourceErr
			}
			sources = append(sources, source)
		}
		if len(sources) != 0 {
			raw, encodeErr := rgb11wallet.EncodeChannelInventory(sources)
			if encodeErr != nil {
				return nil, encodeErr
			}
			inventories = append(inventories, raw)
		}
	}
	if err := p.querySTPRGBEvidence(facts, inventories, packages); err != nil {
		return nil, err
	}
	return facts, nil
}

// Each independent incoming request refreshes chain observations. Only exact
// locally constructed bytes from this same runtime reservation may survive;
// stale status/outspend/UTXO observations never do.
func (p *Manager) PrepareSTPRGBSplicingReservationEvidence(resv *SplicingReservation, request []byte) (*STPRGBReservationEvidence, error) {
	if resv == nil || resv.Channel == nil {
		return nil, fmt.Errorf("missing RGB reservation evidence context")
	}
	channel := resv.Channel
	channel.Mutex.RLock()
	var asset *indexer.AssetName
	if resv.AssetName != nil {
		value := resv.AssetName.AssetName
		asset = &value
	}
	var points []string
	for _, input := range resv.SplicingInputs {
		if input != nil {
			points = append(points, input.OutPointStr)
		}
	}
	facts, inventories, packages, err := captureSTPRGBEvidence(channel, asset, points, resv.AssetFunding, request, resv.RGBEvidence, resv.OldChannel)
	if facts != nil {
		facts.manager = p
		facts.reservation = resv
		facts.reservationID = resv.Id
		facts.reservationStatus = resv.Status
		facts.reservationFunding = append([]byte(nil), resv.AssetFunding[rgb11wallet.Protocol]...)
		// Only an RGB splice constructs a new RGB inventory package. A plain
		// splice retains the prior RGB package, captured and queried above;
		// it must not grant that old package provenance for its new plain tx.
		if asset != nil && asset.Protocol == rgb11wallet.Protocol && resv.SplicingTx != nil {
			if err == nil {
				err = facts.captureConstructedProof(resv.SplicingTx, resv.Channel.AssetFunding[rgb11wallet.Protocol], true)
			}
		}
	}
	channel.Mutex.RUnlock()
	if err != nil || facts == nil {
		return facts, err
	}
	if err := p.querySTPRGBEvidence(facts, inventories, packages); err != nil {
		return nil, err
	}
	return facts, nil
}

// PrepareSTPRGBPaymentReservationEvidence refreshes only the RGB query
// dependencies of one unlock reservation. Mutable chain facts never survive
// a separate request; locally constructed provenance remains phase-local.
func (p *Manager) PrepareSTPRGBPaymentReservationEvidence(resv *PaymentReservation, request []byte) (*STPRGBReservationEvidence, error) {
	if resv == nil || resv.Channel == nil {
		return nil, fmt.Errorf("missing RGB payment evidence context")
	}
	channel := resv.Channel
	channel.Mutex.RLock()
	var asset *indexer.AssetName
	if resv.AssetName != nil {
		value := resv.AssetName.AssetName
		asset = &value
	}
	facts, inventories, packages, err := captureSTPRGBEvidence(channel, asset, nil, nil, request, resv.RGBEvidence, resv.OldChannel)
	if facts != nil {
		facts.manager = p
		facts.paymentReservation = resv
		facts.paymentOldChannel = resv.OldChannel
		facts.reservationID = resv.Id
		facts.reservationStatus = resv.Status
	}
	channel.Mutex.RUnlock()
	if err != nil || facts == nil {
		return facts, err
	}
	if err := p.querySTPRGBEvidence(facts, inventories, packages); err != nil {
		return nil, err
	}
	return facts, nil
}

func captureSTPRGBEvidence(channel *Channel, asset *indexer.AssetName, inputs []string,
	funding map[string][]byte, request []byte, prior *STPRGBReservationEvidence, old *Channel) (*STPRGBReservationEvidence, [][]byte, [][]byte, error) {
	hasRGB := asset != nil && asset.Protocol == rgb11wallet.Protocol
	hasRGB = hasRGB || len(channel.AssetFunding[rgb11wallet.Protocol]) != 0 || len(funding[rgb11wallet.Protocol]) != 0
	if !hasRGB {
		for _, commit := range []*ChannelCommitment{channel.LocalCommitment, channel.RemoteCommitment} {
			if commit != nil && len(commit.AssetProofs[rgb11wallet.Protocol]) != 0 {
				hasRGB = true
			}
		}
	}
	if !hasRGB {
		return nil, nil, nil, nil
	}
	owner := channel.LocalWallet()
	if owner == nil || channel.LocalCommitment == nil || channel.RemoteCommitment == nil {
		return nil, nil, nil, fmt.Errorf("missing RGB capture owner or commitments")
	}
	f := newSTPRGBReservationEvidence()
	f.channel, f.channelID, f.network = channel, channel.ChannelId, GetChainParam().Name
	f.manager = channel.manager
	f.height, f.entropy = uint64(channel.CommitHeight)+1, channel.AssetEntropy
	f.capturedStatus, f.capturedHeight, f.capturedResvID = channel.Status, channel.CommitHeight, channel.ResvId
	f.capturedInventory = append([]byte(nil), channel.AssetFunding[rgb11wallet.Protocol]...)
	f.capturedLocalProof = append([]byte(nil), channel.LocalCommitment.AssetProofs[rgb11wallet.Protocol]...)
	f.capturedRemoteProof = append([]byte(nil), channel.RemoteCommitment.AssetProofs[rgb11wallet.Protocol]...)
	f.walletID, f.account = owner.GetId(), owner.GetSubAccount()
	f.paymentKey = append([]byte(nil), owner.GetPaymentPubKey().SerializeCompressed()...)
	f.requestHash, f.selectedInputs = sha256.Sum256(request), append([]string(nil), inputs...)
	f.prepared = make(map[string][]byte)
	// Provenance belongs to this exact reservation, never a different channel.
	if prior != nil {
		if prior.channel != channel || prior.channelID != channel.ChannelId || prior.capturedResvID != channel.ResvId && prior.capturedResvID != 0 {
			return nil, nil, nil, fmt.Errorf("RGB evidence belongs to a different reservation")
		}
		for id, raw := range prior.prepared {
			f.prepared[id] = append([]byte(nil), raw...)
		}
	}
	var inventories, packages [][]byte
	for _, raw := range [][]byte{channel.AssetFunding[rgb11wallet.Protocol], funding[rgb11wallet.Protocol]} {
		if len(raw) != 0 {
			inventories = append(inventories, append([]byte(nil), raw...))
		}
	}
	captureCommit := func(commit *ChannelCommitment) error {
		if commit == nil {
			return nil
		}
		if raw := commit.AssetProofs[rgb11wallet.Protocol]; len(raw) != 0 {
			packages = append(packages, append([]byte(nil), raw...))
			if err := f.captureConstructedProof(commit.CommitTx, raw, false); err != nil {
				return err
			}
		}
		if raw := commit.RecoveryProofs[rgb11wallet.Protocol]; len(raw) != 0 {
			packages = append(packages, append([]byte(nil), raw...))
		}
		if child := commit.RGBClientRecovery; child != nil {
			if raw := child.AssetProofs[rgb11wallet.Protocol]; len(raw) != 0 {
				packages = append(packages, append([]byte(nil), raw...))
				if err := f.captureConstructedProof(child.CommitTx, raw, false); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, commit := range []*ChannelCommitment{channel.LocalCommitment, channel.RemoteCommitment} {
		if err := captureCommit(commit); err != nil {
			return nil, nil, nil, err
		}
	}
	remote := channel.RemoteCommitment
	if old != nil {
		remote = old.RemoteCommitment
		if err := captureCommit(old.LocalCommitment); err != nil {
			return nil, nil, nil, err
		}
		if err := captureCommit(old.RemoteCommitment); err != nil {
			return nil, nil, nil, err
		}
		if raw := old.AssetFunding[rgb11wallet.Protocol]; len(raw) != 0 {
			inventories = append(inventories, append([]byte(nil), raw...))
		}
	}
	if remote != nil && remote.CommitTx != nil {
		f.oldRemoteTxID = remote.CommitTx.TxID()
		f.oldRemoteProof = append([]byte(nil), remote.AssetProofs[rgb11wallet.Protocol]...)
	}
	return f, inventories, packages, nil
}

// Persisted commitment bytes are previously constructed, signed obligations,
// not arbitrary absent chain transactions. Match their canonical final tx and
// exact channel context before preserving their local Prepared provenance.
func (f *STPRGBReservationEvidence) captureConstructedProof(tx *wire.MsgTx, raw []byte, inventory bool) error {
	if len(raw) == 0 || tx == nil {
		return nil
	}
	if inventory {
		inv, err := rgb11wallet.DecodeChannelInventory(raw)
		if err != nil {
			return err
		}
		raw = inv.LastPackage
		if len(raw) == 0 {
			return nil
		}
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(raw)
	if err != nil {
		return err
	}
	unsigned := tx.Copy()
	for _, input := range unsigned.TxIn {
		input.Witness = nil
	}
	var encoded bytes.Buffer
	if err := unsigned.Serialize(&encoded); err != nil {
		return err
	}
	if !bytes.Equal(encoded.Bytes(), pkg.Steps[len(pkg.Steps)-1].Tx) {
		return fmt.Errorf("stored RGB obligation changed its canonical parent")
	}
	for _, step := range pkg.Steps {
		if step.Context.ChannelID != f.channelID || step.Context.BitcoinNetwork != f.network || step.Context.Entropy != f.entropy {
			return fmt.Errorf("stored RGB obligation changed its captured channel context")
		}
	}
	// Matching the final transaction does not establish local provenance
	// for its entire history. Each ancestor needs independent capture or
	// positive chain facts, even when its RGB history is valid.
	f.prepared[unsigned.TxID()] = append([]byte(nil), encoded.Bytes()...)
	return nil
}

func (p *Manager) querySTPRGBEvidence(f *STPRGBReservationEvidence, inventories, packages [][]byte) error {
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return err
	}
	txids, points := make(map[string]bool), make(map[string]bool)
	type material struct {
		raw     []byte
		reveals [][]byte
	}
	var materials []material
	addSource := func(source rgb11wallet.ChannelFunding) {
		points[source.OutPoint] = true
		materials = append(materials, material{source.Consignment, source.Reveals})
	}
	for _, raw := range inventories {
		inv, err := rgb11wallet.DecodeChannelInventory(raw)
		if err != nil {
			return err
		}
		for _, source := range inv.Sources {
			addSource(source)
		}
		if len(inv.LastPackage) != 0 {
			packages = append(packages, inv.LastPackage)
		}
	}
	for _, raw := range packages {
		pkg, err := rgb11wallet.DecodeChannelPackage(raw)
		if err != nil {
			return err
		}
		for _, source := range pkg.Funding {
			addSource(source)
		}
		for _, step := range pkg.Steps {
			for _, source := range step.Sources {
				addSource(source)
			}
			materials = append(materials, material{step.Consignment, step.Reveals})
		}
	}
	seen := make(map[[32]byte]bool)
	for _, m := range materials {
		// Discovery validates material history, not current wallet balances.
		key := sha256.Sum256(append(append([]byte(nil), m.raw...), bytes.Join(m.reveals, nil)...))
		if seen[key] {
			continue
		}
		seen[key] = true
		var reveals []seals.GraphBlindSeal
		for _, raw := range m.reveals {
			seal, err := seals.DecodeGraphBlindSeal(raw)
			if err != nil {
				return err
			}
			reveals = append(reveals, seal)
		}
		validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...)
		report, err := validator.ValidatePreparedHistoryConsignmentReport(context.Background(), m.raw, evidence)
		if err != nil {
			return err
		}
		for _, allocation := range report.HistoricalAllocations {
			points[allocation.Allocation.OutPoint] = true
			if allocation.Allocation.WitnessTxID != "" {
				txids[allocation.Allocation.WitnessTxID] = true
			}
		}
		for _, allocation := range report.Allocations {
			points[allocation.OutPoint] = true
			if allocation.WitnessTxID != "" {
				txids[allocation.WitnessTxID] = true
			}
		}
		for _, edge := range report.Consumers {
			if edge.WitnessTxID != "" {
				txids[edge.WitnessTxID] = true
			}
		}
		for _, id := range report.WitnessTxIDs {
			txids[id] = true
		}
	}
	for point := range points {
		parsed, err := wire.NewOutPointFromString(point)
		if err != nil {
			return err
		}
		txids[parsed.Hash.String()] = true
		value, queryErr := evidence.GetOutspend(point)
		if value != nil {
			copy := *value
			value = &copy
		}
		f.outspends[point] = stpRGBOutspendFact{value: value, err: queryErr}
		utxo, utxoErr := evidence.GetUTXO(point)
		if utxo != nil {
			copy := *utxo
			copy.PkScript = append([]byte(nil), utxo.PkScript...)
			utxo = &copy
		}
		f.utxos[point] = stpRGBUTXOFact{value: utxo, err: utxoErr}
	}
	for id := range txids {
		raw, queryErr := evidence.GetRawTx(id)
		f.rawTxs[id] = stpRGBRawFact{raw: append([]byte(nil), raw...), err: queryErr}
		status, statusErr := evidence.GetTxStatus(id)
		if status != nil {
			copy := *status
			status = &copy
		}
		f.statuses[id] = stpRGBStatusFact{value: status, err: statusErr}
	}
	// Check the complete query closure using only copied facts. Core can
	// tolerate an embedded Prepared witness after a resolver error; sticky
	// missing must reject that case before the business phase can sign.
	view := f.useView()
	for _, m := range materials {
		var reveals []seals.GraphBlindSeal
		for _, raw := range m.reveals {
			seal, err := seals.DecodeGraphBlindSeal(raw)
			if err != nil {
				return err
			}
			reveals = append(reveals, seal)
		}
		validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...)
		if _, err := validator.ValidatePreparedHistoryConsignmentReport(context.Background(), m.raw, view); err != nil {
			return err
		}
		if err := view.complete(); err != nil {
			return err
		}
	}
	return view.complete()
}
