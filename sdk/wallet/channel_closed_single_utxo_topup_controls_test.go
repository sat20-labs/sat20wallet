package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

// Ordinary deterministic local Bitcoin fixtures exercise the public API and
// actual P2TR signing, durable-before-send order and restart-safe raw identity.
// The indexer is a transport/evidence fixture, not a Bitcoin consensus claim.
type singleUTXOCountingWallet struct {
	common.Wallet
	signatures *int
}

func (w *singleUTXOCountingWallet) Clone() common.Wallet {
	return &singleUTXOCountingWallet{Wallet: w.Wallet.Clone(), signatures: w.signatures}
}
func (w *singleUTXOCountingWallet) SignPsbt(packet *psbt.Packet) error {
	*w.signatures++
	return w.Wallet.SignPsbt(packet)
}

type singleUTXOExecutionRPC struct {
	*singleUTXOTopupQuoteIndexer
	manager            *Manager
	channelID          string
	parent, source     *wire.MsgTx
	outputs            map[string]*TxOutput
	raw                map[string]string
	broadcasts         []*wire.MsgTx
	testFailures       int
	rejectBeforeAccept int
	acknowledged       bool
	confirmed          bool
	parentConfirmed    bool
	durableError       error
}

func (r *singleUTXOExecutionRPC) IsTxConfirmed(id string) bool {
	return id == r.parent.TxID() && r.parentConfirmed || r.confirmed && r.raw[id] != ""
}
func (r *singleUTXOExecutionRPC) GetTxHeight(id string) (int, error) {
	if r.IsTxConfirmed(id) {
		return 100, nil
	}
	return 0, nil
}
func (r *singleUTXOExecutionRPC) GetRawTx(id string) (string, error) {
	if raw := r.raw[id]; raw != "" {
		return raw, nil
	}
	return "", indexer.ErrKeyNotFound
}
func (r *singleUTXOExecutionRPC) GetTxOutput(outpoint string) (*TxOutput, error) {
	if out := r.outputs[outpoint]; out != nil {
		return out, nil
	}
	return nil, indexer.ErrKeyNotFound
}
func (r *singleUTXOExecutionRPC) GetExistingUtxos(points []string) ([]string, error) {
	var out []string
	for _, point := range points {
		if r.outputs[point] != nil {
			out = append(out, point)
		}
	}
	return out, nil
}
func (r *singleUTXOExecutionRPC) GetAscendData(string) (*sindexer.AscendData, error) {
	return nil, indexer.ErrKeyNotFound
}
func (r *singleUTXOExecutionRPC) TestRawTx_Bitcoin(raw []string) error {
	if len(raw) != 1 {
		return errors.New("not an ordinary single transaction")
	}
	tx, err := DecodeMsgTx(raw[0])
	if err != nil {
		return err
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, r.source)
	if err := VerifySignedTx(tx, fetcher); err != nil {
		return err
	}
	if r.testFailures > 0 {
		r.testFailures--
		return errors.New("ordinary transient mempool-test failure before broadcast")
	}
	return nil
}
func (r *singleUTXOExecutionRPC) BroadCastTx(tx *wire.MsgTx) (string, error) {
	exact, _ := EncodeMsgTx(tx)
	found := false
	for _, item := range r.manager.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != "closedchannelrecovery" {
			continue
		}
		saved, err := r.manager.db.Read([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)))
		if err != nil {
			continue
		}
		var durable LocalActionPerformData
		if DecodeFromBytes(saved, &durable) != nil {
			continue
		}
		raw, ok := durable.ActionParam.(string)
		if !ok {
			continue
		}
		var intent struct{ ChannelID, OldParentID, Choice, TopupJSON string }
		if json.Unmarshal([]byte(raw), &intent) != nil {
			continue
		}
		var record struct {
			Quote, SignedRaw, TxID, Phase string
			Vout                          uint32
		}
		if json.Unmarshal([]byte(intent.TopupJSON), &record) != nil {
			continue
		}
		if intent.ChannelID == r.channelID && intent.OldParentID == r.parent.TxID() && intent.Choice == "reopen" && record.SignedRaw == exact && record.TxID == tx.TxID() && record.Vout == 0 && record.Phase == "broadcast_inflight" && record.Quote != "" {
			found = true
		}
	}
	if !found {
		r.durableError = errors.New("broadcast preceded exact durable owner raw/txid/vout/quote/phase")
	}
	for _, input := range tx.TxIn {
		lock := r.manager.utxoLockerL1.GetLockedUtxoList()[input.PreviousOutPoint.String()]
		if lock == nil || lock.ReservationID == "" {
			r.durableError = errors.New("broadcast preceded owned input reservation")
		}
	}
	r.broadcasts = append(r.broadcasts, tx.Copy())
	if r.rejectBeforeAccept > 0 {
		r.rejectBeforeAccept--
		return "", errors.New("ordinary transport failed before submission")
	}
	r.raw[tx.TxID()] = exact
	for i := range tx.TxOut {
		out := indexer.GenerateTxOutput(tx, i)
		r.outputs[out.OutPointStr] = out
	}
	if !r.acknowledged {
		return "", errors.New("ordinary transport lost acknowledgement")
	}
	return tx.TxID(), nil
}

type singleUTXOExecutionFixture struct {
	manager    *Manager
	rpc        *singleUTXOExecutionRPC
	gate       *singleUTXOTopupGateClient
	signatures *int
	channelID  string
	stored     *ChannelInDB
}

func newSingleUTXOExecutionFixture(t *testing.T) *singleUTXOExecutionFixture {
	t.Helper()
	base := &channelHeartbeatTestClient{}
	m := newChannelHeartbeatTestManager(t, base)
	m.resetResvMapsLocked()
	db := NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("fixture DB")
	}
	t.Cleanup(func() { db.Close() })
	m.db = db
	m.status = &Status{SyncHeight: 1000}
	signatures := new(int)
	m.wallet = &singleUTXOCountingWallet{Wallet: m.wallet, signatures: signatures}
	channelID, err := m.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	script, err := GetPkScriptFromAddress(m.wallet.GetAddress())
	if err != nil {
		t.Fatal(err)
	}
	makeTx := func(tag byte, value int64) *wire.MsgTx {
		tx := wire.NewMsgTx(wire.TxVersion)
		var hash chainhash.Hash
		hash[0] = tag
		tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&hash, 0), nil, nil))
		tx.AddTxOut(wire.NewTxOut(value, script))
		return tx
	}
	parent, source := makeTx(111, NewFeeConfig().MinCapacity()-1), makeTx(112, 200000)
	stored := NewChannelInDB()
	stored.ChannelId, stored.Status = channelID, CS_CLOSED
	stored.IsInitiator = true
	stored.LocalWalletId = m.wallet.GetId()
	stored.LocalChanCfg.WalletId = m.wallet.GetSubAccount()
	stored.LocalChanCfg.PaymentKey = m.wallet.GetPaymentPubKey()
	stored.RemoteChanCfg.PaymentKey = m.serverNode.Pubkey
	stored.RemoteCommitment = NewChannelCommitment()
	stored.RemoteCommitment.CommitTx = parent
	raw, err := EncodeToBytes(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Write([]byte(GetChannelKey(channelID)), raw); err != nil {
		t.Fatal(err)
	}
	input := indexer.GenerateTxOutput(source, 0)
	probe := &singleUTXOTopupQuoteIndexer{address: m.wallet.GetAddress(), output: &indexerwire.TxOutputInfo{OutPoint: input.OutPointStr, Value: input.Value(), PkScript: script, UtxoId: indexer.ToUtxoId(1, 0, 0)}}
	parentRaw, _ := EncodeMsgTx(parent)
	rpc := &singleUTXOExecutionRPC{singleUTXOTopupQuoteIndexer: probe, manager: m, channelID: channelID, parent: parent, source: source, outputs: map[string]*TxOutput{input.OutPointStr: input}, raw: map[string]string{parent.TxID(): parentRaw}, acknowledged: true, parentConfirmed: true}
	m.l1IndexerClient = &IndexerRPCClientMgr{active: rpc}
	m.l2IndexerClient = &IndexerRPCClientMgr{active: rpc}
	m.utxoLockerL1 = NewUtxoLocker(db, rpc, L1_NETWORK_BITCOIN)
	m.utxoLockerL1.Init()
	gate := &singleUTXOTopupGateClient{response: &wwire.PingResp{BaseResp: wwire.BaseResp{Code: 0}, PingResponse: &wwire.PingResponse{NextAction: "rebuild", ActionParam: channelID}}}
	m.serverNode.client = gate
	return &singleUTXOExecutionFixture{m, rpc, gate, signatures, channelID, stored}
}
func (f *singleUTXOExecutionFixture) quote(t *testing.T) string {
	t.Helper()
	q, err := singleUTXOTopupAPIForTest(t, f.manager).QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
func TestSTPSingleUTXOTopupQuoteNeverSignsOrReserves(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.quote(t)
	f.quote(t)
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("unconfirmed quote changed wallet state or signed a transaction")
	}
}
func TestSTPSingleUTXOTopupApproveSendsOneFullOutput(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	quote := f.quote(t)
	result, err := singleUTXOTopupAPIForTest(t, f.manager).ConfirmClosedChannelSingleUTXOTopup(quote)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || len(f.rpc.broadcasts) != 1 || *f.signatures != 1 || f.rpc.durableError != nil {
		t.Fatalf("ordinary approval result=%s sends=%d signs=%d durable=%v", result, len(f.rpc.broadcasts), *f.signatures, f.rpc.durableError)
	}
	tx := f.rpc.broadcasts[0]
	script, _ := GetPkScriptFromAddress(f.channelID)
	if len(tx.TxIn) != 1 || tx.TxIn[0].PreviousOutPoint.Hash != f.rpc.source.TxHash() || len(tx.TxOut) < 1 || tx.TxOut[0].Value != NewFeeConfig().MinCapacity() || !bytes.Equal(tx.TxOut[0].PkScript, script) {
		t.Fatal("top-up mixed old funds or did not create one full funding output")
	}
}
func TestSTPSingleUTXOTopupQuoteChangeNeedsFreshApproval(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	quote := f.quote(t)
	f.rpc.output.Value++
	if _, err := singleUTXOTopupAPIForTest(t, f.manager).ConfirmClosedChannelSingleUTXOTopup(quote); err == nil {
		t.Fatal("changed selected input accepted under stale quote")
	}
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("stale quote signed, reserved or broadcast")
	}
}
func TestSTPSingleUTXOTopupLostAckNeverPaysAgain(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.rpc.acknowledged = false
	quote := f.quote(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	first, err := api.ConfirmClosedChannelSingleUTXOTopup(quote)
	if err != nil {
		t.Fatal(err)
	}
	again, err := api.ConfirmClosedChannelSingleUTXOTopup(quote)
	if err != nil {
		t.Fatal(err)
	}
	if first != again || len(f.rpc.broadcasts) != 1 || *f.signatures != 1 || f.rpc.durableError != nil {
		t.Fatalf("uncertain send replaced original transaction: %q/%q sends=%d signs=%d durable=%v", first, again, len(f.rpc.broadcasts), *f.signatures, f.rpc.durableError)
	}
	if resumed, quoteErr := api.QuoteClosedChannelSingleUTXOTopup(5); quoteErr == nil && resumed != quote {
		t.Fatal("uncertain original transaction allowed a different top-up quote")
	}
}
func TestSTPSingleUTXOTopupConfirmedHookPinsExactOutput(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	quote := f.quote(t)
	if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); err != nil {
		t.Fatal(err)
	}
	hook, ok := interface{}(f.manager).(interface {
		closedChannelSingleUTXOTopupFunding(common.Wallet, *LocalActionPerformData, *closedChannelIntent) (*TxOutput, error)
	})
	if !ok {
		t.Fatal("confirmed exact-root hook is missing")
	}
	for _, item := range f.manager.GetLocalActionReservations() {
		owner, ok := item.(*LocalActionPerformData)
		if !ok || owner.Action != "closedchannelrecovery" {
			continue
		}
		intent, err := loadClosedChannelIntent(owner)
		if err != nil {
			t.Fatal(err)
		}
		if output, err := hook.closedChannelSingleUTXOTopupFunding(f.manager.wallet.Clone(), owner, intent); err == nil || output != nil {
			t.Fatal("unconfirmed top-up was admitted as funding")
		}
		f.rpc.confirmed = true
		output, err := hook.closedChannelSingleUTXOTopupFunding(f.manager.wallet.Clone(), owner, intent)
		if err != nil {
			t.Fatal(err)
		}
		wanted := fmt.Sprintf("%s:0", f.rpc.broadcasts[0].TxID())
		if output == nil || output.OutPointStr != wanted {
			t.Fatalf("funding root=%v want %s", output, wanted)
		}
		return
	}
	t.Fatal("original durable owner missing")
}
func TestSTPSingleUTXOTopupOfflineHintRequiresCompleteConfirmedLocalPackage(t *testing.T) {
	for _, kind := range []string{"missing", "invalid", "unconfirmed", "native", "valid"} {
		t.Run(kind, func(t *testing.T) {
			f := newSingleUTXOExecutionFixture(t)
			api := singleUTXOTopupAPIForTest(t, f.manager)
			f.gate.err = errors.New("offline")
			if kind != "missing" {
				other := wire.NewMsgTx(wire.TxVersion)
				other.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&[]chainhash.Hash{f.rpc.parent.TxHash()}[0], 0), nil, nil))
				other.AddTxOut(wire.NewTxOut(f.rpc.parent.TxOut[0].Value-500, f.rpc.parent.TxOut[0].PkScript))
				if kind != "invalid" {
					fetcher := txscript.NewMultiPrevOutFetcher(nil)
					AddTxOutputsToPrevFetcher(fetcher, f.rpc.parent)
					var err error
					other, err = SignTxWithWallet(f.manager.wallet, other, fetcher)
					if err != nil {
						t.Fatal(err)
					}
					*f.signatures = 0
				}
				f.stored.RemoteCommitment.Others = []*CommitmentTx{{CommitTx: other}}
				raw, _ := EncodeToBytes(f.stored)
				if err := f.manager.db.Write([]byte(GetChannelKey(f.channelID)), raw); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "unconfirmed" {
				f.rpc.parentConfirmed = false
			}
			if kind == "native" {
				f.stored.AssetFunding = map[string][]byte{"rgb11": []byte("fixture native inventory classification")}
				raw, _ := EncodeToBytes(f.stored)
				if err := f.manager.db.Write([]byte(GetChannelKey(f.channelID)), raw); err != nil {
					t.Fatal(err)
				}
			}
			q, err := api.QuoteClosedChannelSingleUTXOTopup(5)
			if err == nil || q != "" {
				t.Fatal("offline returned spendable quote")
			}
			suggested := strings.Contains(strings.ToLower(err.Error()), "broadcast your local others")
			if suggested != (kind == "valid") {
				t.Fatalf("offline %s hint inappropriate: %v", kind, err)
			}
			if len(f.rpc.broadcasts) != 0 || *f.signatures != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
				t.Fatal("offline hint spent, signed or created intent")
			}
		})
	}
}

func TestSTPSingleUTXOTopupStillActiveChannelNeverSuggestsWithdrawal(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.gate.err = errors.New("offline")
	active := &Channel{ChannelInDB: *f.stored, manager: f.manager, localWallet: f.manager.wallet.Clone()}
	active.Status = CS_READY
	f.manager.channelMap[f.channelID] = active
	quote, err := singleUTXOTopupAPIForTest(t, f.manager).QuoteClosedChannelSingleUTXOTopup(5)
	if err == nil || quote != "" || strings.Contains(strings.ToLower(err.Error()), "others") {
		t.Fatalf("active channel must stay active without withdrawal suggestion: %q %v", quote, err)
	}
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("active channel hint mutated wallet")
	}
}
