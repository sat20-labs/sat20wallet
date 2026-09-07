package wallet

import "github.com/sat20-labs/satoshinet/chaincfg/chainhash"

func chainHashFromETag(value string) (*chainhash.Hash, error) {
	return chainhash.NewHashFromStr(value)
}
