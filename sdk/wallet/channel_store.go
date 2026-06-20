package wallet

import (
	"fmt"
	"strings"

	db "github.com/sat20-labs/indexer/common"
)

func GetChannelKey(channelId string) string {
	return GetDBKeyPrefix() + DB_KEY_CHANNEL + channelId
}

func ParseChannelKey(key string) (string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_CHANNEL
	if !strings.HasPrefix(key, prefix) {
		return "", fmt.Errorf("not a channel: %s", key)
	}
	return strings.TrimPrefix(key, prefix), nil
}

func SaveChannelInDB(kv db.KVDB, c *ChannelInDB) error {
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
		Log.Errorf("SaveChannelInDB EncodeToBytes failed. %v", err)
		return err
	}
	err = kv.Write([]byte(GetChannelKey(c.ChannelId)), buf)
	if err != nil {
		Log.Errorf("SaveChannelInDB %s failed. %v", c.ChannelId, err)
		return err
	}
	Log.Infof("SaveChannelInDB succ. %s  %x", c.ChannelId, c.Status)
	return nil
}

func LoadChannelInDB(kv db.KVDB, channelId string) (*ChannelInDB, error) {
	key := GetChannelKey(channelId)
	buf, err := kv.Read([]byte(key))
	if err != nil {
		Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}
	var channel ChannelInDB
	if err := DecodeFromBytes(buf, &channel); err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}
	if ok, _ := channel.CheckDataWithCleanup(); !ok {
		return nil, fmt.Errorf("channel %s CheckData failed", channel.ChannelId)
	}
	return &channel, nil
}

func LoadAllChannelInDBFromDB(kv db.KVDB) (map[string]*ChannelInDB, error) {
	result := make(map[string]*ChannelInDB)
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_CHANNEL)
	err := kv.BatchRead(prefix, false, func(k, v []byte) error {
		var channel ChannelInDB
		if err := DecodeFromBytes(v, &channel); err != nil {
			Log.Errorf("DecodeFromBytes %s failed. %v", string(k), err)
			return err
		}
		if ok, _ := channel.CheckDataWithCleanup(); !ok {
			return fmt.Errorf("channel %s CheckData failed", channel.ChannelId)
		}
		result[channel.ChannelId] = &channel
		Log.Infof("channel %s loaded, status %x, commit height %d", channel.ChannelId, channel.Status, channel.CommitHeight)
		return nil
	})
	return result, err
}

func DeleteChannelInDB(kv db.KVDB, channelId string) error {
	return kv.Delete([]byte(GetChannelKey(channelId)))
}

func (p *Manager) SaveChannelInDB(c *ChannelInDB) error {
	return SaveChannelInDB(p.db, c)
}

func (p *Manager) LoadChannelInDB(channelId string) (*ChannelInDB, error) {
	return LoadChannelInDB(p.db, channelId)
}

func (p *Manager) LoadAllChannelInDBFromDB() (map[string]*ChannelInDB, error) {
	return LoadAllChannelInDBFromDB(p.db)
}

func (p *Manager) DeleteChannelInDB(channelId string) error {
	return DeleteChannelInDB(p.db, channelId)
}
