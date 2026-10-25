package wallet

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/wire"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

// These are local boundary tests, not RGB consensus fixtures. A collector must
// reject a detached or malformed dedicated slot rather than silently omit it.
// The real native 50/10 punishment integration supplies the positive proof.
func TestNativeWatchtowerCannotSilentlyOmitRGBRecovery(t *testing.T) {
	for _, scenario := range []string{"missing-proof", "foreign-parent", "missing-signature", "foreign-proof", "missing-child"} {
		t.Run(scenario, func(t *testing.T) {
			f, encoded := rgbRecoveryExchangeFixture(t)
			delete(f.channel.RemoteCommitment.AssetProofs, "rgb11")
			child, err := DecodeMsgTx(encoded[0])
			if err != nil {
				t.Fatal(err)
			}
			recovery := f.channel.RemoteCommitment.RGBClientRecovery
			recovery.CommitTx = child
			txs := []*wire.MsgTx{child}
			switch scenario {
			case "missing-proof":
				recovery.AssetProofs = nil
			case "foreign-parent":
				child.TxIn[0].PreviousOutPoint.Hash[0]++
			case "missing-signature":
				child.TxIn[0].Witness[1] = nil
			case "foreign-proof":
				recovery.AssetProofs["rgb11"] = []byte("foreign RGB proof")
			case "missing-child":
				txs = []*wire.MsgTx{minimalPersistedCommitmentTx(95)}
			}
			tower := newWatchTower(f.manager, false)
			if _, err := tower.nativePunishProofs(f.channel, f.channel.RemoteCommitment.CommitTx, txs); err == nil {
				t.Fatal("watchtower silently omitted an unbound RGB recovery obligation")
			}
			if tower.HasCommitTx(f.channel.RemoteCommitment.CommitTx.TxID()) {
				t.Fatal("rejected recovery became a punishment package")
			}
		})
	}
}

func TestSTPRGBUnverifiedRevocationCannotPublishRecovery(t *testing.T) {
	f, _ := rgbRecoveryExchangeFixture(t)
	delete(f.channel.RemoteCommitment.AssetProofs, "rgb11")
	current := f.channel.Clone()
	current.RemoteCommitment.RGBClientRecovery = nil
	current.CommitHeight++
	// Channel.Clone is estimate-only and deliberately omits CommitTx. Copy
	// the actual saved original parent; never change production Clone semantics.
	if f.channel.RemoteCommitment.CommitTx == nil {
		t.Fatal("saved revocation fixture parent is missing")
	}
	current.RemoteCommitment.CommitTx = f.channel.RemoteCommitment.CommitTx.Copy()
	current.RemoteCommitment.CommitTx.LockTime++
	if current.RemoteCommitment.CommitTx.TxID() == f.channel.RemoteCommitment.CommitTx.TxID() {
		t.Fatal("revocation fixture did not construct a distinct current parent")
	}
	tower := newWatchTower(f.manager, false)
	f.manager.watchTower = tower
	secret, _ := btcec.PrivKeyFromBytes([]byte{31})
	wrong, _ := btcec.PrivKeyFromBytes([]byte{32})
	msg := &wwire.RevokeAndAck{NextRevKey: secret.PubKey().SerializeCompressed()}
	copy(msg.Revocation[:], secret.Serialize())
	info := &RevocationInfo{Channel: current, OldChannel: f.channel, RemoteRevKey: wrong.PubKey().SerializeCompressed()}
	if err := f.manager.ReceiveRevocation(info, msg); err == nil || !strings.Contains(err.Error(), "revocation key mismatch") {
		t.Fatalf("unverified old secret was not rejected first: %v", err)
	}
	if info.SignedPunishTx != nil || tower.HasCommitTx(f.channel.RemoteCommitment.CommitTx.TxID()) {
		t.Fatal("unverified revocation published a recovery package")
	}
	for _, commitment := range []*ChannelCommitment{f.channel.RemoteCommitment, current.RemoteCommitment} {
		if txs := appendCommitmentOtherTxs(nil, commitment); len(txs) != 0 {
			t.Fatal("the default/current commitment package acquired a dormant RGB child")
		}
	}
}

// This test-only protocol exercises the existing per-transaction confirmation,
// import and durable retry plumbing without claiming to validate RGB receipts.
// Its importer records unique transaction identities and can withhold one receipt.
type punishReceiptBoundaryProtocol struct {
	otherJournalTestProtocol
	accepted map[string]bool
	blocked  map[string]bool
}

func (*punishReceiptBoundaryProtocol) Protocol() string { return "punish-receipt-boundary-test" }
func (p *punishReceiptBoundaryProtocol) ImportChannelSettlement(_ *Manager, tx *wire.MsgTx, proof []byte) error {
	hash := tx.TxHash()
	if !bytes.Equal(hash[:], proof) || p.blocked[tx.TxID()] {
		return fmt.Errorf("test settlement receipt is unavailable")
	}
	p.accepted[tx.TxID()] = true
	return nil
}

func TestNativeWatchtowerIndependentReceiptsSurviveRestart(t *testing.T) {
	for _, missing := range []int{0, 1} {
		t.Run(fmt.Sprintf("unconfirmed-branch-%d", missing), func(t *testing.T) {
			f := newNativeRecoveryFixture(t)
			protocol := &punishReceiptBoundaryProtocol{accepted: make(map[string]bool), blocked: make(map[string]bool)}
			previous := stpAssetProtocols
			protocols := append([]STPAssetProtocol{}, previous.ordered...)
			var err error
			stpAssetProtocols, err = NewSTPAssetRegistry(append(protocols, protocol)...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stpAssetProtocols = previous })
			commit := f.channel.RemoteCommitment.CommitTx
			txs := []*wire.MsgTx{minimalPersistedCommitmentTx(96), minimalPersistedCommitmentTx(97)}
			record := &nativePunishRecord{Version: 1, ChannelID: f.channel.ChannelId, CommitTxID: commit.TxID(),
				Transactions: txs, Proofs: make(map[string]map[string][]byte)}
			for i, tx := range txs {
				hash := tx.TxHash()
				record.Proofs[tx.TxID()] = map[string][]byte{protocol.Protocol(): append([]byte(nil), hash[:]...)}
				f.rpc.raw[tx.TxID()], err = EncodeMsgTx(tx)
				if err != nil {
					t.Fatal(err)
				}
				f.rpc.confirmed[tx.TxID()] = i != missing
			}
			if err := saveNativePunishRecord(f.db, record); err != nil {
				t.Fatal(err)
			}
			tower := newWatchTower(f.manager, false)
			f.manager.watchTower = tower
			if err := tower.markPunishPending(commit.TxID()); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.completePunishBroadcastState(f.channel.ChannelId, commit.TxID()); err == nil {
				t.Fatal("one confirmed branch completed both punishment receipts")
			}
			if err := f.db.Close(); err != nil {
				t.Fatal(err)
			}
			f.db.KVDB = NewKVDB(f.directory)
			tower = newWatchTower(f.manager, false)
			f.manager.watchTower = tower
			loaded, err := loadNativePunishRecord(f.db, f.channel.ChannelId, commit.TxID())
			if err != nil || loaded == nil || !equalSTPBitcoinChain(record.Transactions, loaded.Transactions) {
				t.Fatalf("restart lost exact independent transaction bytes: %v", err)
			}
			for id, proofs := range record.Proofs {
				if !equalSTPAssetProofs(proofs, loaded.Proofs[id]) {
					t.Fatal("restart changed an independent recovery proof")
				}
			}
			if !tower.HasCommitTx(commit.TxID()) || !tower.HasBroadcastedCommitTx(commit.TxID()) {
				t.Fatal("restart lost the pending punishment task")
			}
			for _, tx := range txs {
				f.rpc.confirmed[tx.TxID()] = true
			}
			protocol.blocked[txs[1].TxID()] = true
			if err := f.manager.completePunishBroadcastState(f.channel.ChannelId, commit.TxID()); err == nil {
				t.Fatal("Bitcoin confirmation replaced the missing asset receipt")
			}
			if !tower.HasCommitTx(commit.TxID()) || !tower.HasBroadcastedCommitTx(commit.TxID()) {
				t.Fatal("failed second import cleaned up the durable recovery")
			}
			delete(protocol.blocked, txs[1].TxID())
			for i := 0; i < 2; i++ {
				if err := tower.importNativePunishment(f.channel.ChannelId, commit.TxID()); err != nil {
					t.Fatal(err)
				}
			}
			if len(protocol.accepted) != 2 {
				t.Fatal("retry did not import both unique transaction receipts")
			}
			if err := f.manager.completePunishBroadcastState(f.channel.ChannelId, commit.TxID()); err != nil {
				t.Fatal(err)
			}
			if tower.HasCommitTx(commit.TxID()) || tower.HasBroadcastedCommitTx(commit.TxID()) {
				t.Fatal("both imported receipts did not complete the recovery")
			}
		})
	}
}
