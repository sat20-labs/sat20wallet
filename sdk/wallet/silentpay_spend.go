package wallet

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

// BuildSilentPaySweep spends the selected confirmed SP outputs in one
// transaction. The destination receives the total less fees, with no change.
func (p *Manager) BuildSilentPaySweep(ctx context.Context, walletID int64, index uint32,
	outpoints []string, destination string, feeRate int64) (*wire.MsgTx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.l1IndexerClient == nil || p.utxoLockerL1 == nil || p.db == nil {
		return nil, ErrSilentPayUnsupported
	}
	// The indexer accepts at most 500 positions in one snapshot query.
	if len(outpoints) == 0 || len(outpoints) > 500 {
		return nil, fmt.Errorf("silentpay: select between 1 and 500 outputs")
	}
	requested := make(map[string]int, len(outpoints))
	for i, outpoint := range outpoints {
		if _, exists := requested[outpoint]; exists {
			return nil, fmt.Errorf("silentpay: duplicate outpoint %s", outpoint)
		}
		requested[outpoint] = i
	}

	keys, err := p.silentPayKeysForAccount(walletID, index)
	if err != nil {
		return nil, err
	}
	defer keys.Scan.Zero()
	defer keys.Spend.Zero()
	last, err := p.LastSyncedSilentPay(walletID, index)
	if err != nil || !last.Complete {
		return nil, fmt.Errorf("silentpay: no complete confirmed snapshot")
	}
	selected := make([]*SPReceivedOutput, len(outpoints))
	for i := range last.Outputs {
		out := &last.Outputs[i]
		outpoint := out.TxID + ":" + strconv.FormatUint(uint64(out.Vout), 10)
		if position, ok := requested[outpoint]; ok {
			selected[position] = out
		}
	}

	query := make([]SPIndexerPosition, len(selected))
	tx := wire.NewMsgTx(2)
	var total int64
	var highest int
	for i, out := range selected {
		outpoint := outpoints[i]
		if out == nil || !out.Unspent || out.TxIndex == 0 || out.Value < 330 {
			return nil, fmt.Errorf("silentpay: selected output %s missing or not spendable", outpoint)
		}
		if out.Value > btcutil.MaxSatoshi-total {
			return nil, fmt.Errorf("silentpay: selected input value exceeds Bitcoin supply")
		}
		total += out.Value
		if p.isL1SendInputProtected(outpoint) {
			return nil, fmt.Errorf("silentpay: selected BTC input %s is locked or protected", outpoint)
		}
		point, err := UtxoToWireOutpoint(outpoint)
		if err != nil {
			return nil, err
		}
		tx.AddTxIn(wire.NewTxIn(point, nil, nil))
		query[i] = SPIndexerPosition{
			Height: out.Height, BlockHash: out.BlockHash,
			TxIndex: out.TxIndex, Vout: out.Vout, ExpectedTxID: out.TxID,
		}
		highest = max(highest, out.Height)
	}

	spReader, rawReader, err := p.spScanRPC()
	if err != nil {
		return nil, err
	}
	status, err := spReader.SPStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status.Network != spIndexerExpectedNetwork() || status.SchemaVersion != 1 ||
		status.BaseAnchor.Height < highest {
		return nil, fmt.Errorf("silentpay: Indexer not ready for spend")
	}
	snapshot, err := spReader.SPSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	anchor, positions, err := spReader.SPOutpoints(ctx, snapshot.SnapshotID, query)
	if err != nil {
		return nil, err
	}
	if anchor != snapshot.Anchor || anchor.Height < highest || len(positions) != len(query) {
		return nil, fmt.Errorf("silentpay: inconsistent spend snapshot")
	}
	for i, position := range positions {
		if position.SPIndexerPosition != query[i] || position.TxID != selected[i].TxID ||
			position.State != "unspent" || position.Error != "" {
			return nil, fmt.Errorf("silentpay: selected UTXO %s is not unspent at the verified snapshot", outpoints[i])
		}
	}

	sources := make(map[string]*wire.MsgTx)
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	inputs := make([]SilentPaySenderInput, 0, len(selected))
	var weight utils.TxWeightEstimator
	for i, out := range selected {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sourceTx := sources[out.TxID]
		if sourceTx == nil {
			raw, err := rawReader.GetBitcoinRawTx(out.TxID)
			if err != nil || raw == nil || raw.Error != "" {
				return nil, fmt.Errorf("silentpay: confirmed transaction %s unavailable: %v", out.TxID, err)
			}
			sourceTx, err = spParseFoundTransaction(raw.RawTx, out.TxID)
			if err != nil {
				return nil, err
			}
			sources[out.TxID] = sourceTx
		}
		if uint64(out.Vout) >= uint64(len(sourceTx.TxOut)) {
			return nil, fmt.Errorf("silentpay: confirmed output index invalid")
		}
		source := sourceTx.TxOut[out.Vout]
		if source.Value != out.Value || !txscript.IsPayToTaproot(source.PkScript) {
			return nil, fmt.Errorf("silentpay: recovered Taproot output no longer matches Bitcoin")
		}

		// An unknown asset state is never interpreted as pure BTC.
		assetValue, err := p.l1IndexerClient.GetTxOutput(outpoints[i])
		if err != nil || assetValue == nil || len(assetValue.Assets) != 0 ||
			assetValue.Value() != source.Value || !bytes.Equal(assetValue.OutValue.PkScript, source.PkScript) {
			return nil, fmt.Errorf("silentpay: input %s is not verified asset-free BTC", outpoints[i])
		}
		signKey, err := SilentPayOutputSpendKey(keys, SilentPayMatch{
			Vout: out.Vout, K: out.K, Tweak: out.Tweak, OutputKey: source.PkScript[2:],
		})
		if err != nil {
			return nil, err
		}
		defer signKey.Zero()
		point := tx.TxIn[i].PreviousOutPoint
		fetcher.AddPrevOut(point, source)
		inputs = append(inputs, SilentPaySenderInput{Outpoint: point, Key: signKey})
		weight.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}

	destScript, err := spSweepDestScript(destination, inputs)
	if err != nil {
		return nil, err
	}
	weight.AddOutput(destScript)
	vsize := weight.VSize()
	rate := feeRate
	if rate <= 0 {
		rate = p.GetFeeRate()
	}
	if rate <= 0 {
		return nil, fmt.Errorf("silentpay: invalid fee rate")
	}
	if rate > (total-330)/vsize {
		return nil, fmt.Errorf("silentpay: insufficient balance after fee")
	}
	tx.AddTxOut(wire.NewTxOut(total-rate*vsize, destScript))

	sighashes := txscript.NewTxSigHashes(tx, fetcher)
	for i, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		digest, err := txscript.CalcTaprootSignatureHash(sighashes, txscript.SigHashDefault, tx, i, fetcher)
		if err != nil {
			return nil, err
		}
		// The derived key is the Taproot output key. Sign it without another tweak.
		sig, err := schnorr.Sign(input.Key, digest)
		if err != nil {
			return nil, err
		}
		tx.TxIn[i].Witness = wire.TxWitness{sig.Serialize()}
		prev := fetcher.FetchPrevOutput(input.Outpoint)
		engine, err := txscript.NewEngine(prev.PkScript, tx, i, txscript.StandardVerifyFlags,
			nil, sighashes, prev.Value, fetcher)
		if err != nil {
			return nil, err
		}
		if err := engine.Execute(); err != nil {
			return nil, fmt.Errorf("silentpay: invalid BTC spend signature for input %d: %w", i, err)
		}
	}
	return tx, nil
}

func spSweepDestScript(destination string, inputs []SilentPaySenderInput) ([]byte, error) {
	lower := strings.ToLower(destination)
	if strings.HasPrefix(lower, "sp1") || strings.HasPrefix(lower, "tsp1") {
		target, err := DecodeSilentPayAddress(destination, GetChainParam())
		if err != nil {
			return nil, err
		}
		return SilentPayDestinationScript(target, inputs, 0)
	}
	addr, err := btcutil.DecodeAddress(destination, GetChainParam())
	if err != nil {
		return nil, err
	}
	if !addr.IsForNet(GetChainParam()) {
		return nil, fmt.Errorf("silentpay: destination network mismatch")
	}
	return txscript.PayToAddrScript(addr)
}

func (p *Manager) SendSilentPaySweep(ctx context.Context, walletID int64, index uint32,
	outpoints []string, destination string, feeRate int64) (string, error) {
	if p == nil {
		return "", ErrSilentPayUnsupported
	}
	p.l1SendMu.Lock()
	defer p.l1SendMu.Unlock()

	tx, err := p.BuildSilentPaySweep(ctx, walletID, index, outpoints, destination, feeRate)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// The ordinary broadcast path locks each input until confirmation.
	return p.BroadcastTx(tx)
}
