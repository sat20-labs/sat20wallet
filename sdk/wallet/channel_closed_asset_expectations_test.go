package wallet

import (
	"encoding/json"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestClosedAssetExpectationsCaptureClientBalances(t *testing.T) {
	old := NewChannelInDB()
	old.LocalCommitment, old.RemoteCommitment = NewChannelCommitment(), NewChannelCommitment()
	brc := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}}
	unsupported := AssetName{AssetName: indexer.AssetName{Protocol: "unknown-protocol", Type: indexer.ASSET_TYPE_FT, Ticker: "retained"}}
	zero := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "zero"}}
	amount, err := indexer.NewDecimalFromString("9007199254740993.00000001", 8)
	if err != nil {
		t.Fatal(err)
	}
	old.LocalCommitment.LocalBalance[brc] = amount
	old.LocalCommitment.LocalBalance[unsupported] = indexer.NewDefaultDecimal(2)
	old.LocalCommitment.LocalBalance[zero] = indexer.NewDefaultDecimal(0)
	old.LocalCommitment.LocalBalance[PLAIN_ASSET] = indexer.NewDefaultDecimal(100000)
	old.RemoteCommitment.LocalBalance[brc] = indexer.NewDefaultDecimal(999)
	expected := closedChannelAssetExpectations(old)
	if len(expected) != 2 || expected[0].Asset != brc || expected[0].Amount.Cmp(amount) != 0 || expected[0].Amount.Precision != 8 || expected[1].Asset != unsupported {
		t.Fatalf("lost client amount, precision, order or unsupported identity: %+v", expected)
	}
	amount.Value.SetInt64(0)
	if expected[0].Amount.Sign() <= 0 {
		t.Fatal("expectation aliases the old mutable balance")
	}
}

func TestClosedAssetExpectationsUnknownAndKnownEmptyStayDistinct(t *testing.T) {
	known := NewChannelInDB()
	known.LocalCommitment = NewChannelCommitment()
	for _, test := range []struct {
		name string
		old  *ChannelInDB
		want string
	}{
		{"unknown", nil, "null"},
		{"missing-balance", NewChannelInDB(), "null"},
		{"known-empty", known, "[]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			intent := &closedChannelIntent{Version: 1, ChannelID: "channel", OldParentID: "old-parent", Choice: "reopen", Step: "prepared", AssetExpectations: closedChannelAssetExpectations(test.old)}
			raw, err := json.Marshal(intent)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil || string(fields["AssetExpectations"]) != test.want {
				t.Fatalf("unknown and checked-empty JSON collapsed: %s err=%v", raw, err)
			}
			owner := &LocalActionPerformData{Action: localActionClosedChannelRecovery, ActionParam: string(raw)}
			loaded, err := loadClosedChannelIntent(owner)
			if err != nil || (loaded.AssetExpectations == nil) != (test.want == "null") {
				t.Fatalf("JSON reload changed list knowledge: %+v err=%v", loaded, err)
			}
			if test.want == "null" {
				if err := (&Manager{}).completeClosedChannelAssets(owner); err == nil || !strings.Contains(err.Error(), "asset_expectations_unknown") {
					t.Fatalf("unknown old inventory was allowed to finish: %v", err)
				}
			}
		})
	}
}

func TestClosedAssetExpectationsAreInFirstDurableOwner(t *testing.T) {
	manager, channel := newSignedChannelSyncState(t, &channelHeartbeatTestClient{})
	captured := channel.LocalWallet().Clone()
	asset := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_BRC20, Type: indexer.ASSET_TYPE_FT, Ticker: "ordi"}}
	channel.SetCommitLocalValue(&asset, indexer.NewDefaultDecimal(80))
	channel.SetCommitRemoteValue(&asset, indexer.NewDefaultDecimal(900))
	channel.Status = CS_CLOSED
	if err := manager.SaveChannelToDB(channel); err != nil {
		t.Fatal(err)
	}
	manager.wallet.SetSubAccount(1)
	owner, _, err := manager.closedReopenIntentForWallet(captured, channel.ChannelId)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := LoadReservation(manager.db, nil, RESV_TYPE_LOCALACTION, owner.Id)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := reservation.(*LocalActionPerformData)
	if !ok {
		t.Fatal("first durable owner is missing")
	}
	intent, err := loadClosedChannelIntent(stored)
	if err != nil {
		t.Fatal(err)
	}
	if !closedIntentWalletMatches(stored, captured) || intent.OldParentID != channel.RemoteCommitment.CommitTx.TxID() || intent.Step != "prepared" || intent.Request != nil || len(stored.ActionResvs) != 0 || len(intent.AssetExpectations) != 1 || intent.AssetExpectations[0].Asset != asset || intent.AssetExpectations[0].Amount.Cmp(indexer.NewDefaultDecimal(80)) != 0 {
		t.Fatalf("first durable owner lacks exact captured old client inventory: %+v", intent)
	}
	if len(manager.GetFundingReservations()) != 0 || manager.wallet.GetSubAccount() != 1 {
		t.Fatal("capturing metadata funded or changed the current account")
	}
}
