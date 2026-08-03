package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	coresync "github.com/sat20-labs/rgb11/sync"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type RGB11SnapshotMigrationResult struct {
	Migrated    bool   `json:"migrated"`
	PreviousSeq uint64 `json:"previous_seq"`
	CurrentSeq  uint64 `json:"current_seq"`
}

// MigrateLegacyRGB11WalletSnapshot rewrites the selected account's verified
// legacy snapshot into the current canonical format. Runtime snapshot decoding
// remains strict and does not use this migration path.
func (p *Manager) MigrateLegacyRGB11WalletSnapshot() (*RGB11SnapshotMigrationResult, error) {
	if p == nil || p.rgbManager == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.migrateLegacyRGB11WalletSnapshot()
}

// MigrateLegacyRGB11WalletSnapshotRemote creates the minimum SDK maintenance
// context required for a one-shot remote migration. It deliberately avoids
// application startup, L1 status initialization and RGB11 lock rebuilding.
func MigrateLegacyRGB11WalletSnapshotRemote(cfg *sdkcommon.Config, database indexer.KVDB,
	activeWallet sdkcommon.Wallet) (*RGB11SnapshotMigrationResult, error) {
	if cfg == nil || cfg.IndexerL2 == nil || database == nil || activeWallet == nil ||
		activeWallet.GetPubKey() == nil {
		return nil, ErrRGB11Inconsistent
	}
	_env, _chain, _mode = cfg.Env, cfg.Chain, cfg.Mode
	http := NewHTTPClient()
	l2 := NewIndexerRPCClientMgr()
	l2.Set(NewIndexerClient(cfg.IndexerL2.Scheme, cfg.IndexerL2.Host, cfg.IndexerL2.Proxy, http))
	owner := &Manager{
		cfg: cfg, db: database, http: http, wallet: activeWallet,
		status:          &Status{CurrentWallet: activeWallet.GetId(), CurrentAccount: activeWallet.GetSubAccount()},
		l2IndexerClient: l2, tickerInfoMap: make(map[string]*indexer.TickerInfo),
	}
	manager := &rgb11Manager{Manager: owner, scopeStates: newRGB11ScopeStateRegistry()}
	owner.rgbManager = manager
	owner.ensureDKVSManager()
	defer releaseDKVSManagerRuntime(owner.dkvs)
	return manager.migrateLegacyRGB11WalletSnapshot()
}

func (p *rgb11Manager) migrateLegacyRGB11WalletSnapshot() (*RGB11SnapshotMigrationResult, error) {
	if p == nil || !p.rgb11DKVSConfigured() {
		return nil, ErrDKVSPathNotSynced
	}
	store, err := p.ensureDKVSManager().primaryStore()
	if err != nil {
		return nil, err
	}
	walletID, headKey, snapshotKey, err := p.rgb11StateKeys()
	if err != nil {
		return nil, err
	}
	if err := store.WaitReady(headKey, snapshotKey); err != nil {
		return nil, err
	}
	policy, err := p.rgb11StorePolicy(store)
	if err != nil {
		return nil, err
	}
	result := &RGB11SnapshotMigrationResult{}
	lock := p.backupLock()
	lock.Lock()
	defer lock.Unlock()

	_, err = store.Update([]string{snapshotKey, headKey}, func(current map[string]*dkvsValue,
		next map[string]uint64) ([]dkvsValueMutation, error) {

		headValue := current[headKey]
		snapshotValue := current[snapshotKey]
		if headValue == nil || snapshotValue == nil {
			return nil, ErrDKVSRecordNotFound
		}
		head, decodeErr := decodeRGB11StoredHead(headValue, walletID)
		if decodeErr != nil || head.Seq != headValue.Seq || snapshotValue.Seq != head.Seq {
			return nil, ErrRGB11Inconsistent
		}
		blob, decodeErr := DecodeDKVSBlobValue(snapshotValue.Value)
		if decodeErr != nil {
			return nil, decodeErr
		}
		envelopeWalletID, operationID, ciphertext, decodeErr := rgb11wallet.DecodeEncryptedSnapshot(blob.Data)
		if decodeErr != nil || envelopeWalletID != walletID || operationID != head.OperationID || len(ciphertext) == 0 {
			return nil, ErrRGB11Inconsistent
		}
		cryptor, ok := p.wallet.(rgb11SnapshotCryptor)
		if !ok {
			return nil, fmt.Errorf("active wallet does not support RGB11 snapshot encryption")
		}
		pubkey := p.wallet.GetPubKey().SerializeCompressed()
		plaintext, decryptErr := cryptor.Decrypt(ciphertext, pubkey)
		if decryptErr != nil || !bytes.Equal(head.StateHash[:], hashBytes(plaintext)) {
			return nil, ErrRGB11Inconsistent
		}
		snapshot, migrated, decodeErr := rgb11wallet.DecodeLegacyWalletSnapshotForMigration(plaintext)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result.PreviousSeq = head.Seq
		result.CurrentSeq = head.Seq
		if !migrated {
			return nil, nil
		}
		if snapshot.WalletID != walletID || snapshot.AccountIndex != p.wallet.GetSubAccount() ||
			snapshot.EngineBuildID != rgb11wallet.NativeEngineBuildID {
			return nil, ErrRGB11Inconsistent
		}
		canonical, encodeErr := rgb11wallet.EncodeWalletSnapshotPayload(snapshot)
		if encodeErr != nil {
			return nil, encodeErr
		}
		stateHash := sha256.Sum256(canonical)
		nextSeq := next[headKey]
		if next[snapshotKey] != nextSeq || head.Seq+1 != nextSeq {
			return nil, coresync.ErrHeadSequence
		}
		var sequence [8]byte
		binary.LittleEndian.PutUint64(sequence[:], nextSeq)
		operationInput := append([]byte("SAT20-RGB11-WALLET-SNAPSHOT-V1"), stateHash[:]...)
		newOperationID := sha256.Sum256(append(operationInput, sequence[:]...))
		newHead, headErr := NewRGB11WalletHead(walletID, stateHash, newOperationID, head)
		if headErr != nil || newHead.Seq != nextSeq {
			return nil, coresync.ErrHeadSequence
		}
		newCiphertext, encryptErr := cryptor.EncryptTo(pubkey, canonical)
		if encryptErr != nil {
			return nil, encryptErr
		}
		envelope, encodeErr := rgb11wallet.EncodeEncryptedSnapshot(walletID, newOperationID, newCiphertext)
		if encodeErr != nil {
			return nil, encodeErr
		}
		headBytes, encodeErr := newHead.StrictEncode()
		if encodeErr != nil {
			return nil, encodeErr
		}
		result.Migrated = true
		result.CurrentSeq = newHead.Seq
		return []dkvsValueMutation{
			{Key: snapshotKey, Value: envelope, Owner: p.wallet, Signature: dkvsSignatureAccount, Policy: policy},
			{Key: headKey, Value: headBytes, Owner: p.wallet, Signature: dkvsSignatureAccount, Policy: policy},
		}, nil
	})
	if err != nil {
		if errors.Is(err, dkvsindexer.ErrWriteConflict) || errors.Is(err, dkvsindexer.ErrInvalidSequence) ||
			errors.Is(err, dkvsindexer.ErrStaleGeneration) || errors.Is(err, dkvsindexer.ErrPathDiverged) {
			return nil, coresync.ErrHeadConflict
		}
		return nil, err
	}
	if !result.Migrated {
		return result, nil
	}
	return result, nil
}
