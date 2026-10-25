package wallet

import (
	"context"
	"math/big"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Exercise only the ordinary, local verification helpers. Real signed history,
// confirmation, reopen, and expansion remain covered by the unchanged 0758 and
// 0332 lifecycle tests. No network or alternate recovery flow is introduced.
type rgbClosedPrivateBurnFixture struct {
	graph    *nativeReceiptGraph
	mapping  *nativeReceiptMapping
	receipt  *nativeReceiptDescend
	previous *swire.MsgTx
	rpc      *nativeRecoveryRPC
}

func newRGBClosedPrivateBurnFixture(t *testing.T) *rgbClosedPrivateBurnFixture {
	t.Helper()
	script := append([]byte{0, 32}, make([]byte, 32)...)
	script[len(script)-1] = 1
	asset := swire.AssetInfo{Name: indexer.AssetName{Protocol: "rgb11", Type: "f", Ticker: strings.Repeat("a", 64)}, Amount: *indexer.NewDecimalWithScale(4975, 2)}
	previous := swire.NewMsgTx(2)
	// A zero-input source is ambiguous with the witness marker on decode.
	// Keep this local prevout fixture structurally valid before testing burns.
	previous.AddTxIn(swire.NewTxIn(&swire.OutPoint{Index: ^uint32(0)}, []byte{1, 1}, nil))
	previous.AddTxOut(swire.NewTxOut(1000, swire.TxAssets{asset}, script))
	hash := previous.TxHash()
	tx := swire.NewMsgTx(2)
	tx.AddTxIn(swire.NewTxIn(swire.NewOutPoint(&hash, 0), nil, nil))
	mainID := strings.Repeat("b", 64)
	payload, err := sindexer.EncodeDescendPayloadV2(mainID, sindexer.DESCEND_OP_CLOSE, nil)
	if err != nil {
		t.Fatal(err)
	}
	burnScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DESCENDING, payload)
	if err != nil {
		t.Fatal(err)
	}
	burn := swire.NewTxOut(0, swire.TxAssets{asset}, burnScript)
	tx.AddTxOut(burn)
	channel, err := AddrFromPkScript(script)
	if err != nil {
		t.Fatal(err)
	}
	marker, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID, []byte(channel))
	if err != nil {
		t.Fatal(err)
	}
	tx.AddTxOut(swire.NewTxOut(0, nil, marker))
	raw := rgbClosedPrivatePreviousRaw(t, previous)
	rpc := &nativeRecoveryRPC{raw: map[string]string{previous.TxID(): raw}}
	manager := &Manager{l2IndexerClient: &IndexerRPCClientMgr{active: rpc}}
	graph := &nativeReceiptGraph{asset: asset, chains: &nativeSettlementChains{manager: manager, ctx: context.Background()}}
	return &rgbClosedPrivateBurnFixture{graph: graph, mapping: &nativeReceiptMapping{script: script},
		receipt: &nativeReceiptDescend{id: tx.TxID(), tx: tx, output: burn, payload: &sindexer.DescendPayload{Version: 2, Operation: sindexer.DESCEND_OP_CLOSE, L1TxId: mainID}}, previous: previous, rpc: rpc}
}

func rgbClosedPrivatePreviousRaw(t *testing.T, previous *swire.MsgTx) string {
	t.Helper()
	raw, err := EncodeMsgTx_SatsNet(previous)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := DecodeMsgTx_SatsNet(raw)
	if err != nil || actual.TxHash() != previous.TxHash() {
		t.Fatalf("local L2 prevout fixture does not round-trip to its input hash: %v", err)
	}
	roundTrip, err := EncodeMsgTx_SatsNet(actual)
	if err != nil || roundTrip != raw {
		t.Fatalf("local L2 prevout fixture changed its serialized value, assets or script: %v", err)
	}
	return raw
}

func (f *rgbClosedPrivateBurnFixture) savePrevious(t *testing.T) {
	t.Helper()
	raw := rgbClosedPrivatePreviousRaw(t, f.previous)
	hash := f.previous.TxHash()
	f.receipt.tx.TxIn[0].PreviousOutPoint = *swire.NewOutPoint(&hash, 0)
	f.rpc.raw[f.previous.TxID()] = raw
}

func TestSTPRGBClosedPrivateBurnExactAmountAndChannelControls(t *testing.T) {
	for _, name := range []string{"exact4975", "historical6000", "selectedOnly2500", "wrongAsset", "wrongPrecision", "boundAsset", "foreignInputChannel", "foreignChangeChannel", "unconserved", "wrongMarker", "duplicateMarker"} {
		t.Run(name, func(t *testing.T) {
			f := newRGBClosedPrivateBurnFixture(t)
			want := big.NewInt(4975)
			switch name {
			case "historical6000":
				want.SetInt64(6000)
			case "selectedOnly2500":
				want.SetInt64(2500)
			case "wrongAsset":
				f.receipt.output.Assets[0].Name.Ticker = strings.Repeat("c", 64)
			case "wrongPrecision":
				f.receipt.output.Assets[0].Amount.Precision++
			case "boundAsset":
				f.receipt.output.Assets[0].BindingSat = 1
			case "foreignInputChannel":
				f.previous.TxOut[0].PkScript = []byte{0x51}
				f.savePrevious(t)
			case "foreignChangeChannel":
				change := f.graph.asset
				change.Amount = *indexer.NewDecimalWithScale(1, 2)
				f.receipt.tx.AddTxOut(swire.NewTxOut(1, swire.TxAssets{change}, []byte{0x51}))
			case "unconserved":
				f.previous.TxOut[0].Assets[0].Amount = *indexer.NewDecimalWithScale(4976, 2)
				f.savePrevious(t)
			case "wrongMarker":
				marker, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_CHANNELID, []byte("another-channel"))
				if err != nil {
					t.Fatal(err)
				}
				f.receipt.tx.TxOut[1].PkScript = marker
			case "duplicateMarker":
				f.receipt.tx.AddTxOut(swire.NewTxOut(0, nil, append([]byte(nil), f.receipt.tx.TxOut[1].PkScript...)))
			}
			err := f.graph.verifyDescend(f.receipt, f.mapping, want)
			if name == "exact4975" {
				if err != nil {
					t.Fatalf("ordinary exact client close burn rejected: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid private close burn accepted")
			}
		})
	}
}
