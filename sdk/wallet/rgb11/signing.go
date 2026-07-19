package rgb11wallet

import (
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type WalletSigner struct {
	Wallet common.Wallet
}

func (s WalletSigner) Sign(message []byte) ([]byte, error) {
	return s.Wallet.SignMessage(message)
}

func WalletPubKey(wallet common.Wallet) []byte {
	if wallet == nil || wallet.GetPubKey() == nil {
		return nil
	}
	return wallet.GetPubKey().SerializeCompressed()
}

func VerifyWalletSignature(pubKey, message, signature []byte) bool {
	key, err := btcec.ParsePubKey(pubKey)
	if err != nil {
		return false
	}
	sig, err := ecdsa.ParseDERSignature(signature)
	if err != nil {
		return false
	}
	return sig.Verify(chainhash.HashB(message), key)
}
