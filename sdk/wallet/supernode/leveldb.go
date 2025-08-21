//go:build !wasm

package supernode

import (
	"github.com/sat20-labs/indexer/common"
	db "github.com/sat20-labs/indexer/indexer/db"
)

func NewKVDB(path string) common.KVDB {
	return db.NewKVDB(path)
}
