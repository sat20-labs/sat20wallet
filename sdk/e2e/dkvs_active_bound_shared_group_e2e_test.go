package e2e

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type dkvsActiveBoundRecordSnapshot struct {
	present bool
	seq     uint64
	hash    string
}

type dkvsActiveBoundNodeSnapshot struct {
	height  int32
	tip     string
	binding dkvsActiveBoundRecordSnapshot
	records map[string]dkvsActiveBoundRecordSnapshot
}

type dkvsActiveBoundSnapshot struct {
	accountID  string
	bindingKey string
	core       dkvsActiveBoundNodeSnapshot
	bootstrap  dkvsActiveBoundNodeSnapshot
}

type dkvsActiveManagerLocalSnapshot struct {
	subscriptionStatus string
	statusError        string
	recordPresent      bool
	recordSeq          uint64
	recordHash         string
	recordValue        string
	recordError        string
}

// These roots share the existing no-plugin network. Each root receives its own
// identity, while the chain and fixture remain shared. BoundWalletRPC runs
// first so its identity is unbound before it tests the admission boundary.
func runSDKDKVSActiveBoundSharedGroup(t *testing.T, fixture *templateFixture) {
	t.Helper()
	cases := []struct {
		name     string
		seed     byte
		paths    []string
		run      func(*testing.T, *templateFixture, string) *dkvsActiveManagerLocalSnapshot
		validate func(*testing.T, dkvsActiveBoundSnapshot, dkvsActiveBoundSnapshot, *dkvsActiveManagerLocalSnapshot)
	}{
		{
			name: "BoundWalletRPC", seed: 230, paths: []string{"bound-real/value"},
			run: func(t *testing.T, f *templateFixture, mnemonic string) *dkvsActiveManagerLocalSnapshot {
				runSDKDKVSBoundWalletRPC(t, f, mnemonic)
				return nil
			},
			validate: func(t *testing.T, before, after dkvsActiveBoundSnapshot, _ *dkvsActiveManagerLocalSnapshot) {
				key := "bound-real/value"
				require.True(t, after.bootstrap.binding.present, "BoundWalletRPC must replicate the owner binding to Bootstrap")
				require.Equal(t, after.core.binding.hash, after.bootstrap.binding.hash, "Core and Bootstrap must agree on the binding")
				require.False(t, after.core.records[key].present, "BoundWalletRPC must leave its key absent after deletion")
				require.False(t, after.bootstrap.records[key].present, "the unbound Bootstrap must not retain the wallet key")
			},
		},
		{
			name: "ActiveState", seed: 231, paths: dkvsActiveStateSharedPaths(),
			run: func(t *testing.T, f *templateFixture, mnemonic string) *dkvsActiveManagerLocalSnapshot {
				runSDKDKVSActiveState(t, f, mnemonic)
				return nil
			},
			validate: func(t *testing.T, before, after dkvsActiveBoundSnapshot, _ *dkvsActiveManagerLocalSnapshot) {
				for _, key := range []string{"active-basic/value", "active-pages/a", "active-reopen/value"} {
					require.True(t, after.core.records[key].present, "ActiveState must leave %s present on Core", key)
					require.NotEqual(t, before.core.records[key], after.core.records[key], "ActiveState must change %s on Core", key)
				}
			},
		},
		{
			name: "LaunchBoundReplay", seed: 232, paths: []string{"launch-bound-replay/value"},
			run: func(t *testing.T, f *templateFixture, mnemonic string) *dkvsActiveManagerLocalSnapshot {
				runSDKDKVSLaunchBoundReplay(t, f, mnemonic)
				return nil
			},
			validate: func(t *testing.T, before, after dkvsActiveBoundSnapshot, _ *dkvsActiveManagerLocalSnapshot) {
				key := "launch-bound-replay/value"
				require.False(t, after.core.records[key].present, "replay prevention must leave the deleted Core key absent")
				require.False(t, after.bootstrap.records[key].present, "the non-bound Bootstrap must not retain the key")
			},
		},
		{
			name: "ActiveManager", seed: 233, paths: []string{"active-manager/settings"},
			run: func(t *testing.T, f *templateFixture, mnemonic string) *dkvsActiveManagerLocalSnapshot {
				return runSDKDKVSActiveManager(t, f, mnemonic)
			},
			validate: func(t *testing.T, before, after dkvsActiveBoundSnapshot, local *dkvsActiveManagerLocalSnapshot) {
				key := "active-manager/settings"
				require.True(t, after.core.records[key].present, "ActiveManager must leave its final record on Core")
				require.NotEqual(t, before.core.records[key], after.core.records[key], "ActiveManager must change its Core record")
				require.NotNil(t, local, "ActiveManager must report its local replica state")
				require.Empty(t, local.statusError)
				require.Equal(t, "READY", local.subscriptionStatus)
				require.Empty(t, local.recordError)
				require.True(t, local.recordPresent)
				require.Equal(t, "after-own-delete", local.recordValue)
				require.Equal(t, after.core.records[key].hash, local.recordHash,
					"the local replica record must match the final Core record")
			},
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		passed := t.Run(testCase.name, func(t *testing.T) {
			mnemonic := accountSyncMnemonic(t, testCase.seed)
			owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, mnemonic, 0))
			trackedKeys := dkvsActiveBoundTrackedKeys(t, owner, testCase.paths)
			before := captureDKVSActiveBoundSnapshot(t, fixture, owner, trackedKeys)
			var local *dkvsActiveManagerLocalSnapshot

			// Cleanup captures post-state even when an existing business assertion
			// fails, so reruns can start from a fresh fixture with useful evidence.
			t.Cleanup(func() {
				after := captureDKVSActiveBoundSnapshot(t, fixture, owner, trackedKeys)
				t.Logf("dkvs-active-bound-shared-state: case=%s account_id=%s binding_key=%s core_height=%d->%d core_tip=%s->%s bootstrap_height=%d->%d bootstrap_tip=%s->%s core_binding=%s->%s bootstrap_binding=%s->%s core_records={%s}->{%s} bootstrap_records={%s}->{%s}",
					testCase.name, before.accountID, before.bindingKey,
					before.core.height, after.core.height, before.core.tip, after.core.tip,
					before.bootstrap.height, after.bootstrap.height, before.bootstrap.tip, after.bootstrap.tip,
					dkvsActiveBoundRecordString(before.core.binding), dkvsActiveBoundRecordString(after.core.binding),
					dkvsActiveBoundRecordString(before.bootstrap.binding), dkvsActiveBoundRecordString(after.bootstrap.binding),
					dkvsActiveBoundRecordMapString(before.core.records), dkvsActiveBoundRecordMapString(after.core.records),
					dkvsActiveBoundRecordMapString(before.bootstrap.records), dkvsActiveBoundRecordMapString(after.bootstrap.records))
				require.GreaterOrEqual(t, after.core.height, before.core.height, "Core height must not move backwards")
				require.GreaterOrEqual(t, after.bootstrap.height, before.bootstrap.height, "Bootstrap height must not move backwards")
				require.True(t, after.core.binding.present, "each case identity must be bound on Core")
				require.Greater(t, after.core.binding.seq, before.core.binding.seq, "each case must advance its Core binding sequence")
				require.NotEqual(t, before.core.binding.hash, after.core.binding.hash, "each case must change its Core binding")
				testCase.validate(t, before, after, local)
			})

			require.False(t, before.core.binding.present, "the case identity must begin unbound on Core")
			require.False(t, before.bootstrap.binding.present, "the case identity must begin unbound on Bootstrap")
			for key, state := range before.core.records {
				require.False(t, state.present, "the case key %s must start absent on Core", key)
			}
			for key, state := range before.bootstrap.records {
				require.False(t, state.present, "the case key %s must start absent on Bootstrap", key)
			}

			local = testCase.run(t, fixture, mnemonic)
			if local != nil {
				t.Logf("dkvs-active-bound-manager-local-state: case=%s status=%s status_error=%q record_present=%t record_seq=%d record_hash=%s record_value=%q record_error=%q",
					testCase.name, local.subscriptionStatus, local.statusError, local.recordPresent,
					local.recordSeq, local.recordHash, local.recordValue, local.recordError)
			}
		})
		if !passed {
			onlySelector := sharedE2ERerunSelector(t, testCase.name)
			rootSelector := sharedE2ERootRerunSelector(t)
			t.Fatalf("stopped shared no-plugin DKVS group after %s failed; rerun only it on a fresh network with -run '%s' or rerun the whole fresh group with -run '%s'",
				testCase.name, onlySelector, rootSelector)
		}
	}
}

func dkvsActiveStateSharedPaths() []string {
	paths := []string{
		"active-basic/value",
		"active-watch/watched",
		"active-watch/other",
		"active-delete/value",
		"active-delete/sibling",
		"active-delete/pending",
		"active-reopen/value",
	}
	for n := 0; n < 17; n++ {
		paths = append(paths, "active-pages/"+string(rune('a'+n)))
	}
	return paths
}

func dkvsActiveBoundTrackedKeys(t *testing.T, owner *dkvsKeyPathActor, paths []string) map[string]string {
	t.Helper()
	keys := make(map[string]string, len(paths))
	for _, path := range paths {
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), path)
		require.NoError(t, err)
		keys[path] = key
	}
	return keys
}

func captureDKVSActiveBoundSnapshot(t *testing.T, fixture *templateFixture, owner *dkvsKeyPathActor,
	trackedKeys map[string]string) dkvsActiveBoundSnapshot {
	t.Helper()
	accountID := dkvs.AccountID(owner.Wallet.GetPubKey().SerializeCompressed())
	bindingKey, err := dkvs.AccountMappingKey("testnet", owner.Address)
	require.NoError(t, err)
	return dkvsActiveBoundSnapshot{
		accountID: accountID, bindingKey: bindingKey,
		core:      captureDKVSActiveBoundNodeSnapshot(t, fixture.Network.Core, bindingKey, trackedKeys),
		bootstrap: captureDKVSActiveBoundNodeSnapshot(t, fixture.Network.Bootstrap, bindingKey, trackedKeys),
	}
}

func captureDKVSActiveBoundNodeSnapshot(t *testing.T, node *testHarness, bindingKey string,
	trackedKeys map[string]string) dkvsActiveBoundNodeSnapshot {
	t.Helper()
	tip, height, err := node.Client.GetBestBlock()
	require.NoError(t, err)
	client := dkvsClientForNode(t, node)
	snapshot := dkvsActiveBoundNodeSnapshot{
		height: height, tip: tip.String(),
		binding: captureDKVSActiveBoundRecord(t, client, bindingKey),
		records: make(map[string]dkvsActiveBoundRecordSnapshot, len(trackedKeys)),
	}
	for label, key := range trackedKeys {
		snapshot.records[label] = captureDKVSActiveBoundRecord(t, client, key)
	}
	return snapshot
}

func captureDKVSActiveBoundRecord(t *testing.T, client interface {
	GetRecordDirect(string) (*wire.DKVSRecord, error)
}, key string) dkvsActiveBoundRecordSnapshot {
	t.Helper()
	record, err := client.GetRecordDirect(key)
	if errors.Is(err, dkvs.ErrRecordNotFound) {
		return dkvsActiveBoundRecordSnapshot{}
	}
	require.NoError(t, err)
	require.NotNil(t, record)
	return dkvsActiveBoundRecordSnapshot{present: true, seq: record.Seq, hash: dkvs.RecordHash(record).String()}
}

func dkvsActiveBoundRecordString(state dkvsActiveBoundRecordSnapshot) string {
	return fmt.Sprintf("%t:%d:%s", state.present, state.seq, state.hash)
}

func dkvsActiveBoundRecordMapString(records map[string]dkvsActiveBoundRecordSnapshot) string {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+":"+dkvsActiveBoundRecordString(records[key]))
	}
	return strings.Join(parts, ",")
}
