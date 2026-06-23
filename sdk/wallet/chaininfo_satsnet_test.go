package wallet

import "testing"

func TestSatsNetContractAddressPkScriptRoundTrip(t *testing.T) {
	addr := "tc1qypnktv95wv6xxzpdnm6z6jv4km4xtx3dlfamhj07gsvnaqg2xyydvgmkm9rj"

	pkScript, err := AddrToPkScript_SatsNet(addr, GetChainParam_SatsNet())
	if err != nil {
		t.Fatalf("AddrToPkScript_SatsNet failed: %v", err)
	}

	got, err := AddrFromPkScript_SatsNet(pkScript)
	if err != nil {
		t.Fatalf("AddrFromPkScript_SatsNet failed: %v", err)
	}
	if got != addr {
		t.Fatalf("unexpected address: got %s want %s", got, addr)
	}

	if !IsAddressInPkScript_SatsNet(pkScript, addr) {
		t.Fatalf("IsAddressInPkScript_SatsNet returned false")
	}
}
