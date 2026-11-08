package wallet

import (
	"bytes"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func mergeAccountText(base, local, remote string) (string, error) {
	switch {
	case local == base:
		return remote, nil
	case remote == base || local == remote:
		return local, nil
	default:
		return "", dkvsindexer.ErrWriteConflict
	}
}

func mergeAccountWallet(base, local, remote *account.ManagedWallet) (*account.ManagedWallet, error) {
	equal := func(a, b *account.ManagedWallet) bool {
		return a == nil && b == nil || a != nil && b != nil && managedWalletContentMatches(*a, b)
	}
	switch {
	case equal(local, base):
		return remote, nil
	case equal(remote, base) || equal(local, remote):
		return local, nil
	}
	if base == nil || local == nil || remote == nil || local.Deleted || remote.Deleted || base.Deleted ||
		local.Type != remote.Type || !bytes.Equal(local.Secret, remote.Secret) {
		return nil, dkvsindexer.ErrWriteConflict
	}
	result := cloneManagedWallet(*remote)
	var err error
	result.Name, err = mergeAccountText(base.Name, local.Name, remote.Name)
	if err != nil {
		return nil, err
	}
	result.AccountCount = max(local.AccountCount, remote.AccountCount)
	byIndex := func(wallet *account.ManagedWallet) map[uint32]account.SubAccount {
		items := make(map[uint32]account.SubAccount)
		for _, item := range wallet.SubAccounts {
			items[item.Index] = item
		}
		return items
	}
	b, l, r := byIndex(base), byIndex(local), byIndex(remote)
	result.SubAccounts = nil
	for index := uint32(0); index < result.AccountCount; index++ {
		bv, bok := b[index]
		lv, lok := l[index]
		rv, rok := r[index]
		var item account.SubAccount
		switch {
		case !lok:
			item = rv
		case !rok:
			item = lv
		case !bok:
			if lv != rv {
				return nil, dkvsindexer.ErrWriteConflict
			}
			item = lv
		default:
			item.Index = index
			item.Name, err = mergeAccountText(bv.Name, lv.Name, rv.Name)
			if err != nil {
				return nil, err
			}
			item.DID, err = mergeAccountText(bv.DID, lv.DID, rv.DID)
			if err != nil {
				return nil, err
			}
		}
		result.SubAccounts = append(result.SubAccounts, item)
	}
	return &result, nil
}

func mergeAccountWalletPayload(base, local, remote []byte) ([]byte, error) {
	l, err := account.DecodeManagedState(local)
	if err != nil {
		return nil, err
	}
	r, err := account.DecodeManagedState(remote)
	if err != nil || r.RootFingerprint != l.RootFingerprint {
		return nil, dkvsindexer.ErrWriteConflict
	}
	b := account.ManagedState{Version: account.ManagedStateVersion, RootFingerprint: l.RootFingerprint}
	if len(base) != 0 {
		b, err = account.DecodeManagedState(base)
		if err != nil || b.RootFingerprint != l.RootFingerprint {
			return nil, dkvsindexer.ErrWriteConflict
		}
	}
	keys := make(map[string]bool)
	for _, state := range []account.ManagedState{b, l, r} {
		for _, wallet := range state.Wallets {
			keys[wallet.Fingerprint] = true
		}
	}
	result := account.ManagedState{Version: account.ManagedStateVersion, RootFingerprint: l.RootFingerprint}
	for key := range keys {
		wallet, err := mergeAccountWallet(findManagedWallet(&b, key), findManagedWallet(&l, key), findManagedWallet(&r, key))
		if err != nil {
			return nil, err
		}
		if wallet != nil {
			result.Wallets = append(result.Wallets, cloneManagedWallet(*wallet))
		}
	}
	return account.EncodeManagedState(result)
}
