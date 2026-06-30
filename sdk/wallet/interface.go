package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/psbt"

	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"

	db "github.com/sat20-labs/indexer/common"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func NewManager(cfg *common.Config, db db.KVDB) *Manager {
	Log.Infof("sat20wallet_ver:%s, DB_ver:%s", SOFTWARE_VERSION, DB_VERSION)

	//////////
	indexer.CHAIN = cfg.Chain

	http := NewHTTPClient()
	l1IndexerMgr := NewIndexerRPCClientMgr()
	l1 := NewIndexerClient(cfg.IndexerL1.Scheme, cfg.IndexerL1.Host, cfg.IndexerL1.Proxy, http)
	l1IndexerMgr.Set(l1)

	l2IndexerMgr := NewIndexerRPCClientMgr()
	l2 := NewIndexerClient(cfg.IndexerL2.Scheme, cfg.IndexerL2.Host, cfg.IndexerL2.Proxy, http)
	l2IndexerMgr.Set(l2)

	var l12, l22 *IndexerClient
	if cfg.SlaveIndexerL1 != nil {
		l12 = NewIndexerClient(cfg.SlaveIndexerL1.Scheme, cfg.SlaveIndexerL1.Host, cfg.SlaveIndexerL1.Proxy, http)
		l1IndexerMgr.Set(l12)
	}
	if cfg.SlaveIndexerL2 != nil {
		l22 = NewIndexerClient(cfg.SlaveIndexerL2.Scheme, cfg.SlaveIndexerL2.Host, cfg.SlaveIndexerL2.Proxy, http)
		l2IndexerMgr.Set(l22)
	}

	mgr := &Manager{
		cfg:                  cfg,
		walletInfoMap:        nil,
		tickerInfoMap:        make(map[string]*indexer.TickerInfo),
		utxoLockerL1:         NewUtxoLocker(db, l1, L1_NETWORK_BITCOIN),
		utxoLockerL2:         NewUtxoLocker(db, l2, L2_NETWORK_SATOSHI),
		http:                 http,
		l1IndexerClient:      l1IndexerMgr,
		l2IndexerClient:      l2IndexerMgr,
		channelBackupHandler: noopChannelBackupHandler{},
		bInited:              false,
	}

	_env = cfg.Env
	_chain = cfg.Chain
	_mode = cfg.Mode

	if cfg.Chain == "testnet" {
		LAUNCHPOOL_MIN_SATS = 1000
	}

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

func (p *Manager) Start() {
	p.l1IndexerClient.Start()
	p.l2IndexerClient.Start()
	p.startActionMonitor()
}

func (p *Manager) IsReady() bool {
	return p.bInited && p.actionMonitorRunning
}

func (p *Manager) Stop() {
	if p.btcLuckyMiner != nil {
		p.btcLuckyMiner.Stop()
	}
	p.stopActionMonitor()
	p.l1IndexerClient.Stop()
	p.l2IndexerClient.Stop()
}

func (p *Manager) Close() {
	p.Stop()
	p.bInited = false
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

	err = p.saveMnemonic(mnemonic, password, wallet)
	if err != nil {
		return -1, "", err
	}

	p.wallet = wallet
	p.status.CurrentWallet = wallet.GetId()
	p.status.CurrentAccount = 0
	p.saveStatus()

	return p.status.CurrentWallet, mnemonic, nil
}

// TODO 未完成，还没有保存
func (p *Manager) CreateMonitorWallet(address string) (int64, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	wallet := NewMonitorWallet(address)

	p.wallet = wallet
	p.status.CurrentWallet = wallet.GetId()
	p.status.CurrentAccount = 0
	p.saveStatus()

	return p.status.CurrentWallet, nil
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

	err := p.saveMnemonic(mnemonic, password, wallet)
	if err != nil {
		return -1, err
	}

	p.wallet = wallet
	p.status.CurrentWallet = wallet.GetId()
	p.status.CurrentAccount = 0
	p.saveStatus()

	return p.status.CurrentWallet, nil
}

func (p *Manager) ImportWalletWithPrivateKey(privKey string, password string) (int64, error) {

	privKeyBytes, err := hex.DecodeString(privKey)
	if err != nil {
		return 0, err
	}

	wallet, _, err := NewInternalWalletWithPrivKey(privKeyBytes, GetChainParam())
	if err != nil {
		return -1, err
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	err = p.saveSecret(privKey, password, WALLET_TYPE_PRIVKEY, wallet)
	if err != nil {
		return -1, err
	}

	p.wallet = wallet
	p.status.CurrentWallet = wallet.GetId()
	p.status.CurrentAccount = 0
	p.saveStatus()

	return p.status.CurrentWallet, nil
}

func (p *Manager) ChangePassword(oldPS, newPS string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	for id, v := range p.walletInfoMap {
		mnemonic, err := p.loadWalletSecret(v, oldPS)
		if err != nil {
			Log.Errorf("loadMnemonic %d failed, %v", id, err)
			return err
		}

		err = p.saveWalletSecretWithPassword(mnemonic, newPS, &v.WalletInDB)
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

// 同时解锁所有的钱包
func (p *Manager) unlockWallet(password string) (int64, error) {

	if p.wallet != nil {
		return -1, fmt.Errorf("wallet has been unlocked")
	}
	if len(p.walletInfoMap) == 0 {
		return -1, fmt.Errorf("no wallet")
	}

	for _, walletInfo := range p.walletInfoMap {
		if walletInfo.Wallet != nil {
			continue
		}
		secret, err := p.loadWalletSecret(walletInfo, password)
		if err != nil {
			Log.Errorf("loadWalletSecret %d failed. %v", walletInfo.Id, err)
			return -1, fmt.Errorf("password is incorrect")
		}
		switch walletInfo.Type {
		case WALLET_TYPE_MNEMONIC:
			walletInfo.Wallet = NewInternalWalletWithMnemonic(string(secret), "", GetChainParam())
			if walletInfo.Wallet == nil {
				Log.Errorf("NewInternalWalletWithMnemonic failed")
				continue
			}
		case WALLET_TYPE_PRIVKEY:
			privKeyBytes, err := hex.DecodeString(string(secret))
			if err != nil {
				Log.Errorf("hex.DecodeString failed, %v", err)
				continue
			}
			walletInfo.Wallet, _, _ = NewInternalWalletWithPrivKey(privKeyBytes, GetChainParam())
			if walletInfo.Wallet == nil {
				Log.Errorf("NewInternalWalletWithPrivKey failed")
				continue
			}
		}
	}

	info, ok := p.walletInfoMap[p.status.CurrentWallet]
	if !ok {
		// reset to first wallet
		min := int64(math.MaxInt64)
		for id := range p.walletInfoMap {
			if id < min {
				min = id
			}
		}
		p.status.CurrentWallet = min
		p.status.CurrentAccount = 0
		p.saveStatus()

		info = p.walletInfoMap[p.status.CurrentWallet]
		if info == nil {
			return -1, fmt.Errorf("can't unlock any wallet")
		}
	}

	p.wallet = info.Wallet
	p.wallet.SetSubAccount(p.status.CurrentAccount)

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

// 不再需要密码
func (p *Manager) SwitchWallet(id int64, password string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	if p.status.CurrentWallet == id {
		return nil
	}
	w, ok := p.walletInfoMap[id]
	if !ok {
		// 不再考虑js模块存在两个模块的问题，由js模块自己重新加载
		// 插件钱包有两个钱包对象，需要做数据同步，简单加载就行
		//p.initDB()
		//_, ok := p.walletInfoMap[id]
		//if !ok {
		return fmt.Errorf("can't find wallet %d", id)
		//}
	}

	p.status.CurrentWallet = id
	p.status.CurrentAccount = 0
	p.wallet = w.Wallet
	p.wallet.SetSubAccount(0)
	p.saveStatus()

	return nil
}

func (p *Manager) GetCurrentWalletId() int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.status.CurrentWallet
}

func (p *Manager) GetCurrentAccountId() uint32 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return p.status.CurrentAccount
}

// 不改变当前钱包和账户
func (p *Manager) FindWalletById(id int64) common.Wallet {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if id == 0 {
		return p.wallet
	}

	walletInfo, ok := p.walletInfoMap[id]
	if !ok {
		return nil
	}

	return walletInfo.Wallet
}

// 不改变当前钱包和账户
func (p *Manager) GetWalletId(w common.Wallet) int64 {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	for id, walletInfo := range p.walletInfoMap {
		if walletInfo.Wallet != nil && walletInfo.Wallet.GetNodePubKey().IsEqual(w.GetNodePubKey()) {
			return id
		}
	}

	return 0
}

// 不改变当前钱包和账户
func (p *Manager) FindWalletByPubKey(pubKey []byte) common.Wallet {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	for _, walletInfo := range p.walletInfoMap {
		if bytes.Equal(walletInfo.Wallet.GetNodePubKey().SerializeCompressed(), pubKey) {
			return walletInfo.Wallet
		}
	}

	return nil
}

// 不改变当前钱包和账户
func (p *Manager) FindWalletByPubKeyWithDepth(pubKey []byte, depth uint32) common.Wallet {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	for _, walletInfo := range p.walletInfoMap {
		if bytes.Equal(walletInfo.Wallet.GetNodePubKey().SerializeCompressed(), pubKey) {
			return walletInfo.Wallet
		}
		w := walletInfo.Wallet.Clone()
		for i := uint32(1); i <= depth; i++ {
			w.SetSubAccount(i)
			if bytes.Equal(w.GetNodePubKey().SerializeCompressed(), pubKey) {
				return w
			}
		}
	}

	return nil
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
			saveWallet(p.db, &walletInfo.WalletInDB)
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
		for _, walletInfo := range p.walletInfoMap {
			walletInfo.Wallet = nil
		}
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

	w, ok := p.walletInfoMap[id]
	if !ok {
		return ""
	}

	serect, err := p.loadWalletSecret(w, password)
	if err != nil {
		return ""
	}

	return serect
}

// private key
func (p *Manager) GetCommitRootKey(peer []byte) []byte {
	if p.wallet == nil {
		return nil
	}
	privkey := p.wallet.GetCommitSecret(peer, 0)
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

	// for _, input := range packet.Inputs {
	// 	ty, addr, i, err := txscript.ExtractPkScriptAddrs(input.WitnessUtxo.PkScript, GetChainParam())
	// 	if err != nil {
	// 		continue
	// 	}
	// 	fmt.Printf("%d %s %d\n", ty, addr[0], i)
	// 	fmt.Printf("sig flag: %x\n", input.SighashType)
	// }

	err = p.wallet.SignPsbt(packet)
	if err != nil {
		Log.Errorf("SignPsbt failed, %v", err)
		return "", err
	}

	err = psbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		return "", err
	}

	if bExtract {
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

	err = spsbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		return "", err
	}

	if bExtract {
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

func (p *Manager) GetUtxoWithAddressFromTx(txId, address string) (string, error) {
	raw, err := p.GetIndexerClient().GetRawTx(txId)
	if err != nil {
		return "", err
	}
	tx, err := DecodeMsgTx(raw)
	if err != nil {
		return "", err
	}

	for i, txOut := range tx.TxOut {
		addr, err := AddrFromPkScript(txOut.PkScript)
		if err != nil {
			continue
		}
		if address == addr {
			return fmt.Sprintf("%s:%d", txId, i), nil
		}
	}
	return "", fmt.Errorf("can't find utxo with address %s in tx %s", address, txId)
}

func (p *Manager) GetChannelAddress() (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	if p.serverNode == nil || p.serverNode.Pubkey == nil {
		return "", fmt.Errorf("server node is not initialized")
	}
	return GetP2WSHaddress(p.serverNode.Pubkey.SerializeCompressed(),
		p.wallet.GetPaymentPubKey().SerializeCompressed())
}

// 对某个btc的名字设置属性
func (p *Manager) SetKeyValueToName(name string, key, value string, feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	insc, err := p.InscribeKeyValueInName(name, key, value, feeRate)
	if err != nil {
		if insc != nil && insc.CommitTx != nil {
			return insc.CommitTx.TxID(), fmt.Errorf("broadcast reveal tx failed, %v", err)
		}
		return "", fmt.Errorf("InscribeKeyValueInName failed, %v", err)
	}

	return insc.RevealTx.TxID(), nil
}

// 绑定推荐人地址
func (p *Manager) BindReferrer(referrerName, key string, serverPubkey []byte) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	referrerName = strings.ToLower(referrerName)
	referrerName = strings.TrimSpace(referrerName)

	// 检查该名字是否是有效的推荐人名字
	info, err := p.l1IndexerClient.GetNameInfo(referrerName)
	if err != nil {
		return "", err
	}
	// 检查是否有正确的签名key-value
	Log.Infof("%v", info)
	var sig string
	for _, kv := range info.KVItemList {
		if kv.Key == key {
			sig = kv.Value
		}
	}
	if sig == "" {
		return "", fmt.Errorf("can't find the value of %s", key)
	}
	sigBytes, err := hex.DecodeString(sig)
	if err != nil {
		return "", err
	}
	pubkey, err := utils.BytesToPublicKey(serverPubkey)
	if err != nil {
		return "", err
	}
	if !VerifyMessage(pubkey, []byte(referrerName), sigBytes) {
		return "", fmt.Errorf("referrer %s has no correct signatrue", referrerName)
	}

	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_BINDREFERRER, []byte(referrerName))
	if err != nil {
		return "", err
	}

	txId, err := p.SendNullData_SatsNet(nullDataScript)
	if err != nil {
		Log.Errorf("SendNullData_SatsNet %s failed", referrerName)
		return "", err
	}
	Log.Infof("bind referrer %s with txId %s", referrerName, txId)
	return txId, nil
}
