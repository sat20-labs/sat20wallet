package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/btcutil/psbt"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
)

// SIGHASH_SINGLE_ANYONECANPAY 为 SIGHASH_SINGLE | SIGHASH_ANYONECANPAY
const SIGHASH_SINGLE_ANYONECANPAY = txscript.SigHashSingle | txscript.SigHashAnyOneCanPay

// UtxoInfo 定义单个挂单的 utxo 数据
type UtxoInfo struct {
	common.AssetsInUtxo
	Price     int64           `json:"Price"`       // 价格
	AssetInfo *wire.AssetInfo `json:"TargetAsset"` // 非nil时，指要卖入的资产
}
 

// parseUtxo 解析 "txid:vout" 字符串，返回 txid 与 vout
func parseUtxo(utxo string) (string, uint32, error) {
	parts := strings.Split(utxo, ":")
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid utxo format")
	}
	txid := parts[0]
	vout, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid vout: %v", err)
	}
	return txid, uint32(vout), nil
}

// buildBatchSellOrder 使用 btcd 的 psbt 构造批量挂单交易
func buildBatchSellOrder(utxos []*UtxoInfo, address, network string) (string, error) {

	var params *chaincfg.Params
	if strings.ToLower(network) == "testnet" {
		params = &chaincfg.SatsTestNetParams
	} else {
		params = &chaincfg.SatsMainNetParams
	}

	// 构造一个空的 unsigned transaction
	unsignedTx := wire.NewMsgTx(wire.TxVersion)

	// 遍历 inscriptionUtxos，每个 utxo 添加一个输入和一个输出
	for _, utxoData := range utxos {
		txidStr, vout, err := parseUtxo(utxoData.OutPoint)
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
		addr, err := btcutil.DecodeAddress(address, params)
		if err != nil {
			return "", err
		}
		pkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return "", err
		}

		var assets []wire.AssetInfo
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
	if len(packet.Inputs) != len(utxos) {
		return "", fmt.Errorf("mismatch between psbt inputs and provided meta")
	}
	for i, utxoData := range utxos {
		// 注意：WitnessUtxo 字段为 *wire.TxOut
		packet.Inputs[i].WitnessUtxo = wire.NewTxOut(utxoData.Value, utxoData.ToTxAssets(), utxoData.PkScript)
		packet.Inputs[i].SighashType = SIGHASH_SINGLE_ANYONECANPAY
	}

	// 将 PSBT 序列化为二进制，再转为 hex 字符串
	var buf bytes.Buffer
	if err := packet.Serialize(&buf); err != nil {
		return "", fmt.Errorf("failed to serialize psbt: %v", err)
	}
	psbtHex := hex.EncodeToString(buf.Bytes())

	return psbtHex, nil
}

func finalizeSellOrder(packet *psbt.Packet, utxos []*UtxoInfo,
	buyerAddress, serverAddress, network string, serviceFee, networkFee int64,
) (string, error) {
	var params *chaincfg.Params
	if strings.ToLower(network) == "testnet" {
		params = &chaincfg.SatsTestNetParams
	} else {
		params = &chaincfg.SatsMainNetParams
	}

	addr, err := btcutil.DecodeAddress(buyerAddress, params)
	if err != nil {
		return "", err
	}
	buyerPkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	addr, err = btcutil.DecodeAddress(serverAddress, params)
	if err != nil {
		return "", err
	}

	// 卖出的资产
	var sellAssets wire.TxAssets
	var sellValue int64
	for _, input := range packet.Inputs {
		sellValue += input.WitnessUtxo.Value
		sellAssets.Merge(&input.WitnessUtxo.Assets)
	}

	var buyAssets wire.TxAssets
	var buyValue int64
	for _, utxo := range utxos {
		buyValue += utxo.Value
		assets := utxo.ToTxAssets()
		buyAssets.Merge(&assets)

		txidStr, vout, err := parseUtxo(utxo.OutPoint)
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
		packet.UnsignedTx.AddTxIn(txIn)
		input := psbt.PInput{
			WitnessUtxo: &wire.TxOut{
				Value: utxo.Value,
				Assets: assets,
				PkScript: utxo.PkScript,
			},
			SighashType: txscript.SigHashDefault,
		}
		packet.Inputs = append(packet.Inputs, input)
	}

	var priceValue int64 
	var priceAssets wire.TxAssets
	for _, output := range packet.UnsignedTx.TxOut {
		priceValue += output.Value
		priceAssets.Merge(&output.Assets)
	}
	// 买家支付服务费和gas
	totalValue := sellValue + buyValue
	changeValue := totalValue - priceValue - serviceFee - networkFee
	if changeValue < 0 {
		return "", fmt.Errorf("not enough sats to pay fee %d", changeValue)
	}
	changeAsset := buyAssets.Clone()
	changeAsset.Merge(&sellAssets)
	err = changeAsset.Split(&priceAssets)
	if err != nil {
		return "", fmt.Errorf("not enough asset, %v", err)
	}
	
	if changeAsset.IsZero() {
		changeAsset = nil
	}
	
	txOut := wire.NewTxOut(changeValue, changeAsset, buyerPkScript)
	packet.UnsignedTx.AddTxOut(txOut)
	packet.Outputs = append(packet.Outputs, psbt.POutput{})

	if serviceFee != 0 {
		serverPkScript, err := txscript.PayToAddrScript(addr)
		if err != nil {
			return "", err
		}
		txOut2 := wire.NewTxOut(serviceFee, nil, serverPkScript)
		packet.UnsignedTx.AddTxOut(txOut2)
		packet.Outputs = append(packet.Outputs, psbt.POutput{})
	}

	var buf bytes.Buffer
	if err := packet.Serialize(&buf); err != nil {
		return "", fmt.Errorf("failed to serialize psbt: %v", err)
	}
	finalPsbtHex := hex.EncodeToString(buf.Bytes())

	return finalPsbtHex, nil
}


// splitBatchSignedPsbt 将一个批量签名后的 PSBT 分割成单个 PSBT。
// 参数 signedHex 为签名后的 PSBT 的 hex 字符串，network 可传 "testnet" 或 "mainnet"
func splitBatchSignedPsbt(signedHex string, network string) ([]string, error) {
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
		return nil, fmt.Errorf("input and output count mismatch in batch psbt")
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
			return nil, fmt.Errorf("psbt inputs length mismatch")
		}
		newPacket.Inputs[0] = packet.Inputs[i]

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

func addInputsToPsbt(packet *psbt.Packet, utxos []*common.AssetsInUtxo) (string, error) {
	for _, utxo := range utxos {
		assets := utxo.ToTxAssets()
		txidStr, vout, err := parseUtxo(utxo.OutPoint)
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
		packet.UnsignedTx.AddTxIn(txIn)
		input := psbt.PInput{
			WitnessUtxo: &wire.TxOut{
				Value: utxo.Value,
				Assets: assets,
				PkScript: utxo.PkScript,
			},
			SighashType: txscript.SigHashDefault,
		}
		packet.Inputs = append(packet.Inputs, input)
	}
	var buf bytes.Buffer
	if err := packet.Serialize(&buf); err != nil {
		return "", fmt.Errorf("failed to serialize psbt: %v", err)
	}
	psbtHex := hex.EncodeToString(buf.Bytes())

	return psbtHex, nil
}


func addOutputsToPsbt(packet *psbt.Packet, utxos []*common.AssetsInUtxo) (string, error) {

	for _, utxo := range utxos {
		txOut := wire.NewTxOut(utxo.Value, utxo.ToTxAssets(), utxo.PkScript)
		packet.UnsignedTx.AddTxOut(txOut)
		packet.Outputs = append(packet.Outputs, psbt.POutput{})
	}
	
	var buf bytes.Buffer
	if err := packet.Serialize(&buf); err != nil {
		return "", fmt.Errorf("failed to serialize psbt: %v", err)
	}
	finalPsbtHex := hex.EncodeToString(buf.Bytes())

	return finalPsbtHex, nil
}

func VerifySignedPsbt_SatsNet(packet *psbt.Packet, tx *wire.MsgTx) error {
	prevOutputFetcher := PsbtPrevOutputFetcher_SatsNet(packet)
	return VerifySignedTx_SatsNet(tx, prevOutputFetcher)
}
