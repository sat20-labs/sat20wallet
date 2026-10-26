package e2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"testing"

	btc "github.com/btcsuite/btcd/btcec/v2"
	util "github.com/btcsuite/btcd/btcutil"
	cfg "github.com/btcsuite/btcd/chaincfg"
	script "github.com/btcsuite/btcd/txscript"
	wire "github.com/btcsuite/btcd/wire"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/stretchr/testify/require"
)

// This is a fast fixture-boundary regression, not a browser business verdict.
// The opt-in fault reproduces the former shared-ledger boundary. Group A's
// signed pending transaction and failure are retained; B must start untouched.
func TestPWAGroupL1FixturesDoNotSharePending(t *testing.T) {
	key, _ := btc.PrivKeyFromBytes(bytes.Repeat([]byte{7}, 32))
	address, err := util.NewAddressWitnessPubKeyHash(util.Hash160(key.PubKey().SerializeCompressed()), &cfg.TestNet4Params)
	require.NoError(t, err)
	var shared *posPWAL1Indexer
	var baseline posPWAL1Snapshot
	failed := make(map[string]error)
	for _, group := range []string{"A", "B"} {
		f := shared
		if f == nil || os.Getenv("SAT20_PWA_ISOLATION_SHARED_FIXTURE") != "1" {
			f = newPOSPWAL1Indexer(t, "", nil)
			f.FundAddress(t, address.EncodeAddress(), 10000, nil)
			shared = f
		}
		if group == "B" {
			require.Equal(t, baseline, f.Snapshot(), "group B inherited group A's failed transaction or confirmation state")
			require.Len(t, failed, 1, "fresh B must not erase A's failure")
			require.ErrorContains(t, failed["A"], "intentional group A failure")
			t.Log("group A=FAIL (expected injected failure retained); group B=PASS (exact initial snapshot)")
			continue
		}
		baseline = f.Snapshot()
		var point string
		for outpoint := range f.outputs {
			point = outpoint
		}
		source, ok := f.Output(point)
		require.True(t, ok)
		previous, err := wire.NewOutPointFromString(point)
		require.NoError(t, err)
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(wire.NewTxIn(previous, nil, nil))
		tx.AddTxOut(wire.NewTxOut(9800, source.PkScript))
		fetcher := script.NewCannedPrevOutputFetcher(source.PkScript, source.Value)
		hashes := script.NewTxSigHashes(tx, fetcher)
		tx.TxIn[0].Witness, err = script.WitnessSignature(tx, hashes, 0, source.Value, source.PkScript, script.SigHashAll, key, true)
		require.NoError(t, err)
		var raw bytes.Buffer
		require.NoError(t, tx.Serialize(&raw))
		body, err := json.Marshal(indexerwire.SendRawTxsReq{SignedTxHex: []string{hex.EncodeToString(raw.Bytes())}})
		require.NoError(t, err)
		response, err := http.Post(f.NodeFixture().server.URL+"/testnet/btc/txs", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		var result indexerwire.SendRawTxsResp
		require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
		require.NoError(t, response.Body.Close())
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.Zero(t, result.Code, result.Msg)
		require.Equal(t, []string{tx.TxID()}, f.Snapshot().PendingTxIDs)
		failed[group] = errors.New("intentional group A failure after real signed broadcast")
	}
	require.Len(t, failed, 1)
}

// The child deliberately skips a synthetic group, never a business case. Its
// nonzero exit proves the exact guard used by the real group runner rejects it.
func TestPWASkipGuardRejectsSkippedGroup(t *testing.T) {
	if os.Getenv("SAT20_PWA_SKIP_GUARD_CHILD") == "1" {
		t.Run("unexpected", func(t *testing.T) { defer failUnexpectedPWASkip(t); t.Skip("controlled skip fault") })
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPWASkipGuardRejectsSkippedGroup$", "-test.v", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "SAT20_PWA_SKIP_GUARD_CHILD=1", "TMPDIR="+t.TempDir())
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "a skipped real group must not produce a green exit")
	require.Contains(t, string(output), "PWA group unexpectedly skipped")
	require.Contains(t, string(output), "--- FAIL:")
}
