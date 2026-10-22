package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// The protocol sidecar here authenticates the transaction identity so these
// tests isolate the shared journal boundary. Bitcoin funding and Other sweep
// authorization use real 2-of-2 signatures; RGB consensus has separate E2E tests.
type otherJournalTestProtocol struct{ btcSTPProtocol }

func (otherJournalTestProtocol) Protocol() string { return "other-journal-test" }
func (otherJournalTestProtocol) VerifyChannelJournalContext(_ *Manager, channel *Channel) error {
	if !bytes.Equal(channel.AssetFunding["other-journal-test"], []byte("inventory")) {
		return fmt.Errorf("journal test inventory changed")
	}
	return nil
}
func (otherJournalTestProtocol) VerifyChannelProof(_ *Manager, _ *Channel, tx *wire.MsgTx, proof []byte, _ STPAssetOperation) ([]*TxOutput, error) {
	hash := tx.TxHash()
	if !bytes.Equal(proof, hash[:]) {
		return nil, fmt.Errorf("journal test proof changed")
	}
	return nil, nil
}
func (otherJournalTestProtocol) BuildChannelRecovery(*Manager, STPAssetRecoveryRequest) (*STPAssetRecoveryPlan, error) {
	return nil, fmt.Errorf("journal fixture does not construct asset recovery")
}

type otherJournalFixture struct {
	directory string
	db        *nativeSettlementFaultDB
	manager   *Manager
	channel   *Channel
	packages  [][]string
}

func newOtherJournalFixture(t *testing.T, localRecord bool, halfSigned ...bool) *otherJournalFixture {
	t.Helper()
	_, channel, packages := nativeOtherExchangeFixture(t)
	previousRegistry := stpAssetProtocols
	protocols := append([]STPAssetProtocol{}, previousRegistry.ordered...)
	protocols = append(protocols, otherJournalTestProtocol{})
	var err error
	stpAssetProtocols, err = NewSTPAssetRegistry(protocols...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stpAssetProtocols = previousRegistry })
	previousTesting := ENABLE_TESTING
	ENABLE_TESTING = false
	t.Cleanup(func() { ENABLE_TESTING = previousTesting })
	f := &otherJournalFixture{directory: t.TempDir(), channel: channel, packages: packages}
	f.db = &nativeSettlementFaultDB{KVDB: NewKVDB(f.directory)}
	if f.db.KVDB == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = f.db.Close() })
	f.manager = &Manager{db: f.db}
	channel.ChannelId, channel.CommitHeight, channel.Status = "other-journal-channel", 7, CS_READY
	channel.AssetEntropy[0] = 3
	channel.AssetFunding = map[string][]byte{"other-journal-test": []byte("inventory")}
	channel.FundingUtxos = make(map[AssetName][]*TxOutput)
	commit := channel.RemoteCommitment.CommitTx
	channel.ChanPoint = &TxOutput{OutPointStr: commit.TxIn[0].PreviousOutPoint.String(),
		OutValue: *wire.NewTxOut(12000, append([]byte(nil), commit.TxOut[0].PkScript...))}
	fetcher := channel.GetCommitmentPrefetchor()
	hashes := txscript.NewTxSigHashes(commit, fetcher)
	left, _ := btcec.PrivKeyFromBytes([]byte{1})
	right, _ := btcec.PrivKeyFromBytes([]byte{2})
	redeem := channel.RemoteCommitment.Others[0].CommitTx.TxIn[0].Witness[3]
	channel.LocalChanCfg.PaymentKey, channel.RemoteChanCfg.PaymentKey = left.PubKey(), right.PubKey()
	channel.RedeemScript = append([]byte(nil), redeem...)
	commit.TxIn[0].Witness = wire.TxWitness{nil, nil, nil, redeem}
	for _, key := range []*btcec.PrivateKey{left, right} {
		sig, err := txscript.RawTxInWitnessSignature(commit, hashes, 0, 12000, redeem, txscript.SigHashAll, key)
		if err != nil {
			t.Fatal(err)
		}
		peer := right.PubKey().SerializeCompressed()
		if key == right {
			peer = left.PubKey().SerializeCompressed()
		}
		pos := GetCurrSignPosition2(key.PubKey().SerializeCompressed(), peer)
		commit.TxIn[0].Witness[pos+1] = sig
	}
	if err := VerifySignedTx(commit, fetcher); err != nil {
		t.Fatal(err)
	}
	channel.LocalCommitment.CommitTx = commit.Copy()
	if len(halfSigned) != 0 && halfSigned[0] {
		peerPos := GetCurrSignPosition2(right.PubKey().SerializeCompressed(), left.PubKey().SerializeCompressed())
		channel.LocalCommitment.CommitSig = [][]byte{append([]byte(nil), commit.TxIn[0].Witness[peerPos+1]...)}
		if err := CleanPeerSig(redeem, left.PubKey().SerializeCompressed(), right.PubKey().SerializeCompressed(),
			channel.LocalCommitment.CommitTx, fetcher); err != nil {
			t.Fatal(err)
		}
		if err := VerifySignedTx(channel.LocalCommitment.CommitTx, fetcher); err == nil {
			t.Fatal("half-signed fixture still has full Bitcoin authorization")
		}
	}
	for _, commitment := range []*ChannelCommitment{channel.LocalCommitment, channel.RemoteCommitment} {
		hash := commitment.CommitTx.TxHash()
		commitment.AssetProofs = map[string][]byte{"other-journal-test": append([]byte(nil), hash[:]...)}
		commitment.DeAnchorTx = swire.NewMsgTx(2)
		commitment.RecoveryFeeIndexes = []uint32{0}
	}
	stored, err := EncodeToBytes(&channel.ChannelInDB)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Write([]byte(GetChannelKey(channel.ChannelId)), stored); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.persistSTPNativeCommitment(channel, channel.RemoteCommitment, "remote"); err != nil {
		t.Fatal(err)
	}
	if localRecord {
		if err := f.manager.persistSTPNativeCommitment(channel, channel.LocalCommitment, "local"); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.markSTPNativeRevocationReady(channel); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *otherJournalFixture) read(t *testing.T, role string) ([]byte, stpNativeCommitmentRecord) {
	t.Helper()
	raw, err := f.db.Read(stpNativeCommitmentKey(f.channel, role))
	if err != nil {
		t.Fatal(err)
	}
	var record stpNativeCommitmentRecord
	if err := DecodeFromBytes(raw, &record); err != nil {
		t.Fatal(err)
	}
	return raw, record
}

func (f *otherJournalFixture) reopen(t *testing.T) *ChannelInDB {
	t.Helper()
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db.KVDB = NewKVDB(f.directory)
	if f.db.KVDB == nil {
		t.Fatal("reopen NewKVDB failed")
	}
	f.db.failOperation, f.db.failFlush, f.db.beforeFlush = 0, false, nil
	f.manager = &Manager{db: f.db}
	raw, err := f.db.Read([]byte(GetChannelKey(f.channel.ChannelId)))
	if err != nil {
		t.Fatal(err)
	}
	var stored ChannelInDB
	if err := DecodeFromBytes(raw, &stored); err != nil {
		t.Fatal(err)
	}
	return &stored
}

func (f *otherJournalFixture) restart(t *testing.T) *ChannelInDB {
	t.Helper()
	stored := f.reopen(t)
	recovered, err := f.manager.LoadSTPNativeRevocationRecovery(stored)
	if err != nil {
		t.Fatal(err)
	}
	return recovered
}

func TestSTPNativeRecoveryReassemblesPersistedHalfSignedCommitment(t *testing.T) {
	f := newOtherJournalFixture(t, true, true)
	before, _ := f.read(t, "local")
	half, _ := EncodeMsgTx(f.channel.LocalCommitment.CommitTx)
	peer := append([]byte(nil), f.channel.LocalCommitment.CommitSig[0]...)
	recovered := f.restart(t)
	if f.manager.wallet != nil || recovered == nil || recovered.Status != CS_NATIVE_RECOVERY || recovered.CommitHeight != 8 {
		t.Fatal("recovery did not verify the saved signatures without wallet keys")
	}
	after, _ := f.read(t, "local")
	actual, _ := EncodeMsgTx(recovered.LocalCommitment.CommitTx)
	if !bytes.Equal(before, after) || actual != half || !bytes.Equal(recovered.LocalCommitment.CommitSig[0], peer) {
		t.Fatal("recovery changed the immutable journal or exported a fully signed commitment")
	}
	if err := VerifySignedTx(recovered.LocalCommitment.CommitTx, recovered.GetCommitmentPrefetchor()); err == nil {
		t.Fatal("loader removed the ordinary half-signed export boundary")
	}
	if err := verifySTPNativeRecoveryAuthorization(&Channel{ChannelInDB: *recovered}); err != nil {
		t.Fatalf("persisted signatures cannot independently authorize recovery: %v", err)
	}
}

func TestSTPNativeRecoveryRejectsMissingOrInvalidPersistedPeerSignature(t *testing.T) {
	for _, failure := range []string{"missing", "empty", "wrong", "count", "malformed-local-witness"} {
		t.Run(failure, func(t *testing.T) {
			f := newOtherJournalFixture(t, true, true)
			_, local := f.read(t, "local")
			switch failure {
			case "missing":
				local.Commitment.CommitSig = nil
			case "empty":
				local.Commitment.CommitSig[0] = nil
			case "wrong":
				local.Commitment.CommitSig[0][10] ^= 1
			case "count":
				local.Commitment.CommitSig = append(local.Commitment.CommitSig, []byte{1})
			case "malformed-local-witness":
				local.Commitment.CommitTx.TxIn[0].Witness = wire.TxWitness{{1}}
			}
			raw, err := EncodeToBytes(&local)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.Write(stpNativeCommitmentKey(f.channel, "local"), raw); err != nil {
				t.Fatal(err)
			}
			stored := f.reopen(t)
			if recovered, err := f.manager.LoadSTPNativeRevocationRecovery(stored); err == nil || recovered != nil {
				t.Fatal("invalid persisted authorization exposed a recoverable or READY channel")
			}
			after, _ := f.read(t, "local")
			if !bytes.Equal(raw, after) {
				t.Fatal("failed recovery rewrote the immutable signing obligation")
			}
		})
	}
}

func TestSTPNativeOtherJournalAtomicFailureAndRestart(t *testing.T) {
	for _, failure := range []struct {
		name      string
		operation int
		flush     bool
	}{{"remote-put", 1, false}, {"local-put", 2, false}, {"flush", 0, true}} {
		t.Run(failure.name, func(t *testing.T) {
			f := newOtherJournalFixture(t, true)
			beforeRemote, _ := f.read(t, "remote")
			beforeLocal, _ := f.read(t, "local")
			beforeTx, _ := EncodeMsgTx(f.channel.RemoteCommitment.Others[0].CommitTx)
			f.db.failOperation, f.db.failFlush = failure.operation, failure.flush
			if err := f.manager.acceptSTPNativeOtherTransactions(f.channel, f.packages); !errors.Is(err, errNativeSettlementWrite) {
				t.Fatalf("atomic journal failure was not reported: %v", err)
			}
			afterTx, _ := EncodeMsgTx(f.channel.RemoteCommitment.Others[0].CommitTx)
			if afterTx != beforeTx || f.db.commits != 0 {
				t.Fatal("failed atomic journal update published recovery signatures")
			}
			recovered := f.restart(t)
			afterRemote, _ := f.read(t, "remote")
			afterLocal, local := f.read(t, "local")
			if !bytes.Equal(beforeRemote, afterRemote) || !bytes.Equal(beforeLocal, afterLocal) || !local.RevokedPrevious {
				t.Fatal("a partial journal update or released-secret marker change survived restart")
			}
			if recovered == nil || recovered.Status != CS_NATIVE_RECOVERY || recovered.CommitHeight != 8 {
				t.Fatal("restart exposed the old READY channel after revocation")
			}
		})
	}
}

func TestSTPNativeOtherJournalReloadsCompletePeerRecovery(t *testing.T) {
	f := newOtherJournalFixture(t, true)
	beforeLocal, _ := f.read(t, "local")
	f.db.beforeFlush = func() {
		current, _ := f.read(t, "local")
		live, _ := EncodeMsgTx(f.channel.RemoteCommitment.Others[0].CommitTx)
		if !bytes.Equal(beforeLocal, current) || live == f.packages[0][0] {
			t.Fatal("recovery signatures became visible before the atomic commit")
		}
	}
	if err := f.manager.acceptSTPNativeOtherTransactions(f.channel, f.packages); err != nil {
		t.Fatal(err)
	}
	if f.db.operations != 2 || f.db.commits != 1 {
		t.Fatal("paired journals were not committed in one transaction")
	}
	recovered := f.restart(t)
	if recovered == nil || recovered.Status != CS_NATIVE_RECOVERY || recovered.CommitHeight != 8 {
		t.Fatal("revoked channel did not load its recovery candidate")
	}
	_, local := f.read(t, "local")
	_, remote := f.read(t, "remote")
	if !local.RevokedPrevious || !equalSTPCommitmentPackage(local.Commitment, f.channel.LocalCommitment) {
		t.Fatal("refresh replaced the local signing obligation or released-secret marker")
	}
	for _, commitment := range []*ChannelCommitment{remote.Commitment, local.Channel.RemoteCommitment, recovered.RemoteCommitment} {
		actual, _ := EncodeMsgTx(commitment.Others[0].CommitTx)
		if actual != f.packages[0][0] {
			t.Fatal("restart lost the complete peer recovery authorization")
		}
		fetcher := txscript.NewMultiPrevOutFetcher(nil)
		AddTxOutputsToPrevFetcher(fetcher, commitment.CommitTx)
		if err := VerifySignedTx(commitment.Others[0].CommitTx, fetcher); err != nil {
			t.Fatalf("peer-independent recovery cannot be broadcast after restart: %v", err)
		}
	}
}

func TestSTPNativeOtherJournalRemoteOnlyRemainsAtomic(t *testing.T) {
	f := newOtherJournalFixture(t, false)
	before, _ := f.read(t, "remote")
	f.db.failFlush = true
	if err := f.manager.acceptSTPNativeOtherTransactions(f.channel, f.packages); !errors.Is(err, errNativeSettlementWrite) {
		t.Fatal(err)
	}
	if recovered := f.restart(t); recovered != nil {
		t.Fatal("receiving the peer package fabricated a local revocation")
	}
	after, _ := f.read(t, "remote")
	if !bytes.Equal(before, after) {
		t.Fatal("remote-only journal changed after failed Flush")
	}
	f.db.operations = 0
	if err := f.manager.acceptSTPNativeOtherTransactions(f.channel, f.packages); err != nil {
		t.Fatal(err)
	}
	if f.db.operations != 1 || f.db.commits != 1 {
		t.Fatal("remote-only journal did not use an atomic transaction")
	}
	if recovered := f.restart(t); recovered != nil {
		t.Fatal("remote package created a local revocation marker")
	}
	if _, err := f.db.Read(stpNativeCommitmentKey(f.channel, "local")); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("unexpected local journal: %v", err)
	}
}

func TestSTPNativeOtherJournalRejectsForeignLocalContext(t *testing.T) {
	for _, change := range []string{"role", "height", "entropy", "deanchor"} {
		t.Run(change, func(t *testing.T) {
			f := newOtherJournalFixture(t, true)
			beforeRemote, _ := f.read(t, "remote")
			_, local := f.read(t, "local")
			switch change {
			case "role":
				local.Role = "remote"
			case "height":
				local.Height++
			case "entropy":
				local.AssetEntropy[0]++
			case "deanchor":
				local.Commitment.DeAnchorTx.LockTime++
			}
			raw, err := EncodeToBytes(&local)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.Write(stpNativeCommitmentKey(f.channel, "local"), raw); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.acceptSTPNativeOtherTransactions(f.channel, f.packages); err == nil {
				t.Fatal("matching Bitcoin transaction bypassed the local journal's identity")
			}
			afterRemote, _ := f.read(t, "remote")
			if !bytes.Equal(beforeRemote, afterRemote) || f.db.operations != 0 {
				t.Fatal("invalid local context partially published the remote journal")
			}
		})
	}
}
