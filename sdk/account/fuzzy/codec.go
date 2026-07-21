package fuzzy

import (
	"bytes"
	"encoding/json"
	"io"
)

func MarshalVault(vault *Vault) ([]byte, error) {
	if vault == nil || vault.validate() != nil {
		return nil, ErrInvalidPublicPayload
	}
	out, err := json.Marshal(vault)
	if err != nil || len(out) > MaxPublicPayloadSize {
		return nil, ErrInvalidPublicPayload
	}
	return out, nil
}

func UnmarshalVault(data []byte) (*Vault, error) {
	if len(data) == 0 || len(data) > MaxPublicPayloadSize {
		return nil, ErrInvalidPublicPayload
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var vault Vault
	if err := decoder.Decode(&vault); err != nil {
		return nil, ErrInvalidPublicPayload
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrInvalidPublicPayload
	}
	if err := vault.validate(); err != nil {
		return nil, ErrInvalidPublicPayload
	}
	return &vault, nil
}
