package account

import (
	"bytes"
	"strings"
	"testing"
)

func TestManagedModuleRoundTrip(t *testing.T) {
	id := strings.Repeat("a", 64)
	module := ManagedModule{Provider: "notes", Scope: "global", Codec: "notes-v1", Payload: []byte("a private note")}
	key, err := ManagedModuleKey(id, module.Provider, module.Scope)
	if err != nil {
		t.Fatal(err)
	}
	first, err := SealManagedModule(testAccountSecret(), id, "testnet", module, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenManagedModule(testAccountSecret(), id, "testnet", key, first)
	if err != nil || got.Provider != module.Provider || got.Scope != module.Scope || got.Codec != module.Codec || !bytes.Equal(got.Payload, module.Payload) {
		t.Fatal("module round trip changed provider data", err)
	}
	module.Codec, module.Payload = "notes-v2", []byte("updated private note")
	second, err := SealManagedModule(testAccountSecret(), id, "testnet", module, nil)
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("module update did not produce a fresh envelope", err)
	}
	nextKey, err := ManagedModuleKey(id, module.Provider, module.Scope)
	if err != nil || nextKey != key {
		t.Fatal("content or codec update changed the module key", err)
	}
	got, err = OpenManagedModule(testAccountSecret(), id, "testnet", key, second)
	if err != nil || got.Codec != "notes-v2" || !bytes.Equal(got.Payload, module.Payload) {
		t.Fatal("module update was not recoverable", err)
	}
	module.Payload = nil
	empty, err := SealManagedModule(testAccountSecret(), id, "testnet", module, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = OpenManagedModule(testAccountSecret(), id, "testnet", key, empty)
	if err != nil || len(got.Payload) != 0 {
		t.Fatal("empty provider payload must not become a deletion", err)
	}
}

func TestManagedModuleContext(t *testing.T) {
	id := strings.Repeat("a", 64)
	module := ManagedModule{Provider: "rgb11", Scope: "testnet/abc/0", Codec: "rgb11-v1", Payload: []byte("provider payload")}
	key, err := ManagedModuleKey(id, module.Provider, module.Scope)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := SealManagedModule(testAccountSecret(), id, "testnet", module, nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ManagedModuleKey(id, "silentpay", module.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenManagedModule(testAccountSecret(), id, "testnet", other, sealed); err == nil {
		t.Fatal("module accepted another entry key")
	}
	if _, err := OpenManagedModule(testAccountSecret(), id, "mainnet", key, sealed); err == nil {
		t.Fatal("module accepted another network")
	}
	if _, err := OpenManagedModule(testAccountSecret(), strings.Repeat("b", 64), "testnet", key, sealed); err == nil {
		t.Fatal("module accepted another account")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := OpenManagedModule(testAccountSecret(), id, "testnet", key, sealed); err == nil {
		t.Fatal("module accepted modified ciphertext")
	}
}
