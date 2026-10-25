package wallet

import (
	"bytes"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// These normal signed-package controls exercise the policy/journal boundary,
// not ARC/RGB consensus. The real native fakechain case remains unchanged.
type peerChoiceRPC struct {
	*nativeRecoveryRPC
	spent map[string]string
}

func (r *peerChoiceRPC) GetUtxoSpentTx(point string) (string, error) { return r.spent[point], nil }

type peerChoiceFixture struct {
	manager *Manager
	channel *Channel
	rpc     *peerChoiceRPC
	parent  *wire.MsgTx
	next    *wire.MsgTx
	other   *CommitmentTx
}

func newPeerChoiceFixture(t *testing.T) *peerChoiceFixture {
	t.Helper()
	manager, channel, _, _ := closedWithdrawalBoundsFixture(t, false)
	previousRegistry := stpAssetProtocols
	protocols := append([]STPAssetProtocol(nil), previousRegistry.ordered...)
	protocols = append(protocols, serverOtherLifecycleTestProtocol{})
	var err error
	stpAssetProtocols, err = NewSTPAssetRegistry(protocols...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stpAssetProtocols = previousRegistry })
	peer := NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	sign := func(tx *wire.MsgTx, fetcher txscript.PrevOutputFetcher) {
		t.Helper()
		sigs, err := PartialSignTxWithWallet(peer, tx, fetcher, channel.RedeemScript, false, manager.wallet.GetPaymentPubKey().SerializeCompressed())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := FinalSignTxWithWallet(manager.wallet, tx, fetcher, channel.RedeemScript, false, peer.GetPaymentPubKey().SerializeCompressed(), sigs); err != nil {
			t.Fatal(err)
		}
		if err := VerifySignedTx(tx, fetcher); err != nil {
			t.Fatal(err)
		}
	}
	parent := channel.RemoteCommitment.CommitTx.Copy()
	var inputValue int64
	for _, input := range parent.TxIn {
		prev := channel.GetCommitmentPrefetchor().FetchPrevOutput(input.PreviousOutPoint)
		if prev == nil {
			t.Fatal("ordinary fixture lost a funding prevout")
		}
		inputValue += prev.Value
	}
	first := (inputValue - 2000) / 2
	parent.TxOut = []*wire.TxOut{wire.NewTxOut(first, channel.GetChannelPkScript()), wire.NewTxOut(inputValue-2000-first, channel.GetChannelPkScript())}
	sign(parent, channel.GetCommitmentPrefetchor())
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, parent)
	next := wire.NewMsgTx(2)
	next.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: parent.TxHash(), Index: 0}, nil, nil))
	next.AddTxOut(wire.NewTxOut(parent.TxOut[0].Value-1000, channel.GetChannelPkScript()))
	sign(next, fetcher)
	otherTx := wire.NewMsgTx(2)
	otherTx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: parent.TxHash(), Index: 1}, nil, nil))
	recipient, err := GetP2TRpkScript(manager.wallet.GetPaymentPubKey())
	if err != nil {
		t.Fatal(err)
	}
	otherTx.AddTxOut(wire.NewTxOut(parent.TxOut[1].Value-1000, recipient))
	sign(otherTx, fetcher)
	proof := func(tx *wire.MsgTx) map[string][]byte {
		hash := tx.TxHash()
		return map[string][]byte{"other-journal-test": append([]byte(nil), hash[:]...)}
	}
	other := &CommitmentTx{CommitTx: otherTx, AssetProofs: proof(otherTx)}
	remote := NewChannelCommitment()
	remote.CommitTx, remote.NextTxs, remote.Others = parent, []*wire.MsgTx{next}, []*CommitmentTx{other}
	remote.AssetProofs = proof(parent)
	channel.RemoteCommitment = remote
	channel.AssetFunding = map[string][]byte{"other-journal-test": []byte("inventory")}
	channel.Status = CS_READY
	channel.StaticMerkleRoot = channel.CalcStaticMerkleRoot()
	if err := manager.SaveChannelToDB(channel); err != nil {
		t.Fatal(err)
	}
	rpc := &peerChoiceRPC{nativeRecoveryRPC: &nativeRecoveryRPC{raw: map[string]string{}, confirmed: map[string]bool{}, height: 200}, spent: map[string]string{}}
	rpc.raw[parent.TxID()], err = EncodeMsgTx(parent)
	if err != nil {
		t.Fatal(err)
	}
	rpc.confirmed[parent.TxID()] = true
	manager.l1IndexerClient.SetMaster(rpc)
	manager.utxoLockerL1 = NewUtxoLocker(manager.db, rpc, L1_NETWORK_BITCOIN)
	return &peerChoiceFixture{manager: manager, channel: channel, rpc: rpc, parent: parent, next: next, other: other}
}

func (f *peerChoiceFixture) markConfirmed(t *testing.T, tx *wire.MsgTx) {
	t.Helper()
	var err error
	f.rpc.raw[tx.TxID()], err = EncodeMsgTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	f.rpc.confirmed[tx.TxID()] = true
}

func (f *peerChoiceFixture) persist(t *testing.T, legacy bool) *ClosingReservation {
	t.Helper()
	pkg := []*wire.MsgTx{f.parent, f.next}
	if legacy {
		pkg = append(pkg, f.other.CommitTx)
	}
	resv := &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(902, false, 0, f.manager.wallet),
		ChannelId:       f.channel.ChannelId, CloseTx: f.parent.Copy(), PreTxs: cloneSTPBitcoinChain(pkg),
		AssetProofs: cloneSTPAssetProofs(f.channel.RemoteCommitment.AssetProofs),
	}, Channel: f.channel}
	f.channel.Mutex.Lock()
	err := f.manager.persistNativeForceCloseLocked(resv, resv.ClosingDataInDB, CS_CLOSE_FORCELY_SWEEP_BROADCASTED)
	f.channel.Mutex.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return resv
}

func TestPeerNativeObservationRetainsChoiceAndProtocolSuccessor(t *testing.T) {
	f := newPeerChoiceFixture(t)
	before, _ := EncodeToBytes(f.channel.RemoteCommitment)
	var events []string
	f.manager.RegisterCallback(func(event string, _ interface{}) { events = append(events, event) })
	if err := f.manager.HandleUnexpectedChannelClose(f.channel, f.parent.TxID()); err == nil {
		t.Fatal("unconfirmed protocol successor was falsely terminal")
	}
	reservedOthersAssertPackage(t, f.rpc.broadcasts, []*wire.MsgTx{f.next}, "observation broadcasts only its protocol successor")
	var pending *ClosingReservation
	for _, resv := range f.manager.GetClosingReservations() {
		pending = resv
	}
	if pending == nil {
		t.Fatal("observation lost its durable monitor task")
	}
	reservedOthersAssertPackage(t, pending.PreTxs, []*wire.MsgTx{f.parent, f.next}, "observation durable package excludes unchosen recovery")
	f.markConfirmed(t, f.next)
	if handled, err := f.manager.RetryNativeForceClose(pending); !handled || err != nil {
		t.Fatalf("confirmed protocol successors did not finish observation: handled=%v err=%v", handled, err)
	}
	if f.channel.Status != CS_CLOSED_FORCELY || len(f.manager.GetClosingReservations()) != 0 {
		t.Fatal("observation did not terminate without waiting for the unchosen Other")
	}
	after, _ := EncodeToBytes(f.channel.RemoteCommitment)
	if !bytes.Equal(before, after) {
		t.Fatal("observation changed the original signed withdrawal obligation")
	}
	for _, event := range events {
		if event == MSG_CHANNEL_SWEPT {
			t.Fatal("observation falsely announced that all client funds had been swept")
		}
	}
}

func TestPeerNativeObservationUnconfirmedParentCannotAdvance(t *testing.T) {
	f := newPeerChoiceFixture(t)
	f.rpc.confirmed[f.parent.TxID()] = false
	if err := f.manager.HandleUnexpectedChannelClose(f.channel, f.parent.TxID()); err == nil {
		t.Fatal("unconfirmed server main was accepted")
	}
	if len(f.rpc.broadcasts) != 0 || len(f.manager.GetClosingReservations()) != 0 || f.channel.Status != CS_READY {
		t.Fatal("unconfirmed main published a successor or changed the lifecycle")
	}
}

func TestPeerNativeLegacyObservationKeepsUnknownAndVisibleRecoveryPending(t *testing.T) {
	for _, outcome := range []string{"unknown", "visible", "conflict"} {
		t.Run(outcome, func(t *testing.T) {
			f := newPeerChoiceFixture(t)
			f.markConfirmed(t, f.next)
			resv := f.persist(t, true)
			key := []byte(GetResvKey(RESV_TYPE_CLOSE, resv.Id))
			before, _ := f.manager.db.Read(key)
			if outcome == "visible" {
				f.rpc.raw[f.other.CommitTx.TxID()], _ = EncodeMsgTx(f.other.CommitTx)
			}
			if outcome == "conflict" {
				f.rpc.spent[f.other.CommitTx.TxIn[0].PreviousOutPoint.String()] = strings.Repeat("a", 64)
			}
			handled, err := f.manager.RetryNativeForceClose(resv)
			if !handled || err == nil || outcome == "conflict" && !strings.Contains(err.Error(), "conflicts") {
				t.Fatalf("old outcome did not remain an explained pending/conflict: handled=%v err=%v", handled, err)
			}
			after, _ := f.manager.db.Read(key)
			if !bytes.Equal(before, after) || len(f.rpc.broadcasts) != 0 || f.channel.Status != CS_CLOSE_FORCELY_SWEEP_BROADCASTED {
				t.Fatal("old uncertain recovery was rebroadcast, rewritten or prematurely terminal")
			}
			if _, _, err := f.manager.closedReopenIntentForWallet(f.manager.wallet, f.channel.ChannelId); err == nil {
				t.Fatal("unresolved old recovery allowed a competing C1 rebuild")
			}
			if len(f.manager.GetLocalActionReservations()) != 0 {
				t.Fatal("refused C1 created a competing recovery owner")
			}
		})
	}
}

func TestPeerNativeLegacyObservationImportsOnlyCanonicalConfirmedRecovery(t *testing.T) {
	f := newPeerChoiceFixture(t)
	f.markConfirmed(t, f.next)
	f.markConfirmed(t, f.other.CommitTx)
	resv := f.persist(t, true)
	before := cloneSTPBitcoinChain(resv.PreTxs)
	if handled, err := f.manager.RetryNativeForceClose(resv); !handled || err != nil {
		t.Fatalf("previously published canonical recovery did not reconcile: handled=%v err=%v", handled, err)
	}
	if f.channel.Status != CS_CLOSED_FORCELY || len(f.rpc.broadcasts) != 0 || !equalSTPBitcoinChain(before, resv.PreTxs) {
		t.Fatal("reconciliation lost original bytes, broadcast a new child, or did not terminate")
	}
}

func TestPeerNativeObservationRejectsUnknownIdentityOrPackage(t *testing.T) {
	for _, change := range []string{"parent", "channel", "account", "unknown-package"} {
		t.Run(change, func(t *testing.T) {
			f := newPeerChoiceFixture(t)
			resv := f.persist(t, false)
			switch change {
			case "parent":
				resv.CloseTx = f.channel.LocalCommitment.CommitTx.Copy()
			case "channel":
				resv.ChannelId += "-other"
			case "account":
				resv.WalletId.SubAccountId++
			case "unknown-package":
				resv.PreTxs = append(resv.PreTxs, minimalPersistedCommitmentTx(91))
			}
			if handled, err := f.manager.RetryNativeForceClose(resv); !handled || err == nil {
				t.Fatalf("invalid observation was authorized: %s handled=%v err=%v", change, handled, err)
			}
			if len(f.rpc.broadcasts) != 0 || f.channel.Status != CS_CLOSE_FORCELY_SWEEP_BROADCASTED {
				t.Fatal("invalid observation published or completed recovery")
			}
		})
	}
}

func TestPeerNativeRetryRoutesExplicitOwnerBeforeObservationLocks(t *testing.T) {
	manager, channel, path, rpc := closedWithdrawalBoundsFixture(t, false)
	other := channel.RemoteCommitment.Others[0].CommitTx
	rpc.unconfirmed[other.TxID()] = true
	id, err := manager.WithdrawClosedChannelExact(channel.ChannelId, channel.RemoteCommitment.CommitTx.TxID())
	if err != nil || id <= 0 {
		t.Fatalf("normal explicit pending withdrawal: id=%d err=%v", id, err)
	}
	if err := manager.db.Close(); err != nil {
		t.Fatal(err)
	}
	manager.db = NewKVDB(path)
	if manager.db == nil {
		t.Fatal("reopen explicit withdrawal journal")
	}
	manager.ResetResvMaps()
	manager.channelMap, manager.nodeMap = map[string]*Channel{}, map[string]string{}
	reservations, err := LoadAllResvFromDB(manager.db, manager)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reservations {
		if err := r.InitLocalWallet(manager); err != nil {
			t.Fatal(err)
		}
		manager.AddResv(r)
	}
	manager.rehydratePendingClosingRuntime()
	if err := manager.rehydrateClosedChannelIntents(); err != nil {
		t.Fatal(err)
	}
	resv, ok := manager.GetClosingReservation(id)
	if !ok || resv.Channel == nil {
		t.Fatal("explicit child was not reconstructed")
	}
	original := cloneSTPBitcoinChain(resv.PreTxs)
	// Promote only classification on this in-memory call, not the stored
	// authorized package. The existing owner must take priority over native
	// observation identity checks and preserve its actual ordinary raw bytes.
	resv.AssetProofs = map[string][]byte{"owner-routing-test": []byte("classification only")}
	rpc.unconfirmed[other.TxID()] = false
	if handled, err := manager.RetryNativeForceClose(resv); !handled || err != nil {
		t.Fatalf("direct native entry bypassed or deadlocked its explicit owner: handled=%v err=%v", handled, err)
	}
	raw, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_CLOSE, id)))
	if err != nil {
		t.Fatal(err)
	}
	var completed ClosingDataInDB
	if err := DecodeFromBytes(raw, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Status != RS_CLOSED || !equalSTPBitcoinChain(completed.PreTxs, original) || rpc.broadcasts != 0 {
		t.Fatal("explicit owner retry changed raw bytes, republished a visible child or lost completion")
	}
}
