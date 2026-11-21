package wallet

import (
	"bytes"
	"encoding/binary"
	"sort"

	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/strict_types"
)

// IFA asset and control assignments on the same carrier may require
// different transitions. Commit them in one bundle so the carrier is
// consumed once and every previous assignment has exactly one consumer.
func buildRGB11TwoTransitionBundle(
	primaryInputs []operations.TransitionInput, primary strict_types.Value,
	primaryCommit operations.TransitionCommitment,
	preservedInputs []operations.TransitionInput, preserved strict_types.Value,
	preservedCommit operations.TransitionCommitment,
) (strict_types.Value, operations.BundleCommitment, error) {
	if len(primaryInputs) == 0 || len(preservedInputs) == 0 ||
		primaryCommit.OperationID == preservedCommit.OperationID {
		return strict_types.Value{}, operations.BundleCommitment{}, operations.ErrBuildTransition
	}
	type entry struct {
		input    operations.TransitionInput
		consumer [32]byte
	}
	entries := make([]entry, 0, len(primaryInputs)+len(preservedInputs))
	for _, input := range primaryInputs {
		entries = append(entries, entry{input, primaryCommit.OperationID})
	}
	for _, input := range preservedInputs {
		entries = append(entries, entry{input, preservedCommit.OperationID})
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i].input, entries[j].input
		if order := bytes.Compare(left.OperationID[:], right.OperationID[:]); order != 0 {
			return order < 0
		}
		if left.AssignmentType != right.AssignmentType {
			return left.AssignmentType < right.AssignmentType
		}
		return left.Index < right.Index
	})
	for i := 1; i < len(entries); i++ {
		if entries[i-1].input == entries[i].input {
			return strict_types.Value{}, operations.BundleCommitment{}, operations.ErrBuildTransition
		}
	}
	if len(entries) > 0xffff {
		return strict_types.Value{}, operations.BundleCommitment{}, operations.ErrBuildTransition
	}
	var encoded bytes.Buffer
	writeU16 := func(value uint16) {
		var raw [2]byte
		binary.LittleEndian.PutUint16(raw[:], value)
		encoded.Write(raw[:])
	}
	writeU16(uint16(len(entries)))
	for _, item := range entries {
		encoded.Write(item.input.OperationID[:])
		writeU16(item.input.AssignmentType)
		writeU16(item.input.Index)
		encoded.Write(item.consumer[:])
	}
	writeU16(2)
	if bytes.Compare(primaryCommit.OperationID[:], preservedCommit.OperationID[:]) < 0 {
		encoded.Write(primaryCommit.OperationID[:])
		encoded.Write(primary.Encoded)
		encoded.Write(preservedCommit.OperationID[:])
		encoded.Write(preserved.Encoded)
	} else {
		encoded.Write(preservedCommit.OperationID[:])
		encoded.Write(preserved.Encoded)
		encoded.Write(primaryCommit.OperationID[:])
		encoded.Write(primary.Encoded)
	}
	registry, err := strict_types.RC2Registry()
	if err != nil {
		return strict_types.Value{}, operations.BundleCommitment{}, err
	}
	value, err := registry.Decode("RGBCommit", "TransitionBundle", encoded.Bytes())
	if err != nil {
		return strict_types.Value{}, operations.BundleCommitment{}, err
	}
	commitment, err := operations.CommitBundle(value)
	return value, commitment, err
}
