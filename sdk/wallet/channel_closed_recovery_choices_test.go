package wallet

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

type closedRecoveryChoicesAPI interface {
	PreviewClosedChannelRecovery(string) (string, error)
	WithdrawClosedChannelExact(string, string) (int64, error)
}

func closedRecoveryChoicesAPIForTest(t *testing.T, m *Manager) closedRecoveryChoicesAPI {
	t.Helper()
	api, ok := interface{}(m).(closedRecoveryChoicesAPI)
	if !ok {
		t.Fatal("closed-channel read-only capability and exact-parent withdrawal entry are missing")
	}
	return api
}
func installSingleUTXOLocalOther(t *testing.T, f *singleUTXOExecutionFixture) {
	t.Helper()
	parentHash := f.rpc.parent.TxHash()
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&parentHash, 0), nil, nil))
	tx.AddTxOut(wire.NewTxOut(f.rpc.parent.TxOut[0].Value-500, f.rpc.parent.TxOut[0].PkScript))
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, f.rpc.parent)
	signed, err := SignTxWithWallet(f.manager.wallet, tx, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	*f.signatures = 0
	f.stored.RemoteCommitment.Others = []*CommitmentTx{{CommitTx: signed}}
	raw, err := EncodeToBytes(f.stored)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.manager.db.Write([]byte(GetChannelKey(f.channelID)), raw); err != nil {
		t.Fatal(err)
	}
}
func TestSTPClosedRecoveryChoicesPreviewIsReadOnlyAndLocalOnly(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installSingleUTXOLocalOther(t, f)
	api := closedRecoveryChoicesAPIForTest(t, f.manager)
	raw, err := api.PreviewClosedChannelRecovery(f.channelID)
	if err != nil {
		t.Fatal(err)
	}
	var options struct {
		WalletID               int64
		AccountIndex           uint32
		ChannelID, OldParentID string
		CanWithdraw            bool
		WithdrawFeeSats        int64
		WithdrawTxCount        int
	}
	if err = json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatal(err)
	}
	if !options.CanWithdraw || options.WalletID != f.manager.wallet.GetId() || options.AccountIndex != f.manager.wallet.GetSubAccount() || options.ChannelID != f.channelID || options.OldParentID != f.rpc.parent.TxID() || options.WithdrawFeeSats != 500 || options.WithdrawTxCount != 1 {
		t.Fatalf("local signed capability is not exact: %s", raw)
	}
	if f.gate.calls != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("read-only withdrawal capability used server signing, funds, choice or locks")
	}
}
func TestSTPClosedRecoveryChoicesRejectsUnavailableOrNativePackage(t *testing.T) {
	for _, mode := range []string{"missing", "unconfirmed", "rgb-incomplete", "unsupported-native"} {
		t.Run(mode, func(t *testing.T) {
			f := newSingleUTXOExecutionFixture(t)
			if mode != "missing" {
				installSingleUTXOLocalOther(t, f)
			}
			if mode == "unconfirmed" {
				f.rpc.parentConfirmed = false
			}
			if mode == "rgb-incomplete" || mode == "unsupported-native" {
				protocol := "rgb11"
				if mode == "unsupported-native" {
					protocol = "fixture-unsupported-native"
				}
				f.stored.AssetFunding = map[string][]byte{protocol: []byte("fixture native classification")}
				raw, _ := EncodeToBytes(f.stored)
				if err := f.manager.db.Write([]byte(GetChannelKey(f.channelID)), raw); err != nil {
					t.Fatal(err)
				}
			}
			api := closedRecoveryChoicesAPIForTest(t, f.manager)
			raw, err := api.PreviewClosedChannelRecovery(f.channelID)
			if err != nil {
				t.Fatal(err)
			}
			var options struct {
				CanWithdraw                       bool
				WithdrawReason                    string
				WithdrawFeeSats                   int64
				WithdrawTxCount                   int
				WithdrawPending, WithdrawComplete bool
			}
			if err = json.Unmarshal([]byte(raw), &options); err != nil {
				t.Fatal(err)
			}
			if options.CanWithdraw || options.WithdrawReason == "" {
				t.Fatalf("unavailable %s exit enabled: %s", mode, raw)
			}
			reason := strings.ToLower(options.WithdrawReason)
			if mode == "rgb-incomplete" && (!strings.Contains(reason, "rgb") || !strings.Contains(reason, "incomplete")) {
				t.Fatalf("supported RGB without its complete recovery package was misclassified: %s", raw)
			}
			if mode == "unsupported-native" && !strings.Contains(reason, "native") {
				t.Fatalf("unknown native limitation not disclosed: %s", raw)
			}
			if options.WithdrawFeeSats != 0 || options.WithdrawTxCount != 0 || options.WithdrawPending || options.WithdrawComplete {
				t.Fatalf("rejected capability created a fee, transaction or completed/pending choice: %s", raw)
			}
			if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
				t.Fatal("capability preview mutated wallet")
			}
		})
	}
}
func TestSTPClosedRecoveryChoicesWrongParentOrAccountCannotWithdraw(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installSingleUTXOLocalOther(t, f)
	api := closedRecoveryChoicesAPIForTest(t, f.manager)
	if _, err := api.WithdrawClosedChannelExact(f.channelID, "not-the-reviewed-parent"); err == nil {
		t.Fatal("withdrawal accepted a different reviewed parent")
	}
	f.manager.wallet.SetSubAccount(1)
	if _, err := api.WithdrawClosedChannelExact(f.channelID, f.rpc.parent.TxID()); err == nil {
		t.Fatal("old account approval withdrew after account changed")
	}
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("invalid exact action changed wallet")
	}
}
