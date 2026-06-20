package wallet

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/wire"
	db "github.com/sat20-labs/indexer/common"
)

const (
	DB_KEY_WT_PUNISHTX      = "wtptx-"
	DB_KEY_WT_UTXOCMAP      = "wtucm-"
	DB_KEY_WT_BRODCASTEDCTX = "wtbctx-"
)

func GetPunishTxKey(channelId, commitTxId string) string {
	return GetDBKeyPrefix() + DB_KEY_WT_PUNISHTX + channelId + "-" + commitTxId
}

func ParsePunishTxKey(key string) (string, string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_WT_PUNISHTX
	if !strings.HasPrefix(key, prefix) {
		return "", "", fmt.Errorf("not a punish tx: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], parts[1], nil
}

func savePunishTx(db db.KVDB, channel *Channel, commitTxId string, txs []*wire.MsgTx) error {
	var txsHex string
	for _, tx := range txs {
		if len(txsHex) != 0 {
			txsHex += "-"
		}
		value, err := EncodeMsgTx(tx)
		if err != nil {
			Log.Errorf("savePunishTx EncodeMsgTx failed. %v", err)
			return err
		}
		txsHex += value
	}

	err := db.Write([]byte(GetPunishTxKey(channel.ChannelId, commitTxId)), []byte(txsHex))
	if err != nil {
		Log.Infof("savePunishTx failed. %v", err)
		return err
	}
	Log.Infof("savePunishTx succ. %s", channel.ChannelId)
	return nil
}

func loadPunishTx(db db.KVDB, channelId, commitTxId string) ([]*wire.MsgTx, error) {
	key := GetPunishTxKey(channelId, commitTxId)

	buf, err := db.Read([]byte(key))
	if err != nil {
		Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}

	result, err := decodePunishTxValue(buf)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func decodePunishTxValue(buf []byte) ([]*wire.MsgTx, error) {
	result := make([]*wire.MsgTx, 0)
	var textErr error
	parts := strings.Split(string(buf), "-")
	for _, txHex := range parts {
		tx, err := DecodeMsgTx(txHex)
		if err != nil {
			textErr = err
			break
		}
		result = append(result, tx)
	}
	if textErr == nil {
		return result, nil
	}

	var txsHex []string
	if err := DecodeFromBytes(buf, &txsHex); err == nil && len(txsHex) != 0 {
		result = result[:0]
		for _, txHex := range txsHex {
			tx, err := DecodeMsgTx(txHex)
			if err != nil {
				return nil, err
			}
			result = append(result, tx)
		}
		return result, nil
	}

	var txs []*wire.MsgTx
	if err := DecodeFromBytes(buf, &txs); err == nil && len(txs) != 0 {
		return txs, nil
	}

	tx := wire.NewMsgTx(2)
	if err := tx.Deserialize(bytes.NewReader(buf)); err == nil {
		return []*wire.MsgTx{tx}, nil
	}

	return nil, textErr
}

func deletePunishTx(db db.KVDB, channel *Channel, commitTxIds map[string]bool) error {
	batch := db.NewWriteBatch()
	if batch == nil {
		Log.Errorf("NewBatchWrite failed")
		return fmt.Errorf("NewBatchWrite failed")
	}
	defer batch.Close()

	for k := range commitTxIds {
		key := GetPunishTxKey(channel.ChannelId, k)
		err := batch.Delete([]byte(key))
		if err != nil {
			Log.Errorf("db.Remove %s failed. %v", key, err)
		}
	}
	err := batch.Flush()
	if err != nil {
		Log.Errorf("batch.Flush failed. %v", err)
		return err
	}
	return nil
}

func deleteAllPunishTxWithChannel(db db.KVDB, channelId string) ([]string, error) {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_WT_PUNISHTX + channelId + "-")
	keys, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0)
	for _, k := range keys {
		_, commitTxId, err := ParsePunishTxKey(string(k))
		if err != nil {
			Log.Errorf("ParsePunishTxKey failed. %v", err)
			return nil, err
		}
		result = append(result, commitTxId)
	}
	return result, nil
}

func loadAllCommitTxIdFromDB(db db.KVDB) map[string]string {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_WT_PUNISHTX)

	result := make(map[string]string, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		ch, id, err := ParsePunishTxKey(string(k))
		if err != nil {
			Log.Errorf("ParsePunishTxKey failed. %v", err)
			return nil
		}
		result[id] = ch
		return nil
	})
	return result
}

func GetBroadcastedCommitKey(commitTxId string) string {
	return GetDBKeyPrefix() + DB_KEY_WT_BRODCASTEDCTX + commitTxId
}

func ParseBroadcastedTxKey(key string) (string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_WT_BRODCASTEDCTX
	if !strings.HasPrefix(key, prefix) {
		return "", fmt.Errorf("not a commit tx: %s", key)
	}
	return strings.TrimPrefix(key, prefix), nil
}

func saveBroadcastedCommitTx(db db.KVDB, commitTxId string) error {
	flag := 1
	buf, err := EncodeToBytes(flag)
	if err != nil {
		Log.Errorf("saveBroadcastedCommitTx EncodeToBytes failed. %v", err)
		return err
	}

	err = db.Write([]byte(GetBroadcastedCommitKey(commitTxId)), buf)
	if err != nil {
		Log.Errorf("saveBroadcastedCommitTx failed. %v", err)
		return err
	}
	Log.Infof("saveBroadcastedCommitTx succ. %s", commitTxId)
	return nil
}

func deleteBroadcastedCommitTx(db db.KVDB, commitTxId string) error {
	return db.Delete([]byte(GetBroadcastedCommitKey(commitTxId)))
}

func loadAllBroadcastedCommitTxIdFromDB(db db.KVDB) map[string]bool {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_WT_BRODCASTEDCTX)

	result := make(map[string]bool, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		id, err := ParseBroadcastedTxKey(string(k))
		if err != nil {
			Log.Errorf("ParseBroadcastedTxKey failed. %v", err)
			return nil
		}
		result[id] = true
		return nil
	})
	return result
}

func GetUtxoToCommitTxIdMapKey(channelId, utxo string) string {
	return GetDBKeyPrefix() + DB_KEY_WT_UTXOCMAP + channelId + "-" + utxo
}

func ParseUtxoToCommitTxIdMapKey(key string) (string, string, error) {
	prefix := GetDBKeyPrefix() + DB_KEY_WT_UTXOCMAP
	if !strings.HasPrefix(key, prefix) {
		return "", "", fmt.Errorf("not a utxo to commitTxId key: %s", key)
	}
	key = strings.TrimPrefix(key, prefix)
	parts := strings.Split(key, "-")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid format: %s", key)
	}
	return parts[0], parts[1], nil
}

func saveUtxoCommitTxIdMap(db db.KVDB, channelId, utxo string, m map[string]bool) error {
	buf, err := EncodeToBytes(m)
	if err != nil {
		Log.Errorf("saveUtxoCommitTxIdMap EncodeToBytes failed. %v", err)
		return err
	}

	err = db.Write([]byte(GetUtxoToCommitTxIdMapKey(channelId, utxo)), buf)
	if err != nil {
		Log.Errorf("saveUtxoCommitTxIdMap failed. %v", err)
		return err
	}
	Log.Infof("saveUtxoCommitTxIdMap succ. %s", utxo)
	return nil
}

func loadUtxoCommitTxIdMap(db db.KVDB, channelId, utxo string) (map[string]bool, error) {
	key := GetUtxoToCommitTxIdMapKey(channelId, utxo)

	buf, err := db.Read([]byte(key))
	if err != nil {
		Log.Errorf("Read %s failed. %v", key, err)
		return nil, err
	}
	var m map[string]bool
	err = DecodeFromBytes(buf, &m)
	if err != nil {
		Log.Errorf("DecodeFromBytes %s failed. %v", key, err)
		return nil, err
	}

	return m, nil
}

func deleteUtxoCommitTxIdMap(db db.KVDB, channelId, utxo string) error {
	key := GetUtxoToCommitTxIdMapKey(channelId, utxo)
	return db.Delete([]byte(key))
}

func deleteAllUtxoDataWithChannel(db db.KVDB, channelId string) ([]string, error) {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_WT_UTXOCMAP + channelId)
	keys, err := DeleteAllKeysWithPrefix(db, prefix)
	if err != nil {
		return nil, err
	}

	utxos := make([]string, 0)
	for _, k := range keys {
		_, utxo, err := ParseUtxoToCommitTxIdMapKey(string(k))
		if err != nil {
			Log.Errorf("ParseUtxoToCommitTxIdMapKey failed. %v", err)
			return nil, err
		}
		utxos = append(utxos, utxo)
	}
	return utxos, nil
}

func loadAllUtxoCommitTxIdMap(db db.KVDB) map[string]map[string]bool {
	prefix := []byte(GetDBKeyPrefix() + DB_KEY_WT_UTXOCMAP)

	result := make(map[string]map[string]bool, 0)
	db.BatchRead(prefix, false, func(k, v []byte) error {
		id, utxo, err := ParseUtxoToCommitTxIdMapKey(string(k))
		if err != nil {
			Log.Errorf("ParseUtxoToCommitTxIdMapKey failed. %v", err)
			return nil
		}

		m, err := loadUtxoCommitTxIdMap(db, id, utxo)
		if err != nil {
			Log.Errorf("loadUtxoCommitTxIdMap failed. %v", err)
			return nil
		}
		result[utxo] = m
		return nil
	})
	return result
}
