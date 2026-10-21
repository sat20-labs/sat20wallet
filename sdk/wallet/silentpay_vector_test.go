package wallet

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
)

// Official BIP352 vector: "Simple send: two inputs", published at
// bitcoin/bips/bip-0352/send_and_receive_test_vectors.json.
// This independently fixes v0 address encoding, parity and the checksum.
func TestSilentPayBIP352OfficialAddressVector(t *testing.T) {
	const encoded = "sp1qqgste7k9hx0qftg6qmwlkqtwuy6cycyavzmzj85c6qdfhjdpdjtdgqjuexzk6murw56suy3e0rd2cgqvycxttddwsvgxe2usfpxumr70xc9pkqwv"
	const scan = "0220bcfac5b99e04ad1a06ddfb016ee13582609d60b6291e98d01a9bc9a16c96d4"
	const spend = "025cc9856d6f8375350e123978daac200c260cb5b5ae83106cab90484dcd8fcf36"
	addr, err := DecodeSilentPayAddress(encoded, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("official address rejected: %v", err)
	}
	sc, _ := hex.DecodeString(scan)
	sp, _ := hex.DecodeString(spend)
	if !bytes.Equal(addr.Scan.SerializeCompressed(), sc) || !bytes.Equal(addr.Spend.SerializeCompressed(), sp) {
		t.Fatal("official BIP352 scan/spend public keys mismatch")
	}
	got, err := EncodeSilentPayAddress(addr, &chaincfg.MainNetParams)
	if err != nil || got != encoded {
		t.Fatalf("official encoding mismatch: %s %v", got, err)
	}
	for name, value := range map[string]string{
		"mixed-case":           encoded[:3] + strings.ToUpper(encoded[3:10]) + encoded[10:],
		"foreign-network":      "t" + encoded,
		"noncanonical-payload": encoded + "q",
		"bad-checksum":         encoded[:len(encoded)-1] + "!",
	} {
		if _, err := DecodeSilentPayAddress(value, &chaincfg.MainNetParams); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}
