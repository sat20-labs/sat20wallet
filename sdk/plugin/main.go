package main

import (
	"fmt"
	"os"

	"github.com/sat20-labs/sat20wallet/sdk/config"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
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
	if mnemonic := firstEnv("SATOSHINET_WALLET_MNEMONIC", "SATOSHINET_RPCTEST_STP_MNEMONIC"); mnemonic != "" {
		lcfg.Wallet.Mnemonic = mnemonic
	}
	if password := firstEnv("SATOSHINET_WALLET_PASSWORD", "SATOSHINET_RPCTEST_STP_PASSWORD"); password != "" {
		lcfg.Wallet.Password = password
	} else if os.Getenv("SATOSHINET_RPCTEST_STP_MNEMONIC") != "" {
		lcfg.Wallet.Password = "rpctest"
	}
	wallet.InitLog(lcfg)

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

	if lcfg.Wallet.PSFile != "" {
		pw, loadErr := wallet.LoadPassword(lcfg.DB + "/" + lcfg.Wallet.PSFile)
		if loadErr == nil {
			_, unlockErr := _mgr.UnlockWallet(pw)
			if unlockErr != nil {
				wallet.Log.Warnf("UnlockWallet failed, %v", unlockErr)
			}
		} else if lcfg.Wallet.Mnemonic != "" && lcfg.Wallet.Password != "" {
			wallet.Log.Info("initiate wallet by configuration wallet")
			if _, importErr := _mgr.ImportWallet(lcfg.Wallet.Mnemonic, lcfg.Wallet.Password); importErr != nil {
				wallet.Log.Errorf("ImportWallet failed, %v", importErr)
			}
		}
	}
	if _mgr.GetWallet() == nil && lcfg.Wallet.Mnemonic != "" && lcfg.Wallet.Password != "" {
		wallet.Log.Info("initiate wallet by configuration wallet")
		if _, importErr := _mgr.ImportWallet(lcfg.Wallet.Mnemonic, lcfg.Wallet.Password); importErr != nil {
			wallet.Log.Errorf("ImportWallet failed, %v", importErr)
		}
	}
	return nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

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
	w := _mgr.GetWallet()
	if w == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked/connected")
	}
	return w.SignMessage(msg)
}

func SignPsbt_SatsNet(packet *spsbt.Packet) error {
	if _mgr == nil {
		return fmt.Errorf("STPManager not init")
	}
	w := _mgr.GetWallet()
	if w == nil {
		return fmt.Errorf("wallet is not created/unlocked/connected")
	}
	return w.SignPsbt_SatsNet(packet)
}

func IsWalletExisting() bool {
	return _mgr != nil && _mgr.IsWalletExist()
}

func IsUnlocked() bool {
	return _mgr != nil && _mgr.GetWallet() != nil
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
	_, mnemonic, err := _mgr.CreateWallet(pw)
	return mnemonic, err
}

func ImportWallet(mn, pw string) error {
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
	w := _mgr.GetWallet()
	if w == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked/connected")
	}
	return w.GetPaymentPubKey().SerializeCompressed(), nil
}

// main is intentionally empty. This package is primarily built as a plugin,
// but go build ./... must still be able to compile it as a main package.
func main() {}
