package main

import (
	"github.com/sat20-labs/sat20wallet/sdk/config"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func main() {

	interceptor, err := utils.Intercept()
	if err != nil {
		return
	}

	cfg, err := config.InitConfig()
	if err != nil {
		wallet.Log.Errorf("InitConfig failed")
		return
	}
	wallet.InitLog(cfg)

	db := wallet.NewKVDB(cfg.DB)
	if db == nil {
		wallet.Log.Errorf("NewKVDB failed")
		return
	}
	mgr := wallet.NewManager(cfg, db)
	if mgr == nil {
		wallet.Log.Info("NewSTPManager failed.")
		return
	}
	defer mgr.Close()

	if wallet.IsTestNet() {
		// TODO 为了测试方便，默认生成钱包，
		pw := "12345678"
		_, err := mgr.UnlockWallet(pw)
		if err != nil {
			// if mgr.IsBootstrapNode() {
			mnemonic := "acquire pet news congress unveil erode paddle crumble blue fish match eye"
			_, err := mgr.ImportWallet(mnemonic, pw)
			if err != nil {
				wallet.Log.Errorf("ImportWallet failed. %v", err)
				return
			}
			// } else {
			// 	mnemonic, err := mgr.CreateWallet(pw)
			// 	if err != nil {
			// 		wallet.Log.Errorf("CreateWallet failed. %v", err)
			// 		return
			// 	}
			// 	wallet.Log.Infof("mnemonic: %s", mnemonic)
			// }
		}

		wallet.Log.Infof("wallet address: %s", mgr.GetWallet().GetAddress())
	}
	// 生产环境，需要手动创建钱包，并且解锁
	mgr.Start()

	<-interceptor.ShutdownChannel()

	wallet.Log.Info("main exit.")
}
