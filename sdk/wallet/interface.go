package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/psbt"

	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func NewManager(cfg *common.Config, db common.KVDB) *Manager {
	Log.Infof("sat20wallet_ver:%s, DB_ver:%s", SOFTWARE_VERSION, DB_VERSION)

	//////////
	indexer.CHAIN = cfg.Chain

	http := NewHTTPClient()
	l1 := NewIndexerClient(cfg.IndexerL1.Scheme, cfg.IndexerL1.Host, cfg.IndexerL1.Proxy, http)
	l2 := NewIndexerClient(cfg.IndexerL2.Scheme, cfg.IndexerL2.Host, cfg.IndexerL2.Proxy, http)
	var l12, l22*IndexerClient
	if cfg.SlaveIndexerL1 != nil {
		l12 = NewIndexerClient(cfg.SlaveIndexerL1.Scheme, cfg.SlaveIndexerL1.Host, cfg.SlaveIndexerL1.Proxy, http)
	}
	if cfg.SlaveIndexerL2 != nil {
		l22 = NewIndexerClient(cfg.SlaveIndexerL2.Scheme, cfg.SlaveIndexerL2.Host, cfg.SlaveIndexerL2.Proxy, http)
	}

	mgr := &Manager{
		cfg: cfg,
		walletInfoMap: nil,
		tickerInfoMap: make(map[string]*indexer.TickerInfo),
		utxoLockerL1:              NewUtxoLocker(db, l1, L1_NETWORK_BITCOIN),
		utxoLockerL2:              NewUtxoLocker(db, l2, L2_NETWORK_SATOSHI),
		http:                      http,
		l1IndexerClient:           l1,
		l2IndexerClient:           l2,
		slaveL1IndexerClient:      l12,
		slaveL2IndexerClient:      l22,
		bInited:       false,
		bStop:         false,
	}

	_env = cfg.Env
	_chain = cfg.Chain
	_mode = cfg.Mode

	mgr.db = db
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
	p.mutex.Lock()
	defer p.mutex.Unlock()

	wallet, mnemonic, err := NewInteralWallet(GetChainParam())
	if err != nil {
		return -1, "", err
	}

	id, err := p.saveMnemonic(mnemonic, password)
	if err != nil {
		return -1, "", err
	}

	p.wallet = wallet
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

	p.mutex.Lock()
	defer p.mutex.Unlock()

	id, err := p.saveMnemonic(mnemonic, password)
	if err != nil {
		return -1, err
	}

	p.wallet = wallet
	p.status.CurrentWallet = id
	p.saveStatus()

	return id, nil
}

func (p *Manager) ChangePassword(oldPS, newPS string) (error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for id, v := range p.walletInfoMap {
		mnemonic, err := p.loadMnemonic(id, oldPS)
		if err != nil {
			Log.Errorf("loadMnemonic %d failed, %v", id, err)
			return err
		}
		
		err = p.saveMnemonicWithPassword(mnemonic, newPS, v)
		if err != nil {
			Log.Errorf("saveMnemonicWithPassword %d failed, %v", id, err)
			return err
		}
	}
	
	return nil
}

func (p *Manager) UnlockWallet(password string) (int64, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	return p.unlockWallet(password)
}

func (p *Manager) unlockWallet(password string) (int64, error) {

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
	p.status.CurrentAccount = 0

	return p.status.CurrentWallet, nil
}

func (p *Manager) GetAllWallets() map[int64]int {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	result := make(map[int64]int, 0)
	for k, v := range p.walletInfoMap {
		result[k] = v.Accounts
	}
	return result
}

func (p *Manager) SwitchWallet(id int64, password string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.status.CurrentWallet == id {
		return nil
	}

	oldWalletId := p.status.CurrentWallet
	oldAccount := p.status.CurrentAccount
	p.status.CurrentWallet = id
	oldWallet := p.wallet
	p.wallet = nil
	_, err := p.unlockWallet(password)
	if err == nil {
		p.saveStatus()
	} else {
		p.status.CurrentWallet = oldWalletId
		p.status.CurrentAccount = oldAccount
		p.wallet = oldWallet
	}
	return err
}

func (p *Manager) SwitchAccount(id uint32) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.status.CurrentAccount == id {
		return
	}

	walletInfo, ok := p.walletInfoMap[p.status.CurrentWallet]
	if ok {
		// 必须有
		if walletInfo.Accounts < int(id) {
			walletInfo.Accounts = int(id)
			saveWallet(p.db, walletInfo)
		}
	}

	p.wallet.SetSubAccount(id)
	p.status.CurrentAccount = id
	p.saveStatus()
}

func (p *Manager) SwitchChain(chain, password string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if _chain == chain {
		return nil
	}
	if chain == "mainnet" || chain == "testnet" {
		_chain = chain
		p.status.CurrentChain = chain
		oldWallet := p.wallet
		p.wallet = nil
		_, err := p.unlockWallet(password)
		if err == nil {
			p.saveStatus()
		} else {
			p.wallet = oldWallet
		}
		return err
	}
	return fmt.Errorf("invalid chain %s", chain)
}

func (p *Manager) GetChain() string {
	return _chain
}

func (p *Manager) GetMnemonic(id int64, password string) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	
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

	pubkey := p.wallet.GetPubKeyByIndex(id)
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

	return p.wallet.SignMessage(msg)
}

func (p *Manager) SignWalletMessage(msg string) ([]byte, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}

	return p.wallet.SignWalletMessage(msg)
}

func (p *Manager) SignPsbts(psbtsHex []string, bExtract bool) ([]string, error) {
	result := make([]string, 0, len(psbtsHex))
	for i, psbt := range psbtsHex {
		signed, err := p.SignPsbt(psbt, bExtract)
		if err != nil {
			Log.Errorf("SignPsbt %d failed, %v", i, err)
			return nil, err
		}
		result = append(result, signed)
	}
	return result, nil
}

func (p *Manager) SignPsbt(psbtHex string, bExtract bool) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	hexBytes, err := hex.DecodeString(psbtHex)
	if err != nil {
		return "", err
	}
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

	if bExtract {
		err = psbt.MaybeFinalizeAll(packet)
		if err != nil {
			Log.Errorf("MaybeFinalizeAll failed, %v", err)
			return "", err
		}

		finalTx, err := psbt.Extract(packet)
		if err != nil {
			Log.Errorf("Extract failed, %v", err)
			return "", err
		}

		return EncodeMsgTx(finalTx)
	}

	var buf bytes.Buffer
	err = packet.Serialize(&buf)
	if err != nil {
		Log.Errorf("Serialize failed, %v", err)
		return "", err
	}

	return hex.EncodeToString(buf.Bytes()), nil
}

func (p *Manager) SignPsbts_SatsNet(psbtsHex []string, bExtract bool) ([]string, error) {
	result := make([]string, 0, len(psbtsHex))
	for i, psbt := range psbtsHex {
		signed, err := p.SignPsbt_SatsNet(psbt, bExtract)
		if err != nil {
			Log.Errorf("SignPsbt_SatsNet %d failed, %v", i, err)
			return nil, err
		}
		result = append(result, signed)
	}
	return result, nil
}

func (p *Manager) SignPsbt_SatsNet(psbtHex string, bExtract bool) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	hexBytes, err := hex.DecodeString(psbtHex)
	if err != nil {
		return "", err
	}
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

	if bExtract {
		err = spsbt.MaybeFinalizeAll(packet)
		if err != nil {
			Log.Errorf("MaybeFinalizeAll failed, %v", err)
			return "", err
		}

		finalTx, err := spsbt.Extract(packet)
		if err != nil {
			Log.Errorf("Extract failed, %v", err)
			return "", err
		}

		return EncodeMsgTx_SatsNet(finalTx)
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

func (p *Manager) GetChannelAddrByPeerPubkey(pubkeyHex string) (string, string, error) {
	if p.wallet == nil {
		return "", "", fmt.Errorf("wallet is not created/unlocked")
	}

	pubkey, err := utils.ParsePubkey(pubkeyHex)
	if err != nil {
		return "", "", err
	}
	p2trAddr := PublicKeyToP2TRAddress(pubkey)

	channelAddr, err := GetP2WSHaddress(p.wallet.GetPubKey().SerializeCompressed(), 
		pubkey.SerializeCompressed())
	if err != nil {
		return "", "", err
	}
	return channelAddr, p2trAddr, nil
}
