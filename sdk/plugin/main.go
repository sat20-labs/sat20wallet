package main

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/config"
)

var _mgr *wallet.Manager

func InitWalletMgr(dbPath string) error {

	if _mgr != nil {
		return nil
	}
	fmt.Printf("dbPath: %s\n", dbPath)
	lcfg, err := config.InitConfig()
	if err != nil {
		return fmt.Errorf("InitConfig failed, %v", err)
	}
	if dbPath != "" {
		lcfg.DB = dbPath
	}
	wallet.InitLog(lcfg)


	///////
	db := wallet.NewKVDB(lcfg.DB + "/db/stp/" + lcfg.Chain)
	if db == nil {
		wallet.Log.Errorf("NewKVDB %s failed", lcfg.DB)
		return fmt.Errorf("NewKVDB %s failed", lcfg.DB)
	}
	mgr := wallet.NewManager(lcfg, db)
	if mgr == nil {
		wallet.Log.Info("NewSTPManager failed.")
		return fmt.Errorf("NewSTPManager failed.")
	}
	_mgr = mgr

	// 需要提前把钱包解锁，节点需要计算通道地址
	if lcfg.Wallet.PSFile != "" {
		pw, err := wallet.LoadPassword(lcfg.DB+"/"+lcfg.Wallet.PSFile)
		if err == nil {
			_, err = _mgr.UnlockWallet(pw)
			if err != nil {
				wallet.Log.Warnf("UnlockWallet failed, %v", err)
			}
		} else {
			// 检查是否有初始化的助记词
			if lcfg.Wallet.Mnemonic != "" && lcfg.Wallet.Password != "" {
				wallet.Log.Info("initiate wallet by configuration wallet")
				_, err = _mgr.ImportWallet(lcfg.Wallet.Mnemonic, lcfg.Wallet.Password)
				if err != nil {
					wallet.Log.Errorf("ImportWallet failed, %v", err)
				}
			}
		}
	}

	return nil
}

// 在钱包创建或者解锁后调用
func StartWalletMgr() error {
	if _mgr == nil {
		return fmt.Errorf("STPManager not init")
	}

	_mgr.Start()
	return nil
}

func ReleaseWalletMgr() {
	if _mgr != nil {
		_mgr.Close()
		_mgr = nil
	}
}

func SignMsg(msg []byte) ([]byte, error) {
	if _mgr == nil {
		return nil, fmt.Errorf("STPManager not init")
	}
	wallet := _mgr.GetWallet()
	if wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked/connected")
	}
	sig, err := wallet.SignMessage(msg)
	if err != nil {
		return nil, err
	}

	return sig, nil
}

func IsWalletExisting() bool {
	if _mgr == nil {
		return false
	}
	return _mgr.IsWalletExist()
}

func IsUnlocked() bool {
	if _mgr == nil {
		return false
	}
	wallet := _mgr.GetWallet()
	return wallet != nil
}


func UnlockWallet(pw string) error {
	if _mgr == nil {
		return fmt.Errorf("STPManager not init")
	}
	_, err := _mgr.UnlockWallet(pw)
	return err
}

func CreateWallet(pw string) (string, error) {
	if _mgr == nil {
		return "", fmt.Errorf("STPManager not init")
	}
	_, mn, err := _mgr.CreateWallet(pw)
	return mn, err
}

func ImportWallet(mn, pw string) (error) {
	if _mgr == nil {
		return fmt.Errorf("STPManager not init")
	}
	_, err := _mgr.ImportWallet(mn, pw)
	return err
}

func GetPubKey() ([]byte, error) {
	if _mgr == nil {
		return nil, fmt.Errorf("STPManager not init")
	}
	wallet := _mgr.GetWallet()
	if wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked/connected")
	}
	pubKey := wallet.GetPaymentPubKey()

	return pubKey.SerializeCompressed(), nil
}
