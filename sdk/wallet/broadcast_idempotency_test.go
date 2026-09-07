package wallet

import (
	"context"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type packageBroadcastHTTP struct {
	visible      map[string]string
	postCalls    int
	postResponse []byte
}

func (h *packageBroadcastHTTP) SendGetRequest(url *URL) ([]byte, error) {
	txid := url.Path[strings.LastIndex(url.Path, "/")+1:]
	raw, ok := h.visible[txid]
	if !ok {
		return []byte(`{"code":-1,"msg":"transaction not found"}`), nil
	}
	return []byte(`{"code":0,"msg":"ok","data":"` + raw + `"}`), nil
}

func (h *packageBroadcastHTTP) SendPostRequest(url *URL, _ []byte) ([]byte, error) {
	h.postCalls++
	if h.postResponse != nil {
		return h.postResponse, nil
	}
	if strings.HasSuffix(url.Path, "/btc/tx") {
		return []byte(`{"code":-1,"msg":"duplicate-like human text","data":""}`), nil
	}
	return []byte(`{"code":-1,"msg":"package rejected without per-transaction reason (possible package-error)","data":[]}`), nil
}

func testSatsNetBroadcastTx(t *testing.T, value int64) (string, string) {
	t.Helper()
	tx := swire.NewMsgTx(swire.TxVersion)
	tx.AddTxIn(swire.NewTxIn(&swire.OutPoint{}, nil, nil))
	assetName := swire.NewAssetNameFromString("brc20:f:sgas")
	tx.AddTxOut(swire.NewTxOut(value, swire.TxAssets{{
		Name:   *assetName,
		Amount: *indexer.NewDefaultDecimal(1),
	}}, []byte{0x51}))
	raw, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		t.Fatal(err)
	}
	return raw, tx.TxID()
}

func testBroadcastTx(t *testing.T, value int64) (string, string) {
	t.Helper()
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	tx.AddTxOut(&wire.TxOut{Value: value, PkScript: []byte{0x51}})
	raw, err := EncodeMsgTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	return raw, tx.TxID()
}

func TestBroadCastHexTxsTreatsVisiblePackageAsSuccess(t *testing.T) {
	raw1, txid1 := testBroadcastTx(t, 1)
	raw2, txid2 := testBroadcastTx(t, 2)
	http := &packageBroadcastHTTP{visible: map[string]string{txid1: raw1, txid2: raw2}}
	client := NewIndexerClient("http", "indexer", "testnet", http)
	if _, err := client.GetRawTx(txid1); err != nil {
		t.Fatalf("visible tx lookup failed: %v", err)
	}
	if err := client.broadCastBitcoinHexTxsContext(context.Background(), []string{raw1, raw2, raw2}); err != nil {
		t.Fatalf("visible package was not treated as success: %v", err)
	}
	if http.postCalls != 1 {
		t.Fatalf("package endpoint calls=%d, want 1", http.postCalls)
	}
}

func TestBroadCastHexTxsDoesNotMaskMissingPackageTransaction(t *testing.T) {
	raw1, txid1 := testBroadcastTx(t, 3)
	raw2, _ := testBroadcastTx(t, 4)
	http := &packageBroadcastHTTP{visible: map[string]string{txid1: raw1}}
	client := NewIndexerClient("http", "indexer", "testnet", http)

	if err := client.broadCastBitcoinHexTxsContext(context.Background(), []string{raw1, raw2}); err == nil {
		t.Fatal("missing package transaction was incorrectly treated as success")
	}
}

func TestBroadCastHexTxRequiresTransactionVisibilityForIdempotency(t *testing.T) {
	raw, txid := testBroadcastTx(t, 5)
	missing := &packageBroadcastHTTP{visible: map[string]string{}}
	client := NewIndexerClient("http", "indexer", "testnet", missing)
	if err := client.broadCastBitcoinHexTx(raw); err == nil {
		t.Fatal("human-readable duplicate hint masked a missing transaction")
	}

	visible := &packageBroadcastHTTP{visible: map[string]string{txid: raw}}
	client = NewIndexerClient("http", "indexer", "testnet", visible)
	if err := client.broadCastBitcoinHexTx(raw); err != nil {
		t.Fatalf("visible transaction was not treated as idempotent success: %v", err)
	}
}

func TestSatsNetBroadcastUsesOnlySatsNetTransactionID(t *testing.T) {
	raw, satsNetTxID := testSatsNetBroadcastTx(t, 6)
	bitcoinTxID, err := bitcoinBroadcastTxID(raw)
	if err != nil {
		t.Fatalf("test vector must also be accepted by the bitcoin decoder: %v", err)
	}
	if bitcoinTxID == satsNetTxID {
		t.Fatal("test vector did not distinguish bitcoin and satsnet transaction IDs")
	}

	http := &packageBroadcastHTTP{
		visible:      map[string]string{satsNetTxID: raw},
		postResponse: []byte(`{"code":0,"msg":"ok","data":["` + satsNetTxID + `"]}`),
	}
	client := NewIndexerClient("http", "indexer", "testnet", http)
	if err := client.broadCastSatsNetHexTxs([]string{raw}); err != nil {
		t.Fatalf("valid satsnet response was rejected: %v", err)
	}
}

func TestSatsNetDuplicateBroadcastRequiresSatsNetVisibility(t *testing.T) {
	raw, txID := testSatsNetBroadcastTx(t, 7)
	client := NewIndexerClient("http", "indexer", "testnet", &packageBroadcastHTTP{
		visible: map[string]string{txID: raw},
	})
	if err := client.broadCastSatsNetHexTx(raw); err != nil {
		t.Fatalf("visible satsnet transaction was not treated as success: %v", err)
	}
}

func TestSatsNetAcceptanceTreatsVisibleDuplicateAsSuccess(t *testing.T) {
	raw, txID := testSatsNetBroadcastTx(t, 8)
	client := NewIndexerClient("http", "indexer", "testnet", &packageBroadcastHTTP{
		visible: map[string]string{txID: raw},
		postResponse: []byte(`{"code":0,"msg":"ok","data":[{"txid":"` + txID +
			`","allowed":false,"reject-reason":"already in mempool"}]}`),
	})
	if err := client.TestRawTx_SatsNet([]string{raw}); err != nil {
		t.Fatalf("visible satsnet transaction was rejected by acceptance check: %v", err)
	}
}
