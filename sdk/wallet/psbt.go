package wallet


import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg"             // 网络参数
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"    // 交易哈希
	"github.com/sat20-labs/satoshinet/txscript"              // 脚本构造及 sighash 常量
	"github.com/sat20-labs/satoshinet/wire"                  // 交易构造
	"github.com/sat20-labs/satoshinet/btcutil"               // 地址解析
	"github.com/sat20-labs/satoshinet/btcutil/psbt"          // PSBT 工具
)

// SIGHASH_SINGLE_ANYONECANPAY 为 SIGHASH_SINGLE | SIGHASH_ANYONECANPAY
const SIGHASH_SINGLE_ANYONECANPAY = txscript.SigHashSingle | txscript.SigHashAnyOneCanPay

// BatchSellOrderProps 定义批量挂单参数
type BatchSellOrderProps struct {
	Utxos 		[]SellUtxoInfo 	// 每个挂单的 utxo 与价格信息
	Network     string          // "testnet" 或 "mainnet"
	Address     string          // 输出地址
}

// SellUtxoInfo 定义单个挂单的 utxo 数据
type SellUtxoInfo struct {
	Utxo  string  			 // 格式 "txid:vout"
	TxOut wire.TxOut		 // utxo的资产信息
	Price int64   			 // 价格
	AssetInfo *wire.AssetInfo // 不指定时，直接使用输入的TxOut的资产信息
}

// parseUtxo 解析 "txid:vout" 字符串，返回 txid 与 vout
func parseUtxo(utxo string) (string, uint32, error) {
	parts := strings.Split(utxo, ":")
	if len(parts) != 2 {
		return "", 0, errors.New("invalid utxo format")
	}
	txid := parts[0]
	vout, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid vout: %v", err)
	}
	return txid, uint32(vout), nil
}

// ParseTransactionFromHex 解析交易原始 hex 为 wire.MsgTx 对象
func ParseTransactionFromHex(rawHex string) (*wire.MsgTx, error) {
	rawBytes, err := hex.DecodeString(rawHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex: %v", err)
	}
	var msgTx wire.MsgTx
	if err := msgTx.Deserialize(bytes.NewReader(rawBytes)); err != nil {
		return nil, fmt.Errorf("failed to deserialize transaction: %v", err)
	}
	return &msgTx, nil
}

// BuildBatchSellOrder 使用 btcd 的 psbt 构造批量挂单交易，功能与 JS 代码一致
func BuildBatchSellOrder(props BatchSellOrderProps) (string, error) {
	fmt.Println("build batch sell order params:", props.Utxos, props.Network, props.Address)

	// 选择网络参数
	var params *chaincfg.Params
	if strings.ToLower(props.Network) == "testnet" {
		params = &chaincfg.SatsTestNetParams
	} else {
		params = &chaincfg.SatsMainNetParams
	}

	// 构造一个空的 unsigned transaction
	unsignedTx := wire.NewMsgTx(wire.TxVersion)

	// 遍历 inscriptionUtxos，每个 utxo 添加一个输入和一个输出
	for _, utxoData := range props.Utxos {
		fmt.Println("processing utxo:", utxoData.Utxo, "price:", utxoData.Price)
		txidStr, vout, err := parseUtxo(utxoData.Utxo)
		if err != nil {
			return "", err
		}

		// 创建输入：构造 OutPoint
		hash, err := chainhash.NewHashFromStr(txidStr)
		if err != nil {
			return "", err
		}
		outPoint := wire.NewOutPoint(hash, vout)
		txIn := wire.NewTxIn(outPoint, nil, nil)
		unsignedTx.AddTxIn(txIn)

		
		// 将地址解析成 PkScript
		addr, err := btcutil.DecodeAddress(props.Address, params)
		if err != nil {
			return "", err
		}
		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return "", err
		}

		assets := utxoData.TxOut.Assets
		if utxoData.AssetInfo != nil {
			assets = []wire.AssetInfo{*utxoData.AssetInfo}
		}

		txOut := wire.NewTxOut(utxoData.Price, assets, pkScript)
		unsignedTx.AddTxOut(txOut)
	}

	// 根据 unsignedTx 构造 PSBT
	packet, err := psbt.NewFromUnsignedTx(unsignedTx)
	if err != nil {
		return "", fmt.Errorf("failed to create psbt: %v", err)
	}

	// 对每个输入，设置 witness utxo 与 sighash 类型
	// psbt.Packet.Inputs 的顺序与 unsignedTx.TxIn 顺序一致
	if len(packet.Inputs) != len(props.Utxos) {
		return "", errors.New("mismatch between psbt inputs and provided meta")
	}
	for i, utxoData := range props.Utxos {
		// 注意：WitnessUtxo 字段为 *wire.TxOut
		packet.Inputs[i].WitnessUtxo = &utxoData.TxOut
		packet.Inputs[i].SighashType = SIGHASH_SINGLE_ANYONECANPAY
	}

	// 将 PSBT 序列化为二进制，再转为 hex 字符串
	var buf bytes.Buffer
	if err := packet.Serialize(&buf); err != nil {
		return "", fmt.Errorf("failed to serialize psbt: %v", err)
	}
	psbtHex := hex.EncodeToString(buf.Bytes())

	fmt.Println("constructed psbt:", psbtHex)
	return psbtHex, nil
}

// SplitBatchSignedPsbt 将一个批量签名后的 PSBT 分割成单个 PSBT。
// 参数 signedHex 为签名后的 PSBT 的 hex 字符串，network 可传 "testnet" 或 "mainnet"
func SplitBatchSignedPsbt(signedHex string, network string) ([]string, error) {
	fmt.Println("split batch signed psbt", signedHex)

	// 将 hex 字符串解码为字节
	rawBytes, err := hex.DecodeString(signedHex)
	if err != nil {
		return nil, fmt.Errorf("failed to decode signedHex: %v", err)
	}

	// 反序列化 PSBT 数据包
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(rawBytes), false)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize psbt: %v", err)
	}

	// 获取输入数量，同时确保输入和输出数量一致（批量 PSBT 中每个输入对应一个输出）
	inputCount := len(packet.UnsignedTx.TxIn)
	if inputCount != len(packet.UnsignedTx.TxOut) {
		return nil, errors.New("input and output count mismatch in batch psbt")
	}

	newPsbts := make([]string, 0, inputCount)
	// 遍历每个输入，将其与对应的输出单独打包为新的 PSBT
	for i := 0; i < inputCount; i++ {
		// 构造一个只包含单个输入和输出的 unsigned transaction
		newTx := wire.NewMsgTx(packet.UnsignedTx.Version)
		newTx.AddTxIn(packet.UnsignedTx.TxIn[i])
		newTx.AddTxOut(packet.UnsignedTx.TxOut[i])

		newPacket, err := psbt.NewFromUnsignedTx(newTx)
		if err != nil {
			return nil, fmt.Errorf("failed to create new psbt: %v", err)
		}

		// 从原 PSBT 中复制对应输入的 witnessUtxo 与 finalScriptWitness 数据到新 PSBT 的第 0 个输入
		if i >= len(packet.Inputs) {
			return nil, errors.New("psbt inputs length mismatch")
		}
		newPacket.Inputs[0].WitnessUtxo = packet.Inputs[i].WitnessUtxo
		newPacket.Inputs[0].FinalScriptWitness = packet.Inputs[i].FinalScriptWitness

		// 将新 PSBT 序列化为二进制后转为 hex 字符串
		var buf bytes.Buffer
		if err := newPacket.Serialize(&buf); err != nil {
			return nil, fmt.Errorf("failed to serialize new psbt: %v", err)
		}
		newPsbtHex := hex.EncodeToString(buf.Bytes())
		newPsbts = append(newPsbts, newPsbtHex)
	}
	return newPsbts, nil
}

