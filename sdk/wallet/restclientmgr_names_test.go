package wallet

import (
	"fmt"
	"testing"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

type recordingNamesIndexer struct {
	IndexerRPCClient
	host       string
	pingErr    error
	namesErr   error
	names      []*indexerwire.OrdinalsName
	address    string
	key        string
	queryCount int
}

func (c *recordingNamesIndexer) Host() string { return c.host }
func (c *recordingNamesIndexer) Ping() error  { return c.pingErr }
func (c *recordingNamesIndexer) GetNamesWithKey(address, key string) ([]*indexerwire.OrdinalsName, error) {
	c.address, c.key = address, key
	c.queryCount++
	return c.names, c.namesErr
}

func TestIndexerRPCClientMgrGetNamesWithKeyPreservesArgumentsOnFailover(t *testing.T) {
	first := &recordingNamesIndexer{
		host: "first", pingErr: fmt.Errorf("unavailable"),
		namesErr: fmt.Errorf("connection refused"),
	}
	want := []*indexerwire.OrdinalsName{{}}
	second := &recordingNamesIndexer{host: "second", names: want}
	manager := NewIndexerRPCClientMgr()
	manager.Set(first)
	manager.Set(second)

	got, err := manager.GetNamesWithKey("wallet-address", "referrer_sig")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d names, want %d", len(got), len(want))
	}
	if first.address != "wallet-address" || first.key != "referrer_sig" {
		t.Fatalf("first query = (%q, %q)", first.address, first.key)
	}
	if second.address != first.address || second.key != first.key {
		t.Fatalf("failover query = (%q, %q), want (%q, %q)",
			second.address, second.key, first.address, first.key)
	}
	if first.queryCount != 1 || second.queryCount != 1 {
		t.Fatalf("query counts = (%d, %d), want (1, 1)", first.queryCount, second.queryCount)
	}
}
