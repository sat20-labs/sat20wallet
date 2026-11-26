package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type coreBindingMode uint8

const (
	verifyCoreBinding coreBindingMode = iota
	registerCoreBinding
	moveCoreBinding
)

// acceptCoreBinding publishes the account mapping through the wallet KV RPC.
// That signed control record establishes the binding used by subsequent writes.
// The server verifies that the descriptor targets its own node, commits the CAS,
// and relays the binding update by P2P; no separate acceptance record is stored.
func (p *SatsNetDKVSClient) acceptCoreBinding(root common.Wallet,
	key, coreID string, initialValue []byte, mode coreBindingMode) (*swire.DKVSRecord, error) {
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
	if current == nil && mode == verifyCoreBinding {
		return nil, dkvsindexer.ErrRecordNotFound
	}
	value := append([]byte(nil), initialValue...)
	seq := uint64(1)
	if current != nil {
		_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(current)
		if err != nil || descriptor.AccountID != accountID {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if mode != moveCoreBinding {
			if descriptor.CoreNodeID != coreID {
				return nil, dkvsindexer.ErrEndpointMismatch
			}
			return current, nil
		}
		descriptor.CoreNodeID = coreID
		descriptor.Capabilities |= accountServiceCapabilityRGB11Direct
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
	if current != nil && bytes.Equal(value, current.Value) {
		return current, nil
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
	result, err := p.WithWriteSigner(root).putRecordBatchCASRaw([]dkvsindexer.CASMutation{{
		Record: record, Precondition: precondition,
	}}, coreID, requestID)
	if err != nil {
		return nil, fmt.Errorf("bind account through wallet KV RPC: %w", err)
	}
	if result == nil || len(result.Records) != 1 {
		return nil, dkvsindexer.ErrWriteConflict
	}
	// Submission verifies the final signed record, including a height refresh.
	record = result.Records[0]
	confirmed, err := p.GetRecordDirect(key)
	if err != nil {
		return nil, fmt.Errorf("read committed account binding: %w", err)
	}
	if confirmed == nil || dkvsindexer.RecordHash(confirmed) != dkvsindexer.RecordHash(record) {
		return nil, dkvsindexer.ErrWriteConflict
	}
	return confirmed, nil
}

func (p *Manager) commitAccountCoreBinding(ctx context.Context, root common.Wallet, store *dkvsStore,
	key, coreID, prefix string, value []byte, mode coreBindingMode) error {
	if p == nil || store == nil || store.manager == nil || store.client == nil {
		return ErrMessageServiceUnavailable
	}
	// Authentication bootstrap is not an exception to session write eligibility.
	// Prepare before acquiring the transport slot; nested acquisition would block.
	if err := store.manager.prepareManagedWriteContext(ctx, store.client, []string{key}); err != nil {
		return err
	}
	err := store.manager.runReadTransport(ctx, func() error {
		if err := store.manager.requireWritePaths(store.client, []string{key}); err != nil {
			return err
		}
		if _, err := store.client.withRequestContext(ctx).acceptCoreBinding(root, key, coreID, value, mode); err != nil {
			return err
		}
		if mode == verifyCoreBinding {
			return nil
		}
		// A complete source view, not the binding write response, establishes the
		// local replica/cursor. Other pending business outbox entries are preserved.
		_, err := store.client.SyncActiveScope(ctx, newDKVSReplicaStore(p.db),
			store.client.replicaNamespace, dkvsindexer.ActiveScope{Prefix: prefix}, true)
		if err != nil {
			return fmt.Errorf("sync committed account binding: %w", err)
		}
		return nil
	})
	if err == nil && mode != verifyCoreBinding {
		store.manager.wakeSync()
	}
	return err
}

// The root lifecycle may create an absent mapping. Restoring a wallet on
// another endpoint can never replace its signed service binding.
func (p *Manager) ensureAccountCoreBindingForSync(ctx context.Context) error {
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	return p.ensureDKVSManager().registerRootService(ctx, root, p.rootMessageNode(root))
}

func (m *dkvsManager) registerRootService(ctx context.Context, root common.Wallet, node *Node) error {
	store, err := m.primaryStore()
	if err != nil {
		return err
	}
	err = verifyBindingOnNode(ctx, store, root, node)
	if errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		store.readCtx = ctx
		bind, err := m.owner.prepareBindingOnNode(root, store, node, registerCoreBinding)
		if err != nil {
			return err
		}
		if err := bind(); err != nil {
			return fmt.Errorf("publish root account binding: %w", err)
		}
	} else if err != nil {
		return err
	}
	if err := m.owner.registerRootMailbox(root); err != nil {
		return fmt.Errorf("register root mailbox subscriptions: %w", err)
	}
	return nil
}

func verifyBindingOnNode(ctx context.Context, store *dkvsStore, root common.Wallet, node *Node) error {
	coreID, err := accountServiceCoreNodeID(node)
	if err != nil {
		return err
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return err
	}
	config, err := store.client.getDKVSClientConfigContext(ctx)
	if err != nil {
		return err
	}
	if config == nil || config.EndpointID != coreID {
		return dkvsindexer.ErrEndpointMismatch
	}
	return readRootBinding(ctx, store, root, GetChainParam().Name, accountID, coreID)
}

func readRootBinding(ctx context.Context, store *dkvsStore, root common.Wallet, network, accountID, endpoint string) error {
	key, err := dkvsindexer.AccountMappingKey(network, root.GetAddress())
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = store.client.requestContext()
	}
	readCtx, cancel := context.WithTimeout(ctx, rootQueryTimeout)
	defer cancel()
	record, err := store.client.GetRecordDirectContext(readCtx, key)
	if err != nil {
		return err
	}
	_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(record)
	if err != nil || descriptor.AccountID != accountID {
		return dkvsindexer.ErrInvalidRecord
	}
	if descriptor.CoreNodeID != endpoint {
		return dkvsindexer.ErrEndpointMismatch
	}
	return nil
}
