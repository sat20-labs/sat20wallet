package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	coresync "github.com/sat20-labs/rgb11/sync"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"math/big"
	"sort"
	"strings"
	"time"
)

const (
	rgb11WalletSnapshotVersion  uint32 = 1
	rgb11AutoBackupMetadataName        = "autobackup-policy"
)

type RGB11AutoBackupPolicy struct {
	Version      uint32 `json:"version"`
	Enabled      bool   `json:"enabled"`
	TTL          uint64 `json:"ttl"`
	ExpiryHeight uint64 `json:"expiry_height,omitempty"`
}

type RGB11ActivationResult struct {
	Found      bool                 `json:"found"`
	Restored   bool                 `json:"restored"`
	AutoBackup bool                 `json:"auto_backup"`
	Head       *coresync.WalletHead `json:"head,omitempty"`
}

type RGB11WalletSnapshot struct {
	Version           uint32                       `json:"version"`
	WalletID          string                       `json:"wallet_id"`
	AccountIndex      uint32                       `json:"account_index"`
	EngineBuildID     string                       `json:"engine_build_id"`
	ProjectionRecords []rgb11wallet.SnapshotRecord `json:"projection_records"`
	EngineRecords     []rgb11wallet.SnapshotRecord `json:"engine_records"`
	TickerInfos       []*indexer.TickerInfo        `json:"ticker_infos"`
}

type RGB11EncryptedSnapshot struct {
	Version     uint32   `json:"version"`
	WalletID    string   `json:"wallet_id"`
	OperationID [32]byte `json:"operation_id"`
	Ciphertext  []byte   `json:"ciphertext"`
}

type rgb11SnapshotCryptor interface {
	EncryptTo(pubKeyBytes []byte, plaintext []byte) ([]byte, error)
	Decrypt(data []byte, pubKeyBytes []byte) ([]byte, error)
}

func (p *Manager) RGB11WalletID() (string, error) {
	if p == nil || p.wallet == nil || p.wallet.GetPubKey() == nil {
		return "", ErrRGB11WalletLocked
	}
	return hex.EncodeToString(p.wallet.GetPubKey().SerializeCompressed()), nil
}

func (p *Manager) exportRGB11WalletSnapshot(walletID string) (*RGB11WalletSnapshot, []byte, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.engineStore == nil || p.wallet == nil || walletID == "" {
		return nil, nil, ErrRGB11Inconsistent
	}
	projection, err := p.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		return nil, nil, err
	}
	engine, err := p.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		return nil, nil, err
	}
	p.mutex.RLock()
	tickers := make([]*indexer.TickerInfo, 0)
	for _, info := range p.tickerInfoMap {
		if info != nil && info.AssetName.Protocol == rgb11wallet.Protocol {
			copy := *info
			tickers = append(tickers, &copy)
		}
	}
	p.mutex.RUnlock()
	sort.Slice(tickers, func(i, j int) bool { return tickers[i].AssetName.String() < tickers[j].AssetName.String() })
	snapshot := &RGB11WalletSnapshot{
		Version: rgb11WalletSnapshotVersion, WalletID: walletID,
		AccountIndex: p.wallet.GetSubAccount(), EngineBuildID: rgb11wallet.NativeEngineBuildID,
		ProjectionRecords: projection, EngineRecords: engine, TickerInfos: tickers,
	}
	encoded, err := encodeRGB11WalletSnapshotPayload(snapshot)
	return snapshot, encoded, err
}

// BackupRGB11WalletState publishes an encrypted immutable snapshot and then
// advances the single latest head record. Both records are signed only by the
// owning wallet through DKVS /personal; there is no system/checkpoint key.
func (p *Manager) BackupRGB11WalletState(client *SatsNetDKVSClient, walletID string,
	previous *coresync.WalletHead, opts dkvsindexer.RecordOptions) (*coresync.WalletHead, *swire.DKVSRecord, error) {
	if client == nil || opts.TTL == 0 || p == nil || p.wallet == nil {
		return nil, nil, ErrRGB11Inconsistent
	}
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, nil, err
	}
	if walletID == "" {
		walletID = stableID
	} else if walletID != stableID {
		return nil, nil, coresync.ErrHeadWallet
	}
	_, plaintext, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, nil, err
	}
	stateHash := sha256.Sum256(plaintext)
	operationInput := append([]byte("SAT20-RGB11-WALLET-SNAPSHOT-V1"), stateHash[:]...)
	nextSeq := uint64(1)
	if previous != nil {
		nextSeq = previous.Seq + 1
	}
	var sequence [8]byte
	binary.LittleEndian.PutUint64(sequence[:], nextSeq)
	operationInput = append(operationInput, sequence[:]...)
	operationID := sha256.Sum256(operationInput)
	head, err := NewRGB11WalletHead(walletID, stateHash, operationID, previous)
	if err != nil {
		return nil, nil, err
	}
	cryptor, ok := p.wallet.(rgb11SnapshotCryptor)
	if !ok {
		return nil, nil, fmt.Errorf("active wallet does not support RGB11 snapshot encryption")
	}
	pubkey := p.wallet.GetPubKey().SerializeCompressed()
	ciphertext, err := cryptor.EncryptTo(pubkey, plaintext)
	if err != nil {
		return nil, nil, err
	}
	envelope, err := encodeRGB11EncryptedSnapshot(walletID, operationID, ciphertext)
	if err != nil {
		return nil, nil, err
	}
	autopay := DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet()}
	if _, err := client.PutRGB11WalletSnapshotWithAutopay(p.wallet, walletID, operationID, envelope, opts, autopay); err != nil {
		return nil, nil, err
	}
	record, err := client.PutRGB11WalletHeadWithAutopay(p.wallet, head, opts, autopay)
	if err != nil {
		return nil, nil, err
	}
	if err := p.persistRGB11WalletHead(head); err != nil {
		p.rgbManager.dkvsStatus = "warning"
		return nil, nil, err
	}
	p.rgbManager.dkvsStatus = "synced"
	p.rgbManager.head = head
	return head, record, nil
}

// RestoreRGB11WalletState resolves the active latest wallet-signed head,
// decrypts its immutable snapshot and imports it into the current local scope.
func (p *Manager) RestoreRGB11WalletState(client *SatsNetDKVSClient, walletID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, error) {
	if client == nil || p == nil || p.wallet == nil || p.rgbManager.projectionStore == nil || p.rgbManager.engineStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	if walletID == "" {
		walletID = stableID
	} else if walletID != stableID {
		return nil, coresync.ErrHeadWallet
	}
	pubkey := p.wallet.GetPubKey().SerializeCompressed()
	head, _, err := client.GetRGB11WalletHead(pubkey, walletID, verifyOpts)
	if err != nil {
		p.rgbManager.dkvsStatus = "offline"
		return nil, err
	}
	raw, _, err := client.GetRGB11WalletSnapshot(pubkey, walletID, head.OperationID, verifyOpts)
	if err != nil {
		p.rgbManager.dkvsStatus = "conflict"
		return nil, err
	}
	envelopeWalletID, envelopeOperationID, ciphertext, err := decodeRGB11EncryptedSnapshot(raw)
	if err != nil || envelopeWalletID != walletID || envelopeOperationID != head.OperationID || len(ciphertext) == 0 {
		p.rgbManager.dkvsStatus = "conflict"
		return nil, ErrRGB11Inconsistent
	}
	cryptor, ok := p.wallet.(rgb11SnapshotCryptor)
	if !ok {
		return nil, fmt.Errorf("active wallet does not support RGB11 snapshot decryption")
	}
	plaintext, err := cryptor.Decrypt(ciphertext, pubkey)
	if err != nil || !bytes.Equal(head.StateHash[:], hashBytes(plaintext)) {
		p.rgbManager.dkvsStatus = "conflict"
		return nil, ErrRGB11Inconsistent
	}
	snapshot, err := decodeRGB11WalletSnapshotPayload(plaintext)
	if err != nil || snapshot.Version != rgb11WalletSnapshotVersion ||
		snapshot.WalletID != walletID || snapshot.AccountIndex != p.wallet.GetSubAccount() ||
		snapshot.EngineBuildID != rgb11wallet.NativeEngineBuildID {
		p.rgbManager.dkvsStatus = "conflict"
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
		p.rgbManager.dkvsStatus = "warning"
		return nil, err
	}
	p.rgbManager.dkvsStatus = "synced"
	p.rgbManager.head = head
	return head, nil
}

func (p *Manager) rgb11DKVSClient() (*SatsNetDKVSClient, error) {
	if !p.rgb11DKVSConfigured() {
		return nil, ErrRGB11Inconsistent
	}
	return NewSatsNetDKVSClient(p.cfg.IndexerL2.Scheme, p.cfg.IndexerL2.Host, p.cfg.IndexerL2.Proxy, p.http), nil
}

func (p *Manager) rgb11DKVSConfigured() bool {
	return p != nil && p.cfg != nil && p.http != nil && p.cfg.IndexerL2 != nil && p.cfg.IndexerL2.Host != ""
}

// requireLatestRGB11WalletState is the last guard before an irreversible
// external effect. When DKVS is configured, the local state must still match
// the wallet-signed head currently selected for this wallet.
func (p *Manager) requireLatestRGB11WalletState() error {
	p.waitForRGB11AutoBackup()
	if !p.rgb11DKVSConfigured() {
		return nil
	}
	if p.rgbManager.head == nil {
		p.rgbManager.dkvsStatus = "conflict"
		return coresync.ErrHeadConflict
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return err
	}
	_, plaintext, err := p.exportRGB11WalletSnapshot(walletID)
	if err != nil || sha256.Sum256(plaintext) != p.rgbManager.head.StateHash {
		p.rgbManager.dkvsStatus = "conflict"
		return coresync.ErrHeadConflict
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return err
	}
	active, _, err := client.GetRGB11WalletHead(
		p.wallet.GetPubKey().SerializeCompressed(), walletID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())},
	)
	if err != nil {
		p.rgbManager.dkvsStatus = "offline"
		return err
	}
	localHash, err := p.rgbManager.head.Hash()
	if err != nil {
		return err
	}
	activeHash, err := active.Hash()
	if err != nil || localHash != activeHash {
		p.rgbManager.dkvsStatus = "conflict"
		return coresync.ErrHeadConflict
	}
	p.rgbManager.dkvsStatus = "synced"
	return nil
}

func (p *Manager) SyncRGB11WalletState(walletID string, opts dkvsindexer.RecordOptions) (*coresync.WalletHead, error) {
	return p.syncRGB11WalletState(walletID, opts, true)
}

func (p *Manager) syncRGB11WalletState(walletID string, opts dkvsindexer.RecordOptions, enableAuto bool) (*coresync.WalletHead, error) {
	p.rgbManager.backupMutex.Lock()
	defer p.rgbManager.backupMutex.Unlock()
	if !enableAuto && p.rgbManager.head != nil {
		stableID, err := p.RGB11WalletID()
		if err == nil {
			_, plaintext, exportErr := p.exportRGB11WalletSnapshot(stableID)
			if exportErr == nil && sha256.Sum256(plaintext) == p.rgbManager.head.StateHash {
				return p.rgbManager.head, nil
			}
		}
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	head, _, err := p.BackupRGB11WalletState(client, walletID, p.rgbManager.head, opts)
	if err != nil {
		return nil, err
	}
	if enableAuto {
		if err := p.enableRGB11AutoBackup(opts); err != nil {
			p.rgbManager.dkvsStatus = "warning"
			return nil, err
		}
	}
	return head, nil
}

func (p *Manager) RestoreLatestRGB11WalletState(walletID string,
	verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, error) {
	p.rgbManager.backupMutex.Lock()
	defer p.rgbManager.backupMutex.Unlock()
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	stableID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	if walletID == "" {
		walletID = stableID
	}
	_, record, err := client.GetRGB11WalletHead(p.wallet.GetPubKey().SerializeCompressed(), walletID, verifyOpts)
	if err != nil {
		return nil, err
	}
	head, err := p.RestoreRGB11WalletState(client, walletID, verifyOpts)
	if err != nil {
		return nil, err
	}
	if err := p.enableRGB11AutoBackup(dkvsindexer.RecordOptions{TTL: record.TTL, ExpiryHeight: record.ExpiryHeight}); err != nil {
		p.rgbManager.dkvsStatus = "warning"
		return nil, err
	}
	return head, nil
}

// ActivateRGB11WalletState is called after a wallet/account becomes active. A
// missing wallet-signed head means this wallet has never enabled RGB backup;
// in that case no paid write is attempted and the first backup remains a
// manual user action. When a head exists, the latest snapshot is restored and
// its retention policy enables subsequent automatic backups.
func (p *Manager) ActivateRGB11WalletState(verifyOpts dkvsindexer.RecordVerificationOptions) (*RGB11ActivationResult, error) {
	result := &RGB11ActivationResult{}
	if !p.rgb11DKVSConfigured() || p.wallet == nil || p.wallet.GetPubKey() == nil {
		return result, nil
	}
	if verifyOpts.Now == 0 {
		verifyOpts.Now = uint64(time.Now().UnixMilli())
	}
	walletID, err := p.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	client, err := p.rgb11DKVSClient()
	if err != nil {
		return nil, err
	}
	_, _, err = client.GetRGB11WalletHead(p.wallet.GetPubKey().SerializeCompressed(), walletID, verifyOpts)
	if errors.Is(err, ErrDKVSRecordNotFound) {
		paid, paidErr := p.hasActiveRGB11Autopay()
		if paidErr != nil {
			p.rgbManager.dkvsStatus = "offline"
			return nil, paidErr
		}
		if !paid {
			p.rgbManager.dkvsStatus = "not_configured"
			return result, nil
		}
		head, syncErr := p.SyncRGB11WalletState("", dkvsindexer.RecordOptions{
			TTL: uint64((365 * 24 * time.Hour) / time.Millisecond),
		})
		if syncErr != nil {
			p.rgbManager.dkvsStatus = "warning"
			return nil, syncErr
		}
		result.Found = true
		result.AutoBackup = true
		result.Head = head
		return result, nil
	}
	if err != nil {
		p.rgbManager.dkvsStatus = "offline"
		return nil, err
	}
	result.Found = true
	head, err := p.RestoreLatestRGB11WalletState(walletID, verifyOpts)
	if err != nil {
		return nil, err
	}
	result.Restored = true
	result.AutoBackup = true
	result.Head = head
	return result, nil
}

// hasActiveRGB11Autopay checks the same active delegate properties required by
// the DKVS AUTOPAY verifier. The subsequent DKVS write remains authoritative.
func (p *Manager) hasActiveRGB11Autopay() (bool, error) {
	if p == nil || p.wallet == nil || p.wallet.GetPubKey() == nil || p.l2IndexerClient == nil {
		return false, nil
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	if !defaults.Enabled || defaults.AutopayContract == "" {
		return false, nil
	}
	raw, err := p.l2IndexerClient.GetContractStateJSON(defaults.AutopayContract)
	if err != nil {
		return false, err
	}
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
	if err != nil {
		return false, err
	}
	if state == nil || state.TemplateName != TEMPLATE_CONTRACT_AUTOPAY ||
		!strings.EqualFold(strings.TrimSpace(state.Status), "active") || state.Closed ||
		!strings.EqualFold(strings.TrimSpace(state.ServiceName), defaults.AutopayServiceName) ||
		!strings.EqualFold(strings.TrimSpace(state.Recipient), defaults.AutopayRecipient) ||
		strings.TrimSpace(state.FeeAssetName) != defaults.AutopayFeeAssetName {
		return false, nil
	}
	payer := PublicKeyToP2TRAddress_SatsNet(p.wallet.GetPubKey())
	delegate, ok := state.Delegates[payer]
	if !ok || !strings.EqualFold(strings.TrimSpace(delegate.Status), "active") {
		return false, nil
	}
	amount, amountOK := new(big.Rat).SetString(strings.TrimSpace(delegate.AmountPerBlock))
	balance, balanceOK := new(big.Rat).SetString(strings.TrimSpace(delegate.Balance))
	fullRecordFee, feeOK := new(big.Rat).SetString(strings.TrimSpace(defaults.FullRecordFeePerBlock))
	if !amountOK || !balanceOK || !feeOK || amount.Sign() <= 0 || balance.Sign() < 0 ||
		fullRecordFee.Sign() <= 0 || amount.Cmp(fullRecordFee) < 0 {
		return false, nil
	}
	return balance.Cmp(amount) >= 0, nil
}

func (p *Manager) enableRGB11AutoBackup(opts dkvsindexer.RecordOptions) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || opts.TTL == 0 {
		return ErrRGB11Inconsistent
	}
	policy := &RGB11AutoBackupPolicy{
		Version: 1, Enabled: true, TTL: opts.TTL, ExpiryHeight: opts.ExpiryHeight,
	}
	encoded, err := encodeRGB11AutoBackupPolicy(policy)
	if err != nil {
		return err
	}
	if err := p.rgbManager.projectionStore.SaveLocalMetadata(rgb11AutoBackupMetadataName, encoded); err != nil {
		return err
	}
	p.mutex.Lock()
	p.rgbManager.autoBackup = policy
	p.mutex.Unlock()
	return nil
}

func (p *Manager) loadRGB11AutoBackupPolicy() (*RGB11AutoBackupPolicy, error) {
	encoded, err := p.rgbManager.projectionStore.LoadLocalMetadata(rgb11AutoBackupMetadataName)
	if err != nil {
		return nil, err
	}
	return decodeRGB11AutoBackupPolicy(encoded)
}

func (p *Manager) autoBackupRGB11AfterMutation() {
	if p == nil || p.rgbManager == nil {
		return
	}
	p.mutex.RLock()
	var policy RGB11AutoBackupPolicy
	if p.rgbManager.autoBackup != nil {
		policy = *p.rgbManager.autoBackup
	}
	p.mutex.RUnlock()
	if !policy.Enabled || policy.TTL == 0 {
		return
	}
	p.rgbManager.autoBackupMutex.Lock()
	p.rgbManager.autoBackupPending = true
	if p.rgbManager.autoBackupRunning {
		p.rgbManager.autoBackupMutex.Unlock()
		return
	}
	p.rgbManager.autoBackupRunning = true
	p.rgbManager.autoBackupDone = make(chan struct{})
	p.rgbManager.autoBackupMutex.Unlock()
	go p.runRGB11AutoBackup()
}

func (p *Manager) runRGB11AutoBackup() {
	for {
		p.rgbManager.autoBackupMutex.Lock()
		if !p.rgbManager.autoBackupPending {
			p.rgbManager.autoBackupRunning = false
			close(p.rgbManager.autoBackupDone)
			p.rgbManager.autoBackupMutex.Unlock()
			return
		}
		p.rgbManager.autoBackupPending = false
		p.rgbManager.autoBackupMutex.Unlock()

		p.mutex.RLock()
		var policy RGB11AutoBackupPolicy
		if p.rgbManager.autoBackup != nil {
			policy = *p.rgbManager.autoBackup
		}
		p.mutex.RUnlock()
		if !policy.Enabled || policy.TTL == 0 {
			continue
		}

		started := time.Now()
		Log.Infof("automatic RGB11 wallet backup started")
		if _, err := p.syncRGB11WalletState("", dkvsindexer.RecordOptions{
			TTL: policy.TTL, ExpiryHeight: policy.ExpiryHeight,
		}, false); err != nil {
			p.rgbManager.dkvsStatus = "warning"
			Log.Errorf("automatic RGB11 wallet backup failed after %v: %v", time.Since(started), err)
			continue
		}
		Log.Infof("automatic RGB11 wallet backup finished in %v", time.Since(started))
	}
}

func (p *Manager) waitForRGB11AutoBackup() {
	if p == nil || p.rgbManager == nil {
		return
	}
	p.rgbManager.autoBackupMutex.Lock()
	if !p.rgbManager.autoBackupRunning {
		p.rgbManager.autoBackupMutex.Unlock()
		return
	}
	done := p.rgbManager.autoBackupDone
	p.rgbManager.autoBackupMutex.Unlock()
	if done != nil {
		<-done
	}
}

func hashBytes(value []byte) []byte {
	hash := sha256.Sum256(value)
	return hash[:]
}

func (p *Manager) persistRGB11WalletHead(head *coresync.WalletHead) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || head == nil {
		return ErrRGB11Inconsistent
	}
	encoded, err := head.StrictEncode()
	if err != nil {
		return err
	}
	return p.rgbManager.projectionStore.SaveLocalMetadata("wallet-head", encoded)
}

// NewRGB11WalletHead creates the compact head payload. PutRGB11WalletHead
// applies the owning wallet's signature once, to the enclosing DKVS record.
func NewRGB11WalletHead(walletID string, stateHash, operationID [32]byte, previous *coresync.WalletHead) (*coresync.WalletHead, error) {
	head := &coresync.WalletHead{
		Version:     coresync.HeadVersion,
		WalletID:    walletID,
		Seq:         1,
		StateHash:   stateHash,
		OperationID: operationID,
	}
	if previous != nil {
		head.Seq = previous.Seq + 1
	}
	if err := head.ValidateSuccessor(previous); err != nil {
		return nil, err
	}
	return head, nil
}

func VerifyRGB11WalletHead(head *coresync.WalletHead, walletID string) error {
	if head == nil {
		return coresync.ErrHeadField
	}
	return head.Validate(walletID)
}

func RGB11WalletHeadPath(walletID string) string {
	return "rgb11/" + dkvsindexer.NormalizeNameID(walletID) + "/head"
}

func (p *SatsNetDKVSClient) PutRGB11WalletHead(wallet common.Wallet, head *coresync.WalletHead, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletHead(wallet, head, opts, nil)
}

func (p *SatsNetDKVSClient) PutRGB11WalletHeadWithAutopay(wallet common.Wallet, head *coresync.WalletHead,
	opts dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletHead(wallet, head, opts, &autopay)
}

func (p *SatsNetDKVSClient) putRGB11WalletHead(wallet common.Wallet, head *coresync.WalletHead,
	opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return nil, err
	}
	if err := VerifyRGB11WalletHead(head, head.WalletID); err != nil {
		return nil, err
	}
	value, err := head.StrictEncode()
	if err != nil {
		return nil, err
	}
	opts.Seq = head.Seq
	var posted *swire.DKVSRecord
	if autopay == nil {
		posted, err = p.PutPersonalRecord(wallet, RGB11WalletHeadPath(head.WalletID), value, opts)
	} else {
		posted, err = p.PutPersonalRecordWithAutopay(wallet, RGB11WalletHeadPath(head.WalletID), value, opts, *autopay)
	}
	if err != nil {
		return nil, err
	}
	_, active, err := p.GetRGB11WalletHead(pubKey, head.WalletID, dkvsindexer.RecordVerificationOptions{
		Now: uint64(time.Now().UnixMilli()),
	})
	if err != nil {
		return nil, err
	}
	if dkvsindexer.RecordHash(posted) != dkvsindexer.RecordHash(active) {
		return nil, coresync.ErrHeadConflict
	}
	return active, nil
}

func verifyRGB11DKVSAccountOwner(record *swire.DKVSRecord, walletPubKey []byte) error {
	if record == nil {
		return dkvsindexer.ErrInvalidRecord
	}
	expected, err := dkvsindexer.CanonicalAccountID(walletPubKey)
	if err != nil {
		return dkvsindexer.ErrPermissionDenied
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		return err
	}
	actual, err := dkvsindexer.RecordSignerAccountID(record, parsed)
	if err != nil || actual != expected {
		return dkvsindexer.ErrPermissionDenied
	}
	return nil
}

func (p *SatsNetDKVSClient) GetRGB11WalletHead(walletPubKey []byte, walletID string, verifyOpts dkvsindexer.RecordVerificationOptions) (*coresync.WalletHead, *swire.DKVSRecord, error) {
	key, err := dkvsindexer.PersonalKey(walletPubKey, RGB11WalletHeadPath(walletID))
	if err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyRGB11DKVSAccountOwner(record, walletPubKey); err != nil {
		return nil, nil, err
	}
	head, err := decodeRGB11WalletHead(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if record.Seq != head.Seq {
		return nil, nil, coresync.ErrHeadSequence
	}
	if err := VerifyRGB11WalletHead(head, walletID); err != nil {
		return nil, nil, err
	}
	return head, record, nil
}

func (p *SatsNetDKVSClient) SubscribeRGB11WalletHead(walletPubKey []byte, walletID string) ([]*swire.DKVSRecord, int, error) {
	key, err := dkvsindexer.PersonalKey(walletPubKey, RGB11WalletHeadPath(walletID))
	if err != nil {
		return nil, 0, err
	}
	return p.SubscribeKey(key)
}

func (p *SatsNetDKVSClient) PutRGB11WalletSnapshot(wallet common.Wallet, walletID string,
	operationID [32]byte, value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletSnapshot(wallet, walletID, operationID, value, opts, nil)
}

func (p *SatsNetDKVSClient) PutRGB11WalletSnapshotWithAutopay(wallet common.Wallet, walletID string,
	operationID [32]byte, value []byte, opts dkvsindexer.RecordOptions,
	autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	return p.putRGB11WalletSnapshot(wallet, walletID, operationID, value, opts, &autopay)
}

func (p *SatsNetDKVSClient) putRGB11WalletSnapshot(wallet common.Wallet, walletID string,
	operationID [32]byte, value []byte, opts dkvsindexer.RecordOptions,
	autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	if len(value) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	opts.Seq = 1
	var manifest *swire.DKVSRecord
	var err error
	if autopay == nil {
		manifest, _, err = p.PutBlob(wallet, hex.EncodeToString(operationID[:]), value, nil, opts)
	} else {
		manifest, _, err = p.PutBlobWithAutopay(wallet, hex.EncodeToString(operationID[:]), value, nil, opts, *autopay)
	}
	return manifest, err
}

func (p *SatsNetDKVSClient) GetRGB11WalletSnapshot(walletPubKey []byte, walletID string,
	operationID [32]byte, verifyOpts dkvsindexer.RecordVerificationOptions) ([]byte, *swire.DKVSRecord, error) {
	accountID := dkvsindexer.AccountID(walletPubKey)
	objectID := hex.EncodeToString(operationID[:])
	key, err := dkvsindexer.BlobManifestKey(accountID, objectID)
	if err != nil {
		return nil, nil, err
	}
	verifyOpts.ExpectedKey = key
	record, err := p.GetVerifiedRecord(key, verifyOpts)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyRGB11DKVSAccountOwner(record, walletPubKey); err != nil {
		return nil, nil, err
	}
	_, value, err := p.GetBlob(accountID, objectID, dkvsindexer.BlobPolicy{})
	if err != nil {
		return nil, nil, err
	}
	return value, record, nil
}
