package wallet

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"strings"
	"time"

	"github.com/btcsuite/btcwallet/snacl"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	
	indexer "github.com/sat20-labs/indexer/common"
)

const (
	DB_KEY_STATUS   = "wallet-status"
	DB_KEY_WALLET   = "wallet-id-"
	DB_KEY_ASSET_L1 = "wallet-asset-l1-" //  Id - subId - chain
	DB_KEY_ASSET_L2 = "wallet-asset-l2-"
	DB_KEY_TICKER_INFO    = "t-"
)

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
	Accounts int    // 用户启用的子账户数量
}

func getWalletDBKey(id int64) string {
	return fmt.Sprintf("%s%d", DB_KEY_WALLET, id)
}

func getAssetDBKey(id int64, subId int, chain string) string {
	return fmt.Sprintf("%s%d-%d-%s", DB_KEY_ASSET_L1, id, subId, chain)
}

func getAssetDBKey_SatsNet(id int64, subId int, chain string) string {
	return fmt.Sprintf("%s%d-%d-%s", DB_KEY_ASSET_L2, id, subId, chain)
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
	err := db.BatchRead(prefix, func(k, v []byte) error {
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

	p.repair()

	return nil
}

func (p *Manager) repair() bool {

	return false
}

func loadAllWalletFromDB(db common.KVDB) (map[int64]*WalletInDB, error) {
	prefix := []byte(DB_KEY_WALLET)

	result := make(map[int64]*WalletInDB, 0)
	err := db.BatchRead(prefix, func(k, v []byte) error {

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
		SoftwareVer: SOFTWARE_VERSION,
		DBver:       DB_VERSION,
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

func (p *Manager) loadMnemonic(id int64, password string) (string, error) {
	wallet, ok := p.walletInfoMap[id]
	if !ok {
		return "", fmt.Errorf("can't find wallet %d", id)
	}

	key, err := p.reGenerateSnaclKey(wallet.Salt, password)
	if err != nil {
		Log.Errorf("NewSecretKey failed. %v", err)
		return "", err
	}

	mnemonic, err := key.Decrypt(wallet.Mnemonic)
	if err != nil {
		Log.Errorf("Decrypt failed. %v", err)
		return "", err
	}

	return string(mnemonic), nil
}

func (p *Manager) reGenerateSnaclKey(salt []byte, password string) (*snacl.SecretKey, error) {
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


func GetTickerInfoKey(name string) string {
	return fmt.Sprintf("%s%s", DB_KEY_TICKER_INFO, name)
}

func ParseTickerInfoKey(key string) (string, error) {
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid key %s", key)
	}
	return parts[1], nil
}

func saveTickerInfo(db common.KVDB, ticker *indexer.TickerInfo) error {
	buf, err := EncodeToBytes(ticker)
	if err != nil {
		return err
	}

	err = db.Write([]byte(GetTickerInfoKey(ticker.AssetName.String())), buf)
	if err != nil {
		Log.Infof("saveRuneInfo failed. %v", err)
		return err
	}
	Log.Infof("saveTickerInfo succ. %s", ticker.AssetName.String())
	return nil
}

func loadTickerInfo(db common.KVDB, name *AssetName) (*indexer.TickerInfo, error) {
	key := GetTickerInfoKey(name.String())

	buf, err := db.Read([]byte(key))
	if err != nil {
		Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}
	var value indexer.TickerInfo
	err = DecodeFromBytes(buf, &value)
	if err != nil {
		Log.Errorf("DecodeFromBytes failed. %v", err)
		return nil, err
	}

	return &value, nil
}

// for test
func deleteAllTickerInfoFromDB(db common.KVDB) error {
	prefix := []byte(DB_KEY_TICKER_INFO)

	result := make(map[string]bool, 0)
	err := db.BatchRead(prefix, func(k, v []byte) error {

		tickerName, err := ParseTickerInfoKey(string(k))
		if err != nil {
			Log.Errorf("ParseTickerInfoKey failed. %v", err)
			return err
		}

		result[tickerName] = true
		return nil
	})

	if err != nil {
		return nil
	}

	batch := db.NewBatchWrite()
	if batch == nil {
		Log.Errorf("NewBatchWrite failed")
		return fmt.Errorf("NewBatchWrite failed")
	}
	defer batch.Close()

	for k := range result {
		key := GetTickerInfoKey(k)
		err := batch.Remove([]byte(key))
		if err != nil {
			Log.Errorf("db.Remove %s failed. %v", key, err)
		}
	}
	return batch.Flush()
}
