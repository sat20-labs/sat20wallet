package wallet

import (
	"crypto/sha256"
	"testing"

	"github.com/sat20-labs/rgb11/baid64"
)

func testRGB11ConsignmentID(t testing.TB, seed string) string {
	t.Helper()
	payload := sha256.Sum256([]byte(seed))
	id, err := baid64.Encode32(payload, baid64.ConsignmentIDOptions())
	if err != nil {
		t.Fatalf("encode RGB11 consignment ID: %v", err)
	}
	return id
}
