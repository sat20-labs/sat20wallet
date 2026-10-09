package e2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	btcbtcec "github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/indexer/runes/runestone"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/stretchr/testify/require"
	"lukechampine.com/uint128"
)

// Exercise the same HTTP/indexer client boundary as WASM. Each protocol gets
// an independent fixture and a real signed transaction; no balance response
// or transaction result is substituted by the test.
func TestPOSPWAL1IndexerSharedBRC20AndRunes(t *testing.T) {
	for _, protocol := range []string{indexer.PROTOCOL_NAME_BRC20, indexer.PROTOCOL_NAME_RUNES} {
		t.Run(protocol, func(t *testing.T) {
			f := newPOSPWAL1Indexer(t, "", nil)
			key, _ := btcbtcec.PrivKeyFromBytes(bytes.Repeat([]byte{3}, 32))
			otherKey, _ := btcbtcec.PrivKeyFromBytes(bytes.Repeat([]byte{4}, 32))
			address, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(key.PubKey().SerializeCompressed()), &chaincfg.TestNet4Params)
			require.NoError(t, err)
			recipient, err := btcutil.NewAddressWitnessPubKeyHash(btcutil.Hash160(otherKey.PubKey().SerializeCompressed()), &chaincfg.TestNet4Params)
			require.NoError(t, err)
			name := indexer.AssetName{Protocol: protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "TEST"}
			asset := &indexer.DisplayAsset{AssetName: name, Amount: "1000"}
			if protocol == indexer.PROTOCOL_NAME_BRC20 {
				asset.Offsets = indexer.AssetOffsets{{Start: 0, End: 1}}
				asset.OffsetToAmts = []*indexer.OffsetToAmount{{Offset: 0, Amount: "1000"}}
			}
			point := f.FundAddress(t, address.String(), 10_000, []*indexer.DisplayAsset{asset})
			source, exists := f.Output(point)
			require.True(t, exists)
			outpoint, err := wire.NewOutPointFromString(point)
			require.NoError(t, err)
			script, err := txscript.PayToAddrScript(recipient)
			require.NoError(t, err)
			tx := wire.NewMsgTx(2)
			tx.AddTxIn(wire.NewTxIn(outpoint, nil, nil))
			tx.AddTxOut(wire.NewTxOut(330, script))
			tx.AddTxOut(wire.NewTxOut(9470, source.PkScript))
			if protocol == indexer.PROTOCOL_NAME_RUNES {
				id, err := runestone.RuneIdFromString(f.assets.Network.State.Tickers[name.String()].DisplayName)
				require.NoError(t, err)
				// Send 400 and direct the remaining 600 back to the sender.
				pointer := uint32(0)
				stone := runestone.Runestone{Pointer: &pointer, Edicts: []runestone.Edict{{ID: *id, Amount: uint128.From64(600), Output: 1}}}
				encoded, err := stone.Encipher()
				require.NoError(t, err)
				tx.AddTxOut(wire.NewTxOut(0, encoded))
			}
			sign := func(tx *wire.MsgTx) string {
				prev := txscript.NewCannedPrevOutputFetcher(source.PkScript, source.Value)
				hashes := txscript.NewTxSigHashes(tx, prev)
				tx.TxIn[0].Witness, err = txscript.WitnessSignature(tx, hashes, 0, source.Value, source.PkScript, txscript.SigHashAll, key, true)
				require.NoError(t, err)
				var raw bytes.Buffer
				require.NoError(t, tx.Serialize(&raw))
				return hex.EncodeToString(raw.Bytes())
			}
			post := func(path string, body, result any) {
				raw, err := json.Marshal(body)
				require.NoError(t, err)
				resp, err := http.Post(f.NodeFixture().server.URL+"/testnet"+path, "application/json", bytes.NewReader(raw))
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.NoError(t, json.NewDecoder(resp.Body).Decode(result))
			}
			get := func(path string, result any) {
				resp, err := http.Get(f.NodeFixture().server.URL + "/testnet" + path)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.NoError(t, json.NewDecoder(resp.Body).Decode(result))
			}
			raw := sign(tx)
			before := f.assets.Network.Clone()
			var checked indexerwire.TestRawTxResp
			post("/btc/tx/test", indexerwire.TestRawTxReq{SignedTxs: []string{raw}}, &checked)
			require.Zero(t, checked.Code)
			require.Len(t, checked.Data, 1)
			require.True(t, checked.Data[0].Allowed, checked.Data[0].RejectReason)
			require.Equal(t, before.UtxoUsed, f.assets.Network.UtxoUsed)
			require.Equal(t, before.AddrAssetMap, f.assets.Network.AddrAssetMap)
			require.Equal(t, before.State.Tickers, f.assets.Network.State.Tickers)
			require.Empty(t, f.Snapshot().PendingTxIDs)
			if protocol == indexer.PROTOCOL_NAME_RUNES {
				// An unknown Rune ID is rejected after real signature validation,
				// without poisoning the next valid submission.
				bad := tx.Copy()
				stone := runestone.Runestone{Edicts: []runestone.Edict{{ID: runestone.RuneId{Block: 7, Tx: 1}, Amount: uint128.From64(1), Output: 1}}}
				bad.TxOut[2].PkScript, err = stone.Encipher()
				require.NoError(t, err)
				var rejected indexerwire.TestRawTxResp
				post("/btc/tx/test", indexerwire.TestRawTxReq{SignedTxs: []string{sign(bad)}}, &rejected)
				require.Len(t, rejected.Data, 1)
				require.False(t, rejected.Data[0].Allowed)
				require.Equal(t, before.UtxoUsed, f.assets.Network.UtxoUsed)
				require.Equal(t, before.State.Tickers, f.assets.Network.State.Tickers)
			}
			var sent indexerwire.SendRawTxResp
			post("/btc/tx", indexerwire.SendRawTxReq{SignedTxHex: raw}, &sent)
			require.Zero(t, sent.Code, sent.Msg)
			require.Equal(t, []string{tx.TxID()}, f.Snapshot().PendingTxIDs)
			var summary indexerwire.AssetSummaryRespV3
			get("/v3/address/summary/"+recipient.String(), &summary)
			require.Zero(t, summary.Code)
			for _, item := range summary.Data {
				require.NotEqual(t, name, item.AssetName, "pending credit must not be shown as confirmed")
			}
			require.Equal(t, []string{tx.TxID()}, f.ConfirmPending())
			get("/v3/address/summary/"+recipient.String(), &summary)
			require.Zero(t, summary.Code)
			credited := ""
			for _, item := range summary.Data {
				if item.AssetName == name {
					credited = item.Amount
				}
			}
			expected := "1000"
			if protocol == indexer.PROTOCOL_NAME_RUNES {
				expected = "400"
			}
			require.Equal(t, expected, credited)
			get("/v3/address/summary/"+address.String(), &summary)
			require.Zero(t, summary.Code)
			remaining := "0"
			for _, item := range summary.Data {
				if item.AssetName == name {
					remaining = item.Amount
				}
			}
			expectedRemaining := "0"
			if protocol == indexer.PROTOCOL_NAME_RUNES {
				expectedRemaining = "600"
			}
			require.Equal(t, expectedRemaining, remaining)
			var carriers indexerwire.UtxosWithAssetRespV3
			get("/v3/address/asset/"+recipient.String()+"/"+name.String(), &carriers)
			require.Zero(t, carriers.Code)
			if protocol == indexer.PROTOCOL_NAME_BRC20 {
				require.Empty(t, carriers.Data, "received transfer inscription is consumed")
				get("/v3/address/asset/"+recipient.String()+"/"+name.String()+"?invalid=true", &carriers)
				require.Zero(t, carriers.Code)
			}
			require.Len(t, carriers.Data, 1)
			require.Equal(t, tx.TxID()+":0", carriers.Data[0].OutPoint)
			require.Equal(t, expected, carriers.Data[0].Assets[0].Amount)
			// A new fixture cannot see the first one's seeded ticker or ledger.
			isolated := newPOSPWAL1Indexer(t, "", nil)
			require.NotContains(t, isolated.assets.Network.State.Tickers, name.String())
			require.Empty(t, isolated.assets.Network.AddrAssetMap)
		})
	}
}
