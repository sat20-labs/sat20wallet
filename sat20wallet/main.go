package main

import (
	"fmt"
	"io"
	"os"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/sat20-labs/sat20wallet/wallet/utils"
	"github.com/sat20-labs/sat20wallet/wallet"
	"github.com/sirupsen/logrus"
)

func main() {

	interceptor, err := utils.Intercept()
	if err != nil {
		return 
	}

	cfg := InitConfig()
	InitLog(cfg)

	mgr := wallet.NewManager(cfg, interceptor.GetQuit())
	if mgr == nil {
		wallet.Log.Info("NewSTPManager failed.")
		return
	}

	if wallet.IsTestNet() {
		// TODO 为了测试方便，默认生成钱包，
		pw := "12345678"
		err := mgr.UnlockWallet(pw)
		if err != nil {
			// if mgr.IsBootstrapNode() {
				mnemonic := "acquire pet news congress unveil erode paddle crumble blue fish match eye"
				err := mgr.ImportWallet(mnemonic, pw)
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

		wallet.Log.Infof("wallet address: %s", mgr.GetWallet().GetP2TRAddress())
	}
	// 生产环境，需要手动创建钱包，并且解锁

	err = mgr.Init()
	if err != nil {
		wallet.Log.Infof("init failed. %v", err)
		return
	}
	defer mgr.Close()

	<-interceptor.ShutdownChannel()
	
	wallet.Log.Info("main exit.")
}


func InitLog(cfg *wallet.Config) error {
	var writers []io.Writer
	logPath := "./log/"+cfg.Chain
	
	lvl, err := logrus.ParseLevel(cfg.Log)
	if err != nil {
		lvl = logrus.InfoLevel
	}
	wallet.Log.SetLevel(lvl)
		
	fileHook, err := rotatelogs.New(
		logPath+"/stpd-%Y%m%d%H%M.log",
		rotatelogs.WithLinkName(logPath+"/stpd.log"),
		rotatelogs.WithMaxAge(30*24*time.Hour),
		rotatelogs.WithRotationTime(24*time.Hour),
	)
	if err != nil {
		return fmt.Errorf("failed to create RotateFile hook, error: %s", err)
	}
	writers = append(writers, fileHook)
	writers = append(writers, os.Stdout)
	wallet.Log.SetOutput(io.MultiWriter(writers...))
	
	return nil
}
