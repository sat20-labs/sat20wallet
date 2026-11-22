package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"sort"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/anchors"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/operations"
	corepsbt "github.com/sat20-labs/rgb11/psbt"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/rgb11/strict_types"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11SendBundle struct {
	contractID consensus.ContractID
	base       *coreconsignment.Container
	value      strict_types.Value
	commitment operations.BundleCommitment
	change     []rgb11ChangeSecret
}

func buildRGB11SendBundle(id consensus.ContractID, base *coreconsignment.Container,
	allocations []rgb11SpendAllocation, target *operations.TransitionSpec,
	changeVout uint32, change []rgb11ChangeSecret) (rgb11SendBundle, error) {
	bundle := rgb11SendBundle{contractID: id, base: base, change: change}
	if base == nil || base.ContractID != id.String() {
		return bundle, rgb11wallet.ErrInvalidProof
	}
	specs := make(map[uint16]*operations.TransitionSpec)
	if target != nil {
		specs[target.TransitionType] = target
	}
	reveals := make([]seals.GraphBlindSeal, 0, len(allocations))
	preserved := make(map[uint16]operations.TransitionOutput)
	for _, allocation := range allocations {
		opout, err := rgb11ProofOpout(allocation.proof)
		if err != nil {
			return bundle, err
		}
		reveal, err := seals.DecodeGraphBlindSeal(allocation.proof.SealDisclosure)
		if err != nil {
			return bundle, rgb11wallet.ErrInvalidProof
		}
		reveals = append(reveals, reveal)
		assignment := uint16(opout.Type)
		input := operations.TransitionInput{OperationID: opout.Operation, AssignmentType: assignment, Index: opout.Number}
		if allocation.target {
			target.Inputs = append(target.Inputs, input)
			continue
		}
		transitionType, err := rgb11PreserveType(base.GenesisReport.Kind, assignment)
		if err != nil {
			return bundle, err
		}
		spec := specs[transitionType]
		if spec == nil {
			spec = &operations.TransitionSpec{TransitionType: transitionType}
			specs[transitionType] = spec
		}
		spec.Inputs = append(spec.Inputs, input)
		amount, err := decimalUint64(&allocation.asset.Amount)
		if err != nil {
			return bundle, err
		}
		output, exists := preserved[assignment]
		if exists {
			// The projection represents one assignment for each asset on a carrier.
			if output.Class != "fungible" || allocation.proof.StateClass != output.Class ||
				!bytes.Equal(output.Data, allocation.proof.StateData) || ^uint64(0)-output.Amount < amount {
				return bundle, ErrRGB11AssetPreservation
			}
			output.Amount += amount
		} else {
			var secret rgb11ChangeSecret
			output, secret, err = newRGB11ChangeOutput(changeVout, assignment,
				allocation.proof.StateClass, amount, allocation.proof.StateData)
			if err != nil {
				return bundle, err
			}
			bundle.change = append(bundle.change, secret)
		}
		preserved[assignment] = output
	}
	if _, err := base.RevealGraphSeals(reveals); err != nil {
		return bundle, err
	}
	// Both kinds of control state use the same projection key on one carrier.
	if _, inflation := preserved[4010]; inflation {
		if _, link := preserved[4012]; link {
			return bundle, ErrRGB11AssetPreservation
		}
	}
	for assignment, output := range preserved {
		transitionType, _ := rgb11PreserveType(base.GenesisReport.Kind, assignment)
		specs[transitionType].Outputs = append(specs[transitionType].Outputs, output)
	}

	ordered := make([]int, 0, len(specs))
	for typ := range specs {
		ordered = append(ordered, int(typ))
	}
	sort.Ints(ordered)
	var values []strict_types.Value
	var commits []operations.TransitionCommitment
	nonce := uint64(time.Now().UnixNano())
	for index, typ := range ordered {
		spec := specs[uint16(typ)]
		spec.ContractID = id
		spec.Nonce = nonce + uint64(index)
		value, commit, err := operations.BuildTransition(*spec)
		if err != nil {
			return bundle, err
		}
		values = append(values, value)
		commits = append(commits, commit)
	}
	var err error
	switch len(values) {
	case 1:
		bundle.value, bundle.commitment, err = operations.BuildBundle(specs[uint16(ordered[0])].Inputs, values[0])
	case 2:
		bundle.value, bundle.commitment, err = buildRGB11TwoTransitionBundle(
			specs[uint16(ordered[0])].Inputs, values[0], commits[0],
			specs[uint16(ordered[1])].Inputs, values[1], commits[1],
		)
	default:
		return bundle, ErrRGB11AssetPreservation
	}
	return bundle, err
}

func rgb11PreserveType(kind schemas.Kind, assignment uint16) (uint16, error) {
	switch {
	case assignment == 4000:
		return 10_000, nil
	case assignment == 4010 && kind == schemas.IFA:
		return 8015, nil
	case assignment == 4012 && (kind == schemas.IFA || kind == schemas.BFA):
		return 8016, nil
	default:
		return 0, ErrRGB11AssetPreservation
	}
}

func (b rgb11SendBundle) witness(tx *wire.MsgTx, tree *anchors.MPCTree) (strict_types.Value, error) {
	proof, err := tree.Proof(b.contractID)
	if err != nil {
		return strict_types.Value{}, err
	}
	return operations.BuildOpretWitnessBundleWithTx(tx, b.value, proof)
}

func (b rgb11SendBundle) localChange(witness strict_types.Value) (rgb11wallet.LocalChange, error) {
	var change rgb11wallet.LocalChange
	terminals := make([][32]byte, 0, len(b.change))
	for _, secret := range b.change {
		terminals = append(terminals, secret.secret)
		change.Seals = append(change.Seals, secret.seal)
	}
	container, err := coreconsignment.BuildTransfer(b.base, witness, terminals)
	if err != nil {
		return change, err
	}
	armor, err := coreconsignment.EncodeArmor(container.Value)
	if err != nil {
		return change, err
	}
	change.Consignment = []byte(armor)
	return change, nil
}

func (p *rgb11Manager) checkRGB11PreparedChange(ctx context.Context, txid string, change rgb11wallet.LocalChange) error {
	validator := rgb11wallet.NewRGB11ValidatorWithReveals(change.Seals...)
	prepared, err := rgb11wallet.ValidatePreparedWith(ctx, validator, change.Consignment, p.rgbManager.evidence)
	if err != nil {
		return err
	}
	if prepared.WitnessTxIDs[0] != txid {
		return ErrRGB11Inconsistent
	}
	_, err = matchRGB11ChangeSeals(prepared.Receipt.Allocations, txid, change.Seals)
	return err
}

func buildRGB11PSBT(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	bundles []rgb11SendBundle, tree *anchors.MPCTree) (*psbt.Packet, *wire.MsgTx, []byte, error) {
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(packet.Outputs) == 0 {
		return nil, nil, nil, ErrRGB11Inconsistent
	}
	addField := func(fields *[]*psbt.Unknown, key corepsbt.ProprietaryKey, value []byte) error {
		raw, err := key.RawKey()
		if err != nil {
			return err
		}
		*fields = append(*fields, &psbt.Unknown{Key: raw, Value: value})
		return nil
	}
	if err := addField(&packet.Unknowns, corepsbt.RGBCloseMethod(), []byte{2}); err != nil {
		return nil, nil, nil, err
	}
	host := &packet.Outputs[0].Unknowns
	for _, bundle := range bundles {
		for _, transition := range bundle.commitment.Transitions {
			commit, err := operations.CommitTransition(transition)
			if err != nil {
				return nil, nil, nil, err
			}
			if err := addField(&packet.Unknowns, corepsbt.RGBTransition(commit.OperationID), transition.Encoded); err != nil {
				return nil, nil, nil, err
			}
		}
		inputMap, ok := bundle.value.Field("inputMap")
		encoded := inputMap.Unwrap().Encoded
		if !ok || len(encoded) < 2 {
			return nil, nil, nil, operations.ErrInvalidBundle
		}
		// rc.2 PSBT uses the same Opout -> OpId pairs as the bundle, without
		// the strict map's two-byte length prefix.
		if err := addField(&packet.Unknowns, corepsbt.RGBConsumedBy(bundle.contractID), encoded[2:]); err != nil {
			return nil, nil, nil, err
		}
		if err := addField(host, corepsbt.MPCMessage(bundle.contractID), bundle.commitment.BundleID[:]); err != nil {
			return nil, nil, nil, err
		}
	}
	commitment := tree.Commitment()
	for _, field := range []struct {
		key   corepsbt.ProprietaryKey
		value []byte
	}{
		{corepsbt.MPCMinTreeDepth(), []byte{anchors.MPCMinDepth}},
		{corepsbt.MPCEntropy(), binary.LittleEndian.AppendUint64(nil, tree.Entropy())},
		{corepsbt.MPCCommitment(), commitment[:]},
		{corepsbt.MPCProof(), tree.StrictBytes()},
		{corepsbt.OpretCommitment(), commitment[:]},
	} {
		if err := addField(host, field.key, field.value); err != nil {
			return nil, nil, nil, err
		}
	}
	var encoded bytes.Buffer
	if err := packet.Serialize(&encoded); err != nil {
		return nil, nil, nil, err
	}
	return packet, tx.Copy(), encoded.Bytes(), nil
}
