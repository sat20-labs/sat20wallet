package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Shared signed-package fixtures, not asset-consensus or attack tests.
// Every Bitcoin transaction has real 2-of-2 signatures and checked dependencies.
// Lifecycle tests distinguish a local force-close package from client recovery
// after the exact remote server parent confirms; transport is local.
type reservedOthersFixture struct {
	*nativeRecoveryFixture
	left, right    *btcec.PrivateKey
	redeem, script []byte
	seed           wire.OutPoint
	seedOutput     *wire.TxOut
	expected       []*wire.MsgTx
}

func newReservedOthersFixture(t *testing.T) *reservedOthersFixture {
	t.Helper()
	f := &reservedOthersFixture{nativeRecoveryFixture: newNativeRecoveryFixture(t)}
	f.left, _ = btcec.PrivKeyFromBytes([]byte{1})
	f.right, _ = btcec.PrivKeyFromBytes([]byte{2})
	var err error
	f.redeem, f.script, err = GetP2WSHscript(f.left.PubKey().SerializeCompressed(), f.right.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	f.seed = wire.OutPoint{Hash: chainhash.Hash{41}}
	f.seedOutput = wire.NewTxOut(120_000, f.script)
	prev := f.spend(t, f.seed, f.seedOutput, 110_000)
	commit := f.spend(t, wire.OutPoint{Hash: prev.TxHash()}, prev.TxOut[0], 55_000, 45_000)
	next := f.spend(t, wire.OutPoint{Hash: commit.TxHash()}, commit.TxOut[0], 50_000)
	f.expected = []*wire.MsgTx{prev, commit, next}
	previousRegistry := stpAssetProtocols
	protocols := append([]STPAssetProtocol{}, previousRegistry.ordered...)
	protocols = append(protocols, otherJournalTestProtocol{})
	stpAssetProtocols, err = NewSTPAssetRegistry(protocols...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stpAssetProtocols = previousRegistry })
	f.channel.FundingUtxos = make(map[AssetName][]*TxOutput)
	f.channel.NativeParents, f.channel.NativePrevouts = nil, nil
	f.channel.LocalChanCfg.PaymentKey = f.left.PubKey()
	f.channel.RemoteChanCfg.PaymentKey = f.right.PubKey()
	f.channel.RedeemScript = append([]byte(nil), f.redeem...)
	f.channel.ChanPoint = &TxOutput{OutPointStr: f.seed.String(), OutValue: *f.seedOutput}
	f.channel.AssetFunding = map[string][]byte{"other-journal-test": []byte("inventory")}
	commitHash := commit.TxHash()
	peerPosition := GetCurrSignPosition2(f.right.PubKey().SerializeCompressed(), f.left.PubKey().SerializeCompressed())
	f.channel.LocalCommitment = NewChannelCommitment()
	f.channel.LocalCommitment.PrevTxs = []*wire.MsgTx{prev}
	f.channel.LocalCommitment.CommitTx = commit
	f.channel.LocalCommitment.CommitSig = [][]byte{append([]byte(nil), commit.TxIn[0].Witness[peerPosition+1]...)}
	f.channel.LocalCommitment.NextTxs = []*wire.MsgTx{next}
	f.channel.LocalCommitment.AssetProofs = map[string][]byte{"other-journal-test": append([]byte(nil), commitHash[:]...)}
	f.channel.LocalCommitment.DeAnchorTx = swire.NewMsgTx(2)
	f.rpc.broadcastError = nil
	// L2 confirmation is a transport fixture only. No L2 transaction is sent.
	f.rpc.confirmed[f.channel.LocalCommitment.DeAnchorTx.TxID()] = true
	f.manager.l2IndexerClient = &IndexerRPCClientMgr{active: f.rpc}
	f.channel.StaticMerkleRoot = f.channel.CalcStaticMerkleRoot()
	f.checkChain(t, f.expected, map[wire.OutPoint]*wire.TxOut{f.seed: f.seedOutput})
	return f
}

func (f *reservedOthersFixture) spend(t *testing.T, point wire.OutPoint, previous *wire.TxOut, amounts ...int64) *wire.MsgTx {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
	for _, amount := range amounts {
		tx.AddTxOut(wire.NewTxOut(amount, append([]byte(nil), f.script...)))
	}
	fetcher := txscript.NewMultiPrevOutFetcher(map[wire.OutPoint]*wire.TxOut{point: previous})
	hashes := txscript.NewTxSigHashes(tx, fetcher)
	tx.TxIn[0].Witness = wire.TxWitness{nil, nil, nil, append([]byte(nil), f.redeem...)}
	for _, key := range []*btcec.PrivateKey{f.left, f.right} {
		sig, err := txscript.RawTxInWitnessSignature(tx, hashes, 0, previous.Value, f.redeem, txscript.SigHashAll, key)
		if err != nil {
			t.Fatal(err)
		}
		peer := f.right.PubKey()
		if key == f.right {
			peer = f.left.PubKey()
		}
		position := GetCurrSignPosition2(key.PubKey().SerializeCompressed(), peer.SerializeCompressed())
		tx.TxIn[0].Witness[position+1] = sig
	}
	if err := VerifySignedTx(tx, fetcher); err != nil {
		t.Fatalf("diagnostic fixture signature: %v", err)
	}
	return tx
}

func (f *reservedOthersFixture) checkChain(t *testing.T, txs []*wire.MsgTx, roots map[wire.OutPoint]*wire.TxOut) {
	t.Helper()
	fetcher := txscript.NewMultiPrevOutFetcher(roots)
	spent := make(map[wire.OutPoint]bool)
	for i, tx := range txs {
		for _, input := range tx.TxIn {
			if spent[input.PreviousOutPoint] {
				t.Fatalf("fixture contains a competing spend at transaction %d", i)
			}
			spent[input.PreviousOutPoint] = true
			if fetcher.FetchPrevOutput(input.PreviousOutPoint) == nil {
				t.Fatalf("fixture dependency unavailable at transaction %d", i)
			}
		}
		if err := VerifySignedTx(tx, fetcher); err != nil {
			t.Fatalf("fixture transaction %d lost its full signature: %v", i, err)
		}
		AddTxOutputsToPrevFetcher(fetcher, tx)
	}
}

func reservedOthersRaw(t *testing.T, txs []*wire.MsgTx) []string {
	t.Helper()
	result := make([]string, len(txs))
	for i, tx := range txs {
		var err error
		result[i], err = EncodeMsgTx(tx)
		if err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func reservedOthersAssertPackage(t *testing.T, actual, expected []*wire.MsgTx, boundary string) {
	t.Helper()
	if !equalSTPBitcoinChain(actual, expected) {
		ids := func(txs []*wire.MsgTx) []string {
			result := make([]string, len(txs))
			for i, tx := range txs {
				result[i] = tx.TxID()
			}
			return result
		}
		t.Errorf("%s must contain exactly selected PrevTxs -> CommitTx -> NextTxs with original witnesses; got %v, want %v", boundary, ids(actual), ids(expected))
	}
}

// A real predecessor and successor must survive a database close/reopen with
// exact witness bytes. Removing all follow-ups cannot satisfy this control.
func TestSTPDefaultDependenciesSurviveDatabaseReopen(t *testing.T) {
	f := newReservedOthersFixture(t)
	_, _, err := f.manager.ForcelyCloseChannel(f.channel, 1)
	if err != nil {
		t.Fatal(err)
	}
	var pending *ClosingReservation
	for _, candidate := range f.manager.GetClosingReservations() {
		pending = candidate
	}
	if pending == nil {
		t.Fatal("signed default package was not saved")
	}
	key := []byte(GetResvKey(RESV_TYPE_CLOSE, pending.Id))
	before, err := f.db.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db.KVDB = NewKVDB(f.directory)
	if f.db.KVDB == nil {
		t.Fatal("could not reopen diagnostic database")
	}
	after, err := f.db.Read(key)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("database reopen changed original signed pending bytes: %v", err)
	}
	loaded, err := LoadReservation(f.db, f.manager, RESV_TYPE_CLOSE, pending.Id)
	if err != nil {
		t.Fatal(err)
	}
	restored, ok := loaded.(*ClosingReservation)
	if !ok {
		t.Fatalf("reloaded %T instead of closing reservation", loaded)
	}
	packageTxs, err := nativeForceClosePackage(&restored.ClosingDataInDB)
	if err != nil {
		t.Fatal(err)
	}
	reservedOthersAssertPackage(t, packageTxs, f.expected, "restored signed default package")
	f.checkChain(t, packageTxs, map[wire.OutPoint]*wire.TxOut{f.seed: f.seedOutput})
}
