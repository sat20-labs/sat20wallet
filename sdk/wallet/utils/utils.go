package utils

import (
	"encoding/hex"
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

func ParsePubkey(parsedPubKey string) (*secp256k1.PublicKey, error) {
	
	// Decode the hex pubkey to get the raw compressed pubkey bytes.
	pubKeyBytes, err := hex.DecodeString(parsedPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid address "+
			"pubkey: %w", err)
	}

	// The compressed pubkey should have a length of exactly 33 bytes.
	if len(pubKeyBytes) != 33 {
		return nil, fmt.Errorf("invalid address pubkey: "+
			"length must be 33 bytes, found %d", len(pubKeyBytes))
	}

	// Parse the pubkey bytes to verify that it corresponds to valid public
	// key on the secp256k1 curve.
	pubKey, err := BytesToPublicKey(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid address "+
			"pubkey: %w", err)
	}

	return pubKey, nil
}

func RemoveIndex[T any](slice []T, index int) []T {
    return append(slice[:index], slice[index+1:]...)
}
