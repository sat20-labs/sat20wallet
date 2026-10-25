package wallet

import (
	"errors"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

var errSingleUTXOLegacyLedgerUnavailable = errors.New("ordinary temporary channel ledger read failure before drain/Open")

type singleUTXOLegacyLedgerFailure struct {
	IndexerRPCClient
	reads int
}

func (r *singleUTXOLegacyLedgerFailure) GetChannelLedger(string) ([]*sindexer.ChannelLedgerEntry, error) {
	r.reads++
	return nil, errSingleUTXOLegacyLedgerUnavailable
}

type singleUTXOLegacyReopenClient struct {
	*singleUTXOTopupGateClient
	onlineChecks, contractReads, openCalls, createdCalls int
}

func (c *singleUTXOLegacyReopenClient) SendActionResultNfty(common.Wallet, int64, string, int, string) error {
	c.onlineChecks++
	return nil
}
func (c *singleUTXOLegacyReopenClient) GetDeployedContractsReq() ([]string, error) {
	c.contractReads++
	return nil, nil
}
func (c *singleUTXOLegacyReopenClient) SendOpenChannelReq(*FundingReservation) error {
	c.openCalls++
	return errors.New("unexpected Open after failed ledger read")
}
func (c *singleUTXOLegacyReopenClient) SendFundingCreatedReq(*FundingReservation) error {
	c.createdCalls++
	return errors.New("unexpected FundingCreated after failed ledger read")
}

func TestSTPSingleUTXOTopupAdoptsOnlyNaturalNeverDispatchedPreparedReopen(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.manager.bInited, f.manager.actionMonitorRunning = true, true
	node := &singleUTXOLegacyReopenClient{singleUTXOTopupGateClient: f.gate}
	f.manager.serverNode.client = node
	ledger := &singleUTXOLegacyLedgerFailure{IndexerRPCClient: f.rpc}
	f.manager.l2IndexerClient = &IndexerRPCClientMgr{active: ledger}
	// Exercise the real old public Reopen. Only its normal pre-Open ledger
	// read fails; no recovery owner or uncertain request is fabricated by test.
	if _, err := f.manager.ReopenChannel(false); err == nil || !strings.Contains(err.Error(), errSingleUTXOLegacyLedgerUnavailable.Error()) {
		t.Fatalf("normal Reopen did not fail at its read-only ledger boundary: %v", err)
	}
	owners := f.manager.GetLocalActionReservations()
	if len(owners) != 1 || ledger.reads != 1 || node.onlineChecks != 1 || node.contractReads != 1 || node.openCalls != 0 || node.createdCalls != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 {
		t.Fatal("natural prepared fixture did not stop before any drain/Open/funding effects")
	}
	var owner *LocalActionPerformData
	for _, reservation := range owners {
		candidate, ok := reservation.(*LocalActionPerformData)
		if !ok || candidate.Action != localActionClosedChannelRecovery || !closedIntentWalletMatches(candidate, f.manager.wallet) {
			continue
		}
		candidate.RLock()
		record, parseErr := loadClosedChannelIntent(candidate)
		candidate.RUnlock()
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if record.ChannelID == f.channelID && record.OldParentID == f.rpc.parent.TxID() {
			if owner != nil {
				t.Fatal("natural Reopen created duplicate captured-account/channel owners")
			}
			owner = candidate
		}
	}
	if owner == nil {
		t.Fatal("natural Reopen did not retain its exact captured-account/channel recovery owner")
	}
	owner.RLock()
	intent, err := loadClosedChannelIntent(owner)
	children := len(owner.ActionResvs)
	owner.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if intent.Step != "prepared" || intent.Request != nil || intent.Accept != nil || intent.FundingCreated != nil || intent.FundingSigned != nil || intent.FundingBroadcasted != nil || len(intent.DrainSnapshot) != 0 || intent.L2DrainTxID != "" || intent.TopupJSON != "" || children != 0 {
		t.Fatal("fixture was not a definite never-dispatched prepared owner")
	}
	api := singleUTXOTopupAPIForTest(t, f.manager)
	quote, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatalf("normal never-dispatched prepared Reopen blocks explicit funding approval: %v", err)
	}
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(quote); err != nil {
		t.Fatal(err)
	}
	after := f.manager.GetLocalActionReservations()
	if len(after) != 1 || after[owner.Id] != owner || *f.signatures != 1 || len(f.rpc.broadcasts) != 1 || f.rpc.durableError != nil || node.openCalls != 0 || node.createdCalls != 0 {
		t.Fatal("explicit approval replaced owner, mixed plans or paid outside the durable original owner")
	}
}
