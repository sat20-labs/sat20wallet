//go:build js && wasm

package main

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

// Only the PWA upgrade uses this codec. Keep ciphertext and non-catalog
// records in their original format.
func preparePwa038(_ js.Value, args []js.Value) any {
	if len(args) != 1 || args[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "Invalid 0.1.38 migration input")
	}
	var input struct {
		Status  string
		Target  string
		Wallets map[string]string
		Catalog []struct {
			ID       string
			Name     string
			Accounts []struct {
				Index uint32
				Name  string
			}
		}
	}
	invalid := func() any { return createJsRet(nil, -1, "Invalid 0.1.38 wallet data") }
	if err := json.Unmarshal([]byte(args[0].String()), &input); err != nil {
		return invalid()
	}
	decode := func(value string, target any) bool {
		raw, err := base64.StdEncoding.DecodeString(value)
		return err == nil && wallet.DecodeFromBytes(raw, target) == nil
	}
	if input.Target != "" {
		var target wallet.Status
		if !decode(input.Target, &target) || target.TotalWallet != 0 ||
			target.CurrentWallet != 0 || target.CurrentAccount != 0 {
			return createJsRet(nil, 0, "Destination already in use")
		}
	}
	var status wallet.Status
	// The published PWA bundle used SDK 0.2.9. Its referenced source can
	// also be built locally with SDK 0.2.8 and the same on-disk format.
	if !decode(input.Status, &status) || (status.SoftwareVer != "0.2.8" && status.SoftwareVer != "0.2.9") ||
		status.DBver != "0.0.1" || (status.CurrentChain != "mainnet" && status.CurrentChain != "testnet") ||
		status.CurrentWallet <= 0 || status.CurrentAccount >= account.MaxManagedStateItems ||
		len(input.Wallets) == 0 || len(input.Wallets) > account.MaxManagedStateItems {
		return invalid()
	}
	records := make(map[string]*wallet.WalletInDB, len(input.Wallets))
	for id, value := range input.Wallets {
		var record wallet.WalletInDB
		if !decode(value, &record) || record.Id <= 0 || strconv.FormatInt(record.Id, 10) != id ||
			len(record.Mnemonic) == 0 || len(record.Salt) == 0 ||
			record.Accounts < 1 || record.Accounts > account.MaxManagedStateItems ||
			(record.Type != wallet.WALLET_TYPE_MNEMONIC && record.Type != wallet.WALLET_TYPE_PRIVKEY) ||
			record.Name != "" || record.AccountNames != nil || record.AccountDIDs != nil || record.SPEnabled != nil {
			return invalid()
		}
		records[id] = &record
	}
	selected := records[strconv.FormatInt(status.CurrentWallet, 10)]
	if selected == nil {
		return invalid()
	}
	selected.Accounts = max(selected.Accounts, int(status.CurrentAccount)+1)
	seen := make(map[string]bool, len(input.Catalog))
	for _, item := range input.Catalog {
		record := records[item.ID]
		if record == nil {
			continue
		}
		if seen[item.ID] {
			return invalid()
		}
		seen[item.ID] = true
		record.Name = strings.TrimSpace(item.Name)
		record.AccountNames = make(map[uint32]string, len(item.Accounts))
		for _, sub := range item.Accounts {
			if _, duplicate := record.AccountNames[sub.Index]; duplicate || sub.Index >= account.MaxManagedStateItems {
				return invalid()
			}
			record.AccountNames[sub.Index] = strings.TrimSpace(sub.Name)
			record.Accounts = max(record.Accounts, int(sub.Index)+1)
		}
	}
	result := make(map[string]any, len(records))
	for id, record := range records {
		if record.Type == wallet.WALLET_TYPE_PRIVKEY && record.Accounts != 1 {
			return invalid()
		}
		encoded, err := wallet.EncodeToBytes(record)
		if err != nil {
			return invalid()
		}
		result[wallet.DB_KEY_WALLET+id] = base64.StdEncoding.EncodeToString(encoded)
	}
	return createJsRet(map[string]any{"wallets": result, "network": status.CurrentChain}, 0, "ok")
}

func upgradePwa038Password(_ js.Value, args []js.Value) any {
	if len(args) != 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "Invalid 0.1.38 password upgrade input")
	}
	oldPassword, password := args[0].String(), args[1].String()
	handler := createAsyncJsHandler(func() (any, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		if err := _mgr.UpgradePwa038Password(oldPassword, password); err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}
