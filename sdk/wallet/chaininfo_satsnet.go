package wallet

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satsnet_btcd/btcec"
	"github.com/sat20-labs/satsnet_btcd/btcec/schnorr"
	"github.com/sat20-labs/satsnet_btcd/btcutil"
	"github.com/sat20-labs/satsnet_btcd/chaincfg"
	"github.com/sat20-labs/satsnet_btcd/chaincfg/chainhash"
	"github.com/sat20-labs/satsnet_btcd/txscript"
	"github.com/sat20-labs/satsnet_btcd/wire"
)


func GetChainParam_SatsNet() *chaincfg.Params {
	if IsTestNet() {
		return &chaincfg.SatsTestNetParams
	} else {
		return &chaincfg.SatsMainNetParams
	}
}

func VerifySignedTx_SatsNet(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) error {

	// TODO 做资产验证
	inValue := int64(0)
	var inAssets wire.TxAssets
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	for i, txIn := range tx.TxIn {
		txOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		vm, err := txscript.NewEngine(txOut.PkScript, tx, i, txscript.StandardVerifyFlags,
			nil, sigHashes, txOut.Value, txOut.Assets, prevFetcher)
		if err != nil {
			Log.Errorf("Failed to create script engine for input %d: %v", i, err)
			return err
		}
		if err := vm.Execute(); err != nil {
			Log.Errorf("Failed to execute script for input %d: %v", i, err)
			return err
		}
		inValue += txOut.Value
		if inAssets == nil {
			if txOut.Assets != nil {
				inAssets = txOut.Assets.Clone()
			}
		} else {
			err := inAssets.Merge(&txOut.Assets)
			if err != nil {
				return err
			}
		}
	}

	outValue := int64(0)
	var outAssets wire.TxAssets
	for _, txOut := range tx.TxOut {
		outValue += txOut.Value
		if outAssets == nil {
			if txOut.Assets != nil {
				outAssets = txOut.Assets.Clone()
			}
		} else {
			err := outAssets.Merge(&txOut.Assets)
			if err != nil {
				return err
			}
		}
	}

	if outValue > inValue {
		return fmt.Errorf("outvalue %d bigger than invalue %d", outValue, inValue)
	}

	if inAssets != nil {
		err := inAssets.Split(&outAssets)
		if err != nil {
			return err
		}
	}

	return nil
}

func CalcFee_SatsNet() int64 {
	// TODO 暂时用10聪作为每一个交易的费用
	return DEFAULT_FEE_SATSNET
}

func StandardAnchorScript(fundingUtxo string, witnessScript []byte, value int64, assets wire.TxAssets) ([]byte, error) {
	assetsBuf, err := wire.SerializeTxAssets(&assets)
	if err != nil {
		return nil, err
	}

	return txscript.NewScriptBuilder().
		AddData([]byte(fundingUtxo)).
		AddData(witnessScript).
		AddInt64(int64(value)).
		AddData(assetsBuf).Script()
}

func ParseStandardAnchorScript(script []byte) (utxo string, pkScript []byte,
	value int64, assets wire.TxAssets, err error) {
	tokenizer := txscript.MakeScriptTokenizer(0, script)

	// 读取utxo
	if !tokenizer.Next() {
		return "", nil, 0, nil, fmt.Errorf("script too short: missing txid")
	}
	utxo = string(tokenizer.Data())

	// 读取pkScript
	if !tokenizer.Next() {
		return "", nil, 0, nil, fmt.Errorf("script too short: missing pkScript")
	}
	pkScript = tokenizer.Data()

	// 读取value
	if !tokenizer.Next() {
		return "", nil, 0, nil, fmt.Errorf("script too short: missing value")
	}
	value = extractScriptInt64(tokenizer.Data())

	// 读取assets
	if !tokenizer.Next() {
		return "", nil, 0, nil, fmt.Errorf("script too short: missing assets")
	}
	assetsBuf := tokenizer.Data()
	if assetsBuf != nil {
		err = wire.DeserializeTxAssets(&assets, assetsBuf)
		if err != nil {
			return "", nil, 0, nil, err
		}
	}

	return utxo, pkScript, value, assets, nil
}

// 从比特币脚本中提取int64值
func extractScriptInt64(data []byte) int64 {
	if len(data) == 0 {
		return 0
	}

	// 比特币脚本中的整数是最小化编码的
	isNegative := (data[len(data)-1] & 0x80) != 0

	buf := make([]byte, 8)
	copy(buf, data)

	if isNegative {
		buf[len(data)-1] &= 0x7f
	}

	val := int64(binary.LittleEndian.Uint64(buf))
	if isNegative {
		val = -val
	}

	return val
}

func DecodeSatsMsgTx(txHex string) (*wire.MsgTx, error) {
	// 1. 将十六进制字符串解码为字节切片
	txBytes, err := hex.DecodeString(txHex)
	if err != nil {
		return nil, fmt.Errorf("error decoding hex string: %v", err)
	}

	// 2. 创建一个新的 wire.MsgTx 对象
	msgTx := wire.NewMsgTx(wire.TxVersion)

	// 3. 从字节切片中解析交易
	err = msgTx.Deserialize(bytes.NewReader(txBytes))
	if err != nil {
		return nil, fmt.Errorf("error deserializing transaction: %v", err)
	}

	return msgTx, nil
}

func EncodeMsgTx_SatsNet(msgTx *wire.MsgTx) (string, error) {
	var buf bytes.Buffer

	// Serialize the MsgTx into a byte buffer
	err := msgTx.Serialize(&buf)
	if err != nil {
		return "", err
	}

	// Convert the serialized byte buffer to a hexadecimal string
	return hex.EncodeToString(buf.Bytes()), nil
}

func DecodeMsgTx_SatsNet(txHex string) (*wire.MsgTx, error) {
	// 1. 将十六进制字符串解码为字节切片
	txBytes, err := hex.DecodeString(txHex)
	if err != nil {
		return nil, fmt.Errorf("error decoding hex string: %v", err)
	}

	// 2. 创建一个新的 wire.MsgTx 对象
	msgTx := wire.NewMsgTx(wire.TxVersion)

	// 3. 从字节切片中解析交易
	err = msgTx.Deserialize(bytes.NewReader(txBytes))
	if err != nil {
		return nil, fmt.Errorf("error deserializing transaction: %v", err)
	}

	return msgTx, nil
}

func NewSOutPoint(tx string, index uint32) *wire.OutPoint {
	utxoHash, err := chainhash.NewHashFromStr(tx)
	if err != nil {
		Log.Errorf("Failed to create hash from TXID: %v", err)
		return nil
	}
	return wire.NewOutPoint(utxoHash, index)
}

func SignTxIn_P2WSH(tx *wire.MsgTx, index int, prevFetcher txscript.PrevOutputFetcher,
	redeemScript []byte, privKey *btcec.PrivateKey, pos int) error {

	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)

	in := tx.TxIn[index]
	out := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
	sig, err := txscript.RawTxInWitnessSignature(tx, sigHashes, index,
		out.Value, out.Assets,
		redeemScript, txscript.SigHashAll, privKey)
	if err != nil {
		return fmt.Errorf("failed to create signature A: %v", err)
	}

	if in.Witness == nil {
		in.Witness = wire.TxWitness{nil, nil, nil, redeemScript}
	}
	in.Witness[pos+1] = sig

	return nil
}

func SignTxIn_P2TR(tx *wire.MsgTx, index int, privKey *btcec.PrivateKey,
	prevFetcher txscript.PrevOutputFetcher) error {
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	pubkey := privKey.PubKey()

	txIn := tx.TxIn[index]

	prevPkScript, err := txscript.PayToTaprootScript(pubkey)
	if err != nil {
		return err
	}

	txOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
	witness, err := txscript.TaprootWitnessSignature(tx, sigHashes, index,
		txOut.Value, txOut.Assets, prevPkScript,
		txscript.SigHashDefault, privKey)
	if err != nil {
		return err
	}
	txIn.Witness = witness

	return nil
}

// 本地pubkey放前面
func GetCurrSignPosition2(aPub, bPub []byte) int {
	if bytes.Compare(aPub, bPub) == 1 {
		return 1
	}
	return 0
}

func GetP2WSHscript(a, b []byte) ([]byte, []byte, error) {
	// 根据闪电网络的规则，小的公钥放前面
	witnessScript, err := utils.GenMultiSigScript(a, b)
	if err != nil {
		return nil, nil, err
	}

	pkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, nil, err
	}

	return witnessScript, pkScript, nil
}

func GetP2WSHwitnessScript(pubKeyA, pubKeyB *btcec.PublicKey) ([]byte, error) {
	return utils.GenMultiSigScript(pubKeyA.SerializeCompressed(), pubKeyB.SerializeCompressed())
}

func GetP2WSHpkScript(redeemScript []byte) ([]byte, error) {
	return utils.WitnessScriptHash(redeemScript)
}

func GetP2TRpkScript(pubKey *btcec.PublicKey) ([]byte, error) {
	taprootAddr, err := publicKeyToTaprootAddress(pubKey)
	if err != nil {
		return nil, err
	}
	return txscript.PayToAddrScript(taprootAddr)
}

func GetP2WSHaddress(a, b []byte) (string, error) {
	// 根据闪电网络的规则，小的公钥放前面
	witnessScript, err := utils.GenMultiSigScript(a, b)
	if err != nil {
		return "", err
	}

	pkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return "", err
	}

	_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}

	if len(addresses) == 0 {
		return "", fmt.Errorf("can't generate p2wsh address")
	}

	return addresses[0].EncodeAddress(), nil
}

func publicKeyToTaprootAddress(pubKey *btcec.PublicKey) (*btcutil.AddressTaproot, error) {
	taprootPubKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	return btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), GetChainParam_SatsNet())
}

func IsOpReturn(txOut *wire.TxOut) bool {
	// 解析输出脚本
	pkScript := txOut.PkScript
	scriptClass, _, _, err := txscript.ExtractPkScriptAddrs(pkScript, nil)
	if err != nil {
		return false
	}

	return scriptClass == txscript.NullDataTy
}

func GetOpReturnOutput(txOut *wire.TxOut) (bool, []byte) {
	// 解析输出脚本
	pkScript := txOut.PkScript
	scriptClass, _, _, err := txscript.ExtractPkScriptAddrs(pkScript, nil)
	if err != nil {
		return false, nil
	}

	// 检查脚本类型是否为 OP_RETURN
	if scriptClass == txscript.NullDataTy {
		// 提取 OP_RETURN 数据
		data, err := txscript.PushedData(pkScript)
		if err != nil || len(data) == 0 {
			return true, nil
		}
		return true, data[0]
	}

	return false, nil
}

func IsZeroPoint(point *wire.OutPoint) bool {
	zero := wire.OutPoint{}
	return zero.Hash.IsEqual(&(point.Hash))
}

func EncodeTxToString(tx *btcutil.Tx) (string, error) {
	var buf bytes.Buffer
	err := tx.MsgTx().Serialize(&buf) // Serialize the MsgTx into a byte buffer
	if err != nil {
		return "", err
	}

	// Convert the serialized byte buffer to a hexadecimal string
	return hex.EncodeToString(buf.Bytes()), nil
}

func GetPkScriptType_SatsNet(prevOutScript []byte) txscript.ScriptClass {
	ty, _, _, err := txscript.ExtractPkScriptAddrs(prevOutScript, GetChainParam_SatsNet())
	if err != nil {
		return txscript.WitnessUnknownTy
	}
	return ty

	// // P2TR (Pay-to-Taproot)
	// if len(prevOutScript) == 34 && prevOutScript[0] == 0x51 && prevOutScript[1] == 0x20 {
	// 	return "P2TR"
	// }

	// // P2WPKH (Pay-to-Witness-Public-Key-Hash)
	// if len(prevOutScript) == 22 && prevOutScript[0] == 0x00 && prevOutScript[1] == 0x14 {
	// 	return "P2WPKH"
	// }

	// // P2WSH (Pay-to-Witness-Script-Hash)
	// if len(prevOutScript) == 34 && prevOutScript[0] == 0x00 && prevOutScript[1] == 0x20 {
	// 	return "P2WSH"
	// }

	// // P2PKH (Pay-to-Public-Key-Hash)
	// if len(prevOutScript) == 25 && prevOutScript[0] == 0x76 && prevOutScript[1] == 0xa9 &&
	// 	prevOutScript[2] == 0x14 && prevOutScript[23] == 0x88 && prevOutScript[24] == 0xac {
	// 	return "P2PKH"
	// }

	// // P2SH (Pay-to-Script-Hash)
	// if len(prevOutScript) == 23 && prevOutScript[0] == 0xa9 && prevOutScript[1] == 0x14 &&
	// 	prevOutScript[22] == 0x87 {
	// 	return "P2SH"
	// }

	// return "Unknown"
}

func IsAddressInPkScript_SatsNet(pkScript []byte, address string) bool {
	_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, GetChainParam_SatsNet())
	if err != nil {
		return false
	}

	for _, addr := range addresses {
		if addr.EncodeAddress() == address {
			return true
		}
	}

	return false
}

func PublicKeyToP2TRAddress_SatsNet(pubKey *btcec.PublicKey) string {
	taprootPubKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), GetChainParam_SatsNet())
	if err != nil {
		return ""
	}
	return addr.EncodeAddress()
}

func UtxosToWireOutpoints_SatsNet(utxos []string) ([]*wire.OutPoint, error) {
	var outpoints []*wire.OutPoint
	if len(utxos) == 0 {
		return nil, nil
	}
	for _, utxo := range utxos {
		outpoint, err := wire.NewOutPointFromString(utxo)
		if err != nil {
			return nil, err
		}
		outpoints = append(outpoints, outpoint)
	}

	return outpoints, nil
}

func IsSameTxExceptSigs_SatsNet(tx1, tx2 *wire.MsgTx) bool {
	// 1. 比较基本属性
	if tx1.Version != tx2.Version ||
		tx1.LockTime != tx2.LockTime ||
		len(tx1.TxIn) != len(tx2.TxIn) ||
		len(tx1.TxOut) != len(tx2.TxOut) {
		return false
	}

	// 2. 比较输入(忽略签名脚本和见证数据)
	for i := range tx1.TxIn {
		in1 := tx1.TxIn[i]
		in2 := tx2.TxIn[i]

		// 比较前一个交易的引用
		if in1.PreviousOutPoint != in2.PreviousOutPoint {
			return false
		}

		// 比较序列号
		if in1.Sequence != in2.Sequence {
			return false
		}
		// 注意：这里不比较 SignatureScript 和 Witness
	}

	// 3. 比较输出
	for i := range tx1.TxOut {
		out1 := tx1.TxOut[i]
		out2 := tx2.TxOut[i]

		if len(out1.Assets) > 0 {
			if out2.Assets == nil {
				return false
			}
			if !out1.Assets.Equal(&out2.Assets) {
				return false
			}
		}

		if out1.Value != out2.Value ||
			!bytes.Equal(out1.PkScript, out2.PkScript) {
			return false
		}
	}

	return true
}

func DecodeBtcUtilTx_SatsNet(encodedStr string) (*btcutil.Tx, error) {
	// Convert the hex string back to bytes
	txBytes, err := hex.DecodeString(encodedStr)
	if err != nil {
		return nil, err
	}

	// Create a buffer from the byte slice
	buf := bytes.NewBuffer(txBytes)

	// Create an empty MsgTx to deserialize into
	msgTx := wire.MsgTx{}

	// Deserialize the bytes into the MsgTx
	err = msgTx.Deserialize(buf)
	if err != nil {
		return nil, err
	}

	// Wrap the deserialized MsgTx into a btcutil.Tx and return
	return btcutil.NewTx(&msgTx), nil
}

// GetPkScriptFromAddress 根据比特币地址返回对应的锁定脚本(PkScript)
func GetPkScriptFromAddress_SatsNet(addr string) ([]byte, error) {
	// 解析地址，这里使用主网参数，如果是测试网需要使用&chaincfg.TestNet3Params
	address, err := btcutil.DecodeAddress(addr, GetChainParam_SatsNet())
	if err != nil {
		return nil, fmt.Errorf("invalid address: %v", err)
	}

	// 根据地址类型生成对应的PkScript
	return txscript.PayToAddrScript(address)
}

func UtxoToWireOutpoint_SatsNet(utxo string) (*wire.OutPoint, error) {
	return wire.NewOutPointFromString(utxo)
}

func ConvertMsgTx_SatsNet(tx *wire.MsgTx) *MsgTx {
	if tx == nil {
		return nil
	}

	msg := &MsgTx{
		Version:  tx.Version,
		LockTime: tx.LockTime,
	}
	msg.TxIn = make([]*TxIn, 0)
	for _, in := range tx.TxIn {
		txin := &TxIn{
			PreviousOutPoint: OutPoint{Hash: in.PreviousOutPoint.Hash.String(), Index: in.PreviousOutPoint.Index},
			SignatureScript:  hex.EncodeToString(in.SignatureScript),
			Sequence:         in.Sequence,
		}
		txin.Witness = make([]string, 0)
		for _, w := range in.Witness {
			txin.Witness = append(txin.Witness, hex.EncodeToString(w))
		}

		msg.TxIn = append(msg.TxIn, txin)
	}
	msg.TxOut = make([]*TxOut, 0)
	for _, out := range tx.TxOut {
		txout := &TxOut{
			Value:    out.Value,
			PkScript: hex.EncodeToString(out.PkScript),
			Assets:   out.Assets,
		}
		msg.TxOut = append(msg.TxOut, txout)
	}

	return msg
}

func PrintHexTx_SatsNet(tx *wire.MsgTx) {
	hexTx, err := EncodeMsgTx_SatsNet(tx)
	if err != nil {
		Log.Warnf("EncodeMsgTx_SatsNet failed. %v", err)
	}
	Log.Infof("TX: %s", hexTx)
}

func PrintJsonTx_SatsNet(tx *wire.MsgTx, name string) {
	jsonTx := ConvertMsgTx_SatsNet(tx)
	b, _ := json.Marshal(jsonTx)
	Log.Infof("L2 %s TX: %s", name, string(b))
}
