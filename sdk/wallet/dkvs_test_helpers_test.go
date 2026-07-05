package wallet

import (
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
)

func dkvsTestWalletFromPriv(t *testing.T, priv *btcec.PrivateKey) *InternalWallet {
	t.Helper()
	if priv == nil {
		t.Fatal("nil private key")
	}
	w, _, err := NewInternalWalletWithPrivKey(priv.Serialize(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	return w
}
