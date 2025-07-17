package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"

	"github.com/btcsuite/btcd/btcutil/psbt"
	indexer "github.com/sat20-labs/indexer/common"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)

// 不需要解锁钱包就可以使用的接口


func ExtractTxFromPsbt(psbtStr string) (string, error) {
	hexBytes, err := hex.DecodeString(psbtStr)
	if err != nil {
		return "", err
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		return "", err
	}

	err = psbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		return "", err
	}

	finalTx, err := psbt.Extract(packet)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		return "", err
	}

	// 验证下tx是否正确签名
	prevOutputFetcher := PsbtPrevOutputFetcher(packet)
	err = VerifySignedTx(finalTx, prevOutputFetcher)
	if err != nil {
		return "", err
	}

	return EncodeMsgTx(finalTx)
}

func ExtractTxFromPsbt_SatsNet(psbtStr string) (string, error) {
	hexBytes, err := hex.DecodeString(psbtStr)
	if err != nil {
		return "", err
	}
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		return "", err
	}

	err = spsbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		return "", err
	}

	finalTx, err := spsbt.Extract(packet)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		return "", err
	}

	// 验证下tx是否正确签名
	prevOutputFetcher := PsbtPrevOutputFetcher_SatsNet(packet)
	err = VerifySignedTx_SatsNet(finalTx, prevOutputFetcher)
	if err != nil {
		return "", err
	}

	return EncodeMsgTx_SatsNet(finalTx)
}

func ExtractUnsignedTxFromPsbt(psbtStr string) (string, error) {
	hexBytes, err := hex.DecodeString(psbtStr)
	if err != nil {
		return "", err
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		return "", err
	}

	return EncodeMsgTx(packet.UnsignedTx)
}

func ExtractUnsignedTxFromPsbt_SatsNet(psbtStr string) (string, error) {
	hexBytes, err := hex.DecodeString(psbtStr)
	if err != nil {
		return "", err
	}
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		return "", err
	}

	return EncodeMsgTx_SatsNet(packet.UnsignedTx)
}

func BuildBatchSellOrder_SatsNet(utxos []string, address, network string) (string, error) {
	utxosInfo := make([]*UtxoInfo, 0, len(utxos))
	for _, utxo := range utxos {
		var info UtxoInfo
		err := json.Unmarshal([]byte(utxo), &info)
		if err != nil {
			return "", err
		}
		utxosInfo = append(utxosInfo, &info)
	}

	return buildBatchSellOrder_SatsNet(utxosInfo, address, network)
}

func SplitBatchSignedPsbt_SatsNet(signedHex string, network string) ([]string, error) {
	return splitBatchSignedPsbt_SatsNet(signedHex, network)
}

func MergeBatchSignedPsbt_SatsNet(signedHex []string, network string) (string, error) {
	return mergeBatchSignedPsbt_SatsNet(signedHex, network)
}

func FinalizeSellOrder_SatsNet(psbtHex string, utxos []string, buyerAddress, serverAddress, network string,
	serviceFee, networkFee int64) (string, error) {
	utxosInfo := make([]*UtxoInfo, 0, len(utxos))
	for _, utxo := range utxos {
		var info UtxoInfo
		err := json.Unmarshal([]byte(utxo), &info)
		if err != nil {
			return "", err
		}
		utxosInfo = append(utxosInfo, &info)
	}

	hexBytes, err := hex.DecodeString(psbtHex)
	if err != nil {
		return "", err
	}
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		Log.Errorf("NewFromRawBytes failed, %v", err)
		return "", err
	}

	return finalizeSellOrder_SatsNet(packet, utxosInfo, buyerAddress,
		serverAddress, network, serviceFee, networkFee)
}

func AddInputsToPsbt_SatsNet(psbtHex string, utxos []string) (string, error) {
	utxosInfo := make([]*indexer.AssetsInUtxo, 0, len(utxos))
	for _, utxo := range utxos {
		var info indexer.AssetsInUtxo
		err := json.Unmarshal([]byte(utxo), &info)
		if err != nil {
			return "", err
		}
		utxosInfo = append(utxosInfo, &info)
	}

	hexBytes, err := hex.DecodeString(psbtHex)
	if err != nil {
		return "", err
	}
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		Log.Errorf("NewFromRawBytes failed, %v", err)
		return "", err
	}
	return addInputsToPsbt_SatsNet(packet, utxosInfo)
}

func AddOutputsToPsbt_SatsNet(psbtHex string, utxos []string) (string, error) {
	utxosInfo := make([]*indexer.AssetsInUtxo, 0, len(utxos))
	for _, utxo := range utxos {
		var info indexer.AssetsInUtxo
		err := json.Unmarshal([]byte(utxo), &info)
		if err != nil {
			return "", err
		}
		utxosInfo = append(utxosInfo, &info)
	}

	hexBytes, err := hex.DecodeString(psbtHex)
	if err != nil {
		return "", err
	}
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		Log.Errorf("NewFromRawBytes failed, %v", err)
		return "", err
	}
	return addOutputsToPsbt_SatsNet(packet, utxosInfo)
}
