package wallet

import (
	"fmt"

	db "github.com/sat20-labs/indexer/common"
)

const (
	DB_KEY_BACKUP_CHANNEL = "bc-"
)

func GetChannelBackupKey(channelId string) string {
	return GetDBKeyPrefix() + DB_KEY_BACKUP_CHANNEL + channelId
}

func SaveBackupChannelInDB(kv db.KVDB, c *ChannelInDB) error {
	if c == nil {
		return fmt.Errorf("nil channel")
	}
	err := c.PrepareForSave()
	if err != nil {
		Log.Errorf("PrepareForSave %s failed. %v", c.ChannelId, err)
		return err
	}
	buf, err := EncodeToBytes(c)
	if err != nil {
		Log.Errorf("SaveBackupChannelInDB EncodeToBytes failed. %v", err)
		return err
	}
	err = kv.Write([]byte(GetChannelBackupKey(c.ChannelId)), buf)
	if err != nil {
		Log.Errorf("SaveBackupChannelInDB failed. %v", err)
		return err
	}
	Log.Infof("SaveBackupChannelInDB succ. %s %x", c.ChannelId, c.Status)
	return nil
}

func LoadBackupChannelInDB(kv db.KVDB, channelId string) (*ChannelInDB, error) {
	key := GetChannelBackupKey(channelId)
	buf, err := kv.Read([]byte(key))
	if err != nil {
		Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}

	var channel ChannelInDB
	err = DecodeFromBytes(buf, &channel)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}
	ok, updated := channel.CheckDataWithCleanup()
	if !ok {
		return nil, fmt.Errorf("backup channel %s CheckData failed", channel.ChannelId)
	}
	if updated {
		if err := SaveBackupChannelInDB(kv, &channel); err != nil {
			return nil, err
		}
	}
	return &channel, nil
}

func (p *Manager) SaveBackupChannelToDB(c *ChannelInDB) error {
	return SaveBackupChannelInDB(p.db, c)
}

func (p *Manager) LoadBackupChannel(channelId string) (*Channel, error) {
	channel, err := LoadBackupChannelInDB(p.db, channelId)
	if err != nil {
		return nil, err
	}
	return NewChannel(channel, p), nil
}
