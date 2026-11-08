package account

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestManagedIndexRoundTrip(t *testing.T) {
	id := strings.Repeat("a", 64)
	moduleKey, err := ManagedModuleKey(id, "rgb11", "testnet/abc/0")
	if err != nil {
		t.Fatal(err)
	}
	value := ManagedIndex{Version: ManagedIndexVersion, Entries: []ManagedEntry{
		{Provider: "wallets", Scope: "global", Key: "/personal/" + id + "/account/state", Codec: "wallet-state-v1"},
		{Provider: "rgb11", Scope: "testnet/abc/0", Key: moduleKey, Codec: "rgb11-v1", Deleted: true},
	}}
	want, err := NormalizeManagedIndex(id, value)
	if err != nil {
		t.Fatal(err)
	}
	first, err := SealManagedIndex(testAccountSecret(), id, "testnet", value, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := SealManagedIndex(testAccountSecret(), id, "testnet", value, nil)
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("index seal must use a fresh nonce", err)
	}
	got, err := OpenManagedIndex(testAccountSecret(), id, "testnet", first)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("index round trip changed entries", err)
	}
	key, err := ManagedIndexKey(id)
	if err != nil || key != "/personal/"+id+"/account/index" {
		t.Fatal("index entry point changed", err)
	}
	empty, err := SealManagedIndex(testAccountSecret(), id, "testnet", ManagedIndex{Version: ManagedIndexVersion}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := OpenManagedIndex(testAccountSecret(), id, "testnet", empty)
	if err != nil || len(cleared.Entries) != 0 {
		t.Fatal("empty directory must round trip", err)
	}
}

func TestManagedIndexReferences(t *testing.T) {
	id := strings.Repeat("a", 64)
	key, err := ManagedModuleKey(id, "notes", "global")
	if err != nil {
		t.Fatal(err)
	}
	entry := ManagedEntry{Provider: "notes", Scope: "global", Key: key, Codec: "notes-v1"}
	for _, bad := range []string{
		"https://example.invalid/private", "/blob/" + strings.Repeat("b", 64) + "/" + strings.Repeat("1", 64),
		"/personal/" + id + "/account/root", "/personal/" + id + "/account/index",
	} {
		t.Run(bad, func(t *testing.T) {
			changed := entry
			changed.Key = bad
			_, err := SealManagedIndex(testAccountSecret(), id, "testnet", ManagedIndex{Version: ManagedIndexVersion, Entries: []ManagedEntry{changed}}, nil)
			if err == nil {
				t.Fatal("unmanaged directory reference was accepted")
			}
		})
	}
	if _, err := NormalizeManagedIndex(id, ManagedIndex{Version: ManagedIndexVersion, Entries: []ManagedEntry{entry, entry}}); err == nil {
		t.Fatal("duplicate module identity was accepted")
	}
	for _, value := range []struct {
		name     string
		provider string
		scope    string
	}{
		{"state_provider", "notes", "global"},
		{"state_scope", "wallets", "local"},
	} {
		t.Run(value.name, func(t *testing.T) {
			changed := ManagedEntry{Provider: value.provider, Scope: value.scope,
				Key: "/personal/" + id + "/account/state", Codec: "wallet-state-v1"}
			if _, err := NormalizeManagedIndex(id, ManagedIndex{Version: ManagedIndexVersion, Entries: []ManagedEntry{changed}}); err == nil {
				t.Fatal("core state reference accepted another provider or scope")
			}
		})
	}
	for _, provider := range []string{"notes/local", "notes:local"} {
		t.Run(provider, func(t *testing.T) {
			if _, err := ManagedEntryID(provider, "global"); err == nil {
				t.Fatal("provider accepted a scope separator")
			}
		})
	}
	if _, err := ManagedEntryID("notes.local-1_test", "testnet:wallet/0"); err != nil {
		t.Fatal("valid provider and separated scope were rejected", err)
	}
	left, err := ManagedEntryID("ab", "c")
	if err != nil {
		t.Fatal(err)
	}
	right, err := ManagedEntryID("a", "bc")
	if err != nil || len(left) != 64 || len(right) != 64 || left == right {
		t.Fatal("entry identity lost field boundaries", err)
	}
}

func TestManagedIndexContext(t *testing.T) {
	id := strings.Repeat("a", 64)
	sealed, err := SealManagedIndex(testAccountSecret(), id, "testnet", ManagedIndex{Version: ManagedIndexVersion}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenManagedIndex(testAccountSecret(), id, "mainnet", sealed); err == nil {
		t.Fatal("index accepted a different network")
	}
	if _, err := OpenManagedIndex(testAccountSecret(), strings.Repeat("b", 64), "testnet", sealed); err == nil {
		t.Fatal("index accepted another account")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := OpenManagedIndex(testAccountSecret(), id, "testnet", sealed); err == nil {
		t.Fatal("index accepted modified ciphertext")
	}
}
