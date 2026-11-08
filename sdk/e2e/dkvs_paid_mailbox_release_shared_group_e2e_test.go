package e2e

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dkvsPaidMailboxReleaseRecordState struct {
	present bool
	seq     uint64
	hash    string
}

type dkvsPaidMailboxReleaseSnapshot struct {
	height         int32
	tip            string
	mailboxRecords int
	coreRecords    map[string]dkvsPaidMailboxReleaseRecordState
	bootstrapRecs  map[string]dkvsPaidMailboxReleaseRecordState
}

type dkvsPaidAutopayNameMailboxSnapshot struct {
	height             int32
	tip                string
	nameKey            string
	nameByNode         map[string]dkvsPaidMailboxReleaseRecordState
	bootstrapMailboxes map[string]map[string]dkvsPaidMailboxReleaseRecordState
}

// These paid-DKVS roots use the same AUTOPAY fixture. Each case verifies its
// own namespace before and after running on the shared network.
func TestSDKDKVSPaidMailboxReleaseSharedNetworkGroup(t *testing.T) {
	fixture, owner, successor, payment := sdkDKVSReviewPaidFixture(t)
	accountID := dkvs.AccountID(owner.Wallet.GetPubKey().SerializeCompressed())
	mailboxPrefix := "/mail/" + accountID
	releaseKeys := paidMailboxReleaseKeys(t, owner)

	mailboxBefore := capturePaidMailboxReleaseSnapshot(t, fixture, releaseKeys, mailboxPrefix)
	require.Zero(t, mailboxBefore.mailboxRecords, "the shared mailbox namespace must start empty")
	mailboxPassed := t.Run("ActiveMailbox", func(t *testing.T) {
		runSDKDKVSActiveMailbox(t, fixture)
	})
	mailboxAfter := capturePaidMailboxReleaseSnapshot(t, fixture, releaseKeys, mailboxPrefix)
	mailboxStatePassed := logPaidMailboxReleaseDiff(t, "ActiveMailbox", mailboxBefore, mailboxAfter)
	mailboxDeltaPassed := true
	if !assert.Equal(t, mailboxBefore.coreRecords, mailboxAfter.coreRecords,
		"mailbox deletion must not change the release-test keys") {
		mailboxDeltaPassed = false
	}
	if !assert.Equal(t, mailboxBefore.bootstrapRecs, mailboxAfter.bootstrapRecs,
		"mailbox deletion must not change release-test keys on Bootstrap") {
		mailboxDeltaPassed = false
	}
	if !assert.Equal(t, mailboxBefore.mailboxRecords, mailboxAfter.mailboxRecords,
		"the self-message must be removed from the mailbox namespace") {
		mailboxDeltaPassed = false
	}
	if !mailboxPassed || !mailboxStatePassed || !mailboxDeltaPassed {
		t.Fatalf("stopped shared paid-DKVS group after ActiveMailbox failed; rerun only it with -run '^TestSDKDKVSPaidMailboxReleaseSharedNetworkGroup$/^ActiveMailbox$' or rerun the whole fresh group with -run '^TestSDKDKVSPaidMailboxReleaseSharedNetworkGroup$'")
	}

	nameKey, err := dkvs.NameKey(dkvsE2EName)
	require.NoError(t, err)
	autopaySender := bindDKVSReviewWallet(t, fixture.Network.Core, accountSyncMnemonic(t, 254))
	autopayRecipient := bindDKVSReviewWallet(t, fixture.Network.Core, accountSyncMnemonic(t, 255))
	recipientID := dkvs.AccountID(autopayRecipient.GetWallet().GetPubKey().SerializeCompressed())
	recipientMailboxPrefix := "/mail/" + recipientID
	autopayBefore := capturePaidAutopayNameMailboxSnapshot(t, fixture, nameKey, recipientMailboxPrefix)
	for node, state := range autopayBefore.nameByNode {
		require.False(t, state.present, "the shared 8888.btc name must start absent on %s", node)
	}
	for node, records := range autopayBefore.bootstrapMailboxes {
		require.Empty(t, records, "the bootstrap recipient mailbox must start empty on %s", node)
	}
	var autopayMessageKey string
	autopayPassed := t.Run("AutopayNameAndMailboxSync", func(t *testing.T) {
		autopayMessageKey = runRealSatoshiNetDKVSAutopayNameAndMailboxSync(t, fixture, owner, successor, payment,
			autopaySender, autopayRecipient)
	})
	autopayAfter := capturePaidAutopayNameMailboxSnapshot(t, fixture, nameKey, recipientMailboxPrefix)
	autopayStatePassed := logPaidAutopayNameMailboxDiff(t, autopayBefore, autopayAfter)
	autopayDeltaPassed := assertPaidAutopayNameMailboxExpectedDelta(t, autopayBefore, autopayAfter, autopayMessageKey)
	if !autopayPassed || !autopayStatePassed || !autopayDeltaPassed {
		t.Fatalf("stopped shared paid-DKVS group after AutopayNameAndMailboxSync failed; rerun only it with -run '^TestSDKDKVSPaidMailboxReleaseSharedNetworkGroup$/^AutopayNameAndMailboxSync$' or rerun the whole fresh group with -run '^TestSDKDKVSPaidMailboxReleaseSharedNetworkGroup$'")
	}

	releaseBefore := capturePaidMailboxReleaseSnapshot(t, fixture, releaseKeys, mailboxPrefix)
	for _, state := range releaseBefore.coreRecords {
		require.False(t, state.present, "ReleaseReview keys must begin absent on Core")
	}
	for _, state := range releaseBefore.bootstrapRecs {
		require.False(t, state.present, "ReleaseReview keys must begin absent on Bootstrap")
	}
	releasePassed := t.Run("ReleaseReviewNetwork", func(t *testing.T) {
		runSDKDKVSReleaseReviewNetwork(t, fixture, owner, successor, payment)
	})
	releaseAfter := capturePaidMailboxReleaseSnapshot(t, fixture, releaseKeys, mailboxPrefix)
	releaseStatePassed := logPaidMailboxReleaseDiff(t, "ReleaseReviewNetwork", releaseBefore, releaseAfter)
	releaseDeltaPassed := assertPaidMailboxReleaseExpectedDelta(t, releaseBefore, releaseAfter, releaseKeys)
	if !releasePassed || !releaseStatePassed || !releaseDeltaPassed {
		t.Fatalf("stopped shared paid-DKVS group after ReleaseReviewNetwork failed; rerun only it with -run '^TestSDKDKVSPaidMailboxReleaseSharedNetworkGroup$/^ReleaseReviewNetwork$' or rerun the whole fresh group with -run '^TestSDKDKVSPaidMailboxReleaseSharedNetworkGroup$'")
	}
}

func capturePaidAutopayNameMailboxSnapshot(t *testing.T, fixture *templateFixture, nameKey,
	bootstrapMailboxPrefix string) dkvsPaidAutopayNameMailboxSnapshot {
	t.Helper()
	tip, height, err := fixture.Network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	nodes := []struct {
		name string
		node *testHarness
	}{
		{name: "Core", node: fixture.Network.Core},
		{name: "Bootstrap", node: fixture.Network.Bootstrap},
		{name: "Miner", node: fixture.Network.Miner},
	}
	snapshot := dkvsPaidAutopayNameMailboxSnapshot{
		height: height, tip: tip.String(), nameKey: nameKey,
		nameByNode:         make(map[string]dkvsPaidMailboxReleaseRecordState, len(nodes)),
		bootstrapMailboxes: make(map[string]map[string]dkvsPaidMailboxReleaseRecordState, len(nodes)),
	}
	for _, item := range nodes {
		client := dkvsClientForNode(t, item.node)
		snapshot.nameByNode[item.name] = capturePaidMailboxReleaseRecords(t, client, []string{nameKey})[nameKey]
		mailbox, err := client.ReadPrefixContext(context.Background(), bootstrapMailboxPrefix)
		require.NoError(t, err)
		records := make(map[string]dkvsPaidMailboxReleaseRecordState, len(mailbox.Records))
		for _, record := range mailbox.Records {
			records[record.Key] = dkvsPaidMailboxReleaseRecordState{
				present: true, seq: record.Seq, hash: dkvs.RecordHash(record).String(),
			}
		}
		snapshot.bootstrapMailboxes[item.name] = records
	}
	return snapshot
}

func logPaidAutopayNameMailboxDiff(t *testing.T, before, after dkvsPaidAutopayNameMailboxSnapshot) bool {
	t.Helper()
	t.Logf("dkvs-paid-autopay-state: height=%d->%d tip=%s->%s name_key=%s",
		before.height, after.height, before.tip, after.tip, before.nameKey)
	for _, node := range []string{"Core", "Bootstrap", "Miner"} {
		t.Logf("dkvs-paid-autopay-name: node=%s state=%v->%v bootstrap_mailbox_records=%d->%d",
			node, before.nameByNode[node], after.nameByNode[node],
			len(before.bootstrapMailboxes[node]), len(after.bootstrapMailboxes[node]))
		logPaidAutopayMailboxRecords(t, node, "before", before.bootstrapMailboxes[node])
		logPaidAutopayMailboxRecords(t, node, "after", after.bootstrapMailboxes[node])
	}
	return assert.GreaterOrEqual(t, after.height, before.height, "the shared chain must not move backwards")
}

func logPaidAutopayMailboxRecords(t *testing.T, node, phase string,
	records map[string]dkvsPaidMailboxReleaseRecordState) {
	t.Helper()
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("dkvs-paid-autopay-mailbox-record: node=%s phase=%s key=%s state=%v", node, phase, key, records[key])
	}
}

func assertPaidAutopayNameMailboxExpectedDelta(t *testing.T,
	before, after dkvsPaidAutopayNameMailboxSnapshot, messageKey string) bool {
	t.Helper()
	passed := true
	var canonical dkvsPaidMailboxReleaseRecordState
	for _, node := range []string{"Core", "Bootstrap", "Miner"} {
		beforeName := before.nameByNode[node]
		afterName := after.nameByNode[node]
		if !assert.False(t, beforeName.present, "the name key must start absent on %s", node) {
			passed = false
		}
		if !assert.True(t, afterName.present, "the rewritten name key must be present on %s", node) {
			passed = false
		}
		if !assert.Equal(t, uint64(1), afterName.seq, "rewriting a deleted name must restart at sequence 1 on %s", node) {
			passed = false
		}
		if !assert.NotEmpty(t, afterName.hash, "the rewritten name must have a record hash on %s", node) {
			passed = false
		}
		if node == "Core" {
			canonical = afterName
		} else if !assert.Equal(t, canonical, afterName, "rewritten name replicas must match Core on %s", node) {
			passed = false
		}
		if !assert.Empty(t, before.bootstrapMailboxes[node], "the bootstrap recipient mailbox must start empty on %s", node) {
			passed = false
		}
	}
	for _, node := range []string{"Core", "Bootstrap", "Miner"} {
		afterMailbox := after.bootstrapMailboxes[node]
		if !assert.Empty(t, afterMailbox, "the account message mailbox prefix must be empty after delivery on %s", node) {
			passed = false
		}
		if !assert.NotContains(t, afterMailbox, messageKey,
			"the exact account message record must be absent from the mailbox prefix on %s", node) {
			passed = false
		}
	}
	return passed
}

func paidMailboxReleaseKeys(t *testing.T, owner *dkvsKeyPathActor) []string {
	t.Helper()
	personalKey, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "release-net-delete/value")
	require.NoError(t, err)
	xKey, err := dkvs.ServiceKey("release-net.btc", "config/x")
	require.NoError(t, err)
	yKey, err := dkvs.ServiceKey("release-net.btc", "config/y")
	require.NoError(t, err)
	return []string{personalKey, xKey, yKey}
}

func capturePaidMailboxReleaseSnapshot(t *testing.T, fixture *templateFixture, keys []string,
	mailboxPrefix string) dkvsPaidMailboxReleaseSnapshot {
	t.Helper()
	tip, height, err := fixture.Network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	core := dkvsClientForNode(t, fixture.Network.Core)
	mailbox, err := core.ReadPrefixContext(context.Background(), mailboxPrefix)
	require.NoError(t, err)
	return dkvsPaidMailboxReleaseSnapshot{
		height: height, tip: tip.String(), mailboxRecords: len(mailbox.Records),
		coreRecords:   capturePaidMailboxReleaseRecords(t, core, keys),
		bootstrapRecs: capturePaidMailboxReleaseRecords(t, dkvsClientForNode(t, fixture.Network.Bootstrap), keys),
	}
}

func capturePaidMailboxReleaseRecords(t *testing.T, client *wallet.SatsNetDKVSClient,
	keys []string) map[string]dkvsPaidMailboxReleaseRecordState {
	t.Helper()
	states := make(map[string]dkvsPaidMailboxReleaseRecordState, len(keys))
	for _, key := range keys {
		record, err := client.GetRecordDirect(key)
		if errors.Is(err, dkvs.ErrRecordNotFound) {
			states[key] = dkvsPaidMailboxReleaseRecordState{}
			continue
		}
		require.NoError(t, err)
		require.NotNil(t, record)
		states[key] = dkvsPaidMailboxReleaseRecordState{
			present: true, seq: record.Seq, hash: dkvs.RecordHash(record).String(),
		}
	}
	return states
}

func logPaidMailboxReleaseDiff(t *testing.T, label string,
	before, after dkvsPaidMailboxReleaseSnapshot) bool {
	t.Helper()
	t.Logf("dkvs-paid-shared-state: case=%s height=%d->%d tip=%s->%s mailbox_records=%d->%d",
		label, before.height, after.height, before.tip, after.tip, before.mailboxRecords, after.mailboxRecords)
	logRecords := func(node string, previous, current map[string]dkvsPaidMailboxReleaseRecordState) {
		keys := make([]string, 0, len(previous))
		for key := range previous {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			t.Logf("dkvs-paid-shared-key: case=%s node=%s key=%s state=%v->%v",
				label, node, key, previous[key], current[key])
		}
	}
	logRecords("Core", before.coreRecords, after.coreRecords)
	logRecords("Bootstrap", before.bootstrapRecs, after.bootstrapRecs)
	return assert.GreaterOrEqual(t, after.height, before.height, "the shared chain must not move backwards")
}

func assertPaidMailboxReleaseExpectedDelta(t *testing.T, before, after dkvsPaidMailboxReleaseSnapshot,
	keys []string) bool {
	t.Helper()
	passed := assert.Equal(t, before.mailboxRecords, after.mailboxRecords,
		"the mailbox case must leave no residual message before release tests")
	expected := []struct {
		key string
		seq uint64
	}{
		{key: keys[0], seq: 2},
		{key: keys[1], seq: 3},
		{key: keys[2], seq: 2},
	}
	for _, item := range expected {
		beforeCore, afterCore := before.coreRecords[item.key], after.coreRecords[item.key]
		beforeBootstrap, afterBootstrap := before.bootstrapRecs[item.key], after.bootstrapRecs[item.key]
		if !assert.False(t, beforeCore.present, "release key must start absent on Core: %s", item.key) {
			passed = false
		}
		if !assert.False(t, beforeBootstrap.present, "release key must start absent on Bootstrap: %s", item.key) {
			passed = false
		}
		if !assert.True(t, afterCore.present, "release key must be present on Core: %s", item.key) {
			passed = false
		}
		if !assert.Equal(t, item.seq, afterCore.seq, "release key sequence must match the lifecycle: %s", item.key) {
			passed = false
		}
		if !assert.True(t, afterBootstrap.present, "release key must replicate to Bootstrap: %s", item.key) {
			passed = false
		}
		if !assert.Equal(t, afterCore, afterBootstrap, "release key replicas must match: %s", item.key) {
			passed = false
		}
		if !assert.NotEqual(t, beforeCore, afterCore, "release key must change: %s", item.key) {
			passed = false
		}
	}
	return passed
}
