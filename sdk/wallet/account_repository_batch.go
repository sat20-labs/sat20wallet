package wallet

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type accountRecoveryObject struct {
	path  string
	value interface{}
}

// SaveRecoveryPackage persists the four account-recovery objects in one
// DKVS batch-CAS. This is the repository's normative publish operation; the
// per-object methods remain for read compatibility and non-atomic test doubles.
func (r *AccountDKVSRepository) SaveRecoveryPackage(_ context.Context,
	value account.RecoveryPackage) error {

	if r == nil || r.store == nil || r.owner == nil {
		return fmt.Errorf("writable account DKVS repository is required")
	}
	locator := value.Envelope.Locator
	if err := r.assertLocator(locator); err != nil {
		return err
	}
	if err := r.assertLocator(value.Manifest.Locator); err != nil {
		return err
	}
	objects := []accountRecoveryObject{
		{path: accountPath(locator.PackageID, "envelope"), value: value.Envelope},
		{path: accountPath(locator.PackageID, "share/dkvs"), value: value.DKVSShareCapsule},
		{path: accountPath(locator.PackageID, "questions"), value: value.KnowledgeBundle},
		{path: accountPath(locator.PackageID, "manifest"), value: value.Manifest},
	}
	pubKey, err := dkvsWalletPubKey(r.owner)
	if err != nil {
		return err
	}
	mutations := make([]dkvsValueMutation, 0, len(objects))
	for _, object := range objects {
		encoded, err := json.Marshal(object.value)
		if err != nil {
			return err
		}
		if len(encoded) > account.MaxRecoveryObjectSize {
			return fmt.Errorf("account recovery object exceeds DKVS value limit")
		}
		key, err := dkvsindexer.PersonalKey(pubKey, object.path)
		if err != nil {
			return err
		}
		mutations = append(mutations, dkvsValueMutation{
			Key: key, Value: encoded, Owner: r.owner,
			Signature: dkvsSignatureAccount,
			Policy: dkvsStoragePolicy{
				TTL: r.recordOptions.TTL,
				ExpiryHeight: r.recordOptions.ExpiryHeight,
				Autopay: &r.autopay,
			},
		})
	}
	// PathGeneration is assigned by canonical key order. Preserve that order
	// in the RPC batch and in the resulting P2P notifications so remote nodes
	// observe a contiguous generation stream without a transient gap.
	sort.Slice(mutations, func(i, j int) bool {
		return mutations[i].Key < mutations[j].Key
	})
	values, err := r.store.PutBatch(mutations)
	if err != nil {
		return err
	}
	if len(values) != len(mutations) {
		return dkvsindexer.ErrInvalidRecord
	}
	return nil
}
