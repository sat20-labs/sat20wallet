package wallet

import (
	"errors"
	"testing"
)

func TestSwitchChainFailsClosedWithoutMutatingManager(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := &Manager{status: newDefaultStatus()}
	manager.status.CurrentChain = "testnet"
	for _, target := range []string{"mainnet", "testnet", "invalid"} {
		if err := manager.SwitchChain(target, "password"); !errors.Is(err, ErrInPlaceChainSwitchDisabled) {
			t.Fatalf("SwitchChain(%q) error=%v", target, err)
		}
		if _chain != "testnet" || manager.status.CurrentChain != "testnet" {
			t.Fatalf("SwitchChain(%q) mutated chain state: global=%s status=%s",
				target, _chain, manager.status.CurrentChain)
		}
	}
}
