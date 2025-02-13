//go:build wasm

package wallet

import (

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/lightnode"
)

func NewKVDB(_ string) common.KVDB {
	return lightnode.NewKVDB()
}
