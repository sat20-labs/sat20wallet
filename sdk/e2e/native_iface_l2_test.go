package e2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// The fixture is the real BRC20 distribution from the same private Bitcoin /
// full-indexer / SatoshiNet group; no controlled Anchor evidence is accepted.
func runNativeIfaceL2(t *testing.T, network *realSatoshiNet, cfg *sdkcommon.Config,
	owner, buyer map[string]string, sample map[string]any) {
	t.Helper()
	buyerScript, err := hex.DecodeString(buyer["pk_script"])
	require.NoError(t, err)
	var buyerOrder wallet.UtxoInfo
	require.NoError(t, json.Unmarshal([]byte(sample["buyerOrder"].(string)), &buyerOrder))

	t.Run("18_l2_indexed_selection", func(t *testing.T) {
		d := newNativeIfaceDevice(t, cfg, owner["mnemonic"])
		m := d.mgr
		outputs := m.GetIndexerRPCClient_SatsNet().GetUtxoListWithTicker(owner["address"], &indexercommon.ASSET_PLAIN_SAT)
		require.NotEmpty(t, outputs)
		points := map[string]bool{}
		var total int64
		for _, out := range outputs {
			points[out.OutPoint] = true
			total += out.Value
		}
		selected, fees, err := m.GetUtxosWithAssetV2ForJS_SatsNet(owner["address"], 1000, "1000", "::")
		require.NoError(t, err)
		require.NotEmpty(t, selected)
		seen := map[string]bool{}
		for _, point := range append(selected, fees...) {
			require.True(t, points[point])
			require.False(t, seen[point])
			seen[point] = true
		}
		free, locked, err := m.GetAssetAmountForJS_SatsNet(owner["address"], "::")
		require.NoError(t, err)
		require.Equal(t, total, free.Int64()+locked.Int64())
		_, err = m.GetUtxosWithAssetForJS_SatsNet(owner["address"], fmt.Sprint(total+1), "::")
		require.Error(t, err)
		_, err = wallet.AddrToPkScript_SatsNet(owner["address"], wallet.GetChainParam_SatsNet())
		require.NoError(t, err)
		_, err = wallet.AddrToPkScript_SatsNet("invalid", wallet.GetChainParam_SatsNet())
		require.Error(t, err)
	})

	t.Run("19_l2_owner_lock_cold", func(t *testing.T) {
		d := newNativeIfaceDevice(t, cfg, owner["mnemonic"])
		points, err := d.mgr.GetUtxosWithAssetForJS_SatsNet(owner["address"], "1000", "::")
		require.NoError(t, err)
		require.NotEmpty(t, points)
		lock := wallet.UtxoLockOwner{Origin: "http://127.0.0.1:9001", Network: cfg.Chain, WalletFingerprint: d.mgr.GetWalletCatalog()[0].Fingerprint}
		require.NoError(t, d.mgr.LockUtxoForOwner_SatsNet(owner["address"], points[0], "native L2 owner", lock))
		d.reopen(t)
		_, err = d.mgr.UnlockWallet(nativeIfacePassword)
		require.NoError(t, err)
		require.True(t, d.mgr.IsLocked_SatsNet(owner["address"], points[0]))
		origin, account, fingerprint := lock, lock, lock
		origin.Origin = "http://127.0.0.1:9002"
		account.AccountIndex = 1
		fingerprint.WalletFingerprint = "another-wallet"
		for _, wrong := range []wallet.UtxoLockOwner{origin, account, fingerprint} {
			require.Error(t, d.mgr.UnlockUtxoForOwner_SatsNet(owner["address"], points[0], wrong))
			require.True(t, d.mgr.IsLocked_SatsNet(owner["address"], points[0]))
		}
		require.NoError(t, d.mgr.UnlockUtxoForOwner_SatsNet(owner["address"], points[0], lock))
		require.False(t, d.mgr.IsLocked_SatsNet(owner["address"], points[0]))
	})

	t.Run("20_l2_asset_psbt", func(t *testing.T) {
		d := newNativeIfaceDevice(t, cfg, owner["mnemonic"])
		encoded := sample["psbt"].(string)
		unsigned, err := wallet.ExtractUnsignedTxFromPsbt_SatsNet(encoded)
		require.NoError(t, err)
		require.Equal(t, sample["unsignedTx"], unsigned)
		_, err = wallet.ExtractTxFromPsbt_SatsNet(encoded)
		require.Error(t, err)
		single, err := d.mgr.SignPsbt_SatsNet(encoded, false)
		require.NoError(t, err)
		batch, err := d.mgr.SignPsbts_SatsNet([]string{encoded, encoded}, false)
		require.NoError(t, err)
		require.Len(t, batch, 2)
		for _, signed := range append([]string{single}, batch...) {
			observed, err := posPWAInspectPSBT(signed, true)
			require.NoError(t, err)
			require.Equal(t, sample["unsignedTx"], observed["unsigned_tx"])
			require.Equal(t, sample["inputs"], observed["inputs"])
			require.Equal(t, sample["outputs"], observed["outputs"])
			require.Equal(t, true, observed["signatures_verified"])
			extracted, err := wallet.ExtractTxFromPsbt_SatsNet(signed)
			require.NoError(t, err)
			require.Equal(t, observed["tx"], extracted)
			info, err := wallet.GetTxAssetInfoFromPsbt_SatsNet(signed)
			require.NoError(t, err)
			require.Equal(t, sample["unsignedTx"], info.TxHex)
			require.Equal(t, observed["txid"], info.TxId)
		}
		extracted, err := d.mgr.SignPsbt_SatsNet(encoded, true)
		require.NoError(t, err)
		observed, err := posPWAInspectPSBT(single, true)
		require.NoError(t, err)
		require.Equal(t, observed["tx"], extracted)
		for _, malformed := range []string{"70736274ff00"} {
			_, err = d.mgr.SignPsbt_SatsNet(malformed, false)
			require.Error(t, err)
			_, err = wallet.ExtractTxFromPsbt_SatsNet(malformed)
			require.Error(t, err)
			_, err = wallet.ExtractUnsignedTxFromPsbt_SatsNet(malformed)
			require.Error(t, err)
			_, err = wallet.GetTxAssetInfoFromPsbt_SatsNet(malformed)
			require.Error(t, err)
		}
		for _, tampered := range sample["tampered"].([]string) {
			_, err = d.mgr.SignPsbt_SatsNet(tampered, false)
			require.Error(t, err)
		}
	})

	t.Run("21_l2_sell_order_roundtrip", func(t *testing.T) {
		d := newNativeIfaceDevice(t, cfg, owner["mnemonic"])
		m := d.mgr
		built, err := m.BuildL2SellOrder(sample["orders"].([]string), owner["address"], cfg.Chain)
		require.NoError(t, err)
		signed, err := m.SignPsbt_SatsNet(built, false)
		require.NoError(t, err)
		before, err := posPWAInspectPSBT(signed, true)
		require.NoError(t, err)
		inputs, outputs := before["inputs"].([]map[string]any), before["outputs"].([]map[string]any)
		require.Len(t, inputs, 2)
		require.Len(t, outputs, 2)
		for i, point := range sample["orderOutpoints"].([]string) {
			require.Equal(t, point, inputs[i]["outpoint"])
			require.Equal(t, int64((i+1)*1000), outputs[i]["value"])
		}
		split, err := wallet.SplitBatchSignedPsbt_SatsNet(signed, cfg.Chain)
		require.NoError(t, err)
		require.Len(t, split, 2)
		for i, piece := range split {
			got, err := posPWAInspectPSBT(piece, true)
			require.NoError(t, err)
			require.Equal(t, inputs[i:i+1], got["inputs"])
			require.Equal(t, outputs[i:i+1], got["outputs"])
		}
		merged, err := wallet.MergeBatchSignedPsbt_SatsNet(split, cfg.Chain)
		require.NoError(t, err)
		got, err := posPWAInspectPSBT(merged, true)
		require.NoError(t, err)
		require.Equal(t, before, got)
		withInput, err := wallet.AddInputsToPsbt_SatsNet(sample["psbt"].(string), []string{sample["additionalInput"].(string)})
		require.NoError(t, err)
		withOutput, err := wallet.AddOutputsToPsbt_SatsNet(withInput, []string{sample["additionalOutput"].(string)})
		require.NoError(t, err)
		appended, err := posPWAInspectPSBT(withOutput, false)
		require.NoError(t, err)
		appInputs, appOutputs := appended["inputs"].([]map[string]any), appended["outputs"].([]map[string]any)
		require.Equal(t, sample["inputs"], appInputs[:1])
		require.Equal(t, sample["outputs"], appOutputs[:2])
		require.Equal(t, sample["orderOutpoints"].([]string)[1], appInputs[len(appInputs)-1]["outpoint"])
		require.Equal(t, sample["additionalValue"], appOutputs[len(appOutputs)-1]["value"])
		finalized, err := m.FinalizeL2SellOrder(signed, []string{sample["buyerOrder"].(string)}, buyer["address"], owner["address"], cfg.Chain, 100, 500)
		require.NoError(t, err)
		buyerDevice := newNativeIfaceDevice(t, cfg, buyer["mnemonic"])
		complete, err := buyerDevice.mgr.SignPsbt_SatsNet(finalized, false)
		require.NoError(t, err)
		order, err := posPWAInspectPSBT(complete, true)
		require.NoError(t, err)
		orderInputs, orderOutputs := order["inputs"].([]map[string]any), order["outputs"].([]map[string]any)
		require.Len(t, orderInputs, 3)
		require.Len(t, orderOutputs, 4)
		require.Equal(t, inputs, orderInputs[:2])
		require.Equal(t, outputs, orderOutputs[:2])
		require.Equal(t, buyerOrder.OutPoint, orderInputs[2]["outpoint"])
		require.Equal(t, buyer["pk_script"], orderOutputs[2]["pk_script"])
		require.Equal(t, int64(1_000_000)+sample["buyerValue"].(int64)-3000-100-500, orderOutputs[2]["value"])
		require.Equal(t, owner["pk_script"], orderOutputs[3]["pk_script"])
		require.Equal(t, int64(100), orderOutputs[3]["value"])
		assets := orderOutputs[2]["assets"].(wire.TxAssets)
		require.Len(t, assets, 1)
		require.Equal(t, "50000000", assets[0].Amount.String())
		logs, err := m.GetOperationViews()
		require.NoError(t, err)
		for _, kind := range []string{"build_sell_order_l2", "finalize_sell_order_l2"} {
			count := 0
			for _, log := range logs {
				if log.Kind == kind {
					count++
					require.Equal(t, "succeeded", log.Status)
				}
			}
			require.Equal(t, 1, count)
		}
		_, err = wallet.MergeBatchSignedPsbt_SatsNet([]string{split[0], split[0]}, cfg.Chain)
		require.Error(t, err)
		_, err = wallet.AddInputsToPsbt_SatsNet(sample["psbt"].(string), []string{sample["orders"].([]string)[0]})
		require.Error(t, err)
		_, err = m.BuildL2SellOrder([]string{"invalid"}, owner["address"], cfg.Chain)
		require.Error(t, err)
		_, err = wallet.MergeBatchSignedPsbt_SatsNet([]string{"invalid"}, cfg.Chain)
		require.Error(t, err)
		_, err = m.FinalizeL2SellOrder("invalid", nil, buyer["address"], owner["address"], cfg.Chain, 100, 100)
		require.Error(t, err)
	})

	t.Run("27_real_l2_batch_send", func(t *testing.T) {
		d := newNativeIfaceDevice(t, cfg, owner["mnemonic"])
		before, _, err := d.mgr.GetAssetAmountForJS_SatsNet(buyer["address"], "::")
		require.NoError(t, err)
		baseline, height, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		block := blockAtHeight(t, network.Bootstrap, height)
		submitted := time.Now()
		id, err := d.mgr.BatchSendAssetsV2_SatsNet([]string{buyer["address"], buyer["address"]}, "::", []string{"1000", "2000"}, nil)
		require.NoError(t, err)
		hash, err := chainhash.NewHashFromStr(id)
		require.NoError(t, err)
		waitForScheduledSatsNetTx(t, network, id, submitted, height, baseline, block.Header.Timestamp)
		require.Eventually(t, func() bool {
			tx, err := network.Core.Client.GetRawTransactionVerbose(hash)
			return err == nil && tx.Confirmations > 0
		}, rpcReadyTimeout, 200*time.Millisecond)
		raw, err := network.Core.Client.GetRawTransaction(hash)
		require.NoError(t, err)
		tx := raw.MsgTx()
		require.Equal(t, id, tx.TxID())
		var values []int64
		var totalOut int64
		for _, output := range tx.TxOut {
			totalOut += output.Value
			if bytes.Equal(output.PkScript, buyerScript) {
				values = append(values, output.Value)
			}
		}
		require.Equal(t, []int64{1000, 2000}, values)
		prevs := txscript.NewMultiPrevOutFetcher(nil)
		var totalIn int64
		for _, input := range tx.TxIn {
			require.NotEmpty(t, input.Witness)
			parent, err := network.Core.Client.GetRawTransaction(&input.PreviousOutPoint.Hash)
			require.NoError(t, err)
			previous := parent.MsgTx().TxOut[input.PreviousOutPoint.Index]
			totalIn += previous.Value
			prevs.AddPrevOut(input.PreviousOutPoint, previous)
		}
		require.Equal(t, int64(wallet.DEFAULT_FEE_SATSNET), totalIn-totalOut)
		hashes := txscript.NewTxSigHashes(tx, prevs)
		for i, input := range tx.TxIn {
			prev := prevs.FetchPrevOutput(input.PreviousOutPoint)
			vm, err := txscript.NewEngine(prev.PkScript, tx, i, txscript.StandardVerifyFlags, nil, hashes, prev.Value, prev.Assets, prevs)
			require.NoError(t, err)
			require.NoError(t, vm.Execute())
		}
		require.Eventually(t, func() bool {
			amount, _, err := d.mgr.GetAssetAmountForJS_SatsNet(buyer["address"], "::")
			return err == nil && amount.Int64() == before.Int64()+3000
		}, rpcReadyTimeout, 200*time.Millisecond)
	})
}
