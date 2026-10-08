package wallet

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// acceptCoreBinding publishes the account mapping through the wallet KV RPC.
// That signed control record establishes the binding used by subsequent writes.
// The server verifies that the descriptor targets its own node, commits the CAS,
// and relays the binding update by P2P; no separate acceptance record is stored.
func (p *SatsNetDKVSClient) acceptCoreBinding(root common.Wallet,
	key, coreID string, initialValue []byte) (*swire.DKVSRecord, error) {
	if p == nil || root == nil {
		return nil, ErrMessageServiceUnavailable
	}
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	if config == nil || config.EndpointID != coreID {
		return nil, dkvsindexer.ErrEndpointMismatch
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, err
	}
	current, err := p.GetRecordDirect(key)
	if err != nil && !errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		return nil, fmt.Errorf("read current account binding: %w", err)
	}
	value := append([]byte(nil), initialValue...)
	seq := uint64(1)
	if current != nil {
		_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(current)
		if err != nil || descriptor.AccountID != accountID {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		descriptor.CoreNodeID = coreID
		descriptor.Capabilities |= dkvsindexer.AccountServiceCapabilityRGB11Direct
		value, err = dkvsindexer.EncodeAccountServiceDescriptor(*descriptor)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(value, current.Value) {
			if current.Seq == ^uint64(0) {
				return nil, dkvsindexer.ErrInvalidSequence
			}
			seq = current.Seq + 1
		}
	}
	record := current
	if current == nil || !bytes.Equal(value, current.Value) {
		height, err := p.GetBestHeight()
		if err != nil {
			return nil, err
		}
		if current != nil && height < current.IssueHeight {
			return nil, dkvsindexer.ErrStaleEndpoint
		}
		record, err = NewDKVSAccountSignedRecord(root, key, value, dkvsindexer.RecordOptions{Seq: seq, IssueHeight: height})
		if err != nil {
			return nil, err
		}
	}
	precondition := dkvsindexer.WritePrecondition{ExpectAbsent: true}
	if current != nil {
		hash := dkvsindexer.RecordHash(current)
		precondition = dkvsindexer.WritePrecondition{ExpectedHash: &hash}
	}
	requestID, err := newDKVSRequestID()
	if err != nil {
		return nil, err
	}
	result, err := p.putRecordBatchCASRaw([]dkvsindexer.CASMutation{{
		Record: record, Precondition: precondition,
	}}, coreID, requestID)
	if err != nil {
		return nil, fmt.Errorf("bind account through wallet KV RPC: %w", err)
	}
	if result == nil || len(result.Records) != 1 ||
		dkvsindexer.RecordHash(result.Records[0]) != dkvsindexer.RecordHash(record) {
		return nil, dkvsindexer.ErrWriteConflict
	}
	confirmed, err := p.GetRecordDirect(key)
	if err != nil {
		return nil, fmt.Errorf("read committed account binding: %w", err)
	}
	if confirmed == nil || dkvsindexer.RecordHash(confirmed) != dkvsindexer.RecordHash(record) {
		return nil, dkvsindexer.ErrWriteConflict
	}
	return confirmed, nil
}

func (p *Manager) commitAccountCoreBinding(root common.Wallet, store *dkvsStore,
	key, coreID, prefix string, value []byte) error {
	if p == nil || store == nil || store.manager == nil || store.client == nil {
		return ErrMessageServiceUnavailable
	}
	err := store.manager.runTransport(func() error {
		if _, err := store.client.acceptCoreBinding(root, key, coreID, value); err != nil {
			return err
		}
		// A complete source view, not the binding write response, establishes the
		// local replica/cursor. Other pending business outbox entries are preserved.
		_, err := store.client.SyncActiveScope(store.client.requestContext(), newDKVSReplicaStore(p.db),
			store.client.replicaNamespace, dkvsindexer.ActiveScope{Prefix: prefix}, true)
		if err != nil {
			return fmt.Errorf("sync committed account binding: %w", err)
		}
		return nil
	})
	if err == nil {
		store.manager.wakeSync()
	}
	return err
}

// A background backup may establish a missing binding, but must not move an
// account that is already bound to a different CoreNode.
func (p *Manager) ensureAccountCoreBindingForSync() error {
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	bind, err := p.prepareAccountCoreNodeBinding(root, store)
	if err != nil {
		return err
	}
	key, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, root.GetAddress())
	if err != nil {
		return err
	}
	current, err := store.client.GetRecordDirect(key)
	if err != nil && !errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		return err
	}
	if current == nil {
		return bind()
	}
	_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(current)
	if err != nil {
		return err
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return err
	}
	coreID, err := p.currentCoreNodeID()
	if err != nil {
		return err
	}
	if descriptor.AccountID != accountID {
		return dkvsindexer.ErrInvalidRecord
	}
	if descriptor.CoreNodeID != coreID {
		return dkvsindexer.ErrEndpointMismatch
	}
	return nil
}
