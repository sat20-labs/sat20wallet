package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	coresync "github.com/sat20-labs/rgb11/sync"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *rgb11Manager) rgb11StateKeys() (string, string, string, error) {
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return "", "", "", err
	}
	pubkey := p.wallet.GetPubKey().SerializeCompressed()
	headKey, err := dkvsindexer.PersonalKey(pubkey, RGB11WalletHeadPath(walletID))
	if err != nil {
		return "", "", "", err
	}
	snapshotKey, err := dkvsindexer.BlobKey(
		dkvsindexer.AccountID(pubkey), RGB11WalletSnapshotBlobKey(walletID),
	)
	if err != nil {
		return "", "", "", err
	}
	return walletID, headKey, snapshotKey, nil
}

func decodeRGB11StoredHead(value *dkvsValue, walletID string) (*coresync.WalletHead, error) {
	if value == nil {
		return nil, ErrDKVSRecordNotFound
	}
	head, err := rgb11wallet.DecodeWalletHead(value.Value)
	if err != nil {
		return nil, err
	}
	if err := head.Validate(walletID); err != nil {
		return nil, err
	}
	return head, nil
}

func (p *rgb11Manager) rgb11StorePolicy(store *dkvsStore) (dkvsStoragePolicy, error) {
	paid, err := p.hasActiveRGB11Autopay()
	if err != nil {
		return dkvsStoragePolicy{}, err
	}
	if paid {
		p.setRGB11BackupRetention("autopay", 0)
		return dkvsStoragePolicy{Autopay: &DKVSAutopayOptions{
			AddressParams: GetChainParam_SatsNet(),
		}}, nil
	}
	config, err := store.Config()
	if err != nil {
		return dkvsStoragePolicy{}, err
	}
	if config == nil || !config.Enabled || config.MaxTTL == 0 {
		return dkvsStoragePolicy{}, dkvsindexer.ErrFreeLocalDisabled
	}
	p.setRGB11BackupRetention("temporary", config.MaxTTL)
	return dkvsStoragePolicy{TTL: config.MaxTTL, FreeLocal: true}, nil
}

// loadSynchronizedRGB11State waits for dkvsManager's current-session baseline
// and then applies the synchronized replica to this wallet scope.
func (p *rgb11Manager) loadSynchronizedRGB11State() error {
	if !p.rgb11DKVSConfigured() {
		return nil
	}
	store, err := p.ensureDKVSManager().primaryStore()
	if err != nil {
		return err
	}
	walletID, headKey, snapshotKey, err := p.rgb11StateKeys()
	if err != nil {
		return err
	}
	if err := store.WaitReady(headKey, snapshotKey); err != nil {
		return err
	}
	if _, err := store.Get(headKey); errors.Is(err, ErrDKVSRecordNotFound) {
		snapshot, _, exportErr := p.exportRGB11WalletSnapshot(walletID)
		if exportErr != nil {
			return exportErr
		}
		if rgb11SnapshotHasState(snapshot) {
			if _, persistErr := p.persistRGB11StateToStore(store); persistErr != nil {
				return persistErr
			}
			return p.enableRGB11AutoBackupFromStore(store)
		}
		policy, policyErr := p.rgb11StorePolicy(store)
		if policyErr != nil {
			return policyErr
		}
		if err := p.enableRGB11AutoBackup(dkvsindexer.RecordOptions{
			TTL: policy.TTL, ExpiryHeight: policy.ExpiryHeight,
		}); err != nil {
			return err
		}
		p.setRGB11DKVSStatus("synced")
		return nil
	} else if err != nil {
		return err
	}
	if err := p.reconcileRGB11StateFromStore(store); err != nil {
		return err
	}
	if p.rgb11ScopeState().Status == "pending" {
		if _, err := p.persistRGB11StateToStore(store); err != nil {
			return err
		}
	}
	return p.enableRGB11AutoBackupFromStore(store)
}

func (p *rgb11Manager) persistRGB11StateToStore(store *dkvsStore) (*coresync.WalletHead, error) {
	if p == nil || store == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	lock := p.backupLock()
	lock.Lock()
	defer lock.Unlock()
	if err := p.reloadPersistedRGB11WalletHeadLocked(); err != nil {
		return nil, err
	}

	walletID, headKey, snapshotKey, err := p.rgb11StateKeys()
	if err != nil {
		return nil, err
	}
	_, plaintext, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, err
	}
	policy, err := p.rgb11StorePolicy(store)
	if err != nil {
		return nil, err
	}
	stateHash := sha256.Sum256(plaintext)
	var writtenHead *coresync.WalletHead
	_, err = store.Update([]string{snapshotKey, headKey}, func(current map[string]*dkvsValue,
		next map[string]uint64) ([]dkvsValueMutation, error) {

		var previous *coresync.WalletHead
		if current[headKey] != nil {
			previous, err = decodeRGB11StoredHead(current[headKey], walletID)
			if err != nil {
				return nil, err
			}
		}
		if previous != nil && previous.StateHash == stateHash {
			writtenHead = previous
			return nil, nil
		}
		nextSeq := next[headKey]
		if next[snapshotKey] != nextSeq {
			return nil, coresync.ErrHeadSequence
		}
		if previous != nil && previous.Seq+1 != nextSeq {
			return nil, coresync.ErrHeadSequence
		}
		if p.rgbManager.head != nil && previous != nil {
			localHash, hashErr := p.rgbManager.head.Hash()
			if hashErr != nil {
				return nil, hashErr
			}
			remoteHash, hashErr := previous.Hash()
			if hashErr != nil || localHash != remoteHash {
				return nil, coresync.ErrHeadConflict
			}
		}
		var sequence [8]byte
		binary.LittleEndian.PutUint64(sequence[:], nextSeq)
		operationInput := append([]byte("SAT20-RGB11-WALLET-SNAPSHOT-V1"), stateHash[:]...)
		operationID := sha256.Sum256(append(operationInput, sequence[:]...))
		head, headErr := NewRGB11WalletHead(walletID, stateHash, operationID, previous)
		if headErr != nil {
			return nil, headErr
		}
		if head.Seq != nextSeq {
			return nil, coresync.ErrHeadSequence
		}
		cryptor, ok := p.wallet.(rgb11SnapshotCryptor)
		if !ok {
			return nil, fmt.Errorf("active wallet does not support RGB11 snapshot encryption")
		}
		pubkey := p.wallet.GetPubKey().SerializeCompressed()
		ciphertext, encryptErr := cryptor.EncryptTo(pubkey, plaintext)
		if encryptErr != nil {
			return nil, encryptErr
		}
		envelope, encodeErr := rgb11wallet.EncodeEncryptedSnapshot(walletID, operationID, ciphertext)
		if encodeErr != nil {
			return nil, encodeErr
		}
		headValue, encodeErr := head.StrictEncode()
		if encodeErr != nil {
			return nil, encodeErr
		}
		writtenHead = head
		return []dkvsValueMutation{
			{
				Key: snapshotKey, Value: envelope, Owner: p.wallet,
				Signature: dkvsSignatureAccount, Policy: policy,
			},
			{
				Key: headKey, Value: headValue, Owner: p.wallet,
				Signature: dkvsSignatureAccount, Policy: policy,
			},
		}, nil
	})
	if err != nil {
		if errors.Is(err, dkvsindexer.ErrWriteConflict) ||
			errors.Is(err, dkvsindexer.ErrInvalidSequence) ||
			errors.Is(err, dkvsindexer.ErrStaleGeneration) ||
			errors.Is(err, dkvsindexer.ErrPathDiverged) {
			p.setRGB11DKVSStatus("conflict")
			return nil, coresync.ErrHeadConflict
		}
		p.setRGB11DKVSStatus("warning")
		return nil, err
	}
	if writtenHead == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := p.persistRGB11WalletHead(writtenHead); err != nil {
		p.setRGB11DKVSStatus("warning")
		return nil, err
	}
	p.rgbManager.head = writtenHead
	p.setRGB11DKVSStatus("synced")
	return writtenHead, nil
}

func (p *rgb11Manager) restoreRGB11StateFromStoreLocked(store *dkvsStore) (*coresync.WalletHead, error) {
	walletID, headKey, snapshotKey, err := p.rgb11StateKeys()
	if err != nil {
		return nil, err
	}
	headValue, err := store.Get(headKey)
	if err != nil {
		return nil, err
	}
	head, err := decodeRGB11StoredHead(headValue, walletID)
	if err != nil {
		return nil, err
	}
	snapshotValue, err := store.Get(snapshotKey)
	if err != nil {
		return nil, err
	}
	blob, err := DecodeDKVSBlobValue(snapshotValue.Value)
	if err != nil {
		return nil, err
	}
	envelopeWalletID, operationID, ciphertext, err := rgb11wallet.DecodeEncryptedSnapshot(blob.Data)
	if err != nil || envelopeWalletID != walletID || operationID != head.OperationID || len(ciphertext) == 0 {
		return nil, ErrRGB11Inconsistent
	}
	cryptor, ok := p.wallet.(rgb11SnapshotCryptor)
	if !ok {
		return nil, fmt.Errorf("active wallet does not support RGB11 snapshot decryption")
	}
	pubkey := p.wallet.GetPubKey().SerializeCompressed()
	plaintext, err := cryptor.Decrypt(ciphertext, pubkey)
	if err != nil || !bytes.Equal(head.StateHash[:], hashBytes(plaintext)) {
		return nil, ErrRGB11Inconsistent
	}
	snapshot, err := rgb11wallet.DecodeWalletSnapshotPayload(plaintext)
	if err != nil || snapshot.Version != rgb11WalletSnapshotVersion ||
		snapshot.WalletID != walletID || snapshot.AccountIndex != p.wallet.GetSubAccount() ||
		snapshot.EngineBuildID != rgb11wallet.NativeEngineBuildID {
		return nil, ErrRGB11Inconsistent
	}
	if err := p.rgbManager.engineStore.ImportSnapshot(snapshot.EngineRecords); err != nil {
		return nil, err
	}
	if err := p.rgbManager.projectionStore.ImportSnapshot(snapshot.ProjectionRecords); err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return nil, err
	}
	for _, info := range snapshot.TickerInfos {
		if err := p.RegisterRGB11TickerInfo(info); err != nil {
			p.rgbManager.consistencyStatus = "broken"
			return nil, err
		}
	}
	if err := p.rebuildRGB11Locks(); err != nil {
		return nil, err
	}
	if err := p.persistRGB11WalletHead(head); err != nil {
		return nil, err
	}
	p.rgbManager.head = head
	p.setRGB11DKVSStatus("synced")
	p.setRGB11BackupRetention(
		map[bool]string{true: "temporary", false: "autopay"}[headValue.TTL > 0],
		headValue.TTL,
	)
	return head, nil
}

func rgb11SnapshotHasState(snapshot *RGB11WalletSnapshot) bool {
	return snapshot != nil && (len(snapshot.ProjectionRecords) != 0 ||
		len(snapshot.EngineRecords) != 0 || len(snapshot.TickerInfos) != 0)
}

func (p *rgb11Manager) reconcileRGB11StateFromStore(store *dkvsStore) error {
	if p == nil || store == nil {
		return nil
	}
	lock := p.backupLock()
	lock.Lock()
	defer lock.Unlock()

	walletID, headKey, _, err := p.rgb11StateKeys()
	if err != nil {
		return err
	}
	remoteValue, err := store.Get(headKey)
	if errors.Is(err, ErrDKVSRecordNotFound) {
		snapshot, _, exportErr := p.exportRGB11WalletSnapshot(walletID)
		policy := p.rgb11AutoBackupPolicy()
		autoBackupEnabled := policy != nil && policy.Enabled
		if exportErr == nil && rgb11SnapshotHasState(snapshot) && autoBackupEnabled {
			p.scheduleRGB11StoreWrite()
		}
		return nil
	}
	if err != nil {
		return err
	}
	remoteHead, err := decodeRGB11StoredHead(remoteValue, walletID)
	if err != nil {
		return err
	}
	localSnapshot, plaintext, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return err
	}
	localHash := sha256.Sum256(plaintext)
	if rgb11SnapshotHasState(localSnapshot) && localHash == remoteHead.StateHash {
		if err := p.persistRGB11WalletHead(remoteHead); err != nil {
			return err
		}
		p.rgbManager.head = remoteHead
		p.setRGB11DKVSStatus("synced")
		return nil
	}
	if !rgb11SnapshotHasState(localSnapshot) {
		_, err = p.restoreRGB11StateFromStoreLocked(store)
		return err
	}
	localSeq := uint64(1)
	if p.rgbManager.head != nil {
		localSeq = p.rgbManager.head.Seq
		if localHash != p.rgbManager.head.StateHash {
			localSeq++
		}
	}
	switch {
	case localSeq > remoteHead.Seq:
		p.setRGB11DKVSStatus("pending")
		p.scheduleRGB11StoreWrite()
		return nil
	case localSeq == remoteHead.Seq:
		p.setRGB11DKVSStatus("conflict")
		return coresync.ErrHeadConflict
	}
	_, err = p.restoreRGB11StateFromStoreLocked(store)
	return err
}

func (p *rgb11Manager) scheduleRGB11StoreWrite() {
	if p == nil || p.ensureDKVSManager() == nil {
		return
	}
	if p.wallet == nil || p.status == nil {
		return
	}
	fixedWallet := p.wallet.Clone()
	if fixedWallet != nil {
		fixedWallet.SetSubAccount(p.status.CurrentAccount)
	}
	if fixedWallet == nil || fixedWallet.GetPubKey() == nil || fixedWallet.GetAddress() == "" {
		// Private-key wallets only expose account zero and their Clone currently
		// has no derivation source. The selected wallet object remains fixed when
		// switching to another wallet, so retaining it is safe for that scope.
		if p.wallet.GetSubAccount() != p.status.CurrentAccount ||
			p.wallet.GetPubKey() == nil || p.wallet.GetAddress() == "" {
			p.setRGB11DKVSStatus("warning")
			Log.Warningf("capture fixed RGB11 backup wallet failed")
			return
		}
		fixedWallet = p.wallet
	}
	account := localRGB11Account{
		WalletID:     p.status.CurrentWallet,
		AccountIndex: p.status.CurrentAccount,
		Address:      fixedWallet.GetAddress(),
		Wallet:       fixedWallet,
	}
	scoped, err := p.newScopedRGB11Manager(account)
	if err != nil {
		p.setRGB11DKVSStatus("warning")
		Log.Warningf("create fixed RGB11 backup scope failed: %v", err)
		return
	}
	scope := rgb11StorageScope(account.WalletID, account.AccountIndex)
	scoped.setRGB11DKVSStatus("pending")
	p.ensureDKVSManager().schedule("rgb11:"+scope, func(store *dkvsStore) error {
		_, err := scoped.persistRGB11StateToStore(store)
		return err
	})
}

func (p *Manager) handleRGB11ReplicaUpdate(_ []string) {
	for _, account := range p.localRGB11Accounts() {
		manager, err := p.newScopedRGB11Manager(account)
		if err != nil {
			continue
		}
		store, err := p.ensureDKVSManager().primaryStore()
		if err != nil {
			continue
		}
		if err := manager.reconcileRGB11StateFromStore(store); err != nil {
			Log.Warningf("apply RGB11 replica wallet=%d account=%d failed: %v",
				account.WalletID, account.AccountIndex, err)
		}
	}
}

func (p *Manager) registerDKVSDomainObservers() {
	if p == nil || p.dkvs == nil {
		return
	}
	p.dkvs.addObserver(p.handleRGB11ReplicaUpdate)
	p.dkvs.addObserver(func(_ []string) {
		if err := p.SyncAccountManagementState(nil); err != nil {
			Log.Warningf("apply account management replica failed: %v", err)
		}
	})
}
