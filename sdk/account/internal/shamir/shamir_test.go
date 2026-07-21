package shamir

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestAnyTwoOfThree(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	shares, err := Split(secret, 3, 2, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]int{{0, 1}, {0, 2}, {1, 2}} {
		got, err := Combine([][]byte{shares[pair[0]], shares[pair[1]]})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, secret) {
			t.Fatalf("pair %v mismatch", pair)
		}
	}
	if _, err := Combine([][]byte{shares[0], shares[0]}); err == nil {
		t.Fatal("duplicate share accepted")
	}
}
