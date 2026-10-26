package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// A child is necessary to retain a real failed A verdict while verifying that
// the regression itself succeeds only when B is clean. Evidence and failed A's
// databases remain in the private child runtime, outside automatic TempDir.
func TestPWAGroupDKVSFixturesDoNotShareCommittedWrites(t *testing.T) {
	if os.Getenv("SAT20_PWA_DKVS_AB_CHILD") != "1" {
		evidence, err := os.MkdirTemp(os.TempDir(), "pwa-dkvs-ab-")
		require.NoError(t, err)
		require.NoError(t, os.Chmod(evidence, 0700))
		logPath := filepath.Join(evidence, "child.log")
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		require.NoError(t, err)
		cmd := exec.Command(os.Args[0], "-test.run=^TestPWAGroupDKVSFixturesDoNotShareCommittedWrites$", "-test.v", "-test.timeout=10m")
		cmd.Env = append(os.Environ(), "SAT20_PWA_DKVS_AB_CHILD=1", "TMPDIR="+evidence)
		cmd.Stdout, cmd.Stderr = log, log
		runErr := cmd.Run()
		require.NoError(t, log.Close())
		output, err := os.ReadFile(logPath)
		require.NoError(t, err)
		text := string(output)
		t.Logf("private DKVS A/B evidence retained: %s", evidence)
		require.Error(t, runErr, "intentional A failure must remain a nonzero child result")
		require.True(t, strings.Contains(text, "intentional group A failure after committed DKVS marker"), "A did not reach its actual committed write; inspect private child evidence")
		require.True(t, strings.Contains(text, "--- FAIL: TestPWAGroupDKVSFixturesDoNotShareCommittedWrites/A"), "A failure was lost")
		require.True(t, strings.Contains(text, "--- PASS: TestPWAGroupDKVSFixturesDoNotShareCommittedWrites/B"), "B inherited DKVS state or failed setup; inspect private child evidence")
		return
	}
	fresh := func(t *testing.T) *templateFixture {
		f := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
		waitForDKVSPeerReady(t, f.Network)
		t.Logf("owned node dir=%s rpc=%s p2p=%s", f.Network.Core.nodeDir, f.Network.Core.rpcAddr, f.Network.Core.p2pAddr)
		return f
	}
	var shared *templateFixture
	if os.Getenv("SAT20_PWA_DKVS_AB_SHARED") == "1" {
		shared = fresh(t)
	}
	for _, group := range []string{"A", "B"} {
		t.Run(group, func(t *testing.T) {
			f := shared
			if f == nil {
				f = fresh(t)
			}
			bindDKVSReviewWallet(t, f.Network.Core, dkvsClientMnemonic)
			client := dkvsClientForNode(t, f.Network.Core)
			owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
			key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), "pwa-group-isolation/probe")
			require.NoError(t, err)
			if group == "A" {
				value := []byte("group-A-dkvs-marker-v1")
				record := sdkDKVSReviewPutFree(t, client, owner, key, value)
				require.Equal(t, uint64(1), record.Seq)
				actual, err := client.GetVerifiedRecord(key, dkvs.RecordVerificationOptions{Height: sdkDKVSReviewHeight(t, client), CheckHash: true, ExpectedHash: dkvs.RecordHash(record)})
				require.NoError(t, err)
				require.True(t, bytes.Equal(value, actual.Value), "committed A marker readback mismatch")
				t.Error("intentional group A failure after committed DKVS marker")
				return
			}
			state, err := client.GetKeyState(key)
			require.NoError(t, err)
			require.Equal(t, dkvs.KeyStateNeverSeen, state.Status, "B inherited A's committed DKVS key")
			require.Zero(t, state.Seq)
			require.Empty(t, state.ETag)
			require.Nil(t, state.Record)
			record, err := client.GetRecord(key)
			require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
			require.Nil(t, record)
			t.Log("B exact initial state: never_seen, seq=0, etag empty, record absent")
		})
	}
}
