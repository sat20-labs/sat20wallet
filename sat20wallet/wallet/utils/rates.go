package utils

import (
	"fmt"

	"github.com/btcsuite/btcd/blockchain"
)

const (
	// FeePerKwFloor is the lowest fee rate in sat/kw that we should use for
	// estimating transaction fees before signing.
	FeePerKwFloor SatPerKWeight = 253

	// AbsoluteFeePerKwFloor is the lowest fee rate in sat/kw of a
	// transaction that we should ever _create_. This is the equivalent
	// of 1 sat/byte in sat/kw.
	AbsoluteFeePerKwFloor SatPerKWeight = 250
)

// SatPerVByte represents a fee rate in sat/vbyte.
type SatPerVByte int64

// FeePerKWeight converts the current fee rate from sat/vb to sat/kw.
func (s SatPerVByte) FeePerKWeight() SatPerKWeight {
	return SatPerKWeight(s * 1000 / blockchain.WitnessScaleFactor)
}

// FeePerKVByte converts the current fee rate from sat/vb to sat/kvb.
func (s SatPerVByte) FeePerKVByte() SatPerKVByte {
	return SatPerKVByte(s * 1000)
}

// String returns a human-readable string of the fee rate.
func (s SatPerVByte) String() string {
	return fmt.Sprintf("%v sat/vb", int64(s))
}

// SatPerKVByte represents a fee rate in sat/kb.
type SatPerKVByte int64

// FeeForVSize calculates the fee resulting from this fee rate and the given
// vsize in vbytes.
func (s SatPerKVByte) FeeForVSize(vbytes int64) int64 {
	return int64(s) * int64(vbytes) / 1000
}

// FeePerKWeight converts the current fee rate from sat/kb to sat/kw.
func (s SatPerKVByte) FeePerKWeight() SatPerKWeight {
	return SatPerKWeight(s / blockchain.WitnessScaleFactor)
}

// String returns a human-readable string of the fee rate.
func (s SatPerKVByte) String() string {
	return fmt.Sprintf("%v sat/kvb", int64(s))
}

// SatPerKWeight represents a fee rate in sat/kw.
type SatPerKWeight int64

// NewSatPerKWeight creates a new fee rate in sat/kw.
func NewSatPerKWeight(fee int64, wu int64) SatPerKWeight {
	return SatPerKWeight(float64(fee)*(1000 / float64(wu)))
}

// FeeForWeight calculates the fee resulting from this fee rate and the given
// weight in weight units (wu).
func (s SatPerKWeight) FeeForWeight(wu int64) int64 {
	// The resulting fee is rounded down, as specified in BOLT#03.
	return int64(s) * int64(wu) / 1000
}

// FeeForVByte calculates the fee resulting from this fee rate and the given
// size in vbytes (vb).
func (s SatPerKWeight) FeeForVByte(vb int64) int64 {
	return s.FeePerKVByte().FeeForVSize(vb)
}

// FeePerKVByte converts the current fee rate from sat/kw to sat/kb.
func (s SatPerKWeight) FeePerKVByte() SatPerKVByte {
	return SatPerKVByte(s * blockchain.WitnessScaleFactor)
}

// FeePerVByte converts the current fee rate from sat/kw to sat/vb.
func (s SatPerKWeight) FeePerVByte() SatPerVByte {
	return SatPerVByte(s * blockchain.WitnessScaleFactor / 1000)
}

// String returns a human-readable string of the fee rate.
func (s SatPerKWeight) String() string {
	return fmt.Sprintf("%v sat/kw", int64(s))
}
