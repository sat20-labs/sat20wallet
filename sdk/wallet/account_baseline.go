package wallet

import (
	"bytes"
	"context"
	"errors"
	"github.com/sat20-labs/sat20wallet/sdk/account"
)

type accountBaseline struct {
	snapshot     *accountManagementSyncSnapshot
	store        *dkvsStore
	index        account.ManagedIndex
	remoteIndex  *dkvsValue
	catalog      AccountManagedDataCatalog
	needsRestore bool
	readyLater   bool
	wrapper      *accountLocalKV
	finish       func(error)
}

// Establish service/identity readiness separately from module snapshot exports.
// First local backup may follow in the existing worker; cold imports must finish.
func (p *Manager) prepareAccountBaseline(ctx context.Context) (base *accountBaseline, result error) {
	p.mutex.RLock()
	wasReady := p.dkvs != nil && p.dkvs.scopeReady(p.accountWriteScopeLocked())
	p.mutex.RUnlock()
	finish := p.accountSyncResult("baseline")
	defer func() {
		if result != nil {
			finish(result)
		}
	}()
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	p.mutex.Lock()
	snapshot, _, err := p.captureAccountManagementSyncSnapshotBaseLocked()
	p.mutex.Unlock()
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, ErrDKVSPathNotSynced
	}
	defer func() {
		if result != nil {
			zeroBytes(snapshot.secret)
		}
	}()
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, err
	}
	wrapperKey, err := accountRootWrapperKey(snapshot.root)
	if err != nil {
		return nil, err
	}
	indexKey, err := account.ManagedIndexKey(snapshot.profile.AccountID)
	if err != nil {
		return nil, err
	}
	if err := store.WaitReady(wrapperKey, indexKey); err != nil {
		return nil, err
	}
	if err := store.SyncCurrent(wrapperKey, indexKey); err != nil {
		return nil, err
	}
	wrapper, err := store.Get(wrapperKey)
	if errors.Is(err, ErrDKVSRecordNotFound) {
		wrapper, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	var missingWrapper *accountLocalKV
	if wrapper == nil {
		p.mutex.RLock()
		local, readErr := p.readAccountLocal(snapshot.profile.AccountID, "account", wrapperKey)
		p.mutex.RUnlock()
		if readErr != nil {
			return nil, readErr
		}
		if local == nil || len(local.Value) == 0 {
			return nil, ErrRootAccountDiscoveryPending
		}
		local, err = p.setAccountBaseline(local, nil)
		if err != nil {
			return nil, err
		}
		missingWrapper = local
		wrapper = &dkvsValue{Value: local.Value}
	}
	payload, err := openAccountRootWrapper(snapshot.root, snapshot.network, snapshot.profile.AccountID, wrapper.Value)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(payload.Secret)
	if !bytes.Equal(payload.Secret, snapshot.secret) {
		return nil, ErrRootAccountWrapperInvalid
	}
	if err := p.saveAccountKV(snapshot.profile.AccountID, wrapperKey, wrapper.Value); err != nil {
		return nil, err
	}
	if wrapper.record != nil {
		p.mutex.RLock()
		local, readErr := p.readAccountLocal(snapshot.profile.AccountID, "account", wrapperKey)
		p.mutex.RUnlock()
		if readErr != nil {
			return nil, readErr
		}
		if err := p.recordAccountEffect(local, wrapper); err != nil {
			return nil, err
		}
	}
	if err := p.syncAccountRecovery(store, snapshot, payload); err != nil {
		return nil, err
	}
	remoteIndex, err := store.Get(indexKey)
	if errors.Is(err, ErrDKVSRecordNotFound) {
		remoteIndex, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	index := account.ManagedIndex{Version: account.ManagedIndexVersion}
	if remoteIndex != nil {
		index, err = account.OpenManagedIndex(snapshot.secret, snapshot.profile.AccountID, snapshot.network, remoteIndex.Value)
		if err != nil {
			return nil, err
		}
	}
	readyLater := !wasReady && !(remoteIndex == nil && snapshot.profile.InitialBackupPending)
	needsRestore, err := p.primeAccountImports(index)
	if err != nil {
		return nil, err
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return nil, err
	}
	if !needsRestore {
		// Original devices may back up their first local snapshots in the
		// background; missing remote scopes on a new device were marked above.
		if err := p.ensureAccountModules(catalog, index); err != nil {
			return nil, err
		}
		if !readyLater {
			finish(nil)
		}
	}

	return &accountBaseline{snapshot: snapshot, store: store, index: index, remoteIndex: remoteIndex, catalog: catalog, needsRestore: needsRestore, readyLater: readyLater, wrapper: missingWrapper, finish: finish}, nil
}
