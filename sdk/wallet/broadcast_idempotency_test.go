package wallet

import (
	"context"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
)

type packageBroadcastHTTP struct {
	visible   map[string]bool
	postCalls int
}

func (h *packageBroadcastHTTP) SendGetRequest(url *URL) ([]byte, error) {
	txid := url.Path[strings.LastIndex(url.Path, "/")+1:]
	if !h.visible[txid] {
		return []byte(`{"code":-1,"msg":"transaction not found"}`), nil
	}
	return []byte(`{"code":0,"msg":"ok","data":"00"}`), nil
}

func (h *packageBroadcastHTTP) SendPostRequest(_ *URL, _ []byte) ([]byte, error) {
	h.postCalls++
	return []byte(`{"code":-1,"msg":"package rejected without per-transaction reason (possible package-error)","data":[]}`), nil
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
	http := &packageBroadcastHTTP{visible: map[string]bool{txid1: true, txid2: true}}
	client := NewIndexerClient("http", "indexer", "testnet", http)
	if _, err := client.GetRawTx(txid1); err != nil {
		t.Fatalf("visible tx lookup failed: %v", err)
	}
	if err := client.broadCastHexTxsContext(context.Background(), []string{raw1, raw2, raw2}); err != nil {
		t.Fatalf("visible package was not treated as success: %v", err)
	}
	if http.postCalls != 1 {
		t.Fatalf("package endpoint calls=%d, want 1", http.postCalls)
	}
}

func TestBroadCastHexTxsDoesNotMaskMissingPackageTransaction(t *testing.T) {
	raw1, txid1 := testBroadcastTx(t, 3)
	raw2, _ := testBroadcastTx(t, 4)
	http := &packageBroadcastHTTP{visible: map[string]bool{txid1: true}}
	client := NewIndexerClient("http", "indexer", "testnet", http)

	if err := client.broadCastHexTxsContext(context.Background(), []string{raw1, raw2}); err == nil {
		t.Fatal("missing package transaction was incorrectly treated as success")
	}
}
