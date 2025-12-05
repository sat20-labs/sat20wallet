package wallet

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcwallet/snacl"
	db "github.com/sat20-labs/indexer/common"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	DB_KEY_STATUS = "wallet-status"
	DB_KEY_WALLET = "wallet-id-"

	DB_KEY_INSC        = "insc-"
	DB_KEY_TICKER_INFO = "t-"

	DB_KEY_UTXO          = "u-"  // u-network-address-utxo
	DB_KEY_LOCKEDUTXO    = "l-"  // l-network-address-utxo
	DB_KEY_LOCK_LASTTIME = "lt-" // lt-network-address

	DB_KEY_TEMPLATE_CONTRACT        = "tc-"    // tc-url-
	DB_KEY_TC_INVOKE_HISTORY        = "tch-"   // tch-url-id
	DB_KEY_TC_INVOKE_HISTORY_BACKUP = "tchbk-" // tchbk-url-id
	DB_KEY_TC_INVOKE_RESULT         = "tcr-"   // tcr-url-txid
	DB_KEY_TC_INVOKER_STATUS        = "tcu-"   // tcu-url-addr
	DB_KEY_TC_SWAP_RUNNINGDATA      = "tcsr-"  // tcsr-url-id
	DB_KEY_TC_LIQ_DATA              = "tclp-"  // tclp-url-id
)

var _mode string  //
var _chain string // mainnet, testnet
var _env string   // dev, test, prd

type Status struct {
	SoftwareVer    string
	DBver          string
	TotalWallet    int
	CurrentWallet  int64  // wallet ID
	CurrentAccount uint32 // account ID (index)
	CurrentChain   string
	SyncHeight     int
	SyncHeightL2   int
}

const (
	WALLET_TYPE_MNEMONIC int = 0
	WALLET_TYPE_PRIVKEY  int = 1
	WALLET_TYPE_MONITOR  int = 2
)

type WalletInDB struct {
	Id       int64  // 钱包id，也是创建时间
	Mnemonic []byte // 加密后的数据
	Salt     []byte
	Accounts int // 用户启用的子账户数量
	Type     int // 0: 默认钱包，有助记词；1: 私钥钱包； 2: 观察钱包
}

func getWalletDBKey(id int64) string {
	return fmt.Sprintf("%s%d", DB_KEY_WALLET, id)
}

func EncodeToBytes(data interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(data)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeFromBytes(data []byte, target interface{}) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)
	return dec.Decode(target)
}

func GetItemsFromDB(prefix []byte, db db.KVDB) (map[string][]byte, error) {
	result := make(map[string][]byte, 0)
	err := db.BatchRead(prefix, false, func(k, v []byte) error {
		result[string(k)] = v
		return nil
	})

	return result, err
}

func (p *Manager) initDB() error {

	status := p.loadStatus()
	if status.DBver != DB_VERSION {
		// try to update db

		p.status.DBver = DB_VERSION
	}

	wallets, err := loadAllWalletFromDB(p.db)
	if err != nil {
		return err
	}
	p.walletInfoMap = wallets

	p.inscibeMap = LoadAllInscribeResvFromDB(p.db)

	p.utxoLockerL1.Init()
	p.utxoLockerL2.Init()

	p.repair()

	return nil
}

func (p *Manager) repair() bool {

	return false
}

func loadAllWalletFromDB(db db.KVDB) (map[int64]*WalletInDB, error) {
	prefix := []byte(DB_KEY_WALLET)

	result := make(map[int64]*WalletInDB, 0)
	err := db.BatchRead(prefix, false, func(k, v []byte) error {

		var walletInfo WalletInDB
		err := DecodeFromBytes(v, &walletInfo)
		if err != nil {
			Log.Errorf("DecodeFromBytes failed. %v", err)
			return err
		}

		Log.Infof("wallet %d loaded", walletInfo.Id)

		result[walletInfo.Id] = &walletInfo
		return nil
	})

	return result, err
}

func saveWallet(db db.KVDB, wallet *WalletInDB) error {

	buf, err := EncodeToBytes(wallet)
	if err != nil {
		return err
	}

	err = db.Write([]byte(getWalletDBKey(wallet.Id)), buf)
	if err != nil {
		Log.Infof("saveWallet failed. %v", err)
		return err
	}
	Log.Infof("saveWallet succ")

	return nil
}

func loadWallet(db db.KVDB, id int64) (*WalletInDB, error) {
	buf, err := db.Read([]byte(getWalletDBKey(id)))
	if err != nil {
		Log.Infof("Read %s failed. %v", getWalletDBKey(id), err)
		return nil, err
	}

	var result WalletInDB
	err = DecodeFromBytes(buf, &result)
	if err != nil {
		Log.Errorf("DecodeFromBytes failed. %v", err)
		return nil, err
	}

	return &result, nil
}

func (p *Manager) loadStatus() *Status {

	result := &Status{
		SoftwareVer:  SOFTWARE_VERSION,
		DBver:        DB_VERSION,
		CurrentChain: _chain,
	}
	p.status = result

	buf, err := p.db.Read([]byte(DB_KEY_STATUS))
	if err != nil {
		Log.Infof("Read %s failed. %v", DB_KEY_STATUS, err)
		return result
	}

	err = DecodeFromBytes(buf, &result)
	if err != nil {
		Log.Errorf("DecodeFromBytes failed. %v", err)
		return result
	}

	return result
}

func (p *Manager) saveStatus() error {
	if p.status != nil {
		buf, err := EncodeToBytes(p.status)
		if err != nil {
			return err
		}

		err = p.db.Write([]byte(DB_KEY_STATUS), buf)
		if err != nil {
			Log.Infof("saveStatus failed. %v", err)
			return err
		}
		Log.Infof("saveStatus succ")
	}

	return nil
}

func (p *Manager) saveMnemonic(mn, password string) (int64, error) {
	return p.saveSecret(mn, password, WALLET_TYPE_MNEMONIC)
}

func (p *Manager) saveSecret(secret, password string, ty int) (int64, error) {
	key, err := p.newSnaclKey(password)
	if err != nil {
		Log.Errorf("newSnaclKey failed. %v", err)
		return -1, err
	}

	en, err := key.Encrypt([]byte(secret))
	if err != nil {
		Log.Errorf("Encrypt failed. %v", err)
		return -1, err
	}

	salt := key.Marshal()

	wallet := WalletInDB{
		Id:       time.Now().UnixMicro(),
		Mnemonic: en,
		Salt:     salt,
		Accounts: 1,
		Type:     ty,
	}

	err = saveWallet(p.db, &wallet)
	if err != nil {
		return -1, err
	}

	p.walletInfoMap[wallet.Id] = &wallet
	return wallet.Id, nil
}

func (p *Manager) saveWalletSecretWithPassword(mn, password string, wallet *WalletInDB) error {
	key, err := p.newSnaclKey(password)
	if err != nil {
		Log.Errorf("NewSecretKey failed. %v", err)
		return err
	}

	en, err := key.Encrypt([]byte(mn))
	if err != nil {
		Log.Errorf("Encrypt failed. %v", err)
		return err
	}

	salt := key.Marshal()

	wallet.Mnemonic = en
	wallet.Salt = salt

	err = saveWallet(p.db, wallet)
	if err != nil {
		return err
	}

	return nil
}

func (p *Manager) loadWalletSecret(id int64, password string) (string, error) {
	wallet, ok := p.walletInfoMap[id]
	if !ok {
		// 现在有两个钱包对象在两个模块之中，需要做一些数据同步工作
		err := p.initDB()
		if err != nil {
			return "", fmt.Errorf("can't find wallet %d", id)
		}
		wallet, ok = p.walletInfoMap[id]
		if !ok {
			return "", fmt.Errorf("can't find wallet %d", id)
		}
	}

	key, err := p.restoreSnaclKey(wallet.Salt, password)
	if err != nil {
		Log.Errorf("restoreSnaclKey failed. %v", err)
		return "", err
	}

	mnemonic, err := key.Decrypt(wallet.Mnemonic)
	if err != nil {
		Log.Errorf("Decrypt failed. %v", err)
		return "", err
	}

	return string(mnemonic), nil
}

func (p *Manager) restoreSnaclKey(salt []byte, password string) (*snacl.SecretKey, error) {
	pw := []byte(password)
	var sk snacl.SecretKey
	err := sk.Unmarshal(salt)
	if err == nil {
		err = sk.DeriveKey(&pw)
		if err == nil {
			return &sk, nil
		}
	}

	return nil, err
}

func (p *Manager) newSnaclKey(password string) (*snacl.SecretKey, error) {
	pw := []byte(password)
	key, err := snacl.NewSecretKey(&pw, snacl.DefaultN, snacl.DefaultR, snacl.DefaultP)
	if err != nil {
		Log.Errorf("NewSecretKey failed. %v", err)
		return nil, err
	}

	return key, nil
}

func (p *Manager) IsWalletExist() bool {
	return len(p.walletInfoMap) != 0
}

func GetDBKeyPrefix() string {
	if _mode == LIGHT_NODE {
		return _env + "-" + _chain + "-"
	}
	return ""
}

// 暂时不考虑地址
func GetLockedUtxoKey(network, utxo string) string {
	return GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO + network + "-" + utxo
}

func ParseLockedUtxoKey(key string) (string, string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO
	if !strings.HasPrefix(key, prefix) {
		return "", "", fmt.Errorf("not a locked utxo key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], parts[1], nil
}

func saveLockedUtxo(db db.KVDB, network, utxo string, value *LockedUtxo) error {
	buf, err := EncodeToBytes(value)
	if err != nil {
		Log.Errorf("saveLockedUtxo EncodeToBytes failed. %v", err)
		return err
	}

	err = db.Write([]byte(GetLockedUtxoKey(network, utxo)), buf)
	if err != nil {
		Log.Errorf("saveLockedUtxo failed. %v", err)
		return err
	}
	Log.Infof("saveLockedUtxo succ. %s", utxo)
	return nil
}

func DeleteLockedUtxo(db db.KVDB, network, utxo string) error {
	Log.Infof("DeleteLockedUtxo succ. %s", utxo)
	return db.Delete([]byte(GetLockedUtxoKey(network, utxo)))
}

func DeleteAllLockedUtxo(db db.KVDB, network string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO + network)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return deleteAllLastLockTime(db, network)
}

// 暂时不考虑地址
func loadAllLockedUtxoFromDB(db db.KVDB, network string) map[string]*LockedUtxo {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO + network)

	result := make(map[string]*LockedUtxo, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, utxo, err := ParseLockedUtxoKey(string(k))
		if err != nil {
			Log.Errorf("ParseLockedUtxoKey failed. %v", err)
			return err
		}

		var value LockedUtxo
		err = DecodeFromBytes(v, &value)
		if err != nil {
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return err
		}

		result[utxo] = &value
		return nil
	})

	return result
}

// 暂时不考虑地址
func GeLastLockTimeKey(network string) string {
	//return GetDBKeyPrefix() + DB_KEY_LOCK_LASTTIME + network + "-" + address
	return GetDBKeyPrefix() + DB_KEY_LOCK_LASTTIME + network
}

func ParseLastLockTimeKey(key string) (string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_LOCK_LASTTIME
	if !strings.HasPrefix(key, prefix) {
		return "", fmt.Errorf("not a key of lasttime of lock: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 1 {
		return "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], nil
}

func saveLastLockTime(db db.KVDB, network string, t int64) error {
	buf, err := EncodeToBytes(t)
	if err != nil {
		Log.Errorf("saveLastLockTime EncodeToBytes failed. %v", err)
		return err
	}

	err = db.Write([]byte(GeLastLockTimeKey(network)), buf)
	if err != nil {
		Log.Errorf("saveLastLockTime failed. %v", err)
		return err
	}
	Log.Infof("saveLastLockTime succ. %d", t)
	return nil
}

func loadLastLockTime(db db.KVDB, network string) (int64, error) {
	key := GeLastLockTimeKey(network)

	buf, err := db.Read([]byte(key))
	if err != nil {
		//Log.Errorf("Read %s failed. %v", key, err)
		return 0, err
	}
	var value int64
	err = DecodeFromBytes(buf, &value)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return 0, err
	}

	return value, nil
}

func deleteAllLastLockTime(db db.KVDB, network string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_LOCK_LASTTIME + network)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	return err
}

func DeleteAllKeysWithPrefix(db db.KVDB, prefix []byte) ([]string, error) {
	keys := make([]string, 0)
	err := db.BatchRead(prefix, false, func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	})

	if err != nil {
		return nil, nil
	}

	batch := db.NewWriteBatch()
	if batch == nil {
		Log.Errorf("NewBatchWrite failed")
		return nil, fmt.Errorf("NewBatchWrite failed")
	}
	defer batch.Close()

	for _, key := range keys {
		err := batch.Delete([]byte(key))
		if err != nil {
			Log.Errorf("db.Remove %s failed. %v", key, err)
		}
	}
	err = batch.Flush()
	if err != nil {
		return nil, err
	}
	Log.Infof("deleted %d keys with prefix %s", len(keys), string(prefix))
	return keys, nil
}

func GetTickerInfoKey(name string) string {
	return fmt.Sprintf("%s%s%s", GetDBKeyPrefix(), DB_KEY_TICKER_INFO, name)
}

func ParseTickerInfoKey(key string) (string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_TICKER_INFO
	if !strings.HasPrefix(key, prefix) {
		return "", fmt.Errorf("not a ticker info key: %s", key)
	}
	return strings.TrimPrefix(key, prefix), nil
}

func saveTickerInfo(db db.KVDB, ticker *indexer.TickerInfo) error {
	buf, err := EncodeToBytes(ticker)
	if err != nil {
		Log.Errorf("saveTickerInfo EncodeToBytes failed. %v", err)
		return err
	}

	err = db.Write([]byte(GetTickerInfoKey(ticker.AssetName.String())), buf)
	if err != nil {
		Log.Errorf("saveRuneInfo failed. %v", err)
		return err
	}
	Log.Infof("saveTickerInfo succ. %s", ticker.AssetName.String())
	return nil
}

func loadTickerInfo(db db.KVDB, name *swire.AssetName) (*indexer.TickerInfo, error) {
	key := GetTickerInfoKey(name.String())

	buf, err := db.Read([]byte(key))
	if err != nil {
		Log.Warningf("Read %s failed. %v", key, err)
		return nil, err
	}
	var value indexer.TickerInfo
	err = DecodeFromBytes(buf, &value)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}

	return &value, nil
}

func deleteAllTickerInfoFromDB(db db.KVDB) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TICKER_INFO)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	return err
}

func GetInscribeResvKey(id int64) string {
	return fmt.Sprintf("%s%s%d", GetDBKeyPrefix(), DB_KEY_INSC, id)
}

func ParseInscribeResvKey(key string) (int64, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_INSC
	if !strings.HasPrefix(key, prefix) {
		return -1, fmt.Errorf("not a reservation: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)

	id, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return -1, err
	}

	return id, nil
}

func LoadAllInscribeResvFromDB(db db.KVDB) map[int64]*InscribeResv {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_INSC)

	result := make(map[int64]*InscribeResv, 0)
	invalidKeys := make([]string, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {

		id, err := ParseInscribeResvKey(string(k))
		if err != nil {
			Log.Errorf("ParseInscribeResvKey failed. %v", err)
			return nil
		}

		var value InscribeResv
		err = DecodeFromBytes(v, &value)
		if err != nil {
			invalidKeys = append(invalidKeys, string(k))
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return nil
		}

		if value.Status <= RS_CLOSED {
			return nil
		}

		result[id] = &value
		Log.Infof("loadAllInscribeResvFromDB loaded. %d", value.Id)
		return nil
	})

	deleteInvalidKey := false
	if deleteInvalidKey && len(invalidKeys) > 0 {
		wb := db.NewWriteBatch()
		for _, k := range invalidKeys {
			wb.Delete([]byte(k))
		}
		wb.Flush()
	}

	return result
}

func SaveInscribeResv(db db.KVDB, resv *InscribeResv) error {

	buf, err := EncodeToBytes(resv)
	if err != nil {
		Log.Errorf("saveInscribeResv EncodeToBytes failed. %v", err)
		return err
	}
	key := GetInscribeResvKey(resv.Id)

	err = db.Write([]byte(key), buf)
	if err != nil {
		Log.Errorf("saveInscribeResv failed. %v", err)
		return err
	}
	Log.Infof("saveInscribeResv %d succ. %x", resv.Id, resv.Status)

	if _enable_testing {
		newResv, err := LoadInscribeResv(db, resv.Id)
		if err != nil {
			Log.Panicf("saveInscribeResv loadReservation failed, %v", err)
		}

		buf2, err := EncodeToBytes(newResv)
		if err != nil {
			Log.Panicf("saveInscribeResv EncodeToBytes failed. %v", err)
		}

		if !bytes.Equal(buf, buf2) {
			Log.Panic("buf not equal")
		}
		Log.Infof("resv %d checked", resv.Id)
	}

	return nil
}

func LoadInscribeResv(db db.KVDB, id int64) (*InscribeResv, error) {
	key := GetInscribeResvKey(id)
	var value InscribeResv
	buf, err := db.Read([]byte(key))
	if err != nil {
		//Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}

	err = DecodeFromBytes(buf, &value)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}

	return &value, nil
}

func DeleteInscribeResv(db db.KVDB, id int64) error {
	key := GetInscribeResvKey(id)
	return db.Delete([]byte(key))
}

func GetUtxoKey(network, addr, utxo string) string {
	return GetDBKeyPrefix() + DB_KEY_UTXO + network + "-" + addr + "-" + utxo
}

func ParseUtxoKey(key string) (string, string, string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_UTXO
	if !strings.HasPrefix(key, prefix) {
		return "", "", "", fmt.Errorf("not a utxo key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], parts[1], parts[2], nil
}

func saveUtxo(db db.KVDB, network, addr, utxo string, value *TxOutput_SatsNet) error {
	buf, err := EncodeToBytes(value)
	if err != nil {
		Log.Errorf("saveLockedUtxo EncodeToBytes failed. %v", err)
		return err
	}

	err = db.Write([]byte(GetUtxoKey(network, addr, utxo)), buf)
	if err != nil {
		Log.Errorf("saveLockedUtxo failed. %v", err)
		return err
	}
	Log.Infof("saveLockedUtxo succ. %s", utxo)
	return nil
}

func DeleteUtxo(db db.KVDB, network, addr, utxo string) error {
	return db.Delete([]byte(GetUtxoKey(network, addr, utxo)))
}

func DeleteAllUtxoInAddress(db db.KVDB, network, addr string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_UTXO + network + "-" + addr)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return deleteAllLastLockTime(db, network)
}

func loadAllUtxoFromDB(db db.KVDB, network string) map[string]map[string]*TxOutput_SatsNet {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_UTXO + network)

	result := make(map[string]map[string]*TxOutput_SatsNet)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, addr, utxo, err := ParseUtxoKey(string(k))
		if err != nil {
			Log.Errorf("ParseLockedUtxoKey failed. %v", err)
			return err
		}

		var value TxOutput_SatsNet
		err = DecodeFromBytes(v, &value)
		if err != nil {
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return err
		}

		utxos, ok := result[addr]
		if !ok {
			utxos = make(map[string]*TxOutput_SatsNet)
			result[addr] = utxos
		}
		utxos[utxo] = &value
		return nil
	})

	return result
}

func GetContractRuntimeKey(url string) string {
	return GetDBKeyPrefix() + DB_KEY_TEMPLATE_CONTRACT + url
}

func ParseContractRuntimeKey(key string) (string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_TEMPLATE_CONTRACT
	if !strings.HasPrefix(key, prefix) {
		return "", fmt.Errorf("not a template contract key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)

	strings.Split(key, "-")

	return key, nil
}

func saveContractRuntime(db db.KVDB, value ContractRuntime) error {
	buf := value.RuntimeContent()
	err := db.Write([]byte(GetContractRuntimeKey(value.URL())), buf)
	if err != nil {
		Log.Infof("saveTemplateContract failed. %v", err)
		return err
	}
	Log.Infof("saveTemplateContract succ. %s", value.GetContractName())
	return nil
}

func loadContractRuntime(stp ContractManager, url string) (ContractRuntime, error) {
	key := GetContractRuntimeKey(url)

	buf, err := stp.GetDB().Read([]byte(key))
	if err != nil {
		//Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}
	c, err := ContractRuntimeUnMarsh(stp, ExtractContractType(url), (buf))
	if err != nil {
		Log.Errorf("ContractRuntimeUnMarsh %s failed. %v", key, err)
		return nil, err
	}

	return c, nil
}

func deleteContractRuntime(db db.KVDB, url string) error {
	return db.Delete([]byte(GetContractRuntimeKey(url)))
}

func deleteAllContractRuntime(db db.KVDB, channelId string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TEMPLATE_CONTRACT + channelId)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return nil
}

func loadAllContractRuntimeFromDB(stp ContractManager, channelId string) map[string]ContractRuntime {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TEMPLATE_CONTRACT + channelId)

	result := make(map[string]ContractRuntime, 0)
	stp.GetDB().BatchRead(prefix, false, func(k, v []byte) error {
		ct := ExtractContractType(string(k))

		c, err := ContractRuntimeUnMarsh(stp, ct, (v))
		if err != nil {
			Log.Errorf("ContractJsonUnMarsh failed. %v", err)
			return nil
		}

		result[ct] = c
		return nil
	})

	return result
}

func GetSwapContractRunningDataKey(url string, id int) string {
	return fmt.Sprintf("%s%s-%d", GetDBKeyPrefix()+DB_KEY_TC_SWAP_RUNNINGDATA, url, id)
}

func ParseSwapContractRunningDataKey(key string) (string, int, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_TC_SWAP_RUNNINGDATA
	if !strings.HasPrefix(key, prefix) {
		return "", 0, fmt.Errorf("not a swap contract running data key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)

	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid key of swap contract running data: %s", key)
	}
	id, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return "", 0, err
	}

	return parts[0], int(id), nil
}

func saveSwapContractRunningData(db db.KVDB, url string, id int, value *SwapContractRunningData) error {
	buf, err := EncodeToBytes(value)
	if err != nil {
		Log.Errorf("saveSwapContractRunningData EncodeToBytes failed. %v", err)
		return err
	}
	err = db.Write([]byte(GetSwapContractRunningDataKey(url, id)), buf)
	if err != nil {
		Log.Infof("saveSwapContractRunningData failed. %v", err)
		return err
	}
	Log.Infof("saveSwapContractRunningData succ. %s %d", url, id)
	return nil
}

func loadSwapContractRunningData(db db.KVDB, url string, id int) (*SwapContractRunningData, error) {
	key := GetSwapContractRunningDataKey(url, id)

	buf, err := db.Read([]byte(key))
	if err != nil {
		//Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}
	var value SwapContractRunningData
	err = DecodeFromBytes(buf, &value)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}

	return &value, nil
}

func deleteSwapContractRunningData(db db.KVDB, url string, id int) error {
	return db.Delete([]byte(GetSwapContractRunningDataKey(url, id)))
}

func deleteAllSwapContractRunningData(db db.KVDB, url string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_SWAP_RUNNINGDATA + url)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return nil
}

func loadAllSwapContractRunningDataFromDB(db db.KVDB, channelId string) map[int]*SwapContractRunningData {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_SWAP_RUNNINGDATA + channelId)

	result := make(map[int]*SwapContractRunningData, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, id, err := ParseSwapContractRunningDataKey(string(k))
		if err != nil {
			return nil
		}

		var value SwapContractRunningData
		err = DecodeFromBytes(v, &value)
		if err != nil {
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return nil
		}

		result[id] = &value
		return nil
	})

	return result
}

func GetLiquidityDataKey(url string, id int) string {
	return fmt.Sprintf("%s%s-%d", GetDBKeyPrefix()+DB_KEY_TC_LIQ_DATA, url, id)
}

func ParseLiquidityDataKey(key string) (string, int, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_TC_LIQ_DATA
	if !strings.HasPrefix(key, prefix) {
		return "", 0, fmt.Errorf("not a swap contract stake key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)

	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid key of swap contract stake data: %s", key)
	}
	id, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return "", 0, err
	}

	return parts[0], int(id), nil
}

func saveLiquidityData(db db.KVDB, url string, value *LiquidityData) error {
	buf, err := EncodeToBytes(value)
	if err != nil {
		Log.Errorf("saveLiquidityData EncodeToBytes failed. %v", err)
		return err
	}
	// 0 表示当前池子的流动性提供者数据
	err = db.Write([]byte(GetLiquidityDataKey(url, 0)), buf)
	if err != nil {
		Log.Infof("saveLiquidityData failed. %v", err)
		return err
	}
	if value.Height%5000 == 0 {
		// 作为历史记录
		err = db.Write([]byte(GetLiquidityDataKey(url, value.Height/5000)), buf)
		if err != nil {
			Log.Infof("saveLiquidityData failed. %v", err)
			return err
		}
	}
	Log.Infof("saveLiquidityData succ. %s", url)
	return nil
}

func loadLiquidityData(db db.KVDB, url string) (*LiquidityData, error) {
	key := GetLiquidityDataKey(url, 0)

	buf, err := db.Read([]byte(key))
	if err != nil {
		//Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}
	var value LiquidityData
	err = DecodeFromBytes(buf, &value)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}

	return &value, nil
}

func deleteLiquidityData(db db.KVDB, url string, id int) error {
	return db.Delete([]byte(GetLiquidityDataKey(url, id)))
}

func deleteAllLiquidityData(db db.KVDB, url string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_LIQ_DATA + url)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return nil
}

func loadAllLiquidityDataFromDB(db db.KVDB, channelAddr string) map[int]*LiquidityData {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_LIQ_DATA + channelAddr)

	result := make(map[int]*LiquidityData, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, id, err := ParseLiquidityDataKey(string(k))
		if err != nil {
			return nil
		}

		var value LiquidityData
		err = DecodeFromBytes(v, &value)
		if err != nil {
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return nil
		}

		result[id] = &value
		return nil
	})

	return result
}

func GetContractInvokeHistoryKey(url, utxo string) string {
	return GetDBKeyPrefix() + DB_KEY_TC_INVOKE_HISTORY + url + "-" + utxo
}

func ParseContractInvokeHistoryKey(key string) (string, string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_TC_INVOKE_HISTORY
	if !strings.HasPrefix(key, prefix) {
		return "", "", fmt.Errorf("not a template contract invoke key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], parts[1], nil
}

func SaveContractInvokeHistoryItem(db db.KVDB, url string, value InvokeHistoryItem) error {
	buf, err := EncodeToBytes(value)
	if err != nil {
		Log.Errorf("saveContractInvokeHistoryItem EncodeToBytes failed. %v", err)
		return err
	}
	err = db.Write([]byte(GetContractInvokeHistoryKey(url, value.GetKey())), buf)
	if err != nil {
		Log.Errorf("saveContractInvokeHistoryItem failed. %v", err)
		return err
	}
	Log.Infof("saveContractInvokeHistoryItem succ. %s", value.GetKey())
	return nil
}

func loadContractInvokeHistoryItem(db db.KVDB, url, inkey string) (InvokeHistoryItem, error) {
	key := GetContractInvokeHistoryKey(url, inkey)

	buf, err := db.Read([]byte(key))
	if err != nil {
		Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}
	item := NewInvokeHistoryItem(ExtractContractType(url))
	if item == nil {
		Log.Errorf("NewInvokeHistoryItem %s failed", url)
		return nil, err
	}
	err = DecodeFromBytes(buf, item)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}

	return item, nil
}

func deleteContractInvokeHistoryItem(db db.KVDB, url, inkey string) error {
	return db.Delete([]byte(GetContractInvokeHistoryKey(url, inkey)))
}

func DeleteContractInvokeHistory(db db.KVDB, url string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKE_HISTORY + url)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return nil
}

func LoadContractInvokeHistory(db db.KVDB, url string, excludingDone, reverse bool) map[string]InvokeHistoryItem {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKE_HISTORY + url)

	ty := ExtractContractType(url)
	result := make(map[string]InvokeHistoryItem, 0)
	upgradedItems := make([]InvokeHistoryItem, 0)
	db.BatchRead(prefix, reverse, func(k, v []byte) error {
		_, inkey, err := ParseContractInvokeHistoryKey(string(k))
		if err != nil {
			Log.Errorf("ParseContractInvokeHistoryKey failed. %v", err)
			return nil
		}

		item := NewInvokeHistoryItem(ty)
		err = DecodeFromBytes(v, item)
		if err != nil {
			// try old version
			item = NewInvokeHistoryItem_old(ty)
			err = DecodeFromBytes(v, item)
			if err != nil {
				Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
				return nil
			}
			item = item.ToNewVersion()
			upgradedItems = append(upgradedItems, item)
		}

		if excludingDone && item.HasDone() {
			return nil
		}

		result[inkey] = item
		return nil
	})

	if len(upgradedItems) != 0 {
		for _, item := range upgradedItems {
			SaveContractInvokeHistoryItem(db, url, item)
		}
	}

	return result
}

func findContractInvokeItem(db db.KVDB, url string, target string) *InvokeItem {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKE_HISTORY + url)

	ty := ExtractContractType(url)

	var result *InvokeItem
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, _, err := ParseContractInvokeHistoryKey(string(k))
		if err != nil {
			Log.Errorf("ParseContractInvokeHistoryKey failed. %v", err)
			return nil
		}

		item := NewInvokeHistoryItem(ty)
		err = DecodeFromBytes(v, item)
		if err != nil {
			return nil
		}
		i, ok := item.(*InvokeItem)
		if ok {
			if strings.Contains(i.InUtxo, target) || i.OutTxId == target || i.GetKey() == target {
				result = i
				return fmt.Errorf("found it")
			}
		}

		return nil
	})

	return result
}

func loadContractInvokeHistoryByHeight(db db.KVDB, url string, excludingDone bool,
	height int, bSatsNet bool) map[string]InvokeHistoryItem {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKE_HISTORY + url)

	ty := ExtractContractType(url)
	result := make(map[string]InvokeHistoryItem, 0)
	upgradedItems := make([]InvokeHistoryItem, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, inkey, err := ParseContractInvokeHistoryKey(string(k))
		if err != nil {
			Log.Errorf("ParseContractInvokeHistoryKey failed. %v", err)
			return nil
		}

		item := NewInvokeHistoryItem(ty)
		err = DecodeFromBytes(v, item)
		if err != nil {
			// try old version
			item = NewInvokeHistoryItem_old(ty)
			err = DecodeFromBytes(v, item)
			if err != nil {
				Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
				return nil
			}
			item = item.ToNewVersion()
			upgradedItems = append(upgradedItems, item)
		}

		if excludingDone && item.HasDone() {
			return nil
		}
		if item.FromSatsNet() != bSatsNet {
			return nil
		}
		if item.GetHeight() < height {
			return nil
		}

		result[inkey] = item
		return nil
	})

	if len(upgradedItems) != 0 {
		for _, item := range upgradedItems {
			SaveContractInvokeHistoryItem(db, url, item)
		}
	}

	return result
}

func loadContractInvokeHistoryWithRange(db db.KVDB, url string, start, limit int) map[string]InvokeHistoryItem {
	prefix := []byte(fmt.Sprintf("%s%s%s-", GetDBKeyPrefix(), DB_KEY_TC_INVOKE_HISTORY, url))
	seek := []byte(fmt.Sprintf("%s%s%s-%s", GetDBKeyPrefix(), DB_KEY_TC_INVOKE_HISTORY, url, GetKeyFromId(int64(start))))

	ty := ExtractContractType(url)
	result := make(map[string]InvokeHistoryItem, 0)

	db.BatchReadV2(prefix, seek, false, func(k, v []byte) error {
		_, inkey, err := ParseContractInvokeHistoryKey(string(k))
		if err != nil {
			Log.Errorf("ParseContractInvokeHistoryKey failed. %v", err)
			return err
		}

		item := NewInvokeHistoryItem(ty)
		err = DecodeFromBytes(v, item)
		if err != nil {
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return err
		}

		result[inkey] = item
		if len(result) == limit {
			return fmt.Errorf("reach limit")
		}
		return nil
	})

	return result
}

func GetContractInvokeHistoryBackupKey(url, txId string) string {
	return GetDBKeyPrefix() + DB_KEY_TC_INVOKE_HISTORY_BACKUP + url + "-" + txId
}

// 将该记录从invoke history中删除，备份到backup history中
func backupContractInvokeHistoryItem(db db.KVDB, url string, value InvokeHistoryItem) error {

	deleteContractInvokeHistoryItem(db, url, value.GetKey())

	key := GetContractInvokeHistoryBackupKey(url, value.GetKey())
	buf, err := EncodeToBytes(value)
	if err != nil {
		Log.Errorf("backupContractInvokeHistoryItem EncodeToBytes failed. %v", err)
		return err
	}
	err = db.Write([]byte(key), buf)
	if err != nil {
		Log.Errorf("backupContractInvokeHistoryItem failed. %v", err)
		return err
	}
	Log.Infof("backupContractInvokeHistoryItem succ. %s", value.GetKey())
	return nil
}

func GetContractInvokeResultKey(url, txId string) string {
	return GetDBKeyPrefix() + DB_KEY_TC_INVOKE_RESULT + url + "-" + txId
}

func ParseContractInvokeResultKey(key string) (string, string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_TC_INVOKE_RESULT
	if !strings.HasPrefix(key, prefix) {
		return "", "", fmt.Errorf("not a template contract invoke result key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], parts[1], nil
}

func saveContractInvokeResult(db db.KVDB, url string, txId, reason string) error {
	key := GetContractInvokeResultKey(url, txId)
	err := db.Write([]byte(key), []byte(reason))
	if err != nil {
		Log.Errorf("saveContractInvokeResult failed. %v", err)
		return err
	}
	Log.Infof("saveContractInvokeResult succ. %s", key)
	return nil
}

func deleteContractInvokeResult(db db.KVDB, url, txId string) error {
	return db.Delete([]byte(GetContractInvokeResultKey(url, txId)))
}

func deleteContractAllInvokeResult(db db.KVDB, url string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKE_RESULT + url)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return nil
}

func loadContractAllInvokeResult(db db.KVDB, url string) map[string]string {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKE_RESULT + url)

	result := make(map[string]string, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, txId, err := ParseContractInvokeResultKey(string(k))
		if err != nil {
			Log.Errorf("ParseContractInvokeResultKey failed. %v", err)
			return nil
		}

		result[txId] = string(v)
		return nil
	})

	return result
}

func GetContractInvokerStatusKey(url, address string) string {
	return GetDBKeyPrefix() + DB_KEY_TC_INVOKER_STATUS + url + "-" + address
}

func ParseContractInvokerStatusKey(key string) (string, string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_TC_INVOKER_STATUS
	if !strings.HasPrefix(key, prefix) {
		return "", "", fmt.Errorf("not a user template contract invoke status key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], parts[1], nil
}

func saveContractInvokerStatus(db db.KVDB, url string, value InvokerStatus) error {
	buf, err := EncodeToBytes(value)
	if err != nil {
		Log.Errorf("saveUserContractInvokeStatus EncodeToBytes failed. %v", err)
		return err
	}
	err = db.Write([]byte(GetContractInvokerStatusKey(url, value.GetKey())), buf)
	if err != nil {
		Log.Errorf("saveUserContractInvokeStatus failed. %v", err)
		return err
	}
	Log.Infof("saveUserContractInvokeStatus succ. %s", value.GetKey())
	return nil
}

func loadContractInvokerStatus(db db.KVDB, url, address string) (InvokerStatus, error) {
	key := GetContractInvokerStatusKey(url, address)

	buf, err := db.Read([]byte(key))
	if err != nil {
		//Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}

	status := NewInvokerStatus(ExtractContractType(url))
	if status == nil {
		Log.Errorf("NewInvokeStatus %s failed", url)
		return nil, err
	}
	err = DecodeFromBytes(buf, status)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}

	return status, nil
}

func deleteContractInvokerStatus(db db.KVDB, url, address string) error {
	return db.Delete([]byte(GetContractInvokerStatusKey(url, address)))
}

func DeleteAllContractInvokerStatus(db db.KVDB, url string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKER_STATUS + url)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return nil
}

func loadAllContractInvokerStatus(db db.KVDB, url string) map[string]InvokerStatus {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_TC_INVOKER_STATUS + url)

	ct := ExtractContractType(url)
	result := make(map[string]InvokerStatus, 0)
	//upgradedItems := make([]InvokeHistoryItem, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		_, inkey, err := ParseContractInvokerStatusKey(string(k))
		if err != nil {
			Log.Errorf("ParseUserContractInvokeStatusKey failed. %v", err)
			return nil
		}

		item := NewInvokerStatus(ct)
		if item == nil {
			Log.Errorf("NewInvokeStatus %s failed", url)
			return nil
		}
		err = DecodeFromBytes(v, item)
		if err != nil {
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return nil
		}

		result[inkey] = item
		return nil
	})

	// if len(upgradedItems) != 0 {
	// 	for _, item := range upgradedItems {
	// 		saveContractInvokeHistoryItem(db, url, item)
	// 	}
	// }

	return result
}

func DeleteContractRelatedDataFromDB(db db.KVDB, url string) {
	// 如果是无效的url，直接返回
	if IsValidContractURL(url) {
		// contract 本身由resv保存，需要去删除resv本身
		DeleteContractInvokeHistory(db, url)
		deleteContractAllInvokeResult(db, url)
		DeleteAllContractInvokerStatus(db, url)
		Log.Infof("contract %s related data deleted", url)
	}
}
