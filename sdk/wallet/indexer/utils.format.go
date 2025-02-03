// ConvertTimestampToISO8601 将时间戳转换为 ISO 8601 格式的字符串
package indexer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func ConvertTimestampToISO8601(timestamp int64) string {
	// 将时间戳转换为 time.Time 类型
	t := time.Unix(timestamp, 0).UTC()

	// 检查时间戳是否合法
	if t.IsZero() {
		Log.Error("invalid timestamp")
		return ""
	}

	// 将时间格式化为 ISO 8601 格式的字符串
	//iso8601 := t.Format("2006-01-02T15:04:05Z")
	iso8601 := t.Format(time.RFC3339)

	return iso8601
}

func TxIdFromUtxo(utxo string) string {
	parts := strings.Split(utxo, ":")
	if len(parts) != 2 {
		return ""
	}
	return parts[0]
}

func ParseUtxo(utxo string) (txid string, vout int, err error) {
	parts := strings.Split(utxo, ":")
	if len(parts) != 2 {
		return txid, vout, fmt.Errorf("invalid utxo")
	}

	txid = parts[0]
	vout, err = strconv.Atoi(parts[1])
	if err != nil {
		return txid, vout, err
	}
	if vout < 0 {
		return txid, vout, fmt.Errorf("invalid vout")
	}
	return txid, vout, err
}

func ParseAddressIdKey(addresskey string) (addressId uint64, utxoId uint64, typ, index int64, err error) {
	parts := strings.Split(addresskey, "-")
	if len(parts) < 4 {
		return INVALID_ID, INVALID_ID, 0, 0, fmt.Errorf("invalid address key %s", addresskey)
	}
	addressId, err = strconv.ParseUint(parts[1], 16, 64)
	if err != nil {
		return INVALID_ID, INVALID_ID, 0, 0, err
	}
	utxoId, err = strconv.ParseUint(parts[2], 16, 64)
	if err != nil {
		return INVALID_ID, INVALID_ID, 0, 0, err
	}
	typ, err = strconv.ParseInt(parts[3], 16, 32)
	if err != nil {
		return INVALID_ID, INVALID_ID, 0, 0, err
	}
	index = 0
	if len(parts) > 4 {
		index, err = strconv.ParseInt(parts[4], 16, 32)
		if err != nil {
			return INVALID_ID, INVALID_ID, 0, 0, err
		}
	}
	return addressId, utxoId, typ, index, err
}

func ToUtxo(txid string, vout int) string {
	return txid+":"+strconv.Itoa(vout)
}

/*
最小交易总大小 = 82 bytes
区块大小限制：4MB (4,000,000 bytes)
理论最大交易数 = 4,000,000 / 82 ≈ 48,780 笔交易
每个输入最小大小：41 bytes (前一个输出点36 + 序列号4 + varint 1)
理论最大输入数 = (4,000,000 - 10) / 41 ≈ 97,560个
每个输出最小大小：31 bytes (value 8 + varint 1 + 最小脚本22)
理论最大输出数 = (4,000,000 - 10) / 31 ≈ 129,032个

Height: 29bit 0x1fffffff  	< 536870911
tx: 	17bit 0x1ffff 		< 131071
vout:	18bit 0x3ffff 		< 262143
*/
func ToUtxoId(height int, tx int, vout int) uint64 {
	if height > 0x1fffffff || tx > 0x1ffff || vout > 0x3ffff {
		Log.Panicf("parameters too big %x %x %x", height, tx, vout)
	}

	return (uint64(height)<<35 | uint64(tx)<<18 | uint64(vout))
}

func FromUtxoId(id uint64) (int, int, int) {
	return (int)(id >> 35), (int)((id >> 18) & 0x1ffff), (int)((id) & 0x3ffff)
}

func ParseOrdInscriptionID(inscriptionID string) (txid string, index int, err error) {
	parts := strings.Split(inscriptionID, "i")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid inscriptionID")
	}
	txid = parts[0]
	index, err = strconv.Atoi(parts[1])
	if err != nil {
		return txid, index, err
	}
	if index < 0 {
		return txid, index, fmt.Errorf("invalid index")
	}
	return txid, index, nil
}

func ParseOrdSatPoint(satPoint string) (txid string, outputIndex int, offset int64, err error) {
	parts := strings.Split(satPoint, ":")
	if len(parts) != 3 {
		return "", 0, 0, fmt.Errorf("invalid satPoint")
	}
	txid = parts[0]
	outputIndex, err = strconv.Atoi(parts[1])
	if err != nil {
		return txid, outputIndex, 0, err
	}
	if outputIndex < 0 {
		return txid, outputIndex, 0, fmt.Errorf("invalid index")
	}

	offset, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return txid, outputIndex, offset, err
	}
	return txid, outputIndex, offset, nil
}

func GenerateSeed(data interface{}) string {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(data)
	if err != nil {
		return "0"
	}

	hash := sha256.New()
	_, err = hash.Write(buf.Bytes())
	if err != nil {
		return "0"
	}
	// 获取哈希结果
	hashBytes := hash.Sum(nil)
	// 将哈希值转换为 uint64
	result := binary.LittleEndian.Uint64(hashBytes[:8])

	return fmt.Sprintf("%x", result)
}

func GenerateSeed2(ranges []*Range) string {
	bytes, err := json.Marshal(ranges)
	if err != nil {
		Log.Errorf("json.Marshal failed. %v", err)
		return "0"
	}

	//fmt.Printf("%s\n", string(bytes))

	hash := sha256.New()
	hash.Write(bytes)
	hashResult := hash.Sum(nil)
	return hex.EncodeToString(hashResult[:8])
}
