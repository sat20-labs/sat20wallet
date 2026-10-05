package wallet

import (
	"errors"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// WithWriteSigner creates an isolated request client, never a process-global
// signer registry. Cloning also pins its subaccount during network waits.
func (p *SatsNetDKVSClient) WithWriteSigner(signer common.Wallet) *SatsNetDKVSClient {
	if p == nil { return nil }
	p.endpointMu.RLock()
	endpoint := p.endpointID
	p.endpointMu.RUnlock()
	copyClient := &SatsNetDKVSClient{RESTClient: p.RESTClient, manager: p.manager,
		replicaNamespace: p.replicaNamespace, endpointID: endpoint}
	if signer != nil { copyClient.writeSigner = signer.Clone() }
	return copyClient
}

func walletMatchesDKVSAccount(signer common.Wallet, account string) bool {
	if signer == nil { return false }
	id, err := dkvsAccountID(signer)
	return err == nil && id == account
}

func (p *SatsNetDKVSClient) requestWriteSigner(account string) (common.Wallet, error) {
	if p == nil || p.requestContext().Err() != nil { return nil, dkvs.ErrPermissionDenied }
	if p.writeSigner != nil {
		if !walletMatchesDKVSAccount(p.writeSigner, account) { return nil, dkvs.ErrPermissionDenied }
		return p.writeSigner, nil
	}
	if p.manager == nil || p.manager.owner == nil { return nil, dkvs.ErrPermissionDenied }
	owner := p.manager.owner
	owner.mutex.RLock()
	defer owner.mutex.RUnlock()
	if root, err := owner.accountManagementRootWalletLocked(); err == nil && root != nil && root.Wallet != nil {
		copyRoot := cloneWalletAtAccountZero(root.Wallet)
		if walletMatchesDKVSAccount(copyRoot, account) { return copyRoot, nil }
	}
	if owner.wallet != nil {
		copyWallet := owner.wallet.Clone()
		if walletMatchesDKVSAccount(copyWallet, account) { return copyWallet, nil }
	}
	return nil, dkvs.ErrPermissionDenied
}

// synchronizedWriteContext uses the wallet replica first. A raw/unmanaged
// client has no persistent replica, so its first write performs the existing
// full prefix sync protocol and uses that returned endpoint generation.
func (p *SatsNetDKVSClient) synchronizedWriteContext(prefixes []string, endpointID string) (dkvs.WalletWriteContext, error) {
	ctx := dkvs.WalletWriteContext{EndpointID: endpointID}
	if p != nil && p.manager != nil && p.manager.owner != nil && p.manager.owner.db != nil && p.replicaNamespace != "" {
		store := newDKVSReplicaStore(p.manager.owner.db)
		state, err := store.LoadSubscriptionState(p.replicaNamespace)
		if err == nil && state.EndpointID == endpointID {
			complete := true
			for _, prefix := range prefixes {
				generation, ok := state.Generations[prefix]
				if !ok { complete = false; break }
				ctx.Prefixes = append(ctx.Prefixes, dkvs.PrefixGeneration{Prefix: prefix, Generation: generation})
			}
			if complete { return ctx, nil }
			ctx.Prefixes = nil
		} else if err != nil && !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return dkvs.WalletWriteContext{}, err
		}
	}

	for _, prefix := range prefixes {
		scope := dkvs.ActiveScope{Prefix: prefix}
		meta, records, err := p.collectActivePages(p.requestContext(), dkvs.ActiveSyncRequest{
			Scope: scope, EndpointID: endpointID, Full: true,
		}, nil)
		if err != nil { return dkvs.WalletWriteContext{}, err }
		root, err := dkvs.ActiveRecordsRoot(records)
		if err != nil { return dkvs.WalletWriteContext{}, err }
		if root != meta.Root { return dkvs.WalletWriteContext{}, dkvs.ErrPathDiverged }
		ctx.Prefixes = append(ctx.Prefixes, dkvs.PrefixGeneration{Prefix: prefix, Generation: meta.Generation})
	}
	return ctx, nil
}

func (p *SatsNetDKVSClient) prepareWriteAuthorization(mutations []dkvs.CASMutation, endpointID, requestID string) (*dkvs.WalletWriteAuthorization, error) {
	if len(mutations) == 1 && mutations[0].Record != nil && dkvs.IsAccountMappingBindingKey(mutations[0].Record.Key) { return nil, nil }
	prefixes, err := dkvs.WriteMutationPrefixes(mutations)
	if err != nil { return nil, err }
	var account string
	for _, mutation := range mutations {
		pub, err := dkvs.RecordSignerPubKey(mutation.Record)
		if err != nil { return nil, err }
		id, err := dkvs.CanonicalAccountID(pub)
		if err != nil || (account != "" && id != account) { return nil, dkvs.ErrPermissionDenied }
		account = id
	}
	wallet, err := p.requestWriteSigner(account)
	if err != nil { return nil, err }
	current, err := p.synchronizedWriteContext(prefixes, endpointID)
	if err != nil { return nil, err }
	digest, err := dkvs.WalletWriteDigest(mutations, dkvs.BatchCASOptions{EndpointID: endpointID, RequestID: requestID}, current)
	if err != nil { return nil, err }
	signer, ok := wallet.(dkvsAccountSchnorrSigner)
	if !ok { return nil, dkvs.ErrInvalidSignature }
	signature, err := signer.SignSchnorrMessage(digest[:])
	if err != nil { return nil, err }
	proof := &dkvs.WalletWriteAuthorization{Context: current, Signature: signature}
	if _, err := dkvs.VerifyWalletWriteAuthorization(mutations, dkvs.BatchCASOptions{EndpointID: endpointID, RequestID: requestID}, proof); err != nil { return nil, err }
	return proof, nil
}
