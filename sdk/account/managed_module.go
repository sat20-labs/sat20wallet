package account

import (
	"bytes"
	"io"
	"strings"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

const (
	managedModuleMagic = "AMOD"
	managedModuleSeal  = "AMOE\x01"
)

// Payload remains owned by its provider. Account management neither changes
// its business fields nor adds an object revision to the encrypted value.
type ManagedModule struct {
	Provider string
	Scope    string
	Codec    string
	Payload  []byte
}

func encodeManagedModule(value ManagedModule) ([]byte, error) {
	provider, scope, err := managedNames(value.Provider, value.Scope)
	codec := strings.TrimSpace(value.Codec)
	if err != nil || !validManagedName(codec, managedCodecMax) || len(value.Payload) > ManagedObjectMaxBytes {
		return nil, ErrInvalidBackup
	}
	var buf bytes.Buffer
	e := strict.NewEncoder(&buf)
	_ = e.Raw([]byte(managedModuleMagic))
	_ = e.U8(1)
	_ = e.String(provider, 1, managedProviderMax)
	_ = e.String(scope, 1, managedScopeMax)
	_ = e.String(codec, 1, managedCodecMax)
	if err := e.Bytes(value.Payload, 0, ManagedObjectMaxBytes); err != nil {
		return nil, err
	}
	if buf.Len() > ManagedObjectMaxBytes {
		return nil, ErrInvalidBackup
	}
	return buf.Bytes(), nil
}

func decodeManagedModule(value []byte) (ManagedModule, error) {
	r := bytes.NewReader(value)
	d := strict.NewDecoder(r)
	magic, err := d.Raw(uint64(len(managedModuleMagic)))
	if err != nil || string(magic) != managedModuleMagic {
		return ManagedModule{}, ErrRecoveryFailed
	}
	version, err := d.U8()
	if err != nil || version != 1 {
		return ManagedModule{}, ErrRecoveryFailed
	}
	result := ManagedModule{}
	for _, field := range []struct {
		text *string
		max  uint64
	}{{&result.Provider, managedProviderMax}, {&result.Scope, managedScopeMax}, {&result.Codec, managedCodecMax}} {
		*field.text, err = d.String(1, field.max)
		if err != nil {
			return ManagedModule{}, ErrRecoveryFailed
		}
	}
	result.Payload, err = d.Bytes(0, ManagedObjectMaxBytes)
	if err != nil {
		return ManagedModule{}, ErrRecoveryFailed
	}
	canonical, err := encodeManagedModule(result)
	if err != nil || r.Len() != 0 || !bytes.Equal(canonical, value) {
		return ManagedModule{}, ErrRecoveryFailed
	}
	return result, nil
}

func SealManagedModule(secret []byte, accountID, network string, value ManagedModule, randomSource io.Reader) ([]byte, error) {
	key, err := ManagedModuleKey(accountID, value.Provider, value.Scope)
	if err != nil {
		return nil, err
	}
	plain, err := encodeManagedModule(value)
	if err != nil {
		return nil, err
	}
	defer zero(plain)
	return sealManagedObject(secret, accountID, network, key, "managed-module-v1", managedModuleSeal, plain, randomSource)
}

func OpenManagedModule(secret []byte, accountID, network, key string, value []byte) (ManagedModule, error) {
	plain, err := openManagedObject(secret, accountID, network, key, "managed-module-v1", managedModuleSeal, value)
	if err != nil {
		return ManagedModule{}, err
	}
	defer zero(plain)
	result, err := decodeManagedModule(plain)
	if err != nil {
		return ManagedModule{}, err
	}
	expected, err := ManagedModuleKey(accountID, result.Provider, result.Scope)
	if err != nil || expected != key {
		return ManagedModule{}, ErrRecoveryFailed
	}
	return result, nil
}
