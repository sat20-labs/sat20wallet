package wallet

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

func newRGB11ReservationID(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
