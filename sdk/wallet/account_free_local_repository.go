package wallet

import (
	"context"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type FreeLocalAccountDKVSRepository struct {
	store         *dkvsStore
	owner         common.Wallet
	recordOptions dkvsindexer.RecordOptions
	accountID     string
}

func NewFreeLocalAccountDKVSRepository(store *dkvsStore, owner common.Wallet,
	options dkvsindexer.RecordOptions) (*FreeLocalAccountDKVSRepository, error) {
	if store == nil || owner == nil {
		return nil, fmt.Errorf("DKVS store and owner wallet are required")
	}
	accountID, err := dkvsAccountID(owner)
	if err != nil {
		return nil, err
	}
	if options.TTL == 0 {
		return nil, fmt.Errorf("free-local account storage requires a TTL")
	}
	return &FreeLocalAccountDKVSRepository{
		store: store, owner: owner, recordOptions: options, accountID: accountID,
	}, nil
}

func (r *FreeLocalAccountDKVSRepository) AccountID() string { return r.accountID }

func (r *FreeLocalAccountDKVSRepository) SaveRecoveryPackage(_ context.Context,
	value account.RecoveryPackage) error {

	if err := r.assertLocator(value.Envelope.Locator); err != nil {
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
	key, err := dkvsindexer.PersonalKey(pubKey,
		accountPackagePath(value.Envelope.Locator.PackageID))
	if err != nil {
		return err
	}
	_, err = r.store.Put(dkvsValueMutation{
		Key: key, Value: encoded, Owner: r.owner, Signature: dkvsSignatureAccount,
		Policy: dkvsStoragePolicy{
			TTL: r.recordOptions.TTL, ExpiryHeight: r.recordOptions.ExpiryHeight,
			FreeLocal: true,
		},
	})
	return err
}

func (r *FreeLocalAccountDKVSRepository) LoadRecoveryPackage(_ context.Context,
	locator account.Locator) (*account.RecoveryPackage, error) {

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
	result, err := account.DecodeRecoveryPackageStorage(record.Value)
	if err != nil {
		return nil, err
	}
	if result.Envelope.Locator != locator {
		return nil, account.ErrInvalidRecoveryPackage
	}
	return result, nil
}

func (r *FreeLocalAccountDKVSRepository) assertLocator(locator account.Locator) error {
	if err := account.ValidateLocator(locator); err != nil {
		return err
	}
	if locator.AccountID != r.accountID {
		return fmt.Errorf("locator does not belong to repository account")
	}
	return nil
}
