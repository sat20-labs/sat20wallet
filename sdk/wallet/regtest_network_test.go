package wallet

import (
	"bytes"
	btcutil "github.com/btcsuite/btcd/btcutil"
	satsbtcutil "github.com/sat20-labs/satoshinet/btcutil"
	"strings"
	"testing"
)

func TestRegressionL1AndL2AddressParametersAgree(t *testing.T) {
	previous := _chain
	t.Cleanup(func() { _chain = previous })
	for _, network := range []struct{ chain, hrp string }{{"mainnet", "bc1"}, {"testnet", "tb1"}, {"regtest", "bcrt1"}} {
		_chain = network.chain
		witness := bytes.Repeat([]byte{9}, 32)
		l1, err := btcutil.NewAddressTaproot(witness, GetChainParam())
		if err != nil {
			t.Fatal(err)
		}
		l2, err := satsbtcutil.NewAddressTaproot(witness, GetChainParam_SatsNet())
		if err != nil {
			t.Fatal(err)
		}
		if l1.EncodeAddress() != l2.EncodeAddress() || !strings.HasPrefix(l2.EncodeAddress(), network.hrp) {
			t.Fatalf("%s L1/L2 mismatch %s / %s", network.chain, l1.EncodeAddress(), l2.EncodeAddress())
		}
		if _, err := satsbtcutil.DecodeAddress(l1.EncodeAddress(), GetChainParam_SatsNet()); err != nil {
			t.Fatal(err)
		}
		l1channel, err := btcutil.NewAddressWitnessScriptHash(witness, GetChainParam())
		if err != nil {
			t.Fatal(err)
		}
		l2channel, err := satsbtcutil.NewAddressWitnessScriptHash(witness, GetChainParam_SatsNet())
		if err != nil || l1channel.EncodeAddress() != l2channel.EncodeAddress() {
			t.Fatalf("%s channel address mismatch: %v", network.chain, err)
		}
	}
}
