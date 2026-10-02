package rgb11wallet

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
)

func namingTestAddress(t *testing.T, network *chaincfg.Params, value byte) string {
	t.Helper()
	address, err := btcutil.NewAddressTaproot(bytes.Repeat([]byte{value}, 32), network)
	if err != nil {
		t.Fatal(err)
	}
	return address.EncodeAddress()
}

func TestPrimaryDIDNameLengthBoundary(t *testing.T) {
	for _, name := range []string{"a", "alice", "abcdefghij", strings.Repeat("聪", 10)} {
		if err := ValidatePrimaryDIDName(name); err != nil {
			t.Errorf("valid canonical DID %q: %v", name, err)
		}
	}
	for _, name := range []string{"", "abcdefghijk", strings.Repeat("聪", 11), "Alice", "alice ", " alice", "ali ce", "a@b", "a:b", "a/b", "a\\b", "a\nb", "a\x00b", "a\u200bb", string([]byte{0xff})} {
		if err := ValidatePrimaryDIDName(name); !errors.Is(err, ErrInvalidProviderDID) {
			t.Errorf("invalid DID %q accepted: %v", name, err)
		}
	}
}

func TestProviderBindingRequiresSameGenesisAddress(t *testing.T) {
	a := namingTestAddress(t, &chaincfg.MainNetParams, 1)
	b := namingTestAddress(t, &chaincfg.MainNetParams, 2)
	if err := ValidateProviderBinding(a, a, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProviderBinding(a, b, "alice"); !errors.Is(err, ErrProviderOwnerMismatch) {
		t.Fatalf("different owner accepted: %v", err)
	}
	if err := ValidateProviderBinding(a, a, "abcdefghijk"); !errors.Is(err, ErrInvalidProviderDID) {
		t.Fatalf("overlong DID accepted: %v", err)
	}
	if err := ValidateProviderBinding("not-an-address", a, "alice"); err == nil {
		t.Fatal("invalid genesis address accepted")
	}
}

func TestRegisteredAssetNamesKeepTypeAndUseOrdinal(t *testing.T) {
	for _, tc := range []struct {
		ordinal uint64
		want    string
	}{
		{1, "rgb11:f:usdt@alice"},
		{2, "rgb11:f:usdt_2@alice"},
		{3, "rgb11:f:usdt_3@alice"},
		{math.MaxUint64, "rgb11:f:usdt_18446744073709551615@alice"},
	} {
		name, err := BuildRegisteredAssetName("USDT", indexer.ASSET_TYPE_FT, "alice", tc.ordinal)
		if err != nil || name.String() != tc.want {
			t.Errorf("ordinal %d: name=%s err=%v want=%s", tc.ordinal, name.String(), err, tc.want)
		}
	}
	a, err := BuildRegisteredAssetName("USDT", "f", "alice", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildRegisteredAssetName("USDT", "f", "bob", 1)
	if err != nil || a == b {
		t.Fatalf("different providers collided: %s %s %v", a.String(), b.String(), err)
	}
	if name, err := BuildRegisteredAssetName("ART", indexer.ASSET_TYPE_NFT, "alice", 1); err != nil || name.Type != indexer.ASSET_TYPE_NFT {
		t.Fatalf("asset type was repurposed: %+v %v", name, err)
	}
	for _, ticker := range []string{"USDT_2", "USDT_1", "USDT_0", "USDT_0002", "USDT_18446744073709551616000"} {
		if _, err := BuildRegisteredAssetName(ticker, "f", "alice", 1); !errors.Is(err, ErrReservedTickerSuffix) {
			t.Errorf("reserved base ticker %q accepted: %v", ticker, err)
		}
	}
	for _, ticker := range []string{"", " ", "USDT@alice", "rgb11:f:USDT"} {
		if _, err := BuildRegisteredAssetName(ticker, "f", "alice", 1); err == nil {
			t.Errorf("invalid base ticker %q accepted", ticker)
		}
	}
	if _, err := BuildRegisteredAssetName("USDT", "alice", "alice", 1); err == nil {
		t.Fatal("provider accepted in type field")
	}
	if _, err := BuildRegisteredAssetName("USDT", "f", "alice", 0); err == nil {
		t.Fatal("zero ordinal accepted")
	}
}

func TestLocalDisplayNameUsesAddressLastTwelve(t *testing.T) {
	for _, network := range []*chaincfg.Params{&chaincfg.MainNetParams, &chaincfg.TestNet3Params, &chaincfg.RegressionNetParams} {
		address := namingTestAddress(t, network, 3)
		want := "usdt@" + address[len(address)-12:]
		name, err := BuildLocalDisplayName("USDT", address, "")
		if err != nil || name != want {
			t.Errorf("%s: name=%q err=%v want=%q", network.Name, name, err, want)
		}
		name, err = BuildLocalDisplayName("USDT", address, "alice")
		if err != nil || name != "usdt@alice" {
			t.Errorf("qualified DID: name=%q err=%v", name, err)
		}
	}
	for _, address := range []string{"", "short", "123456789012", " bc1pinvalid"} {
		if _, err := BuildLocalDisplayName("USDT", address, ""); err == nil {
			t.Errorf("invalid address %q accepted", address)
		}
	}
	if _, err := BuildLocalDisplayName("USDT", namingTestAddress(t, &chaincfg.MainNetParams, 3), "abcdefghijk"); err == nil {
		t.Fatal("unqualified provider accepted")
	}
}

func TestLocalNamesCanCollideButContractIDsCannot(t *testing.T) {
	address := namingTestAddress(t, &chaincfg.MainNetParams, 4)
	a, err := BuildLocalDisplayName("USDT", address, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildLocalDisplayName("USDT", address, "")
	if err != nil || a != b {
		t.Fatalf("local aliases must not allocate hidden ordinals: %q %q %v", a, b, err)
	}
	first := LocalNameMetadata{ContractID: "contract-A", LocalName: a}
	second := LocalNameMetadata{ContractID: "contract-B", LocalName: b}
	if first.ContractID == second.ContractID {
		t.Fatal("local-name collision merged contract identities")
	}
}

func TestLocalRenameIsIndependentAndRegisteredNameFrozen(t *testing.T) {
	before := LocalNameMetadata{ContractID: "contract-A", LocalName: "usdt@123456789012"}
	after, err := before.Rename("usdt@alice")
	if err != nil || after.ContractID != before.ContractID || after.LocalName != "usdt@alice" || before.LocalName != "usdt@123456789012" {
		t.Fatalf("rename changed identity or source: before=%+v after=%+v err=%v", before, after, err)
	}
	registered := after
	registered.RegisteredName = "rgb11:f:usdt_2@alice"
	actual, err := registered.Rename("usdt@bob")
	if !errors.Is(err, ErrRegisteredNameFrozen) || actual != registered {
		t.Fatalf("registered name changed: %+v %v", actual, err)
	}
}
