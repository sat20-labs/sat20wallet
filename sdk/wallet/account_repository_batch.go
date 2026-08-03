package wallet

import (
	"context"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// SaveRecoveryPackage persists the immutable recovery material as one compact
// DKVS value. Guardian delivery remains a separate mailbox operation.
func (r *AccountDKVSRepository) SaveRecoveryPackage(_ context.Context,
	value account.RecoveryPackage) error {

	if r == nil || r.store == nil || r.owner == nil {
		return fmt.Errorf("writable account DKVS repository is required")
	}
	locator := value.Envelope.Locator
	if err := r.assertLocator(locator); err != nil {
		return err
	}
	encoded, err := account.EncodeRecoveryPackageStorage(value)
	if err != nil {
		return err
	}
	pubKey, err := dkvsWalletPubKey(r.owner)
	if err != nil {
		return err
	}
	key, err := dkvsindexer.PersonalKey(pubKey, accountPackagePath(locator.PackageID))
	if err != nil {
		return err
	}
	_, err = r.store.Put(dkvsValueMutation{
		Key: key, Value: encoded, Owner: r.owner, Signature: dkvsSignatureAccount,
		Policy: dkvsStoragePolicy{
			TTL: r.recordOptions.TTL, ExpiryHeight: r.recordOptions.ExpiryHeight,
			Autopay: &r.autopay,
		},
	})
	return err
}

func (r *AccountDKVSRepository) LoadRecoveryPackage(_ context.Context,
	locator account.Locator) (*account.RecoveryPackage, error) {

	if r == nil || r.store == nil {
		return nil, fmt.Errorf("account DKVS repository is required")
	}
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	pubKey, err := dkvsindexer.AccountPubKey(r.accountID)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.PersonalKey(pubKey, accountPackagePath(locator.PackageID))
	if err != nil {
		return nil, err
	}
	record, err := r.store.Get(key)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	result, err := account.DecodeRecoveryPackageStorage(record.Value)
	if err != nil {
		return nil, err
	}
	if result.Envelope.Locator != locator {
		return nil, account.ErrInvalidRecoveryPackage
	}
	return result, nil
}
