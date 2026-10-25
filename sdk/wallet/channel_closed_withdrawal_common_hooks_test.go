package wallet

import (
	"bytes"
	"testing"
)

// The same signed ordinary fixture that exercises the public legacy C2 is
// also the positive control for each shared material/completion hook.
func TestClosedWithdrawalSharedPreparationPreservesOrdinaryBytes(t *testing.T) {
	manager, channel, _, client := closedWithdrawalBoundsFixture(t, false)
	before, err := manager.db.Read([]byte(GetChannelKey(channel.ChannelId)))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := closedWithdrawalPackage(channel.RemoteCommitment.CommitTx, channel.RemoteCommitment)
	if err != nil {
		t.Fatal(err)
	}
	material, err := manager.prepareClosedWithdrawal(channel, channel.RemoteCommitment.CommitTx)
	if err != nil {
		t.Fatal(err)
	}
	if !equalSTPBitcoinChain(material.PreTxs, expected) || !equalSTPBitcoinTx(material.CloseTx, channel.RemoteCommitment.CommitTx) ||
		len(material.AssetProofs) != 0 || material.DeAnchorTx != nil || material.Id != 0 {
		t.Fatal("shared preparation changed ordinary signed bytes or added native/lifecycle material")
	}
	after, err := manager.db.Read([]byte(GetChannelKey(channel.ChannelId)))
	if err != nil || !bytes.Equal(before, after) || client.broadcasts != 0 ||
		len(manager.GetClosingReservations()) != 0 || len(manager.GetLocalActionReservations()) != 0 {
		t.Fatal("material preparation wrote a recovery choice, changed the channel, or broadcast")
	}
}

func TestClosedWithdrawalSharedCompletionPreservesOrdinaryOwnership(t *testing.T) {
	manager, channel, _, client := closedWithdrawalBoundsFixture(t, false)
	material, err := manager.prepareClosedWithdrawal(channel, channel.RemoteCommitment.CommitTx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := manager.db.Read([]byte(GetChannelKey(channel.ChannelId)))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.completeClosedWithdrawal(&ClosingReservation{ClosingDataInDB: *material, Channel: channel}); err != nil {
		t.Fatal(err)
	}
	after, err := manager.db.Read([]byte(GetChannelKey(channel.ChannelId)))
	if err != nil || !bytes.Equal(before, after) || manager.GetChannel(channel.ChannelId) != channel || client.broadcasts != 0 ||
		len(manager.GetClosingReservations()) != 0 || len(manager.GetLocalActionReservations()) != 0 {
		t.Fatal("ordinary completion hook took over common persistence, broadcasting or channel ownership")
	}
}

func TestClosedWithdrawalSharedHooksKeepUnsupportedNativeGuard(t *testing.T) {
	manager, channel, _, client := closedWithdrawalBoundsFixture(t, false)
	channel.AssetFunding = map[string][]byte{"unknown-native": {1}}
	if _, err := manager.prepareClosedWithdrawal(channel, channel.RemoteCommitment.CommitTx); err == nil {
		t.Fatal("shared preparation silently removed the native validation guard")
	}
	channel.AssetFunding = nil
	resv := &ClosingReservation{Channel: channel, ClosingDataInDB: ClosingDataInDB{AssetProofs: map[string][]byte{"unknown-native": {1}}}}
	if err := manager.completeClosedWithdrawal(resv); err == nil {
		t.Fatal("unknown durable native material completed through the ordinary no-op")
	}
	if client.broadcasts != 0 || len(manager.GetClosingReservations()) != 0 || len(manager.GetLocalActionReservations()) != 0 {
		t.Fatal("rejected native material changed lifecycle state or broadcast")
	}
}
