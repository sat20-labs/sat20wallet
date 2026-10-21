package wallet

import (
	"bytes"
	"encoding/hex"
	satsbtcutil "github.com/sat20-labs/satoshinet/btcutil"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	evm "github.com/sat20-labs/satoshinet/contract/evm"
	"github.com/sat20-labs/satoshinet/txscript"
	"testing"
)

func TestEVMCallerAndDeployAddressMatchNodeNetwork(t *testing.T) {
	previous := _chain
	t.Cleanup(func() { _chain = previous })
	for _, chain := range []string{"mainnet", "testnet", "regtest"} {
		t.Run(chain, func(t *testing.T) {
			_chain = chain
			address, err := satsbtcutil.NewAddressTaproot(bytes.Repeat([]byte{9}, 32), GetChainParam_SatsNet())
			if err != nil {
				t.Fatal(err)
			}
			script, err := txscript.PayToAddrScript(address)
			if err != nil {
				t.Fatal(err)
			}
			caller, err := (&Manager{}).evmCallerFromPreviousPkScript(script)
			if err != nil {
				t.Fatal(err)
			}
			expected := evm.EVMAddressFromAddressString(address.EncodeAddress())
			want := "0x" + hex.EncodeToString(expected[:])
			if caller != want {
				t.Errorf("SDK caller=%s; canonical %s address=%s gives caller=%s", caller, chain, address.EncodeAddress(), want)
			}
			sdkCaller, err := contractcommon.ParseEVMAddressHex(caller)
			if err != nil {
				t.Fatal(err)
			}
			nodeCaller, err := contractcommon.ParseEVMAddressHex(want)
			if err != nil {
				t.Fatal(err)
			}
			prefix := (&Manager{}).contractAddressPrefix()
			sdkContract, err := contractcommon.DeriveEVMCreateContractAddress(prefix, sdkCaller, 42)
			if err != nil {
				t.Fatal(err)
			}
			nodeContract, err := contractcommon.DeriveEVMCreateContractAddress(prefix, nodeCaller, 42)
			if err != nil {
				t.Fatal(err)
			}
			if sdkContract.EncodeAddress() != nodeContract.EncodeAddress() {
				t.Errorf("SDK deploy=%s; node expected=%s", sdkContract.EncodeAddress(), nodeContract.EncodeAddress())
			}
		})
	}
}
