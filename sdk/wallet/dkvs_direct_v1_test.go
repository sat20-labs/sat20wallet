package wallet

import (
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func TestDirectPathKeyStateUsesActiveRecordSequence(t *testing.T) {
	key := "/name/8888.btc"
	record := &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Seq: 1}
	snapshot := &dkvsindexer.PathSnapshot{
		Path: key, PathMeta: &dkvsindexer.PathMeta{Path: key},
		Records: []*swire.DKVSRecord{record},
	}
	existing, floor, err := directPathKeyState(snapshot, key)
	if err != nil || existing != record || floor != 1 {
		t.Fatalf("existing=%#v floor=%d err=%v", existing, floor, err)
	}
}

func TestDirectPathKeyStateUsesDeleteFloorSequence(t *testing.T) {
	key := "/name/8888.btc"
	snapshot := &dkvsindexer.PathSnapshot{
		Path: key, PathMeta: &dkvsindexer.PathMeta{Path: key},
		DeleteFloors: []dkvsindexer.DeleteFloor{{
			Key: key, FloorSeq: 4, PathGeneration: 5,
			EffectiveHash: chainhash.DoubleHashH([]byte("deleted")),
		}},
	}
	existing, floor, err := directPathKeyState(snapshot, key)
	if err != nil || existing != nil || floor != 4 {
		t.Fatalf("existing=%#v floor=%d err=%v", existing, floor, err)
	}
}

func TestDirectPathKeyStateIgnoresOtherKeys(t *testing.T) {
	key := "/name/8888.btc"
	snapshot := &dkvsindexer.PathSnapshot{
		Path: key, PathMeta: &dkvsindexer.PathMeta{Path: key},
		Records: []*swire.DKVSRecord{{Version: dkvsindexer.Version, Key: "/name/other.btc", Seq: 9}},
	}
	existing, floor, err := directPathKeyState(snapshot, key)
	if err != nil || existing != nil || floor != 0 {
		t.Fatalf("existing=%#v floor=%d err=%v", existing, floor, err)
	}
}
