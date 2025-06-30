package utils

import (
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// HexToSecp256k1PublicKey 将十六进制字符串格式的公钥转换为 secp256k1.PublicKey
func BytesToPublicKey(pubKeyBytes []byte) (*secp256k1.PublicKey, error) {
	// 检查公钥长度
	if len(pubKeyBytes) != 33 && len(pubKeyBytes) != 65 {
		return nil, fmt.Errorf("invalid public key length: %d", len(pubKeyBytes))
	}

	// 解析公钥
	pubKey, err := secp256k1.ParsePubKey(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %v", err)
	}

	return pubKey, nil
}
