package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	
	"github.com/sat20-labs/sat20wallet/sdk/wallet/indexer"
	spsbt "github.com/sat20-labs/satsnet_btcd/btcutil/psbt"
)

func NewManager(cfg *Config, quit chan struct{}) *Manager {
	Log.Infof("sat20wallet_ver:%s, DB_ver:%s", SOFTWARE_VERSION, DB_VERSION)

	//////////

	mgr := &Manager{
		cfg:           cfg,
		walletInfoMap: nil,
		tickerInfoMap: make(map[string]*indexer.TickerInfo),
		bInited:       false,
		bStop:         false,
		quit:          quit,
	}

	_chain = cfg.Chain

	mgr.db = NewKVDB(cfg.DB)
	if mgr.db == nil {
		Log.Errorf("NewKVDB failed")
		return nil
	}
	err := mgr.init()
	if err != nil {
		return nil
	}

	return mgr
}

// 使用内部钱包
func (p *Manager) CreateWallet(password string) (int64, string, error) {
	// if p.wallet != nil {
	// 	return "", fmt.Errorf("wallet has been created, please unlock it first")
	// }

	// if p.IsWalletExist() {
	// 	return "", fmt.Errorf("wallet has been created, please unlock it first")
	// }

	wallet, mnemonic, err := NewInteralWallet(GetChainParam())
	if err != nil {
		return -1, "", err
	}

	id, err := p.saveMnemonic(mnemonic, password)
	if err != nil {
		return -1, "", err
	}

	p.wallet = wallet
	p.password = password
	p.status.CurrentWallet = id
	p.saveStatus()

	return id, mnemonic, nil
}

func (p *Manager) ImportWallet(mnemonic string, password string) (int64, error) {
	// Log.Infof("ImportWallet %s %s", mnemonic, password)
	// if p.wallet != nil {
	// 	return fmt.Errorf("wallet exists, not allow to import new wallet")
	// }

	// if p.IsWalletExist() {
	// 	return fmt.Errorf("wallet exists, not allow to import new wallet")
	// }

	wallet := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if wallet == nil {
		return -1, fmt.Errorf("NewWalletWithMnemonic failed")
	}

	id, err := p.saveMnemonic(mnemonic, password)
	if err != nil {
		return -1, err
	}

	p.wallet = wallet
	p.password = password
	p.status.CurrentWallet = id
	p.saveStatus()

	return id, nil
}

func (p *Manager) UnlockWallet(password string) (int64, error) {

	if p.wallet != nil {
		return -1, fmt.Errorf("wallet has been unlocked")
	}

	mnemonic, err := p.loadMnemonic(p.status.CurrentWallet, password)
	if err != nil {
		return -1, err
	}

	wallet := NewInternalWalletWithMnemonic(string(mnemonic), "", GetChainParam())
	if wallet == nil {
		return -1, fmt.Errorf("NewWalletWithMnemonic failed")
	}

	p.wallet = wallet
	p.password = password

	return p.status.CurrentWallet, nil
}

func (p *Manager) GetAllWallets() map[int64]int {
	result := make(map[int64]int, 0)
	for k, v := range p.walletInfoMap {
		result[k] = v.Accounts
	}
	return result
}

func (p *Manager) SwitchWallet(id int64) {
	if p.status.CurrentWallet == id {
		return
	}

	p.status.CurrentWallet = id
	_, err := p.UnlockWallet(p.password)
	if err == nil {
		p.saveStatus()
	}
}

func (p *Manager) SwitchAccount(id uint32) {
	if p.status.CurrentAccount == id {
		return
	}

	p.status.CurrentAccount = id
	p.saveStatus()
}

func (p *Manager) SwitchChain(chain string) {
	if _chain == chain {
		return
	}
	if chain == "mainnet" || chain == "testnet" {
		_chain = chain
		p.status.CurrentChain = chain
		_, err := p.UnlockWallet(p.password)
		if err == nil {
			p.saveStatus()
		}
	}
}

func (p *Manager) GetChain() string {
	return _chain
}

func (p *Manager) GetMnemonic(id int64, password string) string {
	mnemonic, err := p.loadMnemonic(id, password)
	if err != nil {
		return ""
	}

	return mnemonic
}

// private key
func (p *Manager) GetCommitRootKey(peer []byte) []byte {
	if p.wallet == nil {
		return nil
	}
	privkey, _ := p.wallet.GetCommitRootKey(peer)
	return privkey.Serialize()
}

// private key
func (p *Manager) GetCommitSecret(peer []byte, index int) []byte {
	if p.wallet == nil {
		return nil
	}
	privkey := p.wallet.GetCommitSecret(peer, uint32(index))
	return privkey.Serialize()
}

// private key
func (p *Manager) DeriveRevocationPrivKey(commitsecret []byte) []byte {
	if p.wallet == nil {
		return nil
	}
	privSecret, _ := btcec.PrivKeyFromBytes(commitsecret)
	privkey := p.wallet.DeriveRevocationPrivKey(privSecret)
	return privkey.Serialize()
}

// pub key
func (p *Manager) GetRevocationBaseKey() []byte {
	if p.wallet == nil {
		return nil
	}
	pubKey := p.wallet.GetRevocationBaseKey()
	return pubKey.SerializeCompressed()
}

// pub key
func (p *Manager) GetNodePubKey() []byte {
	if p.wallet == nil {
		return nil
	}
	pubKey := p.wallet.GetNodePubKey()
	return pubKey.SerializeCompressed()
}

func (p *Manager) GetPublicKey(id uint32) []byte {
	if p.wallet == nil {
		return nil
	}

	pubkey := p.wallet.GetPubKey(id)
	if pubkey == nil {
		return nil
	}

	return pubkey.SerializeCompressed()
}

func (p *Manager) GetPaymentPubKey() []byte {
	if p.wallet == nil {
		return nil
	}

	pubkey := p.wallet.GetPaymentPubKey()
	if pubkey == nil {
		return nil
	}

	return pubkey.SerializeCompressed()
}

func (p *Manager) SignMessage(msg []byte) ([]byte, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}

	sig, err := p.wallet.SignMessage(msg)
	if err != nil {
		return nil, err
	}
	return sig.Serialize(), nil
}

func (p *Manager) SignPsbt(psbtHex string) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	hexBytes, _ := hex.DecodeString(psbtHex)
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		Log.Errorf("NewFromRawBytes failed, %v", err)
		return "", err
	}

	err = p.wallet.SignPsbt(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return "", err
	}

	var buf bytes.Buffer
	err = packet.Serialize(&buf)
	if err != nil {
		Log.Errorf("Serialize failed, %v", err)
		return "", err
	}

	return hex.EncodeToString(buf.Bytes()), nil
}

func (p *Manager) SignPsbt_SatsNet(psbtHex string) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	hexBytes, _ := hex.DecodeString(psbtHex)
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		Log.Errorf("NewFromRawBytes failed, %v", err)
		return "", err
	}

	err = p.wallet.SignPsbt_SatsNet(packet)
	if err != nil {
		Log.Errorf("SignPsbt_SatsNet failed, %v", err)
		return "", err
	}

	var buf bytes.Buffer
	err = packet.Serialize(&buf)
	if err != nil {
		Log.Errorf("Serialize failed, %v", err)
		return "", err
	}

	return hex.EncodeToString(buf.Bytes()), nil
}


// 注册回调函数
func (p *Manager) RegisterCallback(callback NotifyCB) {
	p.msgCallback = callback
}

// 发送消息
func (p *Manager) SendMessageToUpper(eventName string, data interface{}) {
	Log.Infof("message notified: %s", eventName)
	if p.msgCallback != nil {
		p.msgCallback(eventName, data)
	}
}

