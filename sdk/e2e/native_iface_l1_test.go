package e2e

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

type nativeIfaceSnapshot struct {
	L1Tip, L2Tip   string
	L2Height       int32
	L1Pool, L2Pool []string
}

func (f *nativeIfaceChain) snapshot(t *testing.T) nativeIfaceSnapshot {
	t.Helper()
	var s nativeIfaceSnapshot
	require.NoError(t, f.core.call("getbestblockhash", nil, &s.L1Tip))
	require.NoError(t, f.core.call("getrawmempool", nil, &s.L1Pool))
	hash, height, err := f.network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	s.L2Tip, s.L2Height = hash.String(), height
	pool, err := f.network.Core.Client.GetRawMempool()
	require.NoError(t, err)
	for _, id := range pool {
		s.L2Pool = append(s.L2Pool, id.String())
	}
	sort.Strings(s.L1Pool)
	sort.Strings(s.L2Pool)
	return s
}

func nativeIfaceBalance(t *testing.T, m *wallet.Manager, address string) int64 {
	t.Helper()
	free, _, err := m.GetAssetAmountForJS(address, "::")
	require.NoError(t, err)
	require.NotNil(t, free)
	return free.Int64()
}

func nativeIfaceRawL1(t *testing.T, f *nativeIfaceChain, txid string) *wire.MsgTx {
	t.Helper()
	var raw string
	require.NoError(t, f.core.call("getrawtransaction", []any{txid}, &raw))
	return spRealDecodeTx(t, raw)
}

// Independently fetch every prevout from Core and execute its script. Neither
// the SDK transaction decoder nor its VerifySignedTx helper supplies evidence.
func nativeIfaceL1Fee(t *testing.T, f *nativeIfaceChain, tx *wire.MsgTx) int64 {
	t.Helper()
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	seen := make(map[wire.OutPoint]bool)
	var in, out int64
	for _, input := range tx.TxIn {
		point := input.PreviousOutPoint
		require.False(t, seen[point], "duplicate input")
		seen[point] = true
		prev := nativeIfaceRawL1(t, f, point.Hash.String())
		require.Less(t, int(point.Index), len(prev.TxOut))
		fetcher.AddPrevOut(point, prev.TxOut[point.Index])
		in += prev.TxOut[point.Index].Value
		require.NotEmpty(t, input.Witness)
	}
	for _, output := range tx.TxOut {
		out += output.Value
	}
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	for i, input := range tx.TxIn {
		prev := fetcher.FetchPrevOutput(input.PreviousOutPoint)
		engine, err := txscript.NewEngine(prev.PkScript, tx, i, txscript.StandardVerifyFlags, nil, hashes, prev.Value, fetcher)
		require.NoError(t, err)
		require.NoError(t, engine.Execute(), "independent L1 input %d verification", i)
	}
	fee := in - out
	vsize := int64((tx.SerializeSizeStripped()*3 + tx.SerializeSize() + 3) / 4)
	require.GreaterOrEqual(t, fee, vsize, "actual signed weight must be funded at 1 sat/vB")
	return fee
}

func runNativeIfaceL1(t *testing.T, f *nativeIfaceChain) {
	t.Helper()
	mnemonic := dkvsClientMnemonic
	address := nativeIfaceAddress(t, keyFromMnemonic(t, mnemonic, 0).PubKey().SerializeCompressed(), &chaincfg.RegressionNetParams)
	recipient := nativeIfaceAddress(t, keyFromMnemonic(t, f.seeds["ordinary-2"], 0).PubKey().SerializeCompressed(), &chaincfg.RegressionNetParams)
	f.fundOutputs(t, address, 1_000_000, 3)
	f.fund(t, recipient, 100_000)

	t.Run("07_l1_psbt_single_batch", func(t *testing.T) {
		d := f.device(t, mnemonic)
		outputs := d.mgr.GetIndexerClient().GetUtxoListWithTicker(address, &indexercommon.ASSET_PLAIN_SAT)
		require.NotEmpty(t, outputs)
		parts := strings.Split(outputs[0].OutPoint, ":")
		require.Len(t, parts, 2)
		var vout uint32
		_, err := fmt.Sscan(parts[1], &vout)
		require.NoError(t, err)
		hash, err := chainhash.NewHashFromStr(parts[0])
		require.NoError(t, err)
		previous := nativeIfaceRawL1(t, f, hash.String()).TxOut[vout]
		require.Equal(t, outputs[0].Value, previous.Value)
		require.Equal(t, outputs[0].PkScript, previous.PkScript)
		recipientScript, err := wallet.AddrToPkScript(recipient, wallet.GetChainParam())
		require.NoError(t, err)
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: *hash, Index: vout}, nil, nil))
		tx.TxIn[0].Sequence = 0xfffffffd
		tx.AddTxOut(wire.NewTxOut(2000, recipientScript))
		tx.AddTxOut(wire.NewTxOut(previous.Value-2500, previous.PkScript))
		packet, err := psbt.NewFromUnsignedTx(tx)
		require.NoError(t, err)
		packet.Inputs[0].WitnessUtxo = previous
		packet.Inputs[0].TaprootInternalKey = d.mgr.GetPublicKey(0)[1:]
		var buf bytes.Buffer
		require.NoError(t, packet.Serialize(&buf))
		encoded := hex.EncodeToString(buf.Bytes())
		unsigned, err := wallet.ExtractUnsignedTxFromPsbt(encoded)
		require.NoError(t, err)
		before := f.snapshot(t)
		single, err := d.mgr.SignPsbt(encoded, false)
		require.NoError(t, err)
		batch, err := d.mgr.SignPsbts([]string{encoded, encoded}, false)
		require.NoError(t, err)
		require.Len(t, batch, 2)
		for _, signed := range append([]string{single}, batch...) {
			raw, err := hex.DecodeString(signed)
			require.NoError(t, err)
			parsed, err := psbt.NewFromRawBytes(bytes.NewReader(raw), false)
			require.NoError(t, err)
			var actual bytes.Buffer
			require.NoError(t, parsed.UnsignedTx.Serialize(&actual))
			require.Equal(t, unsigned, hex.EncodeToString(actual.Bytes()))
			final, err := psbt.Extract(parsed)
			require.NoError(t, err)
			witness := final.TxIn[0].Witness[0]
			require.Contains(t, []int{64, 65}, len(witness))
			hashType := txscript.SigHashDefault
			if len(witness) == 65 {
				hashType = txscript.SigHashType(witness[64])
			}
			fetcher := txscript.NewCannedPrevOutputFetcher(previous.PkScript, previous.Value)
			digest, err := txscript.CalcTaprootSignatureHash(txscript.NewTxSigHashes(final, fetcher), hashType, final, 0, fetcher)
			require.NoError(t, err)
			sig, err := schnorr.ParseSignature(witness[:64])
			require.NoError(t, err)
			pub, err := schnorr.ParsePubKey(previous.PkScript[2:])
			require.NoError(t, err)
			require.True(t, sig.Verify(digest, pub))
			extracted, err := wallet.ExtractTxFromPsbt(signed)
			require.NoError(t, err)
			require.Equal(t, spRealTxHex(t, final), extracted)
		}
		require.NoError(t, d.mgr.LockUtxo(address, outputs[0].OutPoint, sdkcommon.UtxoLockRGB))
		_, err = d.mgr.SignPsbt(encoded, false)
		require.ErrorContains(t, err, "is protected")
		_, err = d.mgr.SignPsbts([]string{encoded}, false)
		require.ErrorContains(t, err, "is protected")
		require.NoError(t, d.mgr.UnlockUtxo(address, outputs[0].OutPoint))
		require.Equal(t, before, f.snapshot(t), "PSBT signing alone must not broadcast")
		tracked, id, err := d.mgr.PerformLocalAction(wallet.LOCAL_ACTION_CONFIRM_TX, hash.String(), 2)
		require.NoError(t, err)
		require.Equal(t, hash.String(), tracked)
		require.NotNil(t, d.mgr.GetLocalAction(id))
		require.Equal(t, before, f.snapshot(t), "tracking the existing transaction must not broadcast")
	})

	t.Run("08_l1_owner_lock_cold", func(t *testing.T) {
		d := f.device(t, mnemonic)
		points, err := d.mgr.GetUtxosWithAssetForJS(address, "1000", "::")
		require.NoError(t, err)
		require.NotEmpty(t, points)
		owner := wallet.UtxoLockOwner{Origin: "http://127.0.0.1:9001", Network: "regtest", WalletFingerprint: d.mgr.GetWalletCatalog()[0].Fingerprint}
		require.NoError(t, d.mgr.LockUtxoForOwner(address, points[0], "native owner", owner))
		require.True(t, d.mgr.IsLocked(address, points[0]))
		d.reopen(t)
		_, err = d.mgr.UnlockWallet(nativeIfacePassword)
		require.NoError(t, err)
		require.True(t, d.mgr.IsLocked(address, points[0]))
		wrong := owner
		wrong.Origin = "http://127.0.0.1:9002"
		require.Error(t, d.mgr.UnlockUtxoForOwner(address, points[0], wrong))
		require.True(t, d.mgr.IsLocked(address, points[0]))
		require.NoError(t, d.mgr.UnlockUtxoForOwner(address, points[0], owner))
		require.False(t, d.mgr.IsLocked(address, points[0]))
	})

	t.Run("16_monitor_funded_read_only", func(t *testing.T) {
		d := f.device(t, "")
		id, err := d.mgr.CreateMonitorWallet(address)
		require.NoError(t, err)
		require.Equal(t, address, d.mgr.GetWallet().GetAddress())
		require.NotEmpty(t, d.mgr.GetIndexerClient().GetUtxoListWithTicker(address, &indexercommon.ASSET_PLAIN_SAT))
		require.Positive(t, nativeIfaceBalance(t, d.mgr, address))
		_, err = d.mgr.SignMessage([]byte("monitor cannot sign"))
		require.Error(t, err)
		_, err = d.mgr.SignWalletMessage("monitor cannot sign")
		require.Error(t, err)
		require.Empty(t, d.mgr.GetMnemonic(id, nativeIfacePassword))
		// Monitor wallets are nonpersistent by design, just as in the old case.
	})

	t.Run("17_invalid_monitor_preserved", func(t *testing.T) {
		d := f.device(t, mnemonic)
		catalog, chain := nativeIfaceCatalog(t, d.mgr), f.snapshot(t)
		mainnet := nativeIfaceAddress(t, d.mgr.GetPublicKey(0), &chaincfg.MainNetParams)
		for _, invalid := range []string{"", "not-an-address", mainnet} {
			_, err := d.mgr.CreateMonitorWallet(invalid)
			require.Error(t, err)
			require.Equal(t, catalog, nativeIfaceCatalog(t, d.mgr))
			require.Equal(t, chain, f.snapshot(t))
			require.Equal(t, address, d.mgr.GetWallet().GetAddress())
		}
	})

	t.Run("18_l1_indexed_selection", func(t *testing.T) {
		d := f.device(t, mnemonic)
		outputs := d.mgr.GetIndexerClient().GetUtxoListWithTicker(address, &indexercommon.ASSET_PLAIN_SAT)
		require.NotEmpty(t, outputs)
		indexed := make(map[string]bool)
		var total int64
		for _, out := range outputs {
			indexed[out.OutPoint] = true
			total += out.Value
		}
		selected, fees, err := d.mgr.GetUtxosWithAssetV2ForJS(address, 1000, "1000", "::")
		require.NoError(t, err)
		require.NotEmpty(t, selected)
		seen := make(map[string]bool)
		for _, point := range selected {
			require.True(t, indexed[point])
			require.False(t, seen[point])
			seen[point] = true
		}
		// The original case deduplicates selected assets, then checks indexed
		// membership of both sets. L1 selects plain fee candidates separately;
		// actual transaction-input uniqueness is checked in cases 26 and 28.
		for _, point := range fees {
			require.True(t, indexed[point])
		}
		free, locked, err := d.mgr.GetAssetAmountForJS(address, "::")
		require.NoError(t, err)
		require.Equal(t, total, free.Int64()+locked.Int64())
		_, err = d.mgr.GetUtxosWithAssetForJS(address, fmt.Sprint(total+1), "::")
		require.Error(t, err)
	})

	t.Run("26_real_l1_batch_send", func(t *testing.T) {
		d := f.device(t, mnemonic)
		before := nativeIfaceBalance(t, d.mgr, recipient)
		old, err := d.mgr.GetOperationViews()
		require.NoError(t, err)
		ids := map[string]bool{}
		for _, log := range old {
			ids[log.ID] = true
		}
		sent, fee, err := d.mgr.BatchSendAssets(recipient, "::", "1000", 2, 1, nil)
		require.NoError(t, err)
		require.NotNil(t, sent)
		tx := nativeIfaceRawL1(t, f, sent.TxID())
		require.Equal(t, sent.TxID(), tx.TxID())
		script, err := wallet.AddrToPkScript(recipient, wallet.GetChainParam())
		require.NoError(t, err)
		var values []int64
		for _, out := range tx.TxOut {
			if bytes.Equal(out.PkScript, script) {
				values = append(values, out.Value)
			}
		}
		require.Equal(t, []int64{1000, 1000}, values)
		require.Equal(t, fee, nativeIfaceL1Fee(t, f, tx))
		logs, err := d.mgr.GetOperationViews()
		require.NoError(t, err)
		var payments []wallet.OperationView
		for _, log := range logs {
			if !ids[log.ID] {
				for _, id := range log.TxIDs {
					if id == tx.TxID() {
						payments = append(payments, log)
					}
				}
			}
		}
		require.Len(t, payments, 1)
		require.Equal(t, "split_send_l1", payments[0].Kind)
		require.Equal(t, "succeeded", payments[0].Status)
		f.mine(t, 1)
		require.Eventually(t, func() bool { return nativeIfaceBalance(t, d.mgr, recipient) == before+2000 }, rpcReadyTimeout, 200*time.Millisecond)
	})

	t.Run("28_garbage_exact_value", func(t *testing.T) {
		d := f.device(t, mnemonic)
		outputs := d.mgr.GetIndexerClient().GetUtxoListWithTicker(address, &indexercommon.ASSET_PLAIN_SAT)
		var selected *indexercommon.AssetsInUtxo
		indexed := map[string]*indexercommon.AssetsInUtxo{}
		for _, out := range outputs {
			indexed[out.OutPoint] = out
			if selected == nil && out.Value > 10000 && len(out.Assets) == 0 {
				selected = out
			}
		}
		require.NotNil(t, selected)
		before := f.snapshot(t)
		require.NoError(t, d.mgr.LockUtxo(address, selected.OutPoint, "garbage owner guard"))
		_, err := d.mgr.SendGarbage(recipient, []string{selected.OutPoint}, 1000, 1)
		require.Error(t, err)
		require.Equal(t, before, f.snapshot(t))
		require.True(t, d.mgr.IsLocked(address, selected.OutPoint))
		require.NoError(t, d.mgr.UnlockUtxo(address, selected.OutPoint))
		locks, err := d.mgr.GetLockedUtxoList(address)
		require.NoError(t, err)
		receiverBefore, ownerBefore := nativeIfaceBalance(t, d.mgr, recipient), nativeIfaceBalance(t, d.mgr, address)
		sent, err := d.mgr.SendGarbage(recipient, []string{selected.OutPoint}, 1000, 1)
		require.NoError(t, err)
		tx := nativeIfaceRawL1(t, f, sent.TxID())
		count := 0
		for _, in := range tx.TxIn {
			point := in.PreviousOutPoint.String()
			out := indexed[point]
			require.NotNil(t, out)
			require.NotContains(t, locks, point)
			require.Equal(t, out.Value, nativeIfaceRawL1(t, f, in.PreviousOutPoint.Hash.String()).TxOut[in.PreviousOutPoint.Index].Value)
			if point == selected.OutPoint {
				count++
			} else {
				require.Empty(t, out.Assets, "extra fee input must be indexed plain BTC")
			}
		}
		require.Equal(t, 1, count)
		script, err := wallet.AddrToPkScript(recipient, wallet.GetChainParam())
		require.NoError(t, err)
		var values []int64
		for _, out := range tx.TxOut {
			if bytes.Equal(out.PkScript, script) {
				values = append(values, out.Value)
			}
		}
		require.Equal(t, []int64{selected.Value}, values)
		fee := nativeIfaceL1Fee(t, f, tx)
		f.mine(t, 1)
		require.Eventually(t, func() bool {
			return nativeIfaceBalance(t, d.mgr, recipient) == receiverBefore+selected.Value && nativeIfaceBalance(t, d.mgr, address) == ownerBefore-selected.Value-fee
		}, rpcReadyTimeout, 200*time.Millisecond)
	})
}
