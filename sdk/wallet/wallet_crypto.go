package wallet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// deriveSharedSecret 从私钥和对方公钥派生共享密钥（返回 32 字节）
// 实现思路：使用 ECDH = 对方公钥 * 私钥 标量乘法，取 X 坐标并做 SHA256 作为最终对称密钥材料。
// 注意：该实现依赖于 secp256k1 库的 ParsePubKey / ToECDSA / PrivateKey.Serialize 等方法。
func deriveSharedSecret(priv *secp256k1.PrivateKey, pubBytes []byte) ([]byte, error) {
    if priv == nil {
        return nil, fmt.Errorf("nil private key")
    }
    if len(pubBytes) == 0 {
        return nil, fmt.Errorf("empty public key bytes")
    }

    // 解析对方公钥
    pub, err := secp256k1.ParsePubKey(pubBytes)
    if err != nil {
        return nil, fmt.Errorf("parse pubkey: %w", err)
    }

    // 转为标准库的 ecdsa.PublicKey 以便使用 ScalarMult
    pubECD := pub.ToECDSA()
    if pubECD == nil || pubECD.X == nil || pubECD.Y == nil {
        return nil, fmt.Errorf("invalid parsed public key")
    }

    // 私钥字节（大端 32 字节），用于标量乘法
    privBytes := priv.Serialize()
    if len(privBytes) == 0 {
        return nil, fmt.Errorf("invalid private key serialization")
    }

    // 标量乘法： (X, Y) = priv * pub
    x, _ := pubECD.Curve.ScalarMult(pubECD.X, pubECD.Y, privBytes)
    if x == nil {
        return nil, fmt.Errorf("scalar multiplication failed")
    }

    // 只取 X 坐标并做 SHA-256 作为共享密钥材料
    h := sha256.Sum256(x.Bytes())
    return h[:], nil
}

// EncryptTo 使用对方公钥加密明文，返回格式: nonce(12)|ciphertext
func (p *InternalWallet) EncryptTo(pubKeyBytes []byte, plaintext []byte) ([]byte, error) {
    p.mutex.Lock()
    priv := p.getPaymentPrivKey()
    p.mutex.Unlock()
    if priv == nil {
        return nil, fmt.Errorf("no payment private key available")
    }

    shared, err := deriveSharedSecret(priv, pubKeyBytes)
    if err != nil {
        return nil, err
    }
    // 派生 AES-256-GCM 密钥
    key := sha256.Sum256(shared)

    block, err := aes.NewCipher(key[:])
    if err != nil {
        return nil, err
    }
    aesgcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }
    nonce := make([]byte, aesgcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return nil, err
    }
    ciphertext := aesgcm.Seal(nil, nonce, plaintext, nil)
    out := make([]byte, 0, len(nonce)+len(ciphertext))
    out = append(out, nonce...)
    out = append(out, ciphertext...)
    return out, nil
}

// Decrypt 使用钱包当前私钥解密由 EncryptTo 产生的数据
// 输入格式：nonce(12)|ciphertext
func (p *InternalWallet) Decrypt(data []byte, pubKeyBytes []byte) ([]byte, error) {
    if len(data) == 0 {
        return nil, fmt.Errorf("empty data")
    }

    p.mutex.Lock()
    priv := p.getPaymentPrivKey()
    p.mutex.Unlock()
    if priv == nil {
        return nil, fmt.Errorf("no payment private key available")
    }

    shared, err := deriveSharedSecret(priv, pubKeyBytes)
    if err != nil {
        return nil, err
    }
    // 派生 AES-256-GCM 密钥
    key := sha256.Sum256(shared)

    block, err := aes.NewCipher(key[:])
    if err != nil {
        return nil, err
    }
    aesgcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    nonceSize := aesgcm.NonceSize()
    if len(data) < nonceSize {
        return nil, fmt.Errorf("ciphertext too short")
    }
    nonce := data[:nonceSize]
    ciphertext := data[nonceSize:]

    plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
    if err != nil {
        return nil, err
    }
    return plaintext, nil
}

