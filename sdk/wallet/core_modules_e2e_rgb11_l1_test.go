package wallet

import (
	"fmt"
	"sync"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

var coreRGB11NameOwners sync.Map

func coreSetRGB11NameOwner(chain *coreE2EChain, name, address string) {
	if chain == nil {
		return
	}
	ownersAny, _ := coreRGB11NameOwners.LoadOrStore(chain, &sync.Map{})
	ownersAny.(*sync.Map).Store(name, address)
}

func (p *coreE2EL1Indexer) GetNameInfo(name string) (*indexerwire.OrdinalsName, error) {
	if p == nil || p.chain == nil {
		return nil, fmt.Errorf("controlled L1 indexer is unavailable")
	}
	ownersAny, ok := coreRGB11NameOwners.Load(p.chain)
	if !ok {
		return nil, fmt.Errorf("controlled name %s not found", name)
	}
	addressAny, ok := ownersAny.(*sync.Map).Load(name)
	if !ok || addressAny.(string) == "" {
		return nil, fmt.Errorf("controlled name %s not found", name)
	}
	return &indexerwire.OrdinalsName{
		NftItem: indexerwire.NftItem{Name: name, Address: addressAny.(string)},
	}, nil
}
