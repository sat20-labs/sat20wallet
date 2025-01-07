package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func GetChainParam() *chaincfg.Params {
	if IsTestNet() {
		return &chaincfg.TestNet4Params
	} else {
		return &chaincfg.MainNetParams
	}
}

func OutpointToUtxo(out *wire.OutPoint) string {
	return out.String()
}

func UtxoToWireOutpoint(utxo string) (*wire.OutPoint, error) {
	return wire.NewOutPointFromString(utxo)
}

func UtxosToWireOutpoints(utxos []string) ([]*wire.OutPoint, error) {
	var outpoints []*wire.OutPoint
	if len(utxos) == 0 {
		return nil, fmt.Errorf("no utxos specified")
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

func EncodeMsgTx(msgTx *wire.MsgTx) (string, error) {
	var buf bytes.Buffer

	// Serialize the MsgTx into a byte buffer
	err := msgTx.Serialize(&buf)
	if err != nil {
		return "", err
	}

	// Convert the serialized byte buffer to a hexadecimal string
	return hex.EncodeToString(buf.Bytes()), nil
}

func DecodeMsgTx(txHex string) (*wire.MsgTx, error) {
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

func IsAddressInPkScript(pkScript []byte, address string) bool {
	_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, GetChainParam())
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

func AddrToPkScript(addr string, netParams *chaincfg.Params) ([]byte, error) {
	address, err := btcutil.DecodeAddress(addr, netParams)
	if err != nil {
		return nil, err
	}

	return txscript.PayToAddrScript(address)
}

func PayToPubKeyHashScript(pubKeyHash []byte) ([]byte, error) {
	return txscript.NewScriptBuilder().AddOp(txscript.OP_DUP).AddOp(txscript.OP_HASH160).
		AddData(pubKeyHash).AddOp(txscript.OP_EQUALVERIFY).AddOp(txscript.OP_CHECKSIG).
		Script()
}

func PayToWitnessPubKeyHashScript(pubKeyHash []byte) ([]byte, error) {
	return txscript.NewScriptBuilder().AddOp(txscript.OP_0).AddData(pubKeyHash).Script()
}


func PublicKeyToP2TRAddress(pubKey *btcec.PublicKey) string {
	taprootPubKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), GetChainParam())
	if err != nil {
		return ""
	}
	return addr.EncodeAddress()
}

func DecodeBtcUtilTx(encodedStr string) (*btcutil.Tx, error) {
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

func IsSameTxExceptSigs(tx1, tx2 *wire.MsgTx) bool {
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

		if out1.Value != out2.Value ||
			!bytes.Equal(out1.PkScript, out2.PkScript) {
			return false
		}
	}

	return true
}

func VerifySignedTx(tx *wire.MsgTx,
	prevFetcher txscript.PrevOutputFetcher) error {

	inValue := int64(0)
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	for i, txIn := range tx.TxIn {
		txOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		vm, err := txscript.NewEngine(txOut.PkScript, tx, i, txscript.StandardVerifyFlags,
			nil, sigHashes, txOut.Value, prevFetcher)
		if err != nil {
			Log.Errorf("Failed to create script engine for input %d: %v", i, err)
			return err
		}
		if err := vm.Execute(); err != nil {
			Log.Errorf("Failed to execute script for input %d: %v", i, err)
			return err
		}
		inValue += txOut.Value
	}

	outValue := int64(0)
	for _, txOut := range tx.TxOut {
		outValue += txOut.Value
	}

	if outValue > inValue {
		return fmt.Errorf("outvalue %d bigger than invalue %d", outValue, inValue)
	}

	return nil
}

// GetPkScriptFromAddress 根据比特币地址返回对应的锁定脚本(PkScript)
func GetPkScriptFromAddress(addr string) ([]byte, error) {
	// 解析地址，这里使用主网参数，如果是测试网需要使用&chaincfg.TestNet3Params
	address, err := btcutil.DecodeAddress(addr, GetChainParam())
	if err != nil {
		return nil, fmt.Errorf("invalid address: %v", err)
	}

	// 根据地址类型生成对应的PkScript
	return txscript.PayToAddrScript(address)
}

func IsNullDataScript(script []byte) bool {

	// A null script is of the form:
	//  OP_RETURN <optional data>
	//
	// Thus, it can either be a single OP_RETURN or an OP_RETURN followed by a
	// data push up to MaxDataCarrierSize bytes.

	// The script can't possibly be a null data script if it doesn't start
	// with OP_RETURN.  Fail fast to avoid more work below.
	if len(script) < 1 || script[0] != txscript.OP_RETURN {
		return false
	}

	// Single OP_RETURN.
	if len(script) == 1 {
		return true
	}

	// OP_RETURN followed by data push up to MaxDataCarrierSize bytes.
	tokenizer := txscript.MakeScriptTokenizer(0, script[1:])
	return tokenizer.Next() && tokenizer.Done() &&
		(txscript.IsSmallInt(tokenizer.Opcode()) || tokenizer.Opcode() <= txscript.OP_PUSHDATA4) &&
		len(tokenizer.Data()) <= txscript.MaxDataCarrierSize
}

func PrintHexTx(tx *wire.MsgTx) {
	hexTx, err := EncodeMsgTx(tx)
	if err != nil {
		Log.Warnf("EncodeMsgTx failed. %v", err)
	}
	Log.Infof("TX: %s", hexTx)
}
