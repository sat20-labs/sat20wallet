package wallet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type FreeLocalAccountDKVSRepository struct {
	client        *SatsNetDKVSClient
	owner         common.Wallet
	recordOptions dkvsindexer.RecordOptions
	accountID     string
}

func NewFreeLocalAccountDKVSRepository(client *SatsNetDKVSClient, owner common.Wallet,
	options dkvsindexer.RecordOptions) (*FreeLocalAccountDKVSRepository, error) {
	if client == nil || owner == nil {
		return nil, fmt.Errorf("DKVS client and owner wallet are required")
	}
	accountID, err := dkvsAccountID(owner)
	if err != nil {
		return nil, err
	}
	if options.TTL == 0 {
		return nil, fmt.Errorf("free-local account storage requires a TTL")
	}
	return &FreeLocalAccountDKVSRepository{
		client: client, owner: owner, recordOptions: options, accountID: accountID,
	}, nil
}

func (r *FreeLocalAccountDKVSRepository) AccountID() string { return r.accountID }

func (r *FreeLocalAccountDKVSRepository) putJSON(path string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > account.MaxRecoveryObjectSize {
		return fmt.Errorf("account recovery object exceeds DKVS value limit")
	}
	_, err = r.client.PutAccountPersonalRecordWithFreeLocal(r.owner, path, encoded, r.recordOptions)
	return err
}

func (r *FreeLocalAccountDKVSRepository) getJSON(path string, target any) error {
	pubKey, err := dkvsindexer.AccountPubKey(r.accountID)
	if err != nil {
		return err
	}
	key, err := dkvsindexer.PersonalKey(pubKey, path)
	if err != nil {
		return err
	}
	record, err := r.client.GetVerifiedRecord(key, dkvsindexer.RecordVerificationOptions{ExpectedKey: key})
	if err != nil {
		return err
	}
	if record == nil || len(record.Value) == 0 || len(record.Value) > account.MaxRecoveryObjectSize {
		return fmt.Errorf("invalid account recovery object")
	}
	return json.Unmarshal(record.Value, target)
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

func (r *FreeLocalAccountDKVSRepository) SaveEnvelope(_ context.Context, value account.Envelope) error {
	if err := r.assertLocator(value.Locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(value.Locator.PackageID, "envelope"), value)
}

func (r *FreeLocalAccountDKVSRepository) SaveDKVSShareCapsule(_ context.Context, locator account.Locator,
	value account.DKVSShareCapsule) error {
	if err := r.assertLocator(locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(locator.PackageID, "share/dkvs"), value)
}

func (r *FreeLocalAccountDKVSRepository) SaveKnowledgeBundle(_ context.Context, locator account.Locator,
	value account.KnowledgeRecoveryBundle) error {
	if err := r.assertLocator(locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(locator.PackageID, "questions"), value)
}

func (r *FreeLocalAccountDKVSRepository) SaveManifest(_ context.Context, value account.Manifest) error {
	if err := r.assertLocator(value.Locator); err != nil {
		return err
	}
	return r.putJSON(accountPath(value.Locator.PackageID, "manifest"), value)
}

func (r *FreeLocalAccountDKVSRepository) LoadEnvelope(_ context.Context, locator account.Locator) (*account.Envelope, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.Envelope
	if err := r.getJSON(accountPath(locator.PackageID, "envelope"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *FreeLocalAccountDKVSRepository) LoadDKVSShareCapsule(_ context.Context, locator account.Locator) (*account.DKVSShareCapsule, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.DKVSShareCapsule
	if err := r.getJSON(accountPath(locator.PackageID, "share/dkvs"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *FreeLocalAccountDKVSRepository) LoadKnowledgeBundle(_ context.Context, locator account.Locator) (*account.KnowledgeRecoveryBundle, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.KnowledgeRecoveryBundle
	if err := r.getJSON(accountPath(locator.PackageID, "questions"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func (r *FreeLocalAccountDKVSRepository) LoadManifest(_ context.Context, locator account.Locator) (*account.Manifest, error) {
	if err := r.assertLocator(locator); err != nil {
		return nil, err
	}
	var value account.Manifest
	if err := r.getJSON(accountPath(locator.PackageID, "manifest"), &value); err != nil {
		return nil, err
	}
	return &value, nil
}
