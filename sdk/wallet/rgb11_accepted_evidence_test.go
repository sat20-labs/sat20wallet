package wallet

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Opt in only when a mock backend really accepts the signed transaction.
// The default flow evidence and delayed/hidden-evidence cases are unchanged.
type rgb11AcceptedEvidence struct {
	*rgb11AddressEvidence
	rpc      *rgb11FlowIndexer
	mu       sync.Mutex
	accepted int
}

func newRGB11AcceptedEvidence(evidence *rgb11AddressEvidence, rpc *rgb11FlowIndexer) *rgb11AcceptedEvidence {
	return &rgb11AcceptedEvidence{rgb11AddressEvidence: evidence, rpc: rpc}
}

func (e *rgb11AcceptedEvidence) Broadcast(raw []byte) (string, error) {
	txid, err := e.rgb11AddressEvidence.Broadcast(raw)
	if err != nil {
		return txid, err
	}
	e.mu.Lock()
	e.accepted++
	e.mu.Unlock()
	if err := acceptRGB11FlowTx(e.rgb11AddressEvidence, e.rpc, raw, txid); err != nil {
		return txid, err
	}
	return txid, nil
}

// Materialize the same Bitcoin facts previously populated by the successful
// flow tests after Broadcast returned. Use the backend's actual signed bytes,
// never an unsigned pending copy, and expose an unconfirmed mempool status.
func acceptRGB11FlowTx(evidence *rgb11AddressEvidence, rpc *rgb11FlowIndexer, raw []byte, txid string) error {
	if evidence == nil || evidence.rgb11FlowEvidence == nil || rpc == nil {
		return fmt.Errorf("RGB broadcast fixture has no Bitcoin evidence/indexer")
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return err
	}
	if tx.TxHash().String() != txid {
		return fmt.Errorf("RGB broadcast fixture transaction ID mismatch")
	}
	base := evidence.rgb11FlowEvidence
	base.mu.Lock()
	defer base.mu.Unlock()
	base.rawTx[txid] = append([]byte(nil), raw...)
	spent := make(map[string]bool, len(tx.TxIn))
	for _, input := range tx.TxIn {
		point := input.PreviousOutPoint.String()
		spent[point] = true
		base.spendingTx[point] = txid
		delete(base.utxos, point)
		delete(rpc.outputs, point)
	}
	plain := make([]*indexerwire.TxOutputInfo, 0, len(rpc.plain)+len(tx.TxOut))
	// This fixture's plain list is sender-scoped; its getter does not filter
	// by address. Keep that original script scope instead of counting every
	// recipient output as the sender's plain balance.
	owned := make(map[string]bool, len(rpc.plain))
	for _, output := range rpc.plain {
		owned[string(output.PkScript)] = true
		if !spent[output.OutPoint] {
			plain = append(plain, output)
		}
	}
	for vout, output := range tx.TxOut {
		if txscript.IsUnspendable(output.PkScript) {
			continue
		}
		point := fmt.Sprintf("%s:%d", txid, vout)
		base.utxos[point] = &rgb11wallet.BitcoinUTXO{
			OutPoint: point, Value: output.Value, PkScript: append([]byte(nil), output.PkScript...),
		}
		view := indexer.NewTxOutput(output.Value)
		view.OutPointStr, view.OutValue.PkScript = point, append([]byte(nil), output.PkScript...)
		rpc.outputs[point] = view
		if owned[string(output.PkScript)] {
			plain = append(plain, &indexerwire.TxOutputInfo{
				OutPoint: point, Value: output.Value, PkScript: append([]byte(nil), output.PkScript...),
			})
		}
	}
	rpc.plain = plain
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true})
	return nil
}

// Read-only observations before/after the original call, not an execution trace
// of its internal branch. Never refresh, sign, publish readiness or change data.
func logRGB11BroadcastState(t testing.TB, phase string, manager *Manager,
	transferID string, evidence *rgb11AcceptedEvidence) {
	t.Helper()
	evidence.mu.Lock()
	accepted := evidence.accepted
	evidence.mu.Unlock()
	base := evidence.rgb11FlowEvidence
	base.mu.Lock()
	calls := base.broadcastCount
	base.mu.Unlock()
	runtime := manager.rgbManager
	release := runtime.beginRGB11ReservationRead()
	defer release()
	pending, err := runtime.projectionStore.LoadPendingTransfer(transferID)
	t.Logf("rgb-broadcast-diag phase=%s base_backend_calls=%d accepted_entries=%d pending_read_ok=%t registry_present=%t",
		phase, calls, accepted, err == nil && pending != nil, runtime.scopeStates != nil)
	if err != nil || pending == nil {
		return
	}
	state := runtime.rgb11ScopeState()
	records, err := runtime.projectionStore.ReadLocalRecords()
	if err != nil {
		t.Logf("rgb-broadcast-diag phase=%s projection_read_ok=false", phase)
		return
	}
	view, err := rgb11wallet.SplitLocalProjection(records, state.ProofFaults...)
	if err != nil {
		t.Logf("rgb-broadcast-diag phase=%s projection_split_ok=false", phase)
		return
	}
	proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(view.Records)
	if err != nil {
		t.Logf("rgb-broadcast-diag phase=%s proofs_decode_ok=false", phase)
		return
	}
	packet, packetErr := psbt.NewFromRawBytes(bytes.NewReader(pending.SignedPSBT), false)
	tx, txErr := decodeRGB11BroadcastTx(pending.SignedTx)
	t.Logf("rgb-broadcast-diag phase=%s txid=%s status=%s ack=%s verified=%t ready_carriers=%d faults=%d psbt_ok=%t tx_ok=%t",
		phase, pending.State.WitnessTxID, pending.State.Status, pending.State.AckStatus,
		state.ProjectionVerified, len(state.ReadyCarriers), len(state.ProofFaults), packetErr == nil, txErr == nil)
	if packetErr != nil || txErr != nil {
		return
	}
	t.Logf("rgb-broadcast-diag phase=%s has_witness=%t unsigned_match=%t input_counts_match=%t",
		phase, tx.HasWitness(), sameRGB11UnsignedTx(tx, packet.UnsignedTx), len(packet.Inputs) == len(tx.TxIn))
	walletScript, scriptErr := AddrToPkScript(runtime.wallet.GetAddress(), GetChainParam())
	for i, input := range tx.TxIn {
		point := input.PreviousOutPoint.String()
		hasProof, bindingOK, rootOK := false, false, false
		var utxo *wire.TxOut
		if i < len(packet.Inputs) {
			utxo = packet.Inputs[i].WitnessUtxo
		}
		for _, proof := range proofs {
			if proof == nil || proof.OutPoint != point {
				continue
			}
			hasProof = true
			binding := proof.CarrierBinding
			if binding == nil || utxo == nil {
				continue
			}
			bindingOK = bytes.Equal(binding.ActualPkScript, utxo.PkScript) && runtime.ownsRGB11Carrier(binding, walletScript)
			rootOK = binding.CommitmentMethod != "tapret1st" || len(binding.TapretRoot) == 32
		}
		ready := state.ProjectionVerified || state.ReadyCarriers[point]
		walletMatch := scriptErr == nil && utxo != nil && bytes.Equal(utxo.PkScript, walletScript)
		first := "remaining-signing-checks"
		switch {
		case view.Points[point]:
			first = "blocked-carrier"
		case hasProof && !ready:
			first = "carrier-not-ready"
		case utxo == nil || len(input.SignatureScript) != 0:
			first = "unsigned-input-shape"
		case !walletMatch && !bindingOK:
			first = "carrier-binding"
		case !walletMatch && !rootOK:
			first = "tapret-root"
		}
		base.mu.Lock()
		raw := base.rawTx[input.PreviousOutPoint.Hash.String()]
		producer := wire.NewMsgTx(wire.TxVersion)
		rawOK := len(raw) > 0 && producer.Deserialize(bytes.NewReader(raw)) == nil && producer.TxHash() == input.PreviousOutPoint.Hash
		outputOK := rawOK && int(input.PreviousOutPoint.Index) < len(producer.TxOut)
		if outputOK {
			output := producer.TxOut[input.PreviousOutPoint.Index]
			outputOK = utxo != nil && output.Value == utxo.Value && bytes.Equal(output.PkScript, utxo.PkScript)
		}
		source := base.utxos[point]
		sourceOK := source != nil && utxo != nil && source.Value == utxo.Value && bytes.Equal(source.PkScript, utxo.PkScript)
		spent := base.spendingTx[point] != ""
		status := base.txStatuses[input.PreviousOutPoint.Hash.String()]
		producerSeen := status != nil && (status.Confirmed || status.InMempool)
		base.mu.Unlock()
		t.Logf("rgb-broadcast-diag phase=%s input=%d outpoint=%s first_readonly_check=%s blocked=%t proof=%t ready=%t witness_utxo=%t script_sig_empty=%t wallet_script=%t binding=%t root=%t producer_raw_valid=%t producer_output_match=%t producer_status=%t source_utxo_match=%t spent=%t",
			phase, i, point, first, view.Points[point], hasProof, ready, utxo != nil, len(input.SignatureScript) == 0,
			walletMatch, bindingOK, rootOK, rawOK, outputOK, producerSeen, sourceOK, spent)
	}
}
