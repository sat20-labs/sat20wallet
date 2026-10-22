package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

// This transport records real signed Bitcoin bytes, but deliberately withholds
// acknowledgement/confirmation. Protocol consensus is exercised by the RGB
// integration tests; these tests inject failures at the durable journal boundary.
type nativeRecoveryRPC struct {
	IndexerRPCClient
	raw             map[string]string
	confirmed       map[string]bool
	height          int
	broadcastError  error
	broadcasts      []*wire.MsgTx
	beforeBroadcast func(*wire.MsgTx)
}

func (c *nativeRecoveryRPC) GetRawTx(id string) (string, error) {
	if raw := c.raw[id]; raw != "" {
		return raw, nil
	}
	return "", fmt.Errorf("transaction unavailable")
}
func (c *nativeRecoveryRPC) IsTxConfirmed(id string) bool    { return c.confirmed[id] }
func (c *nativeRecoveryRPC) GetSyncHeight() int              { return c.height }
func (c *nativeRecoveryRPC) GetTxHeight(string) (int, error) { return c.height, nil }
func (c *nativeRecoveryRPC) BroadCastTxs(txs []*wire.MsgTx) error {
	for _, tx := range txs {
		if c.beforeBroadcast != nil {
			c.beforeBroadcast(tx)
		}
		c.broadcasts = append(c.broadcasts, tx.Copy())
	}
	return c.broadcastError
}

type nativeRecoveryFixture struct {
	directory string
	db        *nativeSettlementFaultDB
	manager   *Manager
	channel   *Channel
	resv      *ClosingReservation
	rpc       *nativeRecoveryRPC
}

func newNativeRecoveryFixture(t *testing.T) *nativeRecoveryFixture {
	t.Helper()
	oldTesting := ENABLE_TESTING
	ENABLE_TESTING = false
	t.Cleanup(func() { ENABLE_TESTING = oldTesting })
	f := &nativeRecoveryFixture{directory: t.TempDir()}
	f.db = &nativeSettlementFaultDB{KVDB: NewKVDB(f.directory)}
	if f.db.KVDB == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = f.db.Close() })
	walletValue := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "", GetChainParam())
	seed := newMemoryKVDB()
	stored := savePendingFundingFixture(t, seed, walletValue, "nativecrashchannel", 901)
	stored.Status = CS_READY
	stored.AssetFunding = map[string][]byte{"rgb11": []byte("durable opaque funding inventory")}
	f.manager = &Manager{
		db: f.db, wallet: walletValue, channelMap: make(map[string]*Channel), nodeMap: make(map[string]string),
		walletInfoMap: map[int64]*WalletInfo{walletValue.GetId(): {WalletInDB: WalletInDB{Id: walletValue.GetId()}, Wallet: walletValue}},
	}
	f.manager.resetResvMapsLocked()
	f.channel = &Channel{ChannelInDB: *stored, manager: f.manager, localWallet: walletValue}
	f.channel.IsInitiator = true
	f.channel.StaticMerkleRoot = f.channel.CalcStaticMerkleRoot()
	f.manager.channelMap[f.channel.ChannelId] = f.channel
	if err := f.manager.SaveChannelToDB(f.channel); err != nil {
		t.Fatal(err)
	}
	f.rpc = &nativeRecoveryRPC{raw: make(map[string]string), confirmed: make(map[string]bool), height: 200,
		broadcastError: errors.New("injected transport acknowledgement loss")}
	f.manager.l1IndexerClient = &IndexerRPCClientMgr{active: f.rpc}
	f.manager.utxoLockerL1 = NewUtxoLocker(f.db, f.rpc, L1_NETWORK_BITCOIN)
	f.resv = &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(902, true, 0, walletValue), ChannelId: f.channel.ChannelId,
		CloseTx: f.channel.LocalCommitment.CommitTx.Copy(), FeeRate: 1,
		AssetProofs: map[string][]byte{"rgb11": []byte("opaque proof must survive before validation")},
	}, Channel: f.channel}
	f.resv.PreTxs = []*wire.MsgTx{f.resv.CloseTx}
	return f
}

func (f *nativeRecoveryFixture) restart(t *testing.T) {
	t.Helper()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db.KVDB = NewKVDB(f.directory)
	if f.db.KVDB == nil {
		t.Fatal("reopen NewKVDB failed")
	}
	f.db.failOperation, f.db.operations, f.db.failFlush = 0, 0, false
	previous := f.manager
	f.manager = &Manager{
		db: f.db, wallet: previous.wallet, walletInfoMap: previous.walletInfoMap,
		channelMap: make(map[string]*Channel), nodeMap: make(map[string]string),
		l1IndexerClient: &IndexerRPCClientMgr{active: f.rpc},
	}
	f.manager.utxoLockerL1 = NewUtxoLocker(f.db, f.rpc, L1_NETWORK_BITCOIN)
	f.manager.resetResvMapsLocked()
	for _, resv := range mustLoadAllResv(t, f.db) {
		f.manager.AddResv(resv)
	}
	f.manager.rehydratePendingClosingRuntime()
	f.resv = f.manager.GetClosingReservations()[902]
	if f.resv == nil || f.resv.Channel == nil {
		t.Fatal("durable native close task was not reconstructed after restart")
	}
	f.channel = f.resv.Channel
}

func assertNativeRecoveryOnDisk(t *testing.T, f *nativeRecoveryFixture, phase ChannelStatus) {
	t.Helper()
	raw, err := f.db.Read([]byte(GetChannelKey(f.channel.ChannelId)))
	if err != nil {
		t.Fatal(err)
	}
	var channel ChannelInDB
	if err := DecodeFromBytes(raw, &channel); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReservation(f.db, f.manager, RESV_TYPE_CLOSE, f.resv.Id)
	if err != nil {
		t.Fatal(err)
	}
	resv := loaded.(*ClosingReservation)
	if channel.Status != phase || channel.UpdateTime != f.resv.Id || resv.Status != ResvStatus(phase) ||
		channel.ClosingTx.TxID() != f.resv.CloseTx.TxID() || !equalSTPBitcoinChain(resv.PreTxs, f.resv.PreTxs) ||
		!equalSTPAssetProofs(resv.AssetProofs, f.resv.AssetProofs) {
		t.Fatal("native close channel, full signed package and proof were not committed together")
	}
}

func TestNativeForceCloseAtomicBeforeBroadcast(t *testing.T) {
	for _, failure := range []struct {
		name      string
		operation int
		flush     bool
	}{
		{"channel write", 1, false}, {"reservation write", 2, false}, {"commit", 0, true},
	} {
		t.Run(failure.name, func(t *testing.T) {
			f := newNativeRecoveryFixture(t)
			before, _ := f.db.Read([]byte(GetChannelKey(f.channel.ChannelId)))
			f.db.failOperation, f.db.failFlush = failure.operation, failure.flush
			if err := f.manager.startNativeForceCloseLocked(f.resv); !errors.Is(err, errNativeSettlementWrite) {
				t.Fatalf("write failure was not reported: %v", err)
			}
			after, _ := f.db.Read([]byte(GetChannelKey(f.channel.ChannelId)))
			if !bytes.Equal(before, after) || f.channel.Status != CS_READY || len(f.rpc.broadcasts) != 0 || len(f.manager.GetClosingReservations()) != 0 {
				t.Fatal("failed durable prepare changed READY state or reached the network")
			}
			if _, err := f.db.Read([]byte(GetResvKey(RESV_TYPE_CLOSE, 902))); !errors.Is(err, indexer.ErrKeyNotFound) {
				t.Fatalf("partial reservation survived failed atomic prepare: %v", err)
			}
		})
	}
	// A successful durable prepare followed by a transport failure must reload
	// as the exact pending package, never as a usable old READY channel.
	f := newNativeRecoveryFixture(t)
	f.rpc.beforeBroadcast = func(*wire.MsgTx) { assertNativeRecoveryOnDisk(t, f, CS_CLOSE_FORCELY_BROADCASTED) }
	if err := f.manager.startNativeForceCloseLocked(f.resv); !errors.Is(err, f.rpc.broadcastError) {
		t.Fatalf("expected acknowledgement loss after prepare: %v", err)
	}
	if f.manager.GetChannel(f.channel.ChannelId) != nil || len(f.rpc.broadcasts) != 1 {
		t.Fatal("durably closing channel remained available for new signing")
	}
	original, proof := f.resv.CloseTx.Copy(), cloneSTPAssetProofs(f.resv.AssetProofs)
	f.restart(t)
	assertNativeRecoveryOnDisk(t, f, CS_CLOSE_FORCELY_BROADCASTED)
	if f.manager.GetChannel(f.channel.ChannelId) != nil || f.channel.Status == CS_READY || !equalSTPAssetProofs(proof, f.resv.AssetProofs) {
		t.Fatal("restart exposed stale READY state or lost the proof")
	}
	f.rpc.height += 1000
	if handled, err := f.manager.RetryNativeForceClose(f.resv); !handled || !errors.Is(err, f.rpc.broadcastError) {
		t.Fatalf("persisted native close was not retried: handled=%v err=%v", handled, err)
	}
	if len(f.rpc.broadcasts) != 2 || !equalSTPBitcoinChain([]*wire.MsgTx{original}, []*wire.MsgTx{f.rpc.broadcasts[1]}) {
		t.Fatal("restart rebuilt the close transaction instead of replaying the frozen bytes")
	}
}

func nativeRecoverySignedSweep(t *testing.T, f *nativeRecoveryFixture) *SignedSweepTxPackage {
	t.Helper()
	script, err := GetP2TRpkScript(f.manager.wallet.GetPaymentPubKey())
	if err != nil {
		t.Fatal(err)
	}
	parent := minimalPersistedCommitmentTx(77)
	parent.TxOut[0] = wire.NewTxOut(30_000, script)
	f.channel.LocalCommitment.CommitTx = parent
	sweep := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: parent.TxHash(), Index: 0}
	sweep.AddTxIn(wire.NewTxIn(&point, nil, nil))
	sweep.AddTxOut(wire.NewTxOut(28_000, script))
	sweep.LockTime = 200
	fetcher := txscript.NewMultiPrevOutFetcher(map[wire.OutPoint]*wire.TxOut{point: parent.TxOut[0]})
	sweep, err = SignTxWithWallet(f.manager.wallet, sweep, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifySignedTx(sweep, fetcher); err != nil {
		t.Fatal(err)
	}
	return &SignedSweepTxPackage{SweepTx: sweep, Txs: []*wire.MsgTx{sweep}, PrevFetcher: fetcher,
		CommitTxId: parent.TxID(), SweepTxId: sweep.TxID(), Signed: true, Verified: true, Broadcastable: true}
}

func TestNativeForceCloseSweepReplaysExactSignedBytesAfterRestart(t *testing.T) {
	f := newNativeRecoveryFixture(t)
	pkg := nativeRecoverySignedSweep(t, f)
	f.resv.CloseTx, f.resv.PreTxs, f.resv.AssetProofs = f.channel.LocalCommitment.CommitTx, nil, nil
	if err := f.manager.persistNativeForceCloseLocked(f.resv, f.resv.ClosingDataInDB, CS_CLOSE_FORCELY_CONFIRMED); err != nil {
		t.Fatal(err)
	}
	f.db.failFlush = true
	if handled, err := f.manager.HandleNativeForceCloseSweep(f.resv, pkg); !handled || !errors.Is(err, errNativeSettlementWrite) {
		t.Fatalf("sweep persistence failure was not reported: %v %v", handled, err)
	}
	if len(f.rpc.broadcasts) != 0 || f.channel.Status != CS_CLOSE_FORCELY_CONFIRMED {
		t.Fatal("sweep broadcast preceded its durable package")
	}
	f.db.failFlush = false
	f.rpc.beforeBroadcast = func(*wire.MsgTx) { assertNativeRecoveryOnDisk(t, f, CS_CLOSE_FORCELY_SWEEP_BROADCASTED) }
	if _, err := f.manager.HandleNativeForceCloseSweep(f.resv, pkg); !errors.Is(err, f.rpc.broadcastError) {
		t.Fatal(err)
	}
	f.restart(t)
	f.rpc.height += 5000
	if handled, err := f.manager.RetryNativeForceClose(f.resv); !handled || !errors.Is(err, f.rpc.broadcastError) {
		t.Fatalf("retry: %v %v", handled, err)
	}
	if len(f.rpc.broadcasts) != 2 || !equalSTPBitcoinChain([]*wire.MsgTx{pkg.SweepTx}, []*wire.MsgTx{f.rpc.broadcasts[1]}) {
		t.Fatal("CSV sweep signatures, locktime or output commitment changed after restart")
	}
	next := f.resv.ClosingDataInDB
	next.CloseTx = next.CloseTx.Copy()
	next.CloseTx.LockTime++
	next.PreTxs = []*wire.MsgTx{next.CloseTx}
	if err := f.manager.persistNativeForceCloseLocked(f.resv, next, CS_CLOSE_FORCELY_SWEEP_BROADCASTED); err == nil {
		t.Fatal("a replacement native sweep overwrote the frozen broadcast package")
	}
}
