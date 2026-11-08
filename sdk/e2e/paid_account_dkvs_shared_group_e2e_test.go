package e2e

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

type paidAccountDKVSRecordState struct {
	present bool
	seq     uint64
	hash    string
}

type paidAccountDKVSSnapshot struct {
	height        int32
	tip           string
	coreAutopay   string
	bootstrapPay  string
	coreRecords   map[string]paidAccountDKVSRecordState
	bootstrapRecs map[string]paidAccountDKVSRecordState
	minerRecords  map[string]paidAccountDKVSRecordState
}

// The account and module roots share the same no-plugin runtime and AUTOPAY
// pool. Its setup allocates all anchors once; the business roots then use
// independent DKVS keys and preserve their original assertions.
func TestSDKPaidAccountDKVSSharedNetworkGroup(t *testing.T) {
	setupDone := e2ePhase(t, "paid_account_dkvs_shared_network_setup")
	defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)
	setup := prepareAccountManagementAutopaySync(t, fixture, bootstrapMnemonic)
	if setup.successor != nil {
		// The original paid-DKVS fixture explicitly binds both signers. The
		// account root binds owner itself; bind the distinct successor here.
		bindDKVSReviewWallet(t, fixture.Network.Core, bootstrapMnemonic)
	}
	setupDone()
	logPaidAccountDKVSFunding(t, setup)
	assertPaidAccountDKVSSharedPool(t, setup)

	accountBefore := capturePaidAccountDKVSSnapshot(t, setup)
	passed := t.Run("AccountManagementAutopaySync", func(t *testing.T) {
		runAccountManagementAutopaySync(t, setup)
	})
	logPaidAccountDKVSDiff(t, setup, "AccountManagementAutopaySync", accountBefore)
	if !passed {
		t.Fatalf("stopped shared paid-account/DKVS group after AccountManagementAutopaySync failed; rerun only it with -run '^TestSDKPaidAccountDKVSSharedNetworkGroup$/^AccountManagementAutopaySync$' or rerun the whole fresh group with -run '^TestSDKPaidAccountDKVSSharedNetworkGroup$'")
	}

	moduleBefore := capturePaidAccountDKVSSnapshot(t, setup)
	assertPaidDKVSModuleKeysAbsent(t, setup)
	passed = t.Run("DKVSModulePaid", func(t *testing.T) {
		payment := wallet.DKVSAutopayOptions{AddressParams: &chaincfg.TestNetParams,
			PoolContract: setup.contractAddress.MustEncode()}
		runSDKDKVSModulePaid(t, fixture, setup.owner, setup.successor, payment)
	})
	logPaidAccountDKVSDiff(t, setup, "DKVSModulePaid", moduleBefore)
	if !passed {
		t.Fatalf("stopped shared paid-account/DKVS group after DKVSModulePaid failed; rerun a nested case with -run '^TestSDKPaidAccountDKVSSharedNetworkGroup$/^DKVSModulePaid$/^<case>$' or rerun the whole fresh group with -run '^TestSDKPaidAccountDKVSSharedNetworkGroup$'")
	}
}

func assertPaidAccountDKVSSharedPool(t *testing.T, setup *accountManagementAutopaySyncSetup) {
	t.Helper()
	pool := setup.contractAddress.MustEncode()
	for _, node := range []*testHarness{setup.fixture.Network.Core, setup.fixture.Network.Bootstrap} {
		state := fetchTemplateAutopayView(t, node, pool)
		require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
		require.NotEmpty(t, state.GasBalance)
		require.NotEqual(t, "0", state.GasBalance)
		require.Contains(t, state.Delegates, setup.owner.Address, "the owner account delegate must be active")
		require.Contains(t, state.Delegates, setup.successor.Address, "the DKVS paid delegate must be present")
		require.Contains(t, state.Delegates, setup.guardianActor.Address, "the Guardian delegate must be present")
		require.Equal(t, "10", state.Delegates[setup.owner.Address].AmountPerBlock)
		require.Equal(t, setup.ownerBlobLimit, state.Delegates[setup.owner.Address].BlobKeyLimit)
		require.Equal(t, "1", state.Delegates[setup.successor.Address].AmountPerBlock)
		require.Equal(t, "10", state.Delegates[setup.guardianActor.Address].AmountPerBlock)
		require.Equal(t, templateruntime.AutopayStatusActive, state.Delegates[setup.owner.Address].Status)
		require.Equal(t, templateruntime.AutopayStatusActive, state.Delegates[setup.successor.Address].Status)
		require.Equal(t, templateruntime.AutopayStatusActive, state.Delegates[setup.guardianActor.Address].Status)
	}
}

func assertPaidDKVSModuleKeysAbsent(t *testing.T, setup *accountManagementAutopaySyncSetup) {
	t.Helper()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	miner := newDKVSKeyPathActor(t, keyFromMnemonic(t, minerMnemonic, 0))
	keys := []string{}
	for _, path := range []string{"review-paid/value", "review-paid-ack/value", "review-upgrade/value", "review-paid-renew/value"} {
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), path)
		require.NoError(t, err)
		keys = append(keys, key)
	}
	unfundedKey, err := dkvs.PersonalKey(miner.Wallet.GetPubKey().SerializeCompressed(), "review-unfunded/value")
	require.NoError(t, err)
	keys = append(keys, unfundedKey)
	for _, path := range []string{"authenticity/pwa", "previous-owner-fresh", "successor-fresh"} {
		key, err := dkvs.ServiceKey("reviewsvc.btc", path)
		require.NoError(t, err)
		keys = append(keys, key)
	}
	for _, node := range []*testHarness{setup.fixture.Network.Core, setup.fixture.Network.Bootstrap} {
		for _, key := range keys {
			requireDKVSAbsent(t, node, key)
		}
	}
}

func logPaidAccountDKVSFunding(t *testing.T, setup *accountManagementAutopaySyncSetup) {
	t.Helper()
	for index, out := range setup.gasOuts {
		t.Logf("paid-account-dkvs-funding: gas_out[%d]=%v value=%d", index, out.Point, out.Output.Value)
	}
	for index, out := range setup.feeOuts {
		t.Logf("paid-account-dkvs-funding: fee_out[%d]=%v value=%d assets=%v", index, out.Point, out.Output.Value, out.Output.Assets)
	}
}

func capturePaidAccountDKVSSnapshot(t *testing.T, setup *accountManagementAutopaySyncSetup) paidAccountDKVSSnapshot {
	t.Helper()
	core := setup.fixture.Network.Core
	bootstrap := setup.fixture.Network.Bootstrap
	miner := setup.fixture.Network.Miner
	tip, height, err := core.Client.GetBestBlock()
	require.NoError(t, err)
	pool := setup.contractAddress.MustEncode()
	return paidAccountDKVSSnapshot{
		height: height, tip: tip.String(),
		coreAutopay:   summarizePaidAutopayView(t, core, pool),
		bootstrapPay:  summarizePaidAutopayView(t, bootstrap, pool),
		coreRecords:   capturePaidAccountDKVSRecords(t, core, paidAccountDKVSTrackedKeys(t, setup)),
		bootstrapRecs: capturePaidAccountDKVSRecords(t, bootstrap, paidAccountDKVSTrackedKeys(t, setup)),
		minerRecords:  capturePaidAccountDKVSRecords(t, miner, paidAccountDKVSTrackedKeys(t, setup)),
	}
}

func summarizePaidAutopayView(t *testing.T, node *testHarness, pool string) string {
	t.Helper()
	state := fetchTemplateAutopayView(t, node, pool)
	delegates := make([]string, 0, len(state.Delegates))
	for address, delegate := range state.Delegates {
		delegates = append(delegates, fmt.Sprintf("%s:%s:%s:%d:%s:%d", address,
			delegate.Status, delegate.AmountPerBlock, delegate.BlobKeyLimit, delegate.Balance, delegate.LastPayHeight))
	}
	sort.Strings(delegates)
	return fmt.Sprintf("status=%s current=%d paid=%d gas=%s fee=%s delegates=[%s]",
		state.Status, state.CurrentBlock, state.PaidBlocks, state.GasBalance, state.FeeBalance, strings.Join(delegates, ","))
}

func paidAccountDKVSTrackedKeys(t *testing.T, setup *accountManagementAutopaySyncSetup) []string {
	t.Helper()
	keys := []string{}
	addPersonal := func(mnemonic string, paths ...string) {
		actor := newDKVSKeyPathActor(t, keyFromMnemonic(t, mnemonic, 0))
		for _, path := range paths {
			key, err := dkvs.PersonalKey(actor.Wallet.GetPubKey().SerializeCompressed(), path)
			require.NoError(t, err)
			keys = append(keys, key)
		}
		binding, err := dkvs.AccountMappingKey("testnet", actor.Address)
		require.NoError(t, err)
		keys = append(keys, binding)
	}
	addPersonal(dkvsClientMnemonic,
		"review-paid/value", "review-paid-ack/value", "review-upgrade/value", "review-paid-renew/value",
		"account/state", "account/root")
	if setup.successor != nil {
		addPersonal(bootstrapMnemonic)
	}
	addPersonal(minerMnemonic, "review-unfunded/value")
	addPersonal(setup.guardianMnemonic, "account/state")

	for _, path := range []string{
		"authenticity/pwa", "previous-owner-fresh", "successor-fresh",
	} {
		key, err := dkvs.ServiceKey("reviewsvc.btc", path)
		require.NoError(t, err)
		keys = append(keys, key)
	}
	indexKey, err := account.ManagedIndexKey(setup.accountID)
	require.NoError(t, err)
	keys = append(keys, indexKey)
	if setup.packageKey != "" {
		keys = append(keys, setup.packageKey)
	}
	if setup.guardianKey != "" {
		keys = append(keys, setup.guardianKey)
	}
	sort.Strings(keys)
	unique := keys[:0]
	for _, key := range keys {
		if len(unique) == 0 || unique[len(unique)-1] != key {
			unique = append(unique, key)
		}
	}
	return unique
}

func capturePaidAccountDKVSRecords(t *testing.T, node *testHarness, keys []string) map[string]paidAccountDKVSRecordState {
	t.Helper()
	client := dkvsClientForNode(t, node)
	states := make(map[string]paidAccountDKVSRecordState, len(keys))
	for _, key := range keys {
		record, err := client.GetRecordDirect(key)
		if errors.Is(err, dkvs.ErrRecordNotFound) {
			states[key] = paidAccountDKVSRecordState{}
			continue
		}
		require.NoError(t, err)
		require.NotNil(t, record)
		states[key] = paidAccountDKVSRecordState{present: true, seq: record.Seq, hash: dkvs.RecordHash(record).String()}
	}
	return states
}

func logPaidAccountDKVSDiff(t *testing.T, setup *accountManagementAutopaySyncSetup, label string,
	before paidAccountDKVSSnapshot) {
	t.Helper()
	after := capturePaidAccountDKVSSnapshot(t, setup)
	t.Logf("paid-account-dkvs-state: case=%s height=%d->%d tip=%s->%s Core.autopay=%s->%s Bootstrap.autopay=%s->%s",
		label, before.height, after.height, before.tip, after.tip,
		before.coreAutopay, after.coreAutopay, before.bootstrapPay, after.bootstrapPay)
	for _, nodeRecords := range []struct {
		node   string
		before map[string]paidAccountDKVSRecordState
		after  map[string]paidAccountDKVSRecordState
	}{{"Core", before.coreRecords, after.coreRecords}, {"Bootstrap", before.bootstrapRecs, after.bootstrapRecs},
		{"Miner", before.minerRecords, after.minerRecords}} {
		keySet := make(map[string]struct{}, len(nodeRecords.before)+len(nodeRecords.after))
		for key := range nodeRecords.before {
			keySet[key] = struct{}{}
		}
		for key := range nodeRecords.after {
			keySet[key] = struct{}{}
		}
		keys := make([]string, 0, len(keySet))
		for key := range keySet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			t.Logf("paid-account-dkvs-key: case=%s node=%s key=%s state=%v->%v",
				label, nodeRecords.node, key, nodeRecords.before[key], nodeRecords.after[key])
		}
	}
	assertPaidAccountDKVSExpectedDelta(t, setup, label, before, after)
	require.GreaterOrEqual(t, after.height, before.height, "the shared chain must not move backwards")
}

func assertPaidAccountDKVSExpectedDelta(t *testing.T, setup *accountManagementAutopaySyncSetup,
	label string, before, after paidAccountDKVSSnapshot) {
	t.Helper()
	core := after.coreRecords
	bootstrap := after.bootstrapRecs
	if label == "AccountManagementAutopaySync" {
		ownerBinding, err := dkvs.AccountMappingKey("testnet", setup.owner.Address)
		require.NoError(t, err)
		require.False(t, before.coreRecords[ownerBinding].present, "the account case starts without an owner binding")
		require.True(t, core[ownerBinding].present, "account management must create the owner binding")
		for _, key := range []string{setup.packageKey} {
			require.NotEmpty(t, key)
			require.True(t, core[key].present, "the recovery package must be written to Core")
			require.True(t, bootstrap[key].present, "the recovery package must replicate to Bootstrap")
			require.Equal(t, core[key], bootstrap[key], "replicated recovery package state must match")
			require.NotEqual(t, before.coreRecords[key], core[key], "the recovery package must change the Core namespace")
		}
		guardianKey := setup.guardianKey
		require.NotEmpty(t, guardianKey)
		require.True(t, core[guardianKey].present, "Guardian mailbox data must stay on Core")
		require.False(t, bootstrap[guardianKey].present, "Guardian mailbox data must not replicate to Bootstrap")
		require.False(t, after.minerRecords[guardianKey].present, "Guardian mailbox data must not replicate to Miner")
		stateKey, err := dkvs.PersonalKey(keyFromMnemonic(t, dkvsClientMnemonic, 0).PubKey().SerializeCompressed(), "account/state")
		require.NoError(t, err)
		indexKey, err := account.ManagedIndexKey(setup.accountID)
		require.NoError(t, err)
		for _, key := range []string{stateKey, indexKey} {
			require.True(t, core[key].present, "managed account state must be written to Core")
			require.True(t, bootstrap[key].present, "managed account state must replicate to Bootstrap")
			require.Equal(t, core[key], bootstrap[key], "replicated managed account state must match")
			require.NotEqual(t, before.coreRecords[key], core[key], "managed account state must change")
		}
		if setup.successor != nil {
			successorBinding, err := dkvs.AccountMappingKey("testnet", setup.successor.Address)
			require.NoError(t, err)
			require.True(t, before.coreRecords[successorBinding].present,
				"the distinct DKVS signer must be bound before the account case")
			require.Equal(t, before.coreRecords[successorBinding], core[successorBinding],
				"the account case must not change the DKVS signer binding")
		}
		return
	}

	if label == "DKVSModulePaid" {
		owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
		for _, expected := range []struct {
			path string
			seq  uint64
		}{{"review-paid/value", 1}, {"review-upgrade/value", 2}, {"review-paid-renew/value", 1}} {
			key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), expected.path)
			require.NoError(t, err)
			require.False(t, before.coreRecords[key].present, "%s must begin absent", expected.path)
			require.True(t, core[key].present, "%s must be present on Core", expected.path)
			require.Equal(t, expected.seq, core[key].seq, "%s sequence must match the business lifecycle", expected.path)
			require.True(t, bootstrap[key].present, "%s must replicate to Bootstrap", expected.path)
			require.Equal(t, core[key], bootstrap[key], "%s replicated state must match", expected.path)
			require.NotEqual(t, before.coreRecords[key], core[key], "%s must create/change the owner namespace", expected.path)
		}
		for _, expected := range []struct {
			path    string
			service bool
		}{
			{path: "review-paid-ack/value"},
			{path: "authenticity/pwa", service: true},
			{path: "previous-owner-fresh", service: true},
		} {
			var key string
			var err error
			if expected.service {
				key, err = dkvs.ServiceKey("reviewsvc.btc", expected.path)
			} else {
				key, err = dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), expected.path)
			}
			require.NoError(t, err)
			require.False(t, before.coreRecords[key].present, "%s must begin absent", expected.path)
			require.False(t, core[key].present, "%s must be absent after the paid lifecycle", expected.path)
			require.False(t, bootstrap[key].present, "%s must be absent on the replication peer", expected.path)
		}
		successorKey, err := dkvs.ServiceKey("reviewsvc.btc", "successor-fresh")
		require.NoError(t, err)
		require.False(t, before.coreRecords[successorKey].present, "successor authorization starts absent")
		require.True(t, core[successorKey].present, "the successor authorization record must be present on Core")
		require.True(t, bootstrap[successorKey].present, "the successor authorization record must replicate")
		require.Equal(t, core[successorKey], bootstrap[successorKey])
		successorBinding, err := dkvs.AccountMappingKey("testnet", setup.successor.Address)
		require.NoError(t, err)
		require.True(t, before.coreRecords[successorBinding].present,
			"the distinct DKVS signer must be bound before the module case")
		require.Equal(t, before.coreRecords[successorBinding], core[successorBinding],
			"the module case must preserve the successor binding")
		return
	}
	t.Fatalf("unknown paid account/DKVS case %q", label)
}
