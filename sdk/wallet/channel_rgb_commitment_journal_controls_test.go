package wallet

import (
	"bytes"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// These controls isolate the new local identity/root predicate. The fixture
// uses actual issuance/transition builders, but these controls do not claim
// to replace the caller's full VerifyChannelProof or the 0332 SDK lifecycle.
func TestSTPRGBCommitmentJournalExactCurrentServerAndRoots(t *testing.T) {
	facts, sources, main, raw := rgbReservationNativeEvidenceFixture(t)
	pkg, err := rgb11wallet.DecodeChannelPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	inventoryRaw, err := rgb11wallet.EncodeChannelInventoryWithLast(sources, raw)
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := rgb11wallet.DecodeChannelInventory(inventoryRaw)
	if err != nil {
		t.Fatal(err)
	}
	serverCommitment := NewChannelCommitment()
	serverCommitment.CommitTx = main.Copy()
	serverCommitment.AssetProofs = map[string][]byte{"rgb11": append([]byte(nil), raw...)}
	channel := &Channel{ChannelInDB: ChannelInDB{ChannelId: facts.channelID,
		CommitHeight: int(facts.height) - 1, AssetEntropy: facts.entropy, IsInitiator: true,
		AssetFunding:     map[string][]byte{"rgb11": inventoryRaw},
		RemoteCommitment: serverCommitment}}
	protocol := rgb11STPProtocol{}
	if err := protocol.validateSTPRGBCommitmentJournal(channel, inventory, pkg, main, raw); err != nil {
		t.Fatalf("exact current server sidecar with unchanged confirmed roots rejected: %v", err)
	}

	t.Run("non-current-sidecar", func(t *testing.T) {
		changed := *channel
		server := *channel.RemoteCommitment
		server.AssetProofs = map[string][]byte{"rgb11": []byte("not the current sidecar")}
		changed.RemoteCommitment = &server
		if err := protocol.validateSTPRGBCommitmentJournal(&changed, inventory, pkg, main, raw); err == nil {
			t.Fatal("non-current server sidecar authorized a commitment journal")
		}
	})
	t.Run("changed-funding-amount", func(t *testing.T) {
		changed := *inventory
		changed.Sources = append([]rgb11wallet.ChannelFunding(nil), inventory.Sources...)
		changed.Sources[0].Amount = *indexer.NewDecimalWithScale(7001, 2)
		if err := protocol.validateSTPRGBCommitmentJournal(channel, &changed, pkg, main, raw); err == nil {
			t.Fatal("changed active funding amount matched original journal roots")
		}
	})
	t.Run("future-commitment-output-is-not-active-funding", func(t *testing.T) {
		future, err := rgb11wallet.ChannelFundingFromStep(pkg.Steps[0], 1, sources[0].Amount)
		if err != nil {
			t.Fatal(err)
		}
		changed := *inventory
		changed.Sources = []rgb11wallet.ChannelFunding{future}
		if err := protocol.validateSTPRGBCommitmentJournal(channel, &changed, pkg, main, raw); err == nil {
			t.Fatal("unbroadcast commitment output was treated as an active funding root")
		}
	})
	t.Run("different-server-transaction-without-local-provenance", func(t *testing.T) {
		changed := *channel
		server := *channel.RemoteCommitment
		server.CommitTx = main.Copy()
		server.CommitTx.TxOut[1].Value--
		changed.RemoteCommitment = &server
		if err := protocol.validateSTPRGBCommitmentJournal(&changed, inventory, pkg, main, raw); err == nil {
			t.Fatal("a different server transaction reused a sidecar without local construction evidence")
		}
	})
	t.Run("exact-transient-construction-is-context-not-signature", func(t *testing.T) {
		changed := *channel
		server := *channel.RemoteCommitment
		server.CommitTx = main.Copy()
		server.CommitTx.TxOut[1].Value-- // The caller has not assigned the new CommitTx yet.
		changed.RemoteCommitment = &server
		view := facts.newView()
		if err := view.addLocallyConstructed(main, raw); err != nil {
			t.Fatal(err)
		}
		prepared := rgb11STPProtocol{view: view}
		if err := prepared.validateSTPRGBCommitmentJournal(&changed, inventory, pkg, main, raw); err != nil {
			t.Fatalf("same-operation exact transient construction rejected: %v", err)
		}
		view.context.Entropy[0]++
		if err := prepared.validateSTPRGBCommitmentJournal(&changed, inventory, pkg, main, raw); err == nil {
			t.Fatal("a different construction context reused the prepared journal")
		}
	})
	t.Run("changed-package-transaction", func(t *testing.T) {
		changed := main.Copy()
		changed.TxOut[1].Value--
		if err := protocol.validateSTPRGBCommitmentJournal(channel, inventory, pkg, changed, raw); err == nil {
			t.Fatal("the journal predicate accepted a transaction other than its exact package")
		}
	})
	t.Run("sources-preserved", func(t *testing.T) {
		after, err := rgb11wallet.EncodeChannelInventoryWithLast(inventory.Sources, inventory.LastPackage)
		if err != nil || !bytes.Equal(after, inventoryRaw) {
			t.Fatal("read-only journal checks changed the active source inventory")
		}
	})
}
