package wallet

import (
	"encoding/json"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

// The ordinary in-process peer preserves the existing nil Decimal == zero
// representation. The HTTP peer parses "0" into a nonnil Decimal. The durable
// accepted snapshot must encode the same balances without moving its write
// past the original pipeline's first commitment signature.
func TestClosedAssetRestoreAcceptedZeroBalanceUsesExistingDecimalSemantics(t *testing.T) {
	for _, zeroLocal := range []bool{false, true} {
		name := "remote-zero"
		if zeroLocal {
			name = "local-zero"
		}
		t.Run(name, func(t *testing.T) {
			_, channel, _, _ := closedAssetRestoreFixture(t)
			positive, err := indexer.NewDecimalFromString("123.45", 2)
			if err != nil {
				t.Fatal(err)
			}
			resv := &SplicingReservation{NewCapacity: channel.Capacity, NewLocalBalance: positive, NewRemoteBalance: nil}
			resv.Channel, resv.OldChannel = channel, channel.Clone()
			if zeroLocal {
				resv.NewLocalBalance, resv.NewRemoteBalance = nil, positive
			}
			raw, err := closedAssetAcceptedJSON(resv)
			if err != nil {
				t.Fatalf("legitimate nil-zero accepted balance was rejected: %v", err)
			}
			var got closedChannelAssetAccept
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatal(err)
			}
			wantLocal, wantRemote := "123.45", "0"
			if zeroLocal {
				wantLocal, wantRemote = "0", "123.45"
			}
			if got.NewLocalBalance != wantLocal || got.NewRemoteBalance != wantRemote || len(got.ChannelSnapshot) == 0 || len(got.OldChannelSnapshot) == 0 {
				t.Fatalf("accepted snapshot lost exact amount/channel: local=%s remote=%s", got.NewLocalBalance, got.NewRemoteBalance)
			}
			if zeroLocal {
				resv.NewLocalBalance = indexer.NewDefaultDecimal(0)
			} else {
				resv.NewRemoteBalance = indexer.NewDefaultDecimal(0)
			}
			explicit, err := closedAssetAcceptedJSON(resv)
			if err != nil {
				t.Fatal(err)
			}
			var withExplicitZero closedChannelAssetAccept
			if err := json.Unmarshal([]byte(explicit), &withExplicitZero); err != nil {
				t.Fatal(err)
			}
			if got.NewLocalBalance != withExplicitZero.NewLocalBalance || got.NewRemoteBalance != withExplicitZero.NewRemoteBalance {
				t.Fatal("nil-zero and HTTP explicit-zero balances have different durable meanings")
			}
		})
	}
}

func TestClosedAssetRestoreAcceptedSnapshotStillRequiresChannelMaterial(t *testing.T) {
	for _, missingOld := range []bool{false, true} {
		name := "current"
		if missingOld {
			name = "old"
		}
		t.Run(name, func(t *testing.T) {
			_, channel, _, _ := closedAssetRestoreFixture(t)
			resv := &SplicingReservation{NewLocalBalance: indexer.NewDefaultDecimal(100), NewRemoteBalance: indexer.NewDefaultDecimal(0)}
			resv.Channel, resv.OldChannel = channel, channel.Clone()
			if missingOld {
				resv.OldChannel = nil
			} else {
				resv.Channel = nil
			}
			if raw, err := closedAssetAcceptedJSON(resv); err == nil || raw != "" {
				t.Fatal("missing accepted channel material was encoded as a complete child")
			}
		})
	}
}
