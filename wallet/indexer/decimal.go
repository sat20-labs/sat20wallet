package indexer

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/sat20-labs/sat20wallet/wallet/utils"
	"lukechampine.com/uint128"
)

var Log = utils.Log

const MAX_PRECISION = 18
const DEFAULT_PRECISION = 0

var MAX_PRECISION_STRING = "18"

var precisionFactor [64]*big.Int = [64]*big.Int{
	new(big.Int).Exp(big.NewInt(10), big.NewInt(0), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(1), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(2), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(3), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(4), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(5), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(6), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(7), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(8), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(9), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(11), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(13), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(14), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(16), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(17), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(19), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(21), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(22), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(23), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(26), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(27), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(28), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(29), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(31), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(32), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(33), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(34), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(35), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(36), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(37), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(39), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(40), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(41), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(42), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(43), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(44), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(45), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(46), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(47), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(48), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(49), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(50), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(51), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(52), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(53), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(54), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(55), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(56), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(57), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(58), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(59), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(60), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(61), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(62), nil),
	new(big.Int).Exp(big.NewInt(10), big.NewInt(63), nil),
}

// Decimal represents a fixed-point decimal number with 18 decimal places
type Decimal struct {
	Precition int
	Value     *big.Int
}

func NewDefaultDecimal(v int64) *Decimal {
	return &Decimal{Precition: DEFAULT_PRECISION, Value: new(big.Int).SetInt64(v)}
}

func NewDecimal(v int64, p int) *Decimal {
	if p > MAX_PRECISION {
		p = MAX_PRECISION
	}
	return &Decimal{Precition: p, Value: new(big.Int).SetInt64(v)}
}

func NewDecimalCopy(other *Decimal) *Decimal {
	if other == nil {
		return nil
	}
	return &Decimal{Precition: other.Precition, Value: new(big.Int).Set(other.Value)}
}

// NewDecimalFromString creates a Decimal instance from a string
func NewDecimalFromString(s string, maxPrecision int) (*Decimal, error) {
	if s == "" {
		return nil, errors.New("empty string")
	}

	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid decimal format: %s", s)
	}

	integerPartStr := parts[0]
	if integerPartStr == "" || integerPartStr[0] == '+' {
		return nil, errors.New("empty integer")
	}

	integerPart, ok := new(big.Int).SetString(parts[0], 10)
	if !ok {
		return nil, fmt.Errorf("invalid integer format: %s", parts[0])
	}

	currPrecision := 0
	decimalPart := big.NewInt(0)
	if len(parts) == 2 {
		decimalPartStr := parts[1]
		if decimalPartStr == "" || decimalPartStr[0] == '-' || decimalPartStr[0] == '+' {
			return nil, errors.New("empty decimal")
		}

		currPrecision = len(decimalPartStr)
		if currPrecision > maxPrecision {
			return nil, fmt.Errorf("decimal exceeds maximum precition: %s", s)
		}
		n := maxPrecision - currPrecision
		for i := 0; i < n; i++ {
			decimalPartStr += "0"
		}
		decimalPart, ok = new(big.Int).SetString(decimalPartStr, 10)
		if !ok || decimalPart.Sign() < 0 {
			return nil, fmt.Errorf("invalid decimal format: %s", parts[0])
		}
	}

	value := new(big.Int).Mul(integerPart, precisionFactor[maxPrecision])
	if value.Sign() < 0 {
		value = value.Sub(value, decimalPart)
	} else {
		value = value.Add(value, decimalPart)
	}

	return &Decimal{Precition: int(maxPrecision), Value: value}, nil
}

// String returns the string representation of a Decimal instance
func (d *Decimal) String() string {
	if d == nil {
		return "0"
	}
	value := new(big.Int).Abs(d.Value)
	quotient, remainder := new(big.Int).QuoRem(value, precisionFactor[d.Precition], new(big.Int))
	sign := ""
	if d.Value.Sign() < 0 {
		sign = "-"
	}
	if remainder.Sign() == 0 {
		return fmt.Sprintf("%s%s", sign, quotient.String())
	}
	decimalPart := fmt.Sprintf("%0*d", d.Precition, remainder)
	decimalPart = strings.TrimRight(decimalPart, "0")
	return fmt.Sprintf("%s%s.%s", sign, quotient.String(), decimalPart)
}

// Add adds two Decimal instances and returns a new Decimal instance
func (d *Decimal) Add(other *Decimal) *Decimal {
	if d == nil && other == nil {
		return nil
	}
	if other == nil {
		value := new(big.Int).Set(d.Value)
		return &Decimal{Precition: d.Precition, Value: value}
	}
	if d == nil {
		value := new(big.Int).Set(other.Value)
		return &Decimal{Precition: other.Precition, Value: value}
	}
	if d.Precition != other.Precition {
		Log.Panic("precition not match")
	}
	value := new(big.Int).Add(d.Value, other.Value)
	return &Decimal{Precition: d.Precition, Value: value}
}

// Sub subtracts two Decimal instances and returns a new Decimal instance
func (d *Decimal) Sub(other *Decimal) *Decimal {
	if d == nil && other == nil {
		return nil
	}
	if other == nil {
		value := new(big.Int).Set(d.Value)
		return &Decimal{Precition: d.Precition, Value: value}
	}
	if d == nil {
		value := new(big.Int).Neg(other.Value)
		return &Decimal{Precition: other.Precition, Value: value}
	}
	if d.Precition != other.Precition {
		Log.Panicf("precition not match, (%d != %d)", d.Precition, other.Precition)
	}
	value := new(big.Int).Sub(d.Value, other.Value)
	return &Decimal{Precition: d.Precition, Value: value}
}

// Mul muls two Decimal instances and returns a new Decimal instance
func (d *Decimal) Mul(other *big.Int) *Decimal {
	if d == nil || other == nil {
		return nil
	}
	value := new(big.Int).Mul(d.Value, other)
	return &Decimal{Precition: d.Precition, Value: value}
}

// Div divs two Decimal instances and returns a new Decimal instance
func (d *Decimal) Div(other *big.Int) *Decimal {
	if d == nil || other == nil {
		return nil
	}
	value := new(big.Int).Div(d.Value, other)
	return &Decimal{Precition: d.Precition, Value: value}
}

func (d *Decimal) Cmp(other *Decimal) int {
	if d == nil && other == nil {
		return 0
	}
	if other == nil {
		return d.Value.Sign()
	}
	if d == nil {
		return -other.Value.Sign()
	}
	if d.Precition != other.Precition {
		Log.Panicf("precition not match, (%d != %d)", d.Precition, other.Precition)
	}
	return d.Value.Cmp(other.Value)
}

func (d *Decimal) CmpAlign(other *Decimal) int {
	if d == nil && other == nil {
		return 0
	}
	if other == nil {
		return d.Value.Sign()
	}
	if d == nil {
		return -other.Value.Sign()
	}
	return d.Value.Cmp(other.Value)
}

func (d *Decimal) Sign() int {
	if d == nil {
		return 0
	}
	return d.Value.Sign()
}

func (d *Decimal) IsOverflowInt64() bool {
	if d == nil {
		return false
	}

	integerPart := new(big.Int).SetInt64(math.MaxInt64)
	value := new(big.Int).Mul(integerPart, precisionFactor[d.Precition])
	return d.Value.Cmp(value) > 0
}

func (d *Decimal) GetMaxInt64() *Decimal {
	if d == nil {
		return nil
	}
	integerPart := new(big.Int).SetInt64(math.MaxInt64)
	value := new(big.Int).Mul(integerPart, precisionFactor[d.Precition])
	return &Decimal{Precition: d.Precition, Value: value}
}

func (d *Decimal) Float64() float64 {
	if d == nil {
		return 0
	}
	value := new(big.Int).Abs(d.Value)
	quotient, remainder := new(big.Int).QuoRem(value, precisionFactor[d.Precition], new(big.Int))
	decimalPart := float64(remainder.Int64()) / float64(precisionFactor[d.Precition].Int64())
	result := float64(quotient.Int64()) + decimalPart
	if d.Value.Sign() < 0 {
		return -result
	}
	return result
}

func (d *Decimal) IntegerPart() int64 {
	if d == nil {
		return 0
	}
	value := new(big.Int).Abs(d.Value)
	quotient, _ := new(big.Int).QuoRem(value, precisionFactor[d.Precition], new(big.Int))
	return quotient.Int64()
}

func (d *Decimal) ToInt64WithMax(max *Decimal) int64 {
	if d == nil {
		return 0
	}

	if d.Cmp(max) > 0 {
		Log.Panic("ToInt64WithMax overflow")
	}

	if !max.IsOverflowInt64() {
		return d.Value.Int64()
	}

	quotient, _ := new(big.Int).QuoRem(max.Value, big.NewInt(math.MaxInt64), new(big.Int))
	scaleIndex := decimalDigits(quotient.Uint64())
	return d.Div(big.NewInt(int64(scaleIndex))).Value.Int64()
}

func NewDecimalFromInt64WithMax(value int64, max *Decimal) (*Decimal) {

	if !max.IsOverflowInt64() {
		return NewDecimal(value, max.Precition)
	}

	quotient, _ := new(big.Int).QuoRem(max.Value, big.NewInt(math.MaxInt64), new(big.Int))
	scaleIndex := decimalDigits(quotient.Uint64())

	result := NewDecimal(value, max.Precition)
	return result.Mul(big.NewInt(int64(scaleIndex)))
}

func NewDecimalFromUint128(n uint128.Uint128, precition int) *Decimal {
	value := new(big.Int).SetUint64(n.Lo)
	value = value.Add(value, new(big.Int).SetUint64(n.Hi).Lsh(new(big.Int).SetUint64(n.Hi), 64))
	return &Decimal{Precition: precition, Value: value}
}

func (d *Decimal) ToUint128() uint128.Uint128 {
	if d == nil {
		return uint128.Uint128{}
	}
	lo := d.Value.Uint64()
	hi := d.Value.Rsh(d.Value, 64).Uint64()
	return uint128.Uint128{Lo: lo, Hi: hi}
}

func decimalDigits(n uint64) int {
	return int(math.Floor(math.Log10(float64(n))) + 1)
}

func Uint128ToInt64(supply, amt uint128.Uint128) int64 {
	if supply.Hi == 0 {
		return amt.Big().Int64()
	} 

	q, _ := supply.QuoRem64(math.MaxInt64)
	scaleIndex := decimalDigits(q.Lo)

	return int64(amt.Div64(precisionFactor[scaleIndex].Uint64()).Lo)
}

func Int64ToUint128(supply uint128.Uint128, amt int64) uint128.Uint128 {
	if supply.Hi == 0 {
		return uint128.From64(uint64(amt))
	} 

	q, _ := supply.QuoRem64(math.MaxInt64)
	scaleIndex := decimalDigits(q.Lo)
	result := uint128.From64(uint64(amt))
	return result.Mul64(precisionFactor[scaleIndex].Uint64())
}
