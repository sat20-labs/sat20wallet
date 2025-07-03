package wallet

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/btcsuite/btcwallet/snacl"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	DB_KEY_STATUS = "wallet-status"
	DB_KEY_WALLET = "wallet-id-"

	DB_KEY_INSC        = "insc-"
	DB_KEY_TICKER_INFO = "t-"

	DB_KEY_LOCKEDUTXO    = "l-"  // l-network-address-utxo
	DB_KEY_LOCK_LASTTIME = "lt-" // lt-network-address
)

var _mode string  // 
var _chain string // mainnet, testnet
var _env string   // dev, test, prd
var _enable_testing bool = false

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

type WalletInDB struct {
	Id       int64  // 钱包id，也是创建时间
	Mnemonic []byte // 加密后的数据
	Salt     []byte
	Accounts int // 用户启用的子账户数量
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

func GetItemsFromDB(prefix []byte, db common.KVDB) (map[string][]byte, error) {
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

	p.repair()

	return nil
}

func (p *Manager) repair() bool {

	return false
}

func loadAllWalletFromDB(db common.KVDB) (map[int64]*WalletInDB, error) {
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

func saveWallet(db common.KVDB, wallet *WalletInDB) error {

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

func loadWallet(db common.KVDB, id int64) (*WalletInDB, error) {
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
	key, err := p.newSnaclKey(password)
	if err != nil {
		Log.Errorf("NewSecretKey failed. %v", err)
		return -1, err
	}

	en, err := key.Encrypt([]byte(mn))
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
	}

	err = saveWallet(p.db, &wallet)
	if err != nil {
		return -1, err
	}

	p.walletInfoMap[wallet.Id] = &wallet
	return wallet.Id, nil
}


func (p *Manager) saveMnemonicWithId(mn, password string, old *WalletInDB) (error) {
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

	wallet := WalletInDB{
		Id:       old.Id,
		Mnemonic: en,
		Salt:     salt,
		Accounts: old.Accounts,
	}

	err = saveWallet(p.db, &wallet)
	if err != nil {
		return err
	}

	p.walletInfoMap[wallet.Id] = &wallet
	return nil
}

func (p *Manager) loadMnemonic(id int64, password string) (string, error) {
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

func saveLockedUtxo(db common.KVDB, network, utxo string, value *LockedUtxo) error {
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

func DeleteLockedUtxo(db common.KVDB, network, utxo string) error {
	return db.Delete([]byte(GetLockedUtxoKey(network, utxo)))
}

func DeleteAllLockedUtxo(db common.KVDB, network string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO + network)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return err
	}
	return deleteAllLastLockTime(db, network)
}

// 暂时不考虑地址
func loadAllLockedUtxoFromDB(db common.KVDB, network string) map[string]*LockedUtxo {
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

func saveLastLockTime(db common.KVDB, network string, t int64) error {
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

func loadLastLockTime(db common.KVDB, network string) (int64, error) {
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

func deleteAllLastLockTime(db common.KVDB, network string) error {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_LOCK_LASTTIME + network)
	_, err := DeleteAllKeysWithPrefix(db, prefix)
	return err
}

func DeleteAllKeysWithPrefix(db common.KVDB, prefix []byte) ([]string, error) {
	keys := make([]string, 0)
	err := db.BatchRead(prefix, false, func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	})

	if err != nil {
		return nil, nil
	}

	batch := db.NewBatchWrite()
	if batch == nil {
		Log.Errorf("NewBatchWrite failed")
		return nil, fmt.Errorf("NewBatchWrite failed")
	}
	defer batch.Close()

	for _, key := range keys {
		err := batch.Remove([]byte(key))
		if err != nil {
			Log.Errorf("db.Remove %s failed. %v", key, err)
		}
	}
	err = batch.Flush()
	if err != nil {
		return nil, err
	}
	Log.Infof("deleted %d keys", len(keys))
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

func saveTickerInfo(db common.KVDB, ticker *indexer.TickerInfo) error {
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

func loadTickerInfo(db common.KVDB, name *swire.AssetName) (*indexer.TickerInfo, error) {
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

func deleteAllTickerInfoFromDB(db common.KVDB) error {
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

func LoadAllInscribeResvFromDB(db common.KVDB) map[int64]*InscribeResv {
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
		wb := db.NewBatchWrite()
		for _, k := range invalidKeys {
			wb.Remove([]byte(k))
		}
		wb.Flush()
	}

	return result
}

func SaveInscribeResv(db common.KVDB, resv *InscribeResv) error {

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

func LoadInscribeResv(db common.KVDB, id int64) (*InscribeResv, error) {
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

func DeleteInscribeResv(db common.KVDB, id int64) error {
	key := GetInscribeResvKey(id)
	return db.Delete([]byte(key))
}
