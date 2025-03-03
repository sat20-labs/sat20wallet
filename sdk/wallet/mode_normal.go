//go:build !wasm

package wallet

import (

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/supernode"
)

func NewKVDB(dbPath string) common.KVDB {
	return supernode.NewKVDB(dbPath)
}
