package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	contract "github.com/sat20-labs/satoshinet/contract"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

type dkvsNoPluginSharedSnapshot struct {
	height          int32
	tip             string
	accountID       string
	bindingPresent  bool
	bindingSeq      uint64
	bindingHash     string
	personalRecords int
	personalStates  int
	personalView    uint64
	personalHash    string
}

type dkvsNoPluginSharedIdentity struct {
	label    string
	mnemonic string
	before   dkvsNoPluginSharedSnapshot
}

// A single no-plugin network exercises the real-node cases that previously
// constructed identical three-node fixtures. Each wallet identity, local DB,
// and any spendable outpoint remains scoped to its case.
func TestSDKDKVSNoPluginSharedNetworkGroup(t *testing.T) {
	setupDone := e2ePhase(t, "dkvs_noplugin_shared_network_setup")
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)
	setupDone()
	runSDKDKVSNoPluginSharedNetworkGroup(t, fixture)
}

// Keep the group body injectable so it can share a fixture with other real-node
// E2E groups; ModuleLocal needs the fixture's actor and gas anchor as well as its network.
func runSDKDKVSNoPluginSharedNetworkGroup(t *testing.T, fixture *templateFixture) {
	t.Helper()
	runSDKDKVSActiveBoundSharedGroup(t, fixture)
	cases := []struct {
		name          string
		seed          byte
		secondarySeed byte
		run           func(*testing.T, *templateFixture, string, string)
	}{
		{name: "ConfirmedReplica", seed: 245, run: func(t *testing.T, f *templateFixture, mnemonic, _ string) {
			runSDKDKVSConfirmedReplica(t, f, mnemonic)
		}},
		{name: "ManagerFreeLocalIsolationAndRecovery", seed: 246, run: func(t *testing.T, f *templateFixture, mnemonic, _ string) {
			runRealSatoshiNetDKVSManagerFreeLocalIsolationAndRecovery(t, f, mnemonic)
		}},
		{name: "ModuleReplica", seed: 247, run: func(t *testing.T, f *templateFixture, mnemonic, _ string) {
			runSDKDKVSModuleReplica(t, f, mnemonic)
		}},
		{name: "RGBPublicRoute", run: func(t *testing.T, f *templateFixture, _, _ string) {
			runRGBPublicRouteE2E(t, f.Network)
		}},
		// This case advances the chain and spends only the fixture gas anchor, so
		// it runs last and owns that outpoint exclusively.
		{name: "ModuleLocal", seed: 248, secondarySeed: 243, run: func(t *testing.T, f *templateFixture, mnemonic, quotaMnemonic string) {
			runSDKDKVSModuleLocal(t, f, mnemonic, quotaMnemonic)
		}},
	}

	for _, testCase := range cases {
		testCase := testCase
		passed := t.Run(testCase.name, func(t *testing.T) {
			gasBefore := captureDKVSNoPluginGroupGas(t, fixture)
			var identities []dkvsNoPluginSharedIdentity
			if testCase.seed != 0 {
				mnemonic := accountSyncMnemonic(t, testCase.seed)
				identities = append(identities, dkvsNoPluginSharedIdentity{
					label: "primary", mnemonic: mnemonic, before: captureDKVSNoPluginSharedSnapshot(t, fixture, mnemonic),
				})
			}
			if testCase.secondarySeed != 0 {
				mnemonic := accountSyncMnemonic(t, testCase.secondarySeed)
				identities = append(identities, dkvsNoPluginSharedIdentity{
					label: "secondary", mnemonic: mnemonic, before: captureDKVSNoPluginSharedSnapshot(t, fixture, mnemonic),
				})
			}
			for _, identity := range identities {
				require.False(t, identity.before.bindingPresent, "%s identity must start without a binding", identity.label)
				require.Zero(t, identity.before.personalRecords, "%s personal namespace must start empty", identity.label)
				require.Zero(t, identity.before.personalStates, "%s personal namespace must have no prior key states", identity.label)
			}

			quotaMnemonic := ""
			if testCase.secondarySeed != 0 {
				quotaMnemonic = identities[1].mnemonic
			}
			testCase.run(t, fixture, firstIdentity(identities), quotaMnemonic)

			gasAfter := captureDKVSNoPluginGroupGas(t, fixture)
			require.Equal(t, gasBefore, gasAfter, "the no-plugin cases must preserve the fixture gas-token balance")
			for _, identity := range identities {
				after := captureDKVSNoPluginSharedSnapshot(t, fixture, identity.mnemonic)
				t.Logf("dkvs-shared-case-state: case=%s identity=%s account_id=%s height=%d->%d tip=%s->%s binding=%t:%d:%s->%t:%d:%s personal_records=%d->%d personal_states=%d->%d personal_view=%d->%d personal_hash=%s->%s gas_asset=%s->%s",
					testCase.name, identity.label, identity.before.accountID,
					identity.before.height, after.height, identity.before.tip, after.tip,
					identity.before.bindingPresent, identity.before.bindingSeq, identity.before.bindingHash,
					after.bindingPresent, after.bindingSeq, after.bindingHash,
					identity.before.personalRecords, after.personalRecords,
					identity.before.personalStates, after.personalStates,
					identity.before.personalView, after.personalView,
					identity.before.personalHash, after.personalHash, gasBefore, gasAfter)
				require.GreaterOrEqual(t, after.height, identity.before.height, "the shared chain must not move backwards")
				require.True(t, after.bindingPresent, "%s case must leave its account binding", identity.label)
				require.Greater(t, after.bindingSeq, identity.before.bindingSeq, "%s binding sequence must advance", identity.label)
				require.NotEqual(t, identity.before.bindingHash, after.bindingHash, "%s binding must change", identity.label)
				require.NotEqual(t, identity.before.personalHash, after.personalHash, "%s personal DKVS state must change", identity.label)
			}
		})
		if !passed {
			onlySelector := sharedE2ERerunSelector(t, testCase.name)
			rootSelector := sharedE2ERootRerunSelector(t)
			t.Fatalf("stopped shared-network DKVS group after %s failed; rerun only it on a fresh network with -run '%s' or rerun the whole fresh group with -run '%s'", testCase.name, onlySelector, rootSelector)
		}
	}
}

func firstIdentity(identities []dkvsNoPluginSharedIdentity) string {
	if len(identities) == 0 {
		return ""
	}
	return identities[0].mnemonic
}

func captureDKVSNoPluginSharedSnapshot(t *testing.T, fixture *templateFixture, mnemonic string) dkvsNoPluginSharedSnapshot {
	t.Helper()
	key := keyFromMnemonic(t, mnemonic, 0)
	actor := newDKVSKeyPathActor(t, key)
	accountID := dkvs.AccountID(key.PubKey().SerializeCompressed())
	bindingKey, err := dkvs.AccountMappingKey("testnet", actor.Address)
	require.NoError(t, err)
	tip, height, err := fixture.Network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	client := dkvsClientForNode(t, fixture.Network.Core)
	binding, err := client.GetRecordDirect(bindingKey)
	bindingPresent, bindingSeq, bindingHash := false, uint64(0), ""
	if errors.Is(err, dkvs.ErrRecordNotFound) {
		err = nil
	} else {
		require.NoError(t, err)
		require.NotNil(t, binding)
		bindingPresent, bindingSeq, bindingHash = true, binding.Seq, dkvs.RecordHash(binding).String()
	}

	personalPrefix := "/personal/" + accountID
	personal, err := client.ReadPrefixContext(context.Background(), personalPrefix)
	require.NoError(t, err)
	entries := make([]string, 0, len(personal.Records)+len(personal.KeyStates))
	for _, record := range personal.Records {
		entries = append(entries, fmt.Sprintf("record:%s:%d:%s", record.Key, record.Seq, dkvs.RecordHash(record).String()))
	}
	for _, state := range personal.KeyStates {
		entries = append(entries, fmt.Sprintf("state:%s:%s:%d:%s:%d:%s", state.Key, state.Status,
			state.Seq, state.ETag, state.ExpiryHeight, state.StorageMode))
	}
	sort.Strings(entries)
	digest := sha256.Sum256([]byte(strings.Join(entries, "\n")))
	return dkvsNoPluginSharedSnapshot{
		height: height, tip: tip.String(), accountID: accountID,
		bindingPresent: bindingPresent, bindingSeq: bindingSeq, bindingHash: bindingHash,
		personalRecords: len(personal.Records), personalStates: len(personal.KeyStates),
		personalView: personal.ViewHeight, personalHash: hex.EncodeToString(digest[:]),
	}
}

func captureDKVSNoPluginGroupGas(t *testing.T, fixture *templateFixture) string {
	t.Helper()
	_, amount, err := fetchAssetBalance(fixture.Network.Core, fixture.A.Address, contract.GetGasAssetName())
	require.NoError(t, err)
	require.NotNil(t, amount)
	return amount.String()
}
