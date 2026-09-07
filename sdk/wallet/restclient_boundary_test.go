package wallet

import (
	"context"
	"errors"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type boundaryHTTP struct {
	get    []byte
	getErr error
}

func (h *boundaryHTTP) SendGetRequest(*URL) ([]byte, error) { return h.get, h.getErr }
func (*boundaryHTTP) SendPostRequest(*URL, []byte) ([]byte, error) {
	return nil, errors.New("unexpected post")
}

func TestAllowDeployTickFailsClosed(t *testing.T) {
	asset := &swire.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "dogcoin"}
	for _, http := range []*boundaryHTTP{
		{getErr: errors.New("transport unavailable")},
		{get: []byte(`not-json`)},
		{get: []byte(`{"code":1,"msg":"denied"}`)},
	} {
		client := NewIndexerClient("http", "indexer", "testnet", http)
		if err := client.AllowDeployTick(asset); err == nil {
			t.Fatal("deploy permission failed open")
		}
	}
}

func TestFeeAndLookupResponsesRejectMissingOrWrongData(t *testing.T) {
	client := NewIndexerClient("http", "indexer", "testnet", &boundaryHTTP{
		get: []byte(`{"code":0,"msg":"ok","data":{"list":[]}}`),
	})
	if rate, err := client.GetFeeRateWithError(); err == nil || rate != 0 {
		t.Fatalf("empty fee response: rate=%d err=%v", rate, err)
	}

	client.Http = &boundaryHTTP{get: []byte(`{"code":0,"msg":"ok","data":123}`)}
	if _, err := client.GetRawTxContext(context.Background(), "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Fatal("non-string raw transaction response was accepted")
	}

	client.Http = &boundaryHTTP{get: []byte(`{"code":0,"msg":"ok","data":null}`)}
	if _, err := client.GetUtxoId("0000000000000000000000000000000000000000000000000000000000000000:0"); err == nil {
		t.Fatal("nil UTXO response was accepted")
	}
}
