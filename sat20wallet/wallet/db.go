package wallet

import (
	"bytes"
	"encoding/gob"

	"github.com/btcsuite/btcwallet/snacl"
	"github.com/sat20-labs/sat20wallet/common"
)

const (
	DB_KEY_STATUS         = "status"
	DB_KEY_MNEMONIC       = "mn"
	DB_KEY_SNACL          = "snacl"
)

type Status struct {
	SoftwareVer  string
	DBver        string
	SyncHeightL1 int
	SyncHeightL2 int
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


func (p *Manager) repair() bool {

	

	return false
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

	if result.SyncHeightL1 <= 0 {
		height := int(p.l1IndexerClient.GetBestHeight())
		if height >= 0 {
			result.SyncHeightL1 = height
		}
	}
	if result.SyncHeightL2 <= 0 {
		height := int(p.l2IndexerClient.GetBestHeight())
		if height >= 0 {
			result.SyncHeightL2 = height
		}
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


func (p *Manager) saveMnemonic(mn, password string) error {
	key, err := p.getSnaclKey(password)
	if err != nil {
		Log.Errorf("NewSecretKey failed. %v", err)
		return err
	}

	en, err := key.Encrypt([]byte(mn))
	if err != nil {
		Log.Errorf("Encrypt failed. %v", err)
		return err
	}

	buf := key.Marshal()
	err = p.db.Write([]byte(DB_KEY_SNACL), buf)
	if err != nil {
		Log.Errorf("Write DB_KEY_SNACL failed. %v", err)
		return err
	}

	err = p.db.Write([]byte(DB_KEY_MNEMONIC), en)
	if err != nil {
		Log.Infof("saveMnemonic failed. %v", err)
		return err
	}
	Log.Infof("saveMnemonic succ")
	return nil
}

func (p *Manager) loadMnemonic(password string) (string, error) {
	key, err := p.getSnaclKey(password)
	if err != nil {
		Log.Errorf("NewSecretKey failed. %v", err)
		return "", err
	}

	buf, err := p.db.Read([]byte(DB_KEY_MNEMONIC))
	if err != nil {
		Log.Errorf("Read %s failed. %v", DB_KEY_MNEMONIC, err)
		return "", err
	}

	mnemonic, err := key.Decrypt(buf)
	if err != nil {
		Log.Errorf("Decrypt failed. %v", err)
		return "", err
	}

	return string(mnemonic), nil
}

func (p *Manager) getSnaclKey(password string) (*snacl.SecretKey, error) {
	pw := []byte(password)

	buf, err := p.db.Read([]byte(DB_KEY_SNACL))
	if err == nil {
		var sk snacl.SecretKey
		err := sk.Unmarshal(buf)
		if err == nil {
			err = sk.DeriveKey(&pw)
			if err == nil {
				return &sk, nil
			}
		}
	}

	key, err := snacl.NewSecretKey(&pw, snacl.DefaultN, snacl.DefaultR, snacl.DefaultP)
	if err != nil {
		Log.Errorf("NewSecretKey failed. %v", err)
		return nil, err
	}

	return key, nil
}

func (p *Manager) IsWalletExist() bool {
	_, err := p.db.Read([]byte(DB_KEY_SNACL))
	if err == nil {
		_, err = p.db.Read([]byte(DB_KEY_MNEMONIC))
		if err == nil {
			return true
		}
	}
	return false
}
