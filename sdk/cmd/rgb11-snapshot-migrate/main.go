package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/btcsuite/btcd/chaincfg"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	sdkwallet "github.com/sat20-labs/sat20wallet/sdk/wallet"
)

func main() {
	mnemonic := flag.String("mnemonic", "", "test wallet mnemonic")
	account := flag.Uint("account", 0, "sub-account index")
	flag.Parse()
	if *mnemonic == "" {
		fmt.Fprintln(os.Stderr, "--mnemonic is required")
		os.Exit(2)
	}
	dir, err := os.MkdirTemp("", "sat20-rgb11-migration-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	database := sdkwallet.NewKVDB(dir)
	if database == nil {
		panic("create temporary wallet database")
	}
	defer database.Close()
	cfg := &sdkcommon.Config{
		Env: "prd", Chain: "testnet", Mode: "light", DB: dir,
		IndexerL1: &sdkcommon.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "btc/testnet"},
		IndexerL2: &sdkcommon.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
	}
	activeWallet := sdkwallet.NewInternalWalletWithMnemonic(*mnemonic, "", &chaincfg.TestNet4Params)
	if activeWallet == nil {
		panic("derive test wallet")
	}
	if *account != 0 {
		activeWallet.SetSubAccount(uint32(*account))
	}
	fmt.Fprintln(os.Stderr, "migration: synchronize and migrate snapshot")
	result, err := sdkwallet.MigrateLegacyRGB11WalletSnapshotRemote(cfg, database, activeWallet)
	if err != nil {
		panic(err)
	}
	fmt.Fprintln(os.Stderr, "migration: completed")
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(encoded))
}
