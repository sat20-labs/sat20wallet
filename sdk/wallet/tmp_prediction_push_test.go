package wallet

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func useOrdXTestnetIndexers(managers ...*Manager) {
	for _, manager := range managers {
		l1 := NewIndexerClient("https", "apiprd.ordx.market", "btc/testnet", manager.http)
		l2 := NewIndexerClient("https", "apiprd.ordx.market", "satsnet/testnet", manager.http)
		manager.l1IndexerClient.SetMaster(l1)
		manager.l2IndexerClient.SetMaster(l2)
		manager.SetIndexerHttpClient(l1)
		manager.SetIndexerHttpClient_SatsNet(l2)
	}
}

func TestTmpPushPredictionBlocks_TestNet(t *testing.T) {
	if os.Getenv("SAT20WALLET_CONTRACT_TESTNET") != "1" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET=1 to modify the live SatoshiNet testnet")
	}
	prepare_TestNet4(t)
	defer clean(t)
	useOrdXTestnetIndexers(_client, _server, _bootstrap)

	const contract = "tc1qyp4sqpn8mgx9f4pzd5crh2ffyxlpwv8n8ndk590s8z7a2kq9fa4l7qdt6vtr"

	type hop struct {
		from *Manager
		to   *Manager
	}
	hops := []hop{
		{from: _client, to: _server},
		{from: _server, to: _bootstrap},
		{from: _bootstrap, to: _client},
	}

	height := int(_client.l2IndexerClient.GetSyncHeight())
	t.Logf("initial L2 height: %d", height)
	for i := 0; i < 16; i++ {
		var txid string
		for attempt := 0; attempt < len(hops)*3; attempt++ {
			h := hops[(i+attempt)%len(hops)]
			tx, err := h.from.SendAssets_SatsNet(h.to.GetWallet().GetAddress(), ASSET_PLAIN_SAT.String(), "10", nil)
			if err != nil {
				t.Logf("push tx %d attempt %d failed from %s: %v", i+1, attempt+1, h.from.GetWallet().GetAddress(), err)
				time.Sleep(2 * time.Second)
				continue
			}
			txid = tx.TxID()
			t.Logf("push tx %d: %s -> %s %s", i+1, h.from.GetWallet().GetAddress(), h.to.GetWallet().GetAddress(), txid)
			break
		}
		if txid == "" {
			t.Fatalf("push tx %d failed after retries", i+1)
		}

		deadline := time.Now().Add(90 * time.Second)
		for {
			next := int(_client.l2IndexerClient.GetSyncHeight())
			if next > height {
				height = next
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("height did not advance after tx %s, still %d", txid, height)
			}
			time.Sleep(3 * time.Second)
		}
		t.Logf("L2 height advanced to %d after %s", height, txid)

		status, err := queryContractState(contract)
		if err == nil {
			fmt.Printf("contract state at height %d: %s\n", height, status)
		} else {
			t.Logf("query contract state failed at height %d: %v", height, err)
		}
		if height >= 3446 {
			return
		}
	}
}

func queryContractState(contract string) (string, error) {
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://apiprd.ordx.market/satsnet/testnet/v3/contracts/" + contract + "/state")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}
