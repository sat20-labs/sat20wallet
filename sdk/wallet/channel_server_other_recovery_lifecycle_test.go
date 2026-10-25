package wallet

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	swire "github.com/sat20-labs/satoshinet/wire"
)

// This protocol retains the existing fixture's exact transaction-identity
// proof check for settlement too. It isolates Bitcoin authorization, persistence
// and lifecycle ordering, not ARC/RGB consensus or real network broadcasting.
type serverOtherLifecycleTestProtocol struct{ otherJournalTestProtocol }

func (protocol serverOtherLifecycleTestProtocol) ImportChannelSettlement(manager *Manager, tx *wire.MsgTx, proof []byte) error {
	_, err := protocol.VerifyChannelProof(manager, nil, tx, proof, STPOpForceClose)
	return err
}

func serverOtherLifecycleProtocol(t *testing.T) {
	t.Helper()
	protocols := append([]STPAssetProtocol(nil), stpAssetProtocols.ordered...)
	for i, protocol := range protocols {
		if protocol.Protocol() == "other-journal-test" {
			protocols[i] = serverOtherLifecycleTestProtocol{}
		}
	}
	registry, err := NewSTPAssetRegistry(protocols...)
	if err != nil {
		t.Fatal(err)
	}
	// newReservedOthersFixture already registered cleanup to restore the
	// original registry after this single synchronous test.
	stpAssetProtocols = registry
}

func serverOtherLifecycleChild(t *testing.T, f *reservedOthersFixture, parent *wire.MsgTx) *CommitmentTx {
	t.Helper()
	client := f.left.PubKey()
	if !f.channel.IsInitiator {
		client = f.right.PubKey()
	}
	recipient, err := GetP2TRpkScript(client)
	if err != nil {
		t.Fatal(err)
	}
	point := wire.OutPoint{Hash: parent.TxHash(), Index: 1}
	previous := parent.TxOut[1]
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&point, nil, nil))
	tx.AddTxOut(wire.NewTxOut(previous.Value-1000, recipient))
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
		t.Fatalf("server Other recovery lacks actual complete Bitcoin authorization: %v", err)
	}
	return &CommitmentTx{CommitTx: tx}
}

func serverOtherLifecycleStoredBytes(t *testing.T, commitment *ChannelCommitment) []byte {
	t.Helper()
	if commitment == nil || len(commitment.Others) != 1 || commitment.Others[0] == nil {
		t.Fatal("signed server Other package was not retained")
	}
	raw, err := EncodeToBytes(commitment.Others)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSTPLocalServerForceCloseRetainsOtherWithoutPublishingIt(t *testing.T) {
	f := newReservedOthersFixture(t)
	f.channel.IsInitiator = false // local owner is server, not the client.
	if !IsServerCommitment(0, f.channel) {
		t.Fatal("fixture does not represent a local server commitment")
	}
	other := serverOtherLifecycleChild(t, f, f.channel.LocalCommitment.CommitTx)
	f.channel.LocalCommitment.Others = []*CommitmentTx{other}
	f.channel.StaticMerkleRoot = f.channel.CalcStaticMerkleRoot()
	if err := f.manager.SaveChannelToDB(f.channel); err != nil {
		t.Fatal(err)
	}
	before := serverOtherLifecycleStoredBytes(t, f.channel.LocalCommitment)
	commitID, _, err := f.manager.ForcelyCloseChannel(f.channel, 1)
	if err != nil || commitID != f.expected[1].TxID() {
		t.Fatalf("local server main commitment did not reach its durable broadcaster: %s %v", commitID, err)
	}
	reservedOthersAssertPackage(t, f.rpc.broadcasts, f.expected, "local server network package")
	var pending *ClosingReservation
	for _, candidate := range f.manager.GetClosingReservations() {
		pending = candidate
	}
	if pending == nil {
		t.Fatal("local close lost its durable main package")
	}
	reservedOthersAssertPackage(t, pending.PreTxs, f.expected, "local server durable package")
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db.KVDB = NewKVDB(f.directory)
	if f.db.KVDB == nil {
		t.Fatal("cannot reopen saved channel DB")
	}
	loaded, err := f.manager.LoadChannel(f.channel.ChannelId)
	if err != nil || loaded == nil {
		t.Fatalf("LoadChannel lost the saved server commitment: %v", err)
	}
	if !bytes.Equal(before, serverOtherLifecycleStoredBytes(t, loaded.LocalCommitment)) {
		t.Fatal("DB close/reopen or LoadChannel changed the pre-signed Other package bytes")
	}
	if loaded.LocalCommitment.RGBClientRecovery != nil {
		t.Fatal("non-RGB server recovery was moved into the RGB-only field")
	}
	main := loaded.LocalCommitment.CommitTx
	f.checkChain(t, []*wire.MsgTx{loaded.LocalCommitment.Others[0].CommitTx}, map[wire.OutPoint]*wire.TxOut{
		{Hash: main.TxHash(), Index: 1}: main.TxOut[1],
	})
}

func TestSTPClientOtherRecoveryUsesOnlyConfirmedRemoteServerParent(t *testing.T) {
	// This account-correct signed fixture keeps the original parent and L2
	// codec boundaries while respecting the explicit client-choice rule.
	f := newPeerChoiceFixture(t)
	remote := f.channel.RemoteCommitment
	remote.DeAnchorTx = swire.NewMsgTx(2)
	remote.DeAnchorTx.AddTxIn(swire.NewTxIn(&swire.OutPoint{Index: swire.MaxPrevOutIndex}, []byte{1, 1}, nil))
	remote.DeAnchorTx.AddTxOut(swire.NewTxOut(1000, nil, []byte{txscript.OP_TRUE}))
	f.channel.StaticMerkleRoot = f.channel.CalcStaticMerkleRoot()
	if err := f.manager.SaveChannelToDB(f.channel); err != nil {
		t.Fatal(err)
	}
	before := serverOtherLifecycleStoredBytes(t, remote)
	var err error
	f.rpc.raw[remote.DeAnchorTx.TxID()], err = EncodeMsgTx_SatsNet(remote.DeAnchorTx)
	if err != nil {
		t.Fatal(err)
	}
	decodedL2, err := DecodeMsgTx_SatsNet(f.rpc.raw[remote.DeAnchorTx.TxID()])
	if err != nil || decodedL2.TxID() != remote.DeAnchorTx.TxID() {
		t.Fatalf("L2 fixture raw/codec/transaction identity disagree: %v", err)
	}
	f.manager.l2IndexerClient = &IndexerRPCClientMgr{active: f.rpc}
	f.rpc.confirmed[remote.DeAnchorTx.TxID()] = true
	f.rpc.confirmed[f.parent.TxID()] = false
	if err := f.manager.HandleUnexpectedChannelClose(f.channel, f.parent.TxID()); err == nil {
		t.Fatal("unconfirmed remote server parent authorized client recovery")
	}
	if len(f.rpc.broadcasts) != 0 || len(f.manager.GetClosingReservations()) != 0 {
		t.Fatal("client published a recovery before the exact remote server parent confirmed")
	}
	f.rpc.confirmed[f.parent.TxID()] = true
	if err := f.manager.HandleUnexpectedChannelClose(f.channel, f.parent.TxID()); err == nil {
		t.Fatal("unconfirmed protocol successor was prematurely terminal")
	}
	reservedOthersAssertPackage(t, f.rpc.broadcasts, []*wire.MsgTx{f.next}, "confirmed remote server protocol successor only")
	if !bytes.Equal(before, serverOtherLifecycleStoredBytes(t, f.channel.RemoteCommitment)) {
		t.Fatal("the peer observation rebuilt or altered the saved Other authorization")
	}
	var pending *ClosingReservation
	for _, resv := range f.manager.GetClosingReservations() {
		pending = resv
	}
	if pending == nil {
		t.Fatal("observation lost its durable protocol successor")
	}
	f.markConfirmed(t, f.next)
	if handled, err := f.manager.RetryNativeForceClose(pending); !handled || err != nil {
		t.Fatalf("confirmed main/protocol successor/burn did not finish observation: handled=%v err=%v", handled, err)
	}
	if f.channel.Status != CS_CLOSED_FORCELY {
		t.Fatal("observation waited for an unchosen client recovery")
	}
	reservedOthersAssertPackage(t, f.rpc.broadcasts, []*wire.MsgTx{f.next}, "unchosen Other remains unpublished after observation completes")
}
