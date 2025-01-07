package wallet

import (
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	sbtcutil "github.com/sat20-labs/satsnet_btcd/btcutil"
	stxscript "github.com/sat20-labs/satsnet_btcd/txscript"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
	
)

func NewManager(cfg *Config, quit chan struct{}) *Manager {
	Log.Infof("sat20wallet_ver:%s, DB_ver:%s", SOFTWARE_VERSION, DB_VERSION)

	//////////

	mgr := &Manager{
		cfg:                cfg,
		bInited:            false,
		bStop:              false,
		quit:               quit,
	}

	_chain = cfg.Chain

	mgr.db = NewKVDB(cfg.DB)
	if mgr.db == nil {
		Log.Errorf("NewKVDB failed")
		return nil
	}

	return mgr
}

// 在客户端模式下，Init和Start是一起调用的
func (p *Manager) Init() error {

	if p.wallet == nil {
		return fmt.Errorf("wallet is not created/unlocked/connected")
	}

	err := p.init()
	if err != nil {
		return err
	}

	return nil
}

// 使用内部钱包
func (p *Manager) CreateWallet(password string) (string, error) {
	if p.wallet != nil {
		return "", fmt.Errorf("wallet has been created, please unlock it first")
	}

	if p.IsWalletExist() {
		return "", fmt.Errorf("wallet has been created, please unlock it first")
	}

	wallet, mnemonic, err := NewInteralWallet(GetChainParam())
	if err != nil {
		return "", err
	}

	err = p.saveMnemonic(mnemonic, password)
	if err != nil {
		return "", err
	}

	p.wallet = wallet

	return mnemonic, nil
}

func (p *Manager) ImportWallet(mnemonic string, password string) error {
	Log.Infof("ImportWallet %s %s", mnemonic, password)
	if p.wallet != nil {
		return fmt.Errorf("wallet exists, not allow to import new wallet")
	}

	if p.IsWalletExist() {
		return fmt.Errorf("wallet exists, not allow to import new wallet")
	}

	wallet := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if wallet == nil {
		return fmt.Errorf("NewWalletWithMnemonic failed")
	}

	err := p.saveMnemonic(mnemonic, password)
	if err != nil {
		return err
	}

	p.wallet = wallet

	return nil
}

func (p *Manager) UnlockWallet(password string) error {

	if p.wallet != nil {
		return fmt.Errorf("wallet has been unlocked")
	}

	mnemonic, err := p.loadMnemonic(password)
	if err != nil {
		return err
	}

	wallet := NewInternalWalletWithMnemonic(string(mnemonic), "", GetChainParam())
	if wallet == nil {
		return fmt.Errorf("NewWalletWithMnemonic failed")
	}

	p.wallet = wallet

	return nil
}

func (p *Manager) GetMnemonic(password string) string {
	mnemonic, err := p.loadMnemonic(password)
	if err != nil {
		return ""
	}

	return mnemonic
}
