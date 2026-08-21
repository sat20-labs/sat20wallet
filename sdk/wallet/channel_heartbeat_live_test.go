package wallet

import (
	"os"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// TestLiveChannelHeartbeatRestore verifies the production ping -> action/sync
// recovery path against the configured testnet service. It is opt-in because
// it requires network access and a wallet that already owns a READY channel.
func TestLiveChannelHeartbeatRestore(t *testing.T) {
	if os.Getenv("SAT20_LIVE_CHANNEL_HEARTBEAT") != "1" {
		t.Skip("set SAT20_LIVE_CHANNEL_HEARTBEAT=1 to run the real testnet recovery")
	}
	mnemonic := os.Getenv("SAT20_TEST_MNEMONIC")
	if mnemonic == "" {
		t.Fatal("SAT20_TEST_MNEMONIC is required")
	}

	db := NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("create temporary wallet database")
	}
	defer db.Close()

	manager := NewManager(&common.Config{
		Env:   "prd",
		Chain: "testnet",
		Mode:  "light",
		Peers: []string{
			"b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@https://apiprd.sat20.org/stp/testnet",
			"s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@https://apiprd.ordx.market/stp/testnet",
		},
		IndexerL1: &common.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "btc/testnet"},
		IndexerL2: &common.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
	}, db)
	if manager == nil {
		t.Fatal("create wallet manager")
	}
	if _, err := manager.ImportWallet(mnemonic, "123456"); err != nil {
		t.Fatalf("import test wallet: %v", err)
	}

	manager.startChannelHeartbeat()
	defer manager.stopChannelHeartbeat()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if channel := manager.GetCurrentChannel(); channel != nil {
			if channel.Status != CS_READY {
				t.Fatalf("restored channel status=%d, want READY=%d", channel.Status, CS_READY)
			}
			commitInfo, err := manager.GetCommitTxAssetInfo(channel.ChannelId)
			if err != nil {
				t.Fatalf("get restored channel commitment asset info: %v", err)
			}
			if commitInfo.TxId == "" || commitInfo.TxHex == "" ||
				len(commitInfo.InputAssets) == 0 || len(commitInfo.OutputAssets) == 0 {
				t.Fatalf("restored channel commitment asset info is incomplete: %+v", commitInfo)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("READY channel was not restored by heartbeat within 60 seconds")
}
