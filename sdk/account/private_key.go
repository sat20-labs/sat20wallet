package account

import "github.com/btcsuite/btcd/btcec/v2"

func ValidatePrivateKey(value []byte) error {
	if len(value) != 32 {
		return ErrInvalidBackup
	}
	var scalar btcec.ModNScalar
	overflow := scalar.SetByteSlice(value)
	valid := !overflow && !scalar.IsZero()
	scalar.Zero()
	if !valid {
		return ErrInvalidBackup
	}
	return nil
}
