//go:build pwa

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Run the real RGB cross-layer case with SAT20_REAL_RGB_REORG=1. Only
// /bestheight is held back, reproducing the indexer's temporary rescan cursor.
// Bitcoin supplies both branches. Both owners use their normal contract DB.
func rgbForkNodes(t *testing.T) *realSatoshiNet {
	t.Helper()
	require.Equal(t, "1", os.Getenv("SAT20_E2E_LOCAL_REAL"))
	require.Empty(t, os.Getenv("SAT20_REAL_SATOSHINET_ATTACH"))
	raw := os.Getenv("SAT20_REAL_L1_INDEXER")
	origin, err := url.Parse(raw)
	require.NoError(t, err)
	require.True(t, net.ParseIP(origin.Hostname()).IsLoopback())
	require.Equal(t, "/btc/regtest", origin.Path)
	proxy := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: origin.Scheme, Host: origin.Host})
	var low atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := low.Load(); h > 0 && r.URL.Path == origin.Path+"/bestheight" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"code":0,"data":{"height":%d}}`, h)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("SAT20_REAL_L1_INDEXER", server.URL+origin.Path)
	network := prepareSDKPWARegtestNodes(t)
	t.Setenv("SAT20_REAL_L1_INDEXER", raw)
	network.rgbDepositFork = func(ctx context.Context, address, contract, txid string) error {
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		rpc := func(args ...string) (string, error) {
			raw, err := runRegtestCLI(ctx, os.Getenv("SAT20_REAL_BITCOIN_CLI"),
				os.Getenv("SAT20_REAL_BITCOIN_DATADIR"), os.Getenv("SAT20_REAL_BITCOIN_RPC_PORT"), args...)
			return strings.TrimSpace(string(raw)), err
		}
		mine := func(reward, txs string) (string, error) {
			raw, err := rpc("generateblock", reward, txs)
			if err != nil {
				return "", err
			}
			var block struct{ Hash string }
			if err := json.Unmarshal([]byte(raw), &block); err != nil || len(block.Hash) != 64 {
				return "", fmt.Errorf("invalid generateblock result %q", raw)
			}
			return block.Hash, nil
		}
		// Testnet4's branches diverged two blocks before the deposit tip.
		// A distinct coinbase script guarantees distinct sibling hashes.
		forkStart, err := mine("raw(51)", "[]")
		if err != nil {
			return err
		}
		orphan, err := mine(address, "[]")
		if err != nil {
			return err
		}
		count, err := rpc("getblockcount")
		if err != nil {
			return err
		}
		height, err := strconv.Atoi(count)
		if err != nil || height <= 72 {
			return fmt.Errorf("unexpected old branch height %q", count)
		}
		owners := []*testHarness{network.Bootstrap, network.Core}
		if err := waitForkPeers(ctx, owners, func(node *testHarness) bool {
			raw, err := rgbContinuationContractRead(node, "/info/contract/"+contract)
			var state struct{ CurrentBlockL1 int }
			return err == nil && json.Unmarshal(raw, &state) == nil && state.CurrentBlockL1 == height
		}); err != nil {
			return fmt.Errorf("owners did not apply empty block %d: %w", height, err)
		}
		lag := height - 72
		low.Store(int64(lag))
		defer low.Store(0)
		if _, err = rpc("invalidateblock", forkStart); err != nil {
			return err
		}
		if _, err := mine(address, "[]"); err != nil {
			return err
		}
		txList, _ := json.Marshal([]string{txid})
		replacement, err := mine(address, string(txList))
		if err != nil {
			return err
		}
		canonical, err := rpc("getblockhash", count)
		if err != nil || canonical != replacement || replacement == orphan {
			return fmt.Errorf("invalid replacement at %s: orphan=%s canonical=%s mined=%s err=%v", count, orphan, canonical, replacement, err)
		}
		if _, err := mine(address, "[]"); err != nil {
			return err
		}
		t.Logf("RGB fork ancestor=%d height=%d old=%s canonical=%s deposit=%s:1 reported=%d", height-2, height, orphan, replacement, txid, lag)
		// Both monitor ticks must actually see the regression before releasing
		// the watermark. Contract and Anchor assertions run on the real tip.
		observation := []byte(fmt.Sprintf("L1 indexer at %d behind checkpoint %d; waiting", lag, height))
		if err := waitForkPeers(ctx, owners, func(node *testHarness) bool {
			log, err := os.ReadFile(filepath.Join(node.nodeDir, "data", "logs", "regtest", "stpd.log"))
			return err == nil && bytes.Contains(log, observation)
		}); err != nil {
			return fmt.Errorf("owners did not observe L1 height %d -> %d: %w", height, lag, err)
		}
		// Expose the replacement at exactly the old tip before allowing the
		// indexer to advance. Both peers must recover the deposit at equal height.
		low.Store(int64(height))
		outpoint := txid + ":1"
		if err := waitRGBPeerDeposit(ctx, network, contract, outpoint); err != nil {
			return err
		}
		for _, owner := range owners {
			items, err := rgbContinuationHistory(owner, contract)
			if err != nil {
				return err
			}
			count := 0
			for _, item := range items {
				if item.InUtxo != outpoint {
					continue
				}
				count++
				if item.GetHeight() != height || item.InAmt == nil || item.InAmt.String() != "100" {
					return fmt.Errorf("%s: unexpected deposit height or amount for %s", owner.role, outpoint)
				}
			}
			if count != 1 {
				return fmt.Errorf("%s: deposit %s has %d history entries", owner.role, outpoint, count)
			}
			t.Logf("%s: one RGB deposit of 100 at height %d for %s", owner.role, height, outpoint)
		}
		return nil
	}
	return network
}

func waitForkPeers(ctx context.Context, nodes []*testHarness, ready func(*testHarness) bool) error {
	for _, node := range nodes {
		for !ready(node) {
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s: %w", node.role, ctx.Err())
			case <-time.After(time.Second):
			}
		}
	}
	return nil
}
