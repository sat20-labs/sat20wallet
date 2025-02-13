package wallet

import (
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

var _client *Manager

func newTestConf(mode, dbPath string) *Config {
	chain := "testnet4"
	ret := &Config{
		Chain: chain,
		Mode:  mode,
		Btcd: Bitcoin{
			Host:           "192.168.10.102:28332",
			User:           "jacky",
			Password:       "123456",
			Zmqpubrawblock: "tcp://192.168.10.102:58332",
			Zmqpubrawtx:    "tcp://192.168.10.102:58333",
		},
		IndexerL1: Indexer{
			Scheme: "http",
			Host:   "127.0.0.1:8009",
		},
		IndexerL2: Indexer{
			Scheme: "http",
			Host:   "127.0.0.1:8019",
		},
		Log: "debug",
		DB:  dbPath,
	}

	return ret
}

func createNode(t *testing.T, mode, dbPath string, quit chan struct{}) *Manager {
	cfg := newTestConf(mode, dbPath)
	manager := NewManager(cfg, quit)

	// mnemonice, err := manager.CreateWallet("123456")
	// if err != nil {
	// 	t.Fatalf("CreateWallet failed. %v", err)
	// }
	// fmt.Printf("mnemonice:%s\n", mnemonice)

	if manager.IsWalletExist() {
		_, err := manager.UnlockWallet("123456")
		if err != nil {
			t.Fatalf("UnlockWallet failed. %v", err)
		}
	} else {
		mnemonic := ""
		
		mnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
		_, err := manager.ImportWallet(mnemonic, "123456")
		if err != nil {
			t.Fatalf("ImportWallet failed. %v", err)
		}
	}
	
	// tb1p62gjhywssq42tp85erlnvnumkt267ypndrl0f3s4sje578cgr79sekhsua
	// nodeId: 03258dd933765d50bc88630c6584726f739129d209bfeb21053c37a3b62e7a4ab1
	// pkscript: 5120d2912b91d0802aa584f4c8ff364f9bb2d5af103368fef4c61584b34f1f081f8b

	fmt.Printf("address: %s\n", manager.GetWallet().GetAddress(0))
	pkScript, _ := GetP2TRpkScript(manager.GetWallet().GetPaymentPubKey())
	fmt.Printf("pkscript: %s\n", hex.EncodeToString(pkScript))
	fmt.Printf("nodeId: %s\n", hex.EncodeToString(manager.GetWallet().GetNodePubKey().SerializeCompressed()))


	return manager
}


func prepare(t *testing.T) {

	lc := make(chan struct{})
	err := os.RemoveAll("../db")
	if err != nil {
		t.Fatalf("RemoveAll failed: %v\n", err)
	}

	_client = createNode(t, "client", "../db/clientDB", lc)
}
