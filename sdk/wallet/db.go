package wallet

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/btcsuite/btcwallet/snacl"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

const (
	DB_KEY_STATUS   = "wallet-status"
	DB_KEY_WALLET   = "wallet-id-"
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
