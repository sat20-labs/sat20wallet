package wallet

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/wire"
)

func TestSTPRecoveryWitnessSizeSerializesEveryBranch(t *testing.T) {
	signature := bytes.Repeat([]byte{1}, 73)
	for _, length := range []int{71, 252, 253, 65535, 65536} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			script := bytes.Repeat([]byte{0x51}, length)
			for _, branch := range []struct {
				name      string
				operation STPAssetOperation
				coSigned  bool
				witness   wire.TxWitness
			}{
				{"client", STPOpForceClose, false, wire.TxWitness{signature, nil, script}},
				{"core", STPOpForceClose, true, wire.TxWitness{nil, signature, signature, nil, script}},
				{"punish-client", STPOpPunish, false, wire.TxWitness{signature, []byte{1}, script}},
				{"punish-core", STPOpPunish, true, wire.TxWitness{signature, []byte{1}, script}},
			} {
				t.Run(branch.name, func(t *testing.T) {
					got := stpRecoveryWitnessSize(script, branch.operation, branch.coSigned)
					if want := int64(branch.witness.SerializeSize()); got != want {
						t.Fatalf("native witness size = %d, want %d", got, want)
					}
				})
			}
			multisig := wire.TxWitness{nil, signature, signature, script}
			if got, want := stpMultisigWitnessSize(script), int64(multisig.SerializeSize()); got != want {
				t.Fatalf("other-sweep witness size = %d, want %d", got, want)
			}
		})
	}
}
