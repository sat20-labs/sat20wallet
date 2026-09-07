//go:build wallet_maintenance
// +build wallet_maintenance

package wallet

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	testnetSeq2RepairAccountID = "148cbe135aea8ee9b72f18ca6ddf0efc052e54b6d723cc473a0cc6011766d776"
	testnetSeq2RepairStateHash = "e1f7f1f15c678fde383f9013db94d9aa938689c9da7fb2c1745eaa10da19d229"
	testnetSeq2RepairBlobHash  = "fbe89f3a57e7f81abfa52a33b7858ea943f2afa340f90ebdcbe6e77b7b0626af"
	testnetSeq2RepairTransfer  = "rgb:csg:DIamFgCg-ZemqatB-7gXloCx-hvXtjI7-Tx8ri15-zcvAvZU#echo-atomic-polite"
	testnetSeq2RepairSeq       = uint64(2)
	testnetSeq2RepairHeight    = uint64(3445)
	testnetSeq2RepairApply     = "APPLY-TESTNET-ACCOUNT-148CBE-SEQ2"
)

type testnetSeq2RepairSnapshot struct {
	Origin       string            `json:"origin"`
	PasswordHash string            `json:"passwordHash"`
	KeyClasses   map[string]int    `json:"keyClassCounts,omitempty"`
	Values       map[string]string `json:"values"`
}

type testnetSeq2RepairPatch struct {
	Origin      string `json:"origin"`
	Key         string `json:"key"`
	ExpectedSHA string `json:"expected_sha256"`
	Value       string `json:"value"`
	RemoteState string `json:"remote_state_hash"`
	RemoteBlob  string `json:"remote_blob_hash"`
}

type testnetSeq2RemotePair struct {
	State, Blob        *swire.DKVSRecord
	EndpointID         string
	VerificationHeight uint64
}

type testnetSeq2CASPlan struct {
	Mutations []dkvsindexer.CASMutation
	StateHash string
	BlobHash  string
}

type testnetSeq2PaidStorageSummary struct {
	Contract       string
	FeeAsset       string
	AmountPerBlock string
	RecordCount    uint64
}

type testnetSeq2RemoteClass uint8

const (
	testnetSeq2RemoteApprovedB testnetSeq2RemoteClass = iota
	testnetSeq2RemoteTargetA
	testnetSeq2RemotePartial
	testnetSeq2RemoteUnknown
)

func requireTestnetSeq2RepairConfig(cfg *common.Config, accountID string) error {
	if cfg == nil || cfg.IndexerL2 == nil || cfg.Env != "prd" || cfg.Chain != "testnet" ||
		cfg.IndexerL2.Scheme != "https" || cfg.IndexerL2.Host != "apiprd.ordx.market" ||
		cfg.IndexerL2.Proxy != "satsnet/testnet" || accountID != testnetSeq2RepairAccountID {
		return fmt.Errorf("testnet account repair target mismatch")
	}
	return nil
}

func testnetSeq2PaidAuthorization(manager *Manager, root common.Wallet) (
	AccountStorageAuthorization, testnetSeq2PaidStorageSummary, error) {
	if manager == nil || root == nil || root.GetPubKey() == nil {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{},
			fmt.Errorf("repair AUTOPAY root is unavailable")
	}
	if err := requireTestnetSeq2RepairConfig(manager.cfg, testnetSeq2RepairAccountID); err != nil {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{}, err
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	if !defaults.Enabled || defaults.AutopayContract == "" ||
		defaults.AutopayFeeAssetName == "" || !defaults.UseAutopayFeeVerifier {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{},
			fmt.Errorf("testnet AUTOPAY defaults are incomplete")
	}
	recordCount, err := normalizeAccountRecordCount(accountDefaultRecordCount)
	if err != nil {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{}, err
	}
	amountPerBlock, err := accountAmountPerBlock(defaults, recordCount)
	if err != nil {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{}, err
	}
	payer := PublicKeyToP2TRAddress_SatsNet(root.GetPubKey())
	if strings.TrimSpace(payer) == "" {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{},
			fmt.Errorf("repair AUTOPAY payer cannot be derived")
	}
	ready, err := manager.accountAutopayReady(defaults, payer, amountPerBlock)
	if err != nil {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{},
			fmt.Errorf("read testnet AUTOPAY state: %w", err)
	}
	if !ready {
		return AccountStorageAuthorization{}, testnetSeq2PaidStorageSummary{},
			fmt.Errorf("testnet AUTOPAY delegate is not active/funded for %s per block", amountPerBlock)
	}
	location := AccountIndexerLocation{Scheme: manager.cfg.IndexerL2.Scheme,
		Host: manager.cfg.IndexerL2.Host, Proxy: manager.cfg.IndexerL2.Proxy}
	authorization := accountPaidStorageAuthorization(location, defaults, recordCount,
		amountPerBlock, "", "", true)
	return *authorization, testnetSeq2PaidStorageSummary{
		Contract: defaults.AutopayContract, FeeAsset: defaults.AutopayFeeAssetName,
		AmountPerBlock: amountPerBlock, RecordCount: recordCount,
	}, nil
}

func applyTestnetSeq2PaidProfilePolicy(profile *accountManagementProfile,
	authorization AccountStorageAuthorization) error {
	if profile == nil || authorization.Mode != AccountStoragePaid ||
		authorization.Autopay == nil || authorization.RecordOptions.TTL != 0 ||
		strings.TrimSpace(authorization.Autopay.PoolContract) == "" ||
		authorization.Location.Scheme != "https" ||
		authorization.Location.Host != "apiprd.ordx.market" ||
		authorization.Location.Proxy != "satsnet/testnet" {
		return fmt.Errorf("repair requires the fixed paid testnet authorization")
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	if authorization.Autopay.PoolContract != defaults.AutopayContract {
		return fmt.Errorf("repair AUTOPAY contract differs from fixed testnet defaults")
	}
	profile.StorageMode = AccountStoragePaid
	profile.RecordTTL = 0
	profile.AutopayContract = authorization.Autopay.PoolContract
	profile.Location = authorization.Location
	return nil
}

func validateTestnetSeq2PaidRecords(records ...*swire.DKVSRecord) error {
	if len(records) == 0 {
		return fmt.Errorf("paid repair plan has no records")
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	for _, record := range records {
		if record == nil || record.Seq != 3 || record.TTL != 0 || record.IssueHeight == 0 {
			return fmt.Errorf("paid repair record has invalid seq/height/TTL")
		}
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		if err != nil || proof.Mode != dkvsindexer.FeeModeAutopay ||
			proof.PoolContract != defaults.AutopayContract {
			return fmt.Errorf("paid repair record has invalid AUTOPAY proof")
		}
	}
	return nil
}

func classifyTestnetSeq2Remote(stateHash, blobHash, targetStateHash,
	targetBlobHash string) testnetSeq2RemoteClass {
	stateB := strings.EqualFold(stateHash, testnetSeq2RepairStateHash)
	blobB := strings.EqualFold(blobHash, testnetSeq2RepairBlobHash)
	stateA := targetStateHash != "" && strings.EqualFold(stateHash, targetStateHash)
	blobA := targetBlobHash != "" && strings.EqualFold(blobHash, targetBlobHash)
	switch {
	case stateB && blobB:
		return testnetSeq2RemoteApprovedB
	case stateA && blobA:
		return testnetSeq2RemoteTargetA
	case stateB || blobB || stateA || blobA:
		return testnetSeq2RemotePartial
	default:
		return testnetSeq2RemoteUnknown
	}
}

func validateTestnetSeq2ApprovedRemote(pair testnetSeq2RemotePair,
	stateKey, blobKey string) error {
	return validateTestnetSeq2ApprovedRemoteHashes(pair, stateKey, blobKey,
		testnetSeq2RepairStateHash, testnetSeq2RepairBlobHash)
}

func validateTestnetSeq2ApprovedRemoteHashes(pair testnetSeq2RemotePair,
	stateKey, blobKey, approvedStateHash, approvedBlobHash string) error {
	if pair.State == nil || pair.Blob == nil || strings.TrimSpace(pair.EndpointID) == "" ||
		pair.VerificationHeight == 0 || pair.State.Key != stateKey || pair.Blob.Key != blobKey ||
		pair.State.Seq != testnetSeq2RepairSeq || pair.Blob.Seq != testnetSeq2RepairSeq ||
		pair.State.IssueHeight != testnetSeq2RepairHeight ||
		pair.Blob.IssueHeight != testnetSeq2RepairHeight ||
		pair.State.IssueHeight > pair.VerificationHeight || pair.Blob.IssueHeight > pair.VerificationHeight ||
		dkvsindexer.RecordHash(pair.State).String() != approvedStateHash ||
		dkvsindexer.RecordHash(pair.Blob).String() != approvedBlobHash {
		return fmt.Errorf("remote Seq2 records do not match the approved B pair from one subscription snapshot")
	}
	return nil
}

func testnetSeq2ApplyEnabled(value string) bool { return value == testnetSeq2RepairApply }

func testnetSeq2CommitAfterVerification(verify, commit func() error) error {
	if verify == nil || commit == nil {
		return fmt.Errorf("repair verification and commit are required")
	}
	if err := verify(); err != nil {
		return err
	}
	return commit()
}

func buildTestnetSeq2CASPlan(pair testnetSeq2RemotePair, stateRecord,
	blobRecord *swire.DKVSRecord) (*testnetSeq2CASPlan, error) {
	return buildTestnetSeq2CASPlanHashes(pair, stateRecord, blobRecord,
		testnetSeq2RepairStateHash, testnetSeq2RepairBlobHash)
}

func buildTestnetSeq2CASPlanHashes(pair testnetSeq2RemotePair, stateRecord,
	blobRecord *swire.DKVSRecord, approvedStateHash,
	approvedBlobHash string) (*testnetSeq2CASPlan, error) {
	if stateRecord == nil || blobRecord == nil || stateRecord.Seq != 3 || blobRecord.Seq != 3 ||
		stateRecord.IssueHeight != pair.VerificationHeight ||
		blobRecord.IssueHeight != pair.VerificationHeight {
		return nil, fmt.Errorf("replacement records must be Seq3 at the trusted height")
	}
	if err := validateTestnetSeq2ApprovedRemoteHashes(pair, stateRecord.Key, blobRecord.Key,
		approvedStateHash, approvedBlobHash); err != nil {
		return nil, err
	}
	stateHash, blobHash := dkvsindexer.RecordHash(pair.State), dkvsindexer.RecordHash(pair.Blob)
	return &testnetSeq2CASPlan{
		Mutations: []dkvsindexer.CASMutation{
			{Record: stateRecord, Precondition: dkvsindexer.WritePrecondition{ExpectedHash: &stateHash}},
			{Record: blobRecord, Precondition: dkvsindexer.WritePrecondition{ExpectedHash: &blobHash}},
		},
		StateHash: dkvsindexer.RecordHash(stateRecord).String(),
		BlobHash:  dkvsindexer.RecordHash(blobRecord).String(),
	}, nil
}

func testnetSeq2SnapshotContainsTransfer(bundle account.ManagedDataBundle,
	transferID string) bool {
	for _, item := range bundle.Items {
		if item.Provider != rgb11AccountManagedProviderID {
			continue
		}
		pkg, err := rgb11wallet.DecodeRecoveryPackage(item.Payload)
		if err != nil {
			continue
		}
		snapshot, err := pkg.WalletSnapshot()
		if err != nil {
			continue
		}
		for _, record := range snapshot.ProjectionRecords {
			if record.Key == "pending-"+transferID {
				return true
			}
		}
	}
	return false
}

func addTestnetSeq2PreparedRGB11Payload(bundle account.ManagedDataBundle, scope string,
	payload []byte) (account.ManagedDataBundle, string, error) {
	if strings.TrimSpace(scope) == "" || len(payload) == 0 {
		return account.ManagedDataBundle{}, "", fmt.Errorf("prepared RGB11 managed payload is incomplete")
	}
	for _, item := range bundle.Items {
		if item.Provider == rgb11AccountManagedProviderID && item.Scope == scope {
			return account.ManagedDataBundle{}, "", fmt.Errorf("prepared RGB11 logical scope already has a different canonical payload")
		}
	}
	bundle.Items = append(bundle.Items, account.ManagedDataItem{
		Provider: rgb11AccountManagedProviderID, Scope: scope,
		Payload: append([]byte(nil), payload...),
	})
	normalized, err := account.NormalizeManagedDataBundle(bundle)
	if err != nil {
		return account.ManagedDataBundle{}, "", err
	}
	hash, err := accountManagedDataContentHash(normalized.Items)
	return normalized, hash, err
}

func parseTestnetSeq2PendingScope(key, transferID string) (int64, uint32, bool) {
	const prefix = "rgb11-wallet-"
	const accountMarker = "-account-"
	suffix := "-rgb11v2-pending-" + transferID
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
		return 0, 0, false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
	parts := strings.Split(middle, accountMarker)
	if len(parts) != 2 {
		return 0, 0, false
	}
	walletID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || walletID <= 0 {
		return 0, 0, false
	}
	accountIndex, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0, 0, false
	}
	return walletID, uint32(accountIndex), true
}

func testnetSeq2SnapshotKeyClass(key string) string {
	switch {
	case key == "prd-testnet-"+accountManagementProfileDBKey:
		return "profile"
	case key == DB_KEY_STATUS:
		return "status"
	case strings.HasPrefix(key, DB_KEY_WALLET):
		return "wallet"
	case strings.HasPrefix(key, "rgb11-engine-wallet-"):
		return "engine"
	case strings.HasPrefix(key, "rgb11-local-wallet-"):
		return "rgbLocal"
	case strings.HasPrefix(key, "rgb11-wallet-"):
		return "projection"
	case strings.HasPrefix(key, "prd-testnet-"+DB_KEY_LOCKEDUTXO):
		return "locker"
	case strings.HasPrefix(key, "prd-testnet-"+DB_KEY_LOCK_LASTTIME):
		return "lockTime"
	case strings.HasPrefix(key, "prd-testnet-"+DB_KEY_TICKER_INFO):
		return "ticker"
	case strings.HasPrefix(key, string(dkvsOutboxPrefix)):
		return "outbox"
	default:
		return ""
	}
}

func validateTestnetSeq2SnapshotKeys(values map[string]string) error {
	counts := make(map[string]int)
	pending := 0
	for key := range values {
		class := testnetSeq2SnapshotKeyClass(key)
		if class == "" {
			return fmt.Errorf("repair snapshot contains an unapproved key class")
		}
		counts[class]++
		if _, _, ok := parseTestnetSeq2PendingScope(key, testnetSeq2RepairTransfer); ok {
			pending++
		}
	}
	if counts["profile"] != 1 || counts["status"] != 1 || counts["wallet"] < 1 ||
		counts["projection"] < 1 || counts["engine"] < 1 || counts["rgbLocal"] < 1 ||
		counts["locker"] < 1 || pending != 1 {
		return fmt.Errorf("incomplete repair snapshot key classes: profile=%d status=%d wallet=%d projection=%d engine=%d rgb_local=%d locker=%d target_pending=%d",
			counts["profile"], counts["status"], counts["wallet"], counts["projection"],
			counts["engine"], counts["rgbLocal"], counts["locker"], pending)
	}
	return nil
}

func testnetSeq2PreparedRGB11Manager(manager *Manager) (*rgb11Manager, error) {
	if manager == nil || manager.db == nil {
		return nil, fmt.Errorf("repair RGB11 database is unavailable")
	}
	var walletID int64
	var accountIndex uint32
	matches := 0
	err := manager.db.BatchRead([]byte("rgb11-wallet-"), false, func(key, _ []byte) error {
		id, index, ok := parseTestnetSeq2PendingScope(string(key), testnetSeq2RepairTransfer)
		if !ok {
			return nil
		}
		walletID, accountIndex, matches = id, index, matches+1
		return nil
	})
	if err != nil {
		return nil, err
	}
	if matches != 1 {
		return nil, fmt.Errorf("repair snapshot contains %d exact RGB11 pending records", matches)
	}
	manager.mutex.RLock()
	info := manager.walletInfoMap[walletID]
	if info == nil || info.Wallet == nil {
		manager.mutex.RUnlock()
		return nil, fmt.Errorf("prepared RGB11 wallet scope is absent or locked in the encrypted wallet catalog")
	}
	accountCount := info.Accounts
	if accountCount < 1 {
		accountCount = 1
	}
	if uint64(accountIndex) >= uint64(accountCount) {
		manager.mutex.RUnlock()
		return nil, fmt.Errorf("prepared RGB11 account scope exceeds the encrypted wallet catalog")
	}
	wallet := info.Wallet.Clone()
	manager.mutex.RUnlock()
	wallet.SetSubAccount(accountIndex)
	if wallet.GetPubKey() == nil || wallet.GetAddress() == "" {
		return nil, fmt.Errorf("prepared RGB11 wallet scope cannot derive its account")
	}
	return manager.newScopedRGB11Manager(localRGB11Account{
		WalletID: walletID, AccountIndex: accountIndex,
		Address: wallet.GetAddress(), Wallet: wallet,
	})
}

func testnetSeq2ExactRecord(snapshot *dkvsindexer.PrefixSnapshot,
	key string) (*swire.DKVSRecord, error) {
	if snapshot == nil || strings.TrimSpace(snapshot.EndpointID) == "" || snapshot.ViewHeight == 0 {
		return nil, fmt.Errorf("missing DKVS subscription snapshot")
	}
	var result *swire.DKVSRecord
	for _, record := range snapshot.Records {
		if record != nil && record.Key == key {
			if result != nil {
				return nil, fmt.Errorf("duplicate DKVS key in subscription snapshot")
			}
			result = record
		}
	}
	if result == nil {
		return nil, ErrDKVSRecordNotFound
	}
	return result, nil
}

func readTestnetSeq2Remote(client *SatsNetDKVSClient, stateKey,
	blobKey string) (testnetSeq2RemotePair, error) {
	statePath, err := dkvsindexer.CollectionPathForKey(stateKey)
	if err != nil {
		return testnetSeq2RemotePair{}, err
	}
	blobPath, err := dkvsindexer.CollectionPathForKey(blobKey)
	if err != nil {
		return testnetSeq2RemotePair{}, err
	}
	stateSnapshot, err := client.GetPrefixSnapshot(statePath)
	if err != nil {
		return testnetSeq2RemotePair{}, err
	}
	blobSnapshot, err := client.GetPrefixSnapshot(blobPath)
	if err != nil {
		return testnetSeq2RemotePair{}, err
	}
	if stateSnapshot.EndpointID != blobSnapshot.EndpointID {
		return testnetSeq2RemotePair{}, dkvsindexer.ErrEndpointMismatch
	}
	state, err := testnetSeq2ExactRecord(stateSnapshot, stateKey)
	if err != nil {
		return testnetSeq2RemotePair{}, err
	}
	blob, err := testnetSeq2ExactRecord(blobSnapshot, blobKey)
	if err != nil {
		return testnetSeq2RemotePair{}, err
	}
	config, err := client.GetDKVSClientConfig()
	if err != nil {
		return testnetSeq2RemotePair{}, err
	}
	if config.EndpointID != stateSnapshot.EndpointID {
		return testnetSeq2RemotePair{}, dkvsindexer.ErrEndpointMismatch
	}
	verificationHeight := stateSnapshot.ViewHeight
	if blobSnapshot.ViewHeight < verificationHeight {
		verificationHeight = blobSnapshot.ViewHeight
	}
	return testnetSeq2RemotePair{
		State: state, Blob: blob, EndpointID: stateSnapshot.EndpointID,
		VerificationHeight: verificationHeight,
	}, nil
}

func enterTestnetSeq2RepairDBScope() func() {
	oldMode, oldEnv, oldChain := _mode, _env, _chain
	_mode, _env, _chain = LIGHT_NODE, "prd", "testnet"
	return func() { _mode, _env, _chain = oldMode, oldEnv, oldChain }
}

func loadTestnetSeq2RepairManager(path string) (*Manager, string, string, func(), error) {
	restore := enterTestnetSeq2RepairDBScope()
	manager, profileKey, oldProfile, err := loadTestnetSeq2RepairManagerInScope(path)
	if err != nil {
		restore()
		return nil, "", "", nil, err
	}
	return manager, profileKey, oldProfile, restore, nil
}

func loadTestnetSeq2RepairManagerInScope(path string) (*Manager, string, string, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, "", "", err
	}
	defer zeroBytes(encoded)
	var snapshot testnetSeq2RepairSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return nil, "", "", err
	}
	if snapshot.Origin != "http://localhost:5173" || snapshot.PasswordHash == "" {
		return nil, "", "", fmt.Errorf("unexpected PWA snapshot origin or password derivation")
	}
	if err := validateTestnetSeq2SnapshotKeys(snapshot.Values); err != nil {
		return nil, "", "", err
	}
	database := newMemoryKVDB()
	profileKey, oldProfile := "", ""
	for key, value := range snapshot.Values {
		raw, decodeErr := base64.StdEncoding.DecodeString(value)
		if decodeErr != nil {
			return nil, "", "", decodeErr
		}
		if err := database.Write([]byte(key), raw); err != nil {
			zeroBytes(raw)
			return nil, "", "", err
		}
		if strings.HasSuffix(key, accountManagementProfileDBKey) {
			profileKey, oldProfile = key, value
		}
		zeroBytes(raw)
	}
	if profileKey == "" {
		return nil, "", "", fmt.Errorf("account profile is absent from snapshot")
	}
	cfg := &common.Config{Env: "prd", Chain: "testnet", Mode: LIGHT_NODE,
		IndexerL2: &common.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"}}
	httpClient := NewHTTPClient()
	l2Indexer := NewIndexerRPCClientMgr()
	l2Indexer.Set(NewIndexerClient(cfg.IndexerL2.Scheme, cfg.IndexerL2.Host,
		cfg.IndexerL2.Proxy, httpClient))
	manager := &Manager{
		db: database, cfg: cfg, http: httpClient, l2IndexerClient: l2Indexer,
		walletInfoMap:        make(map[int64]*WalletInfo),
		tickerInfoMap:        make(map[string]*indexer.TickerInfo),
		managedDataProviders: make(map[string]AccountManagedDataProvider),
	}
	manager.status = loadStatusFromDB(database)
	manager.walletInfoMap, err = loadAllWalletFromDB(database)
	if err != nil {
		return nil, "", "", err
	}
	manager.utxoLockerL1 = NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)
	manager.utxoLockerL2 = NewUtxoLocker(database, nil, L2_NETWORK_SATOSHI)
	manager.rgbManager, err = newRGB11Manager(manager, database, manager.utxoLockerL1, nil)
	if err != nil {
		return nil, "", "", err
	}
	if err := manager.RegisterAccountManagedDataProvider(&rgb11AccountManagedDataProvider{owner: manager}); err != nil {
		return nil, "", "", err
	}
	if err := manager.loadAccountManagementProfileLocked(); err != nil {
		return nil, "", "", err
	}
	if manager.accountProfile == nil || manager.accountProfile.AccountID != testnetSeq2RepairAccountID {
		return nil, "", "", fmt.Errorf("local account profile does not match repair target")
	}
	if _, err := manager.unlockWallet(snapshot.PasswordHash); err != nil {
		return nil, "", "", err
	}
	manager.ensureDKVSManager()
	return manager, profileKey, oldProfile, nil
}

func buildTestnetSeq2Target(manager *Manager, height uint64,
	authorization AccountStorageAuthorization) (*testnetSeq2CASPlan,
	account.ManagedState, *accountManagedDataSnapshot, *accountManagementSyncSnapshot,
	common.Wallet, error) {
	manager.mutex.Lock()
	snapshot, err := manager.captureAccountManagementSyncSnapshotLocked()
	manager.mutex.Unlock()
	if err != nil || snapshot == nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	if err := applyTestnetSeq2PaidProfilePolicy(&snapshot.profile, authorization); err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	localState, err := account.OpenManagedState(snapshot.secret, snapshot.profile.AccountID,
		snapshot.profile.StateEnvelope)
	if err != nil || localState.RootFingerprint != snapshot.profile.RootFingerprint {
		return nil, account.ManagedState{}, nil, nil, nil, fmt.Errorf("local A state/root verification failed")
	}
	if _, err := openProfileManagedDataBundle(snapshot.profile, snapshot.secret); err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, fmt.Errorf("local A managed data verification failed: %w", err)
	}
	preparedRGB11, err := testnetSeq2PreparedRGB11Manager(manager)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	resume, err := preparedRGB11.ResumeRGB11PreparedTransfer(testnetSeq2RepairTransfer)
	if err != nil || resume == nil || resume.State == nil ||
		(resume.State.Status != "prepared" && resume.State.Status != "relayed") {
		return nil, account.ManagedState{}, nil, nil, nil, fmt.Errorf("original RGB11 prepared transfer is unavailable")
	}
	walletID, err := preparedRGB11.RGB11WalletID()
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	full, _, err := preparedRGB11.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	if err := rgb11wallet.ValidateWalletSnapshot(full); err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	catalog, err := manager.accountManagedDataCatalog()
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	revision := snapshot.profile.ManagedDataRevision + 1
	if revision <= localState.DataRevision {
		revision = localState.DataRevision + 1
	}
	bundle, bundleHash, err := manager.exportAccountManagedData(catalog, revision)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	if !testnetSeq2SnapshotContainsTransfer(bundle, testnetSeq2RepairTransfer) {
		preparedAccount, err := preparedRGB11.fixedRGB11ScopeAccount()
		if err != nil {
			return nil, account.ManagedState{}, nil, nil, nil, err
		}
		preparedScope := AccountManagedDataScope{
			WalletID: preparedAccount.WalletID, WalletFingerprint: walletFingerprint(preparedAccount.Wallet),
			AccountIndex: preparedAccount.AccountIndex, Network: _chain,
		}
		if _, ok := accountManagedScopeSet(catalog)[preparedScope.ID()]; !ok {
			return nil, account.ManagedState{}, nil, nil, nil, fmt.Errorf("prepared RGB11 logical scope is absent from the managed catalog")
		}
		recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(full, time.Now().Unix())
		if err != nil {
			return nil, account.ManagedState{}, nil, nil, nil, err
		}
		encoded, err := rgb11wallet.EncodeRecoveryPackage(recovery)
		if err != nil {
			return nil, account.ManagedState{}, nil, nil, nil, err
		}
		provider := &rgb11AccountManagedDataProvider{owner: manager}
		if err := provider.Validate(catalog, []AccountManagedDataPayload{{
			Scope: preparedScope.ID(), Payload: encoded,
		}}); err != nil {
			return nil, account.ManagedState{}, nil, nil, nil, fmt.Errorf("validate prepared RGB11 recovery payload: %w", err)
		}
		bundle, bundleHash, err = addTestnetSeq2PreparedRGB11Payload(bundle,
			preparedScope.ID(), encoded)
		if err != nil {
			return nil, account.ManagedState{}, nil, nil, nil, err
		}
		if !testnetSeq2SnapshotContainsTransfer(bundle, testnetSeq2RepairTransfer) {
			return nil, account.ManagedState{}, nil, nil, nil, fmt.Errorf("managed RGB11 bundle still omits the prepared transfer")
		}
	}
	managedEnvelope, info, err := account.SealManagedDataBundleWithInfo(snapshot.secret,
		snapshot.profile.AccountID, bundle, nil)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	target := localState
	target.Revision++
	target.DataRevision, target.DataHash = bundle.Revision, bundleHash
	stateEnvelope, err := account.SealManagedState(snapshot.secret, snapshot.profile.AccountID, target, nil)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	managed := &accountManagedDataSnapshot{Catalog: catalog, Bundle: bundle,
		Hash: bundleHash, Envelope: managedEnvelope, Compressed: info.Compressed}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	stateKey, _ := manager.accountManagedStateKey(root)
	blobKey, _ := manager.accountManagedDataBlobKey(root)
	stateMutation, err := accountStateMutation(&snapshot.profile, root, stateKey, stateEnvelope)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	blobMutation, err := accountManagedDataMutation(&snapshot.profile, root, blobKey, managedEnvelope)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	stateMutation.IssueHeight, blobMutation.IssueHeight = height, height
	stateRecord, err := manager.dkvs.buildValueRecord(stateMutation, 3)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	blobRecord, err := manager.dkvs.buildValueRecord(blobMutation, 3)
	if err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	if err := validateTestnetSeq2PaidRecords(stateRecord, blobRecord); err != nil {
		return nil, account.ManagedState{}, nil, nil, nil, err
	}
	return &testnetSeq2CASPlan{Mutations: []dkvsindexer.CASMutation{
		{Record: stateRecord}, {Record: blobRecord},
	}}, target, managed, snapshot, root, nil
}

func validateTestnetSeq2Target(secret []byte, accountID string,
	stateRecord, blobRecord *swire.DKVSRecord, root common.Wallet) (account.ManagedState,
	*accountManagedDataSnapshot, error) {
	state, err := account.OpenManagedState(secret, accountID, stateRecord.Value)
	if err != nil || state.RootFingerprint != walletFingerprint(root) {
		return account.ManagedState{}, nil, fmt.Errorf("replacement state failed A/root validation")
	}
	managed, err := openAccountManagedDataValue(secret, accountID, cloneDKVSValue(blobRecord), state)
	if err != nil || managed.Bundle.Revision != state.DataRevision || managed.Hash != state.DataHash ||
		!testnetSeq2SnapshotContainsTransfer(managed.Bundle, testnetSeq2RepairTransfer) {
		return account.ManagedState{}, nil, fmt.Errorf("replacement blob failed A/state/RGB11 validation")
	}
	return state, managed, nil
}

func commitTestnetSeq2VerifiedLocalProfile(manager *Manager, state account.ManagedState,
	snapshot *accountManagementSyncSnapshot, envelope []byte,
	managed *accountManagedDataSnapshot, authorization AccountStorageAuthorization) error {
	if manager == nil || snapshot == nil || managed == nil {
		return fmt.Errorf("verified local repair state is incomplete")
	}
	if err := applyTestnetSeq2PaidProfilePolicy(&snapshot.profile, authorization); err != nil {
		return err
	}
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.accountProfile == nil {
		return fmt.Errorf("local repair profile is unavailable")
	}
	previous := *manager.accountProfile
	if err := applyTestnetSeq2PaidProfilePolicy(manager.accountProfile, authorization); err != nil {
		return err
	}
	_, _, err := manager.commitAccountManagedStateLocked(state, snapshot, envelope, managed)
	if err != nil {
		manager.accountProfile = &previous
	}
	return err
}

// TestManageTestnetAccountSeq2Repair is an opt-in, one-account management tool.
// It is dry-run unless SAT20_ACCOUNT_REPAIR_APPLY exactly equals the fixed token.
// No production startup path imports or invokes this code.
func TestManageTestnetAccountSeq2Repair(t *testing.T) {
	snapshotPath := os.Getenv("SAT20_ACCOUNT_REPAIR_SNAPSHOT")
	if snapshotPath == "" {
		t.Skip("SAT20_ACCOUNT_REPAIR_SNAPSHOT is not set")
	}
	manager, profileKey, oldProfile, restoreDBScope, err := loadTestnetSeq2RepairManager(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restoreDBScope()
	defer manager.clearAccountManagementSession()
	if err := requireTestnetSeq2RepairConfig(manager.cfg, manager.accountProfile.AccountID); err != nil {
		t.Fatal(err)
	}
	root, err := manager.accountManagementRootWallet()
	if err != nil {
		t.Fatal(err)
	}
	paidAuthorization, paidSummary, err := testnetSeq2PaidAuthorization(manager, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AUTOPAY preflight: contract=%s fee_asset=%s amount_per_block=%s records=%d reused=true",
		paidSummary.Contract, paidSummary.FeeAsset, paidSummary.AmountPerBlock,
		paidSummary.RecordCount)
	stateKey, _ := manager.accountManagedStateKey(root)
	blobKey, _ := manager.accountManagedDataBlobKey(root)
	client := NewSatsNetDKVSClient("https", "apiprd.ordx.market", "satsnet/testnet", nil)
	pair, err := readTestnetSeq2Remote(client, stateKey, blobKey)
	if err != nil {
		t.Fatal(err)
	}
	preparedRGB11, err := testnetSeq2PreparedRGB11Manager(manager)
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := preparedRGB11.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	fullRGB11, _, err := preparedRGB11.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if err := rgb11wallet.ValidateWalletSnapshot(fullRGB11); err != nil {
		t.Fatal(err)
	}
	resumed, err := preparedRGB11.ResumeRGB11PreparedTransfer(testnetSeq2RepairTransfer)
	if err != nil || resumed == nil || resumed.State == nil {
		t.Fatalf("resume original prepared transfer: %v", err)
	}
	encodedRGB11, err := json.Marshal(fullRGB11)
	if err != nil {
		t.Fatal(err)
	}
	rgb11Hash := sha256.Sum256(encodedRGB11)
	zeroBytes(encodedRGB11)
	targetPlan, _, targetManaged, syncSnapshot, _, err :=
		buildTestnetSeq2Target(manager, pair.VerificationHeight, paidAuthorization)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RGB11 preflight: projection_records=%d engine_records=%d transfer=%s status=%s snapshot_hash=%s",
		len(fullRGB11.ProjectionRecords), len(fullRGB11.EngineRecords),
		diagnosticShort(testnetSeq2RepairTransfer), resumed.State.Status,
		hex.EncodeToString(rgb11Hash[:6]))
	stateHash := dkvsindexer.RecordHash(pair.State).String()
	blobHash := dkvsindexer.RecordHash(pair.Blob).String()
	approvedB := strings.EqualFold(stateHash, testnetSeq2RepairStateHash) &&
		strings.EqualFold(blobHash, testnetSeq2RepairBlobHash)
	var plan *testnetSeq2CASPlan
	confirmed := pair
	if approvedB {
		if err := validateTestnetSeq2ApprovedRemote(pair, stateKey, blobKey); err != nil {
			t.Fatal(err)
		}
		plan, err = buildTestnetSeq2CASPlan(pair, targetPlan.Mutations[0].Record,
			targetPlan.Mutations[1].Record)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("repair dry-run: account=%s remote=B/seq2/height3445 target=seq3 verification_height=%d managed_revision=%d summary=%s",
			diagnosticAccountID(testnetSeq2RepairAccountID), pair.VerificationHeight,
			targetManaged.Bundle.Revision, hex.EncodeToString(rgb11Hash[:6]))
	} else {
		if pair.State.Seq != 3 || pair.Blob.Seq != 3 || pair.State.Key != stateKey ||
			pair.Blob.Key != blobKey || pair.State.IssueHeight != pair.Blob.IssueHeight ||
			pair.State.IssueHeight <= testnetSeq2RepairHeight ||
			pair.State.IssueHeight > pair.VerificationHeight {
			t.Fatalf("repair stopped on partial or unapproved third state/blob records")
		}
		if _, _, err := validateTestnetSeq2Target(syncSnapshot.secret,
			testnetSeq2RepairAccountID, pair.State, pair.Blob, root); err != nil {
			stateIsB := strings.EqualFold(stateHash, testnetSeq2RepairStateHash)
			blobIsB := strings.EqualFold(blobHash, testnetSeq2RepairBlobHash)
			if stateIsB != blobIsB {
				t.Fatalf("repair stopped on partial B/A state/blob outcome")
			}
			t.Fatalf("repair stopped on unapproved third state/blob records: %v", err)
		}
		t.Logf("repair dry-run: account=%s remote=verified-A/seq3 verification_height=%d summary=%s",
			diagnosticAccountID(testnetSeq2RepairAccountID), pair.VerificationHeight,
			hex.EncodeToString(rgb11Hash[:6]))
	}
	if !testnetSeq2ApplyEnabled(os.Getenv("SAT20_ACCOUNT_REPAIR_APPLY")) {
		return
	}
	if plan != nil {
		requestID, requestErr := newDKVSRequestID()
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		result, applyErr := client.putRecordBatchCASRaw(plan.Mutations, "", requestID)
		if applyErr == nil && (result == nil || len(result.Records) != 2) {
			applyErr = fmt.Errorf("repair CAS returned an incomplete result")
		}
		confirmed, err = readTestnetSeq2Remote(client, stateKey, blobKey)
		if err != nil {
			t.Fatalf("repair outcome is unknown; read-only reconciliation failed: %v (submit=%v)", err, applyErr)
		}
		stateHash = dkvsindexer.RecordHash(confirmed.State).String()
		blobHash = dkvsindexer.RecordHash(confirmed.Blob).String()
		switch classifyTestnetSeq2Remote(stateHash, blobHash, plan.StateHash, plan.BlobHash) {
		case testnetSeq2RemoteTargetA:
		case testnetSeq2RemoteApprovedB:
			t.Fatalf("repair CAS was not applied: %v", applyErr)
		case testnetSeq2RemotePartial:
			t.Fatalf("repair stopped on partial state/blob outcome")
		default:
			t.Fatalf("repair stopped on unapproved third state/blob hashes")
		}
	}
	verifiedState, verifiedManaged, err := validateTestnetSeq2Target(syncSnapshot.secret,
		testnetSeq2RepairAccountID, confirmed.State, confirmed.Blob, root)
	if err != nil {
		t.Fatal(err)
	}
	err = testnetSeq2CommitAfterVerification(func() error {
		_, _, verifyErr := validateTestnetSeq2Target(syncSnapshot.secret,
			testnetSeq2RepairAccountID, confirmed.State, confirmed.Blob, root)
		return verifyErr
	}, func() error {
		return commitTestnetSeq2VerifiedLocalProfile(manager, verifiedState, syncSnapshot,
			confirmed.State.Value, verifiedManaged, paidAuthorization)
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedProfile, err := manager.db.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	oldRaw, err := base64.StdEncoding.DecodeString(oldProfile)
	if err != nil {
		t.Fatal(err)
	}
	oldHash := sha256.Sum256(oldRaw)
	zeroBytes(oldRaw)
	patch := testnetSeq2RepairPatch{Origin: "http://localhost:5173", Key: profileKey,
		ExpectedSHA: hex.EncodeToString(oldHash[:]), Value: base64.StdEncoding.EncodeToString(updatedProfile),
		RemoteState: stateHash, RemoteBlob: blobHash}
	patchPath := os.Getenv("SAT20_ACCOUNT_REPAIR_PATCH")
	if !strings.HasPrefix(patchPath, "/private/tmp/") {
		t.Fatal("SAT20_ACCOUNT_REPAIR_PATCH must be an explicit /private/tmp path")
	}
	encodedPatch, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patchPath, encodedPatch, 0o600); err != nil {
		t.Fatal(err)
	}
	zeroBytes(encodedPatch)
	if err := os.Chmod(patchPath, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := manager.dkvs.primaryStore()
	if err != nil {
		t.Fatalf("remote A committed and local patch saved; root wrapper pending: %v", err)
	}
	if err := manager.syncAccountRootWrapper(store); err != nil {
		t.Fatalf("remote A committed and local patch saved; root wrapper pending: %v", err)
	}
	wrapperKey, _ := accountRootWrapperKey(root)
	if err := store.Refresh(wrapperKey); err != nil {
		t.Fatalf("root wrapper refresh: %v", err)
	}
	wrapper, err := store.Get(wrapperKey)
	if err != nil || wrapper == nil {
		t.Fatalf("root wrapper verification unavailable: %v", err)
	}
	payload, err := openAccountRootWrapper(root, "testnet", testnetSeq2RepairAccountID, wrapper.Value)
	if err != nil || !strings.EqualFold(hex.EncodeToString(sha256Bytes(payload.Secret)),
		hex.EncodeToString(sha256Bytes(syncSnapshot.secret))) || wrapper.Seq != 1 ||
		payload.StorageMode != AccountStoragePaid || payload.RecordTTL != 0 ||
		payload.AutopayContract != paidSummary.Contract ||
		!accountRecordMatchesStorage(wrapper, manager.accountProfile) {
		zeroBytes(payload.Secret)
		t.Fatal("root wrapper does not contain verified paid Secret A metadata at Seq1")
	}
	if _, _, err := validateTestnetSeq2Target(payload.Secret, testnetSeq2RepairAccountID,
		confirmed.State, confirmed.Blob, root); err != nil {
		zeroBytes(payload.Secret)
		t.Fatalf("root wrapper Secret A cannot open the verified remote state/blob: %v", err)
	}
	zeroBytes(payload.Secret)
}

func sha256Bytes(value []byte) []byte {
	hash := sha256.Sum256(value)
	return hash[:]
}

func TestTestnetSeq2RepairGuardsAndClassification(t *testing.T) {
	good := &common.Config{Env: "prd", Chain: "testnet", IndexerL2: &common.Indexer{
		Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"}}
	if err := requireTestnetSeq2RepairConfig(good, testnetSeq2RepairAccountID); err != nil {
		t.Fatal(err)
	}
	bad := *good
	bad.Chain = "mainnet"
	if err := requireTestnetSeq2RepairConfig(&bad, testnetSeq2RepairAccountID); err == nil {
		t.Fatal("mainnet repair configuration accepted")
	}
	if got := classifyTestnetSeq2Remote(testnetSeq2RepairStateHash,
		testnetSeq2RepairBlobHash, "a-state", "a-blob"); got != testnetSeq2RemoteApprovedB {
		t.Fatalf("approved B class=%d", got)
	}
	if got := classifyTestnetSeq2Remote("a-state", "a-blob", "a-state", "a-blob"); got != testnetSeq2RemoteTargetA {
		t.Fatalf("target A class=%d", got)
	}
	if got := classifyTestnetSeq2Remote("a-state", testnetSeq2RepairBlobHash,
		"a-state", "a-blob"); got != testnetSeq2RemotePartial {
		t.Fatalf("partial class=%d", got)
	}
	if got := classifyTestnetSeq2Remote("third-state", "third-blob", "a-state", "a-blob"); got != testnetSeq2RemoteUnknown {
		t.Fatalf("third class=%d", got)
	}
	if testnetSeq2ApplyEnabled("1") || testnetSeq2ApplyEnabled("true") ||
		!testnetSeq2ApplyEnabled(testnetSeq2RepairApply) {
		t.Fatal("apply gate is not exact")
	}
}

func TestTestnetSeq2RepairPendingScopeAndSnapshotKeyClasses(t *testing.T) {
	pendingKey := "rgb11-wallet-1786424671107000-account-3-rgb11v2-pending-" +
		testnetSeq2RepairTransfer
	walletID, accountIndex, ok := parseTestnetSeq2PendingScope(pendingKey,
		testnetSeq2RepairTransfer)
	if !ok || walletID != 1786424671107000 || accountIndex != 3 {
		t.Fatalf("pending scope=(%d,%d,%v)", walletID, accountIndex, ok)
	}
	for _, key := range []string{
		"rgb11-engine-wallet-1786424671107000-account-3-rgb11v2-wallet/head",
		"rgb11-local-wallet-1786424671107000-account-3-rgb11v2-l1-monitor-v1",
		"rgb11-wallet-1786424671107000-account-3-rgb11v2-pending-other",
		"rgb11-wallet-bad-account-3-rgb11v2-pending-" + testnetSeq2RepairTransfer,
	} {
		if _, _, ok := parseTestnetSeq2PendingScope(key, testnetSeq2RepairTransfer); ok {
			t.Fatalf("non-target pending scope accepted: %s", key)
		}
	}

	classes := map[string]string{
		"prd-testnet-account-management-profile-v2": "profile",
		"wallet-status":              "status",
		"wallet-id-1786424671107000": "wallet",
		"rgb11-engine-wallet-1-account-0-rgb11v2-wallet/head":  "engine",
		"rgb11-local-wallet-1-account-0-rgb11v2-l1-monitor-v1": "rgbLocal",
		pendingKey:                      "projection",
		"prd-testnet-l-l1-outpoint":     "locker",
		"prd-testnet-lt-l1":             "lockTime",
		"prd-testnet-t-rgb11:f:test":    "ticker",
		"dkvs-batch-outbox:scope:entry": "outbox",
	}
	for key, want := range classes {
		if got := testnetSeq2SnapshotKeyClass(key); got != want {
			t.Fatalf("key %q class=%q want=%q", key, got, want)
		}
	}
	if got := testnetSeq2SnapshotKeyClass("unrelated-application-key"); got != "" {
		t.Fatalf("unbounded key accepted as %q", got)
	}

	values := make(map[string]string, len(classes))
	for key := range classes {
		values[key] = "ignored-by-key-validation"
	}
	if err := validateTestnetSeq2SnapshotKeys(values); err != nil {
		t.Fatal(err)
	}
	delete(values, "prd-testnet-l-l1-outpoint")
	if err := validateTestnetSeq2SnapshotKeys(values); err == nil {
		t.Fatal("snapshot without RGB11 locker state accepted")
	}
	values["prd-testnet-l-l1-outpoint"] = "ignored-by-key-validation"
	values["unrelated-application-key"] = "must-not-be-copied"
	if err := validateTestnetSeq2SnapshotKeys(values); err == nil {
		t.Fatal("snapshot with an unbounded key class accepted")
	}
}

func TestTestnetSeq2RepairPreparedScopeUsesPersistedWalletIDBeforeFingerprintDedup(t *testing.T) {
	const mnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	canonical := NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.TestNet4Params)
	target := NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.TestNet4Params)
	if canonical == nil || target == nil {
		t.Fatal("create duplicate test wallets")
	}
	database := newMemoryKVDB()
	targetKey := "rgb11-wallet-2-account-0-rgb11v2-pending-" + testnetSeq2RepairTransfer
	if err := database.Write([]byte(targetKey), []byte("prepared-record-placeholder")); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		db: database, cfg: &common.Config{Chain: "testnet"},
		status: &Status{CurrentWallet: 1, CurrentAccount: 0},
		walletInfoMap: map[int64]*WalletInfo{
			1: {WalletInDB: WalletInDB{Id: 1, Accounts: 1}, Wallet: canonical},
			2: {WalletInDB: WalletInDB{Id: 2, Accounts: 1}, Wallet: target},
		},
		tickerInfoMap: make(map[string]*indexer.TickerInfo),
	}
	manager.utxoLockerL1 = NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN)

	accounts := manager.localRGB11Accounts()
	if len(accounts) != 1 || accounts[0].WalletID != 1 {
		t.Fatalf("test fixture did not exercise fingerprint dedup: %+v", accounts)
	}
	prepared, err := testnetSeq2PreparedRGB11Manager(manager)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.status.CurrentWallet != 2 || prepared.status.CurrentAccount != 0 ||
		prepared.rgb11ScopeKey() != rgb11StorageScope(2, 0) {
		t.Fatalf("prepared manager used wrong persisted scope: wallet=%d account=%d scope=%s",
			prepared.status.CurrentWallet, prepared.status.CurrentAccount, prepared.rgb11ScopeKey())
	}
}

func TestTestnetSeq2RepairAddsPreparedPayloadWithoutReplacingCanonicalScope(t *testing.T) {
	bundle := account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 2,
		Items: []account.ManagedDataItem{{Provider: "other", Scope: AccountManagedDataGlobalScope,
			Payload: []byte("preserved")}}}
	updated, hash, err := addTestnetSeq2PreparedRGB11Payload(bundle,
		"testnet/fingerprint/0", []byte("prepared-recovery-package"))
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" || len(updated.Items) != 2 || updated.Revision != bundle.Revision {
		t.Fatalf("prepared payload was not added deterministically: hash=%q bundle=%+v", hash, updated)
	}
	foundOther, foundRGB11 := false, false
	for _, item := range updated.Items {
		switch {
		case item.Provider == "other" && string(item.Payload) == "preserved":
			foundOther = true
		case item.Provider == rgb11AccountManagedProviderID &&
			item.Scope == "testnet/fingerprint/0" &&
			string(item.Payload) == "prepared-recovery-package":
			foundRGB11 = true
		}
	}
	if !foundOther || !foundRGB11 {
		t.Fatalf("prepared injection lost managed data: %+v", updated.Items)
	}
	if _, _, err := addTestnetSeq2PreparedRGB11Payload(updated,
		"testnet/fingerprint/0", []byte("replacement")); err == nil {
		t.Fatal("existing canonical RGB11 scope was silently overwritten")
	}
}

func TestTestnetSeq2RepairRejectsTemporaryTTLZeroAndBuildsPaidAutopayRecords(t *testing.T) {
	restore := enterTestnetSeq2RepairDBScope()
	defer restore()
	root := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", &chaincfg.TestNet4Params)
	if root == nil {
		t.Fatal("create repair root")
	}
	stateKey, err := dkvsindexer.AccountPersonalKey(strings.Repeat("a", 64), accountManagedStatePath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &accountManagementProfile{StorageMode: AccountStorageTemporary, RecordTTL: 0}
	if _, err := accountStateMutation(legacy, root, stateKey, []byte("state")); !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("temporary TTL=0 failure=%v", err)
	}

	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	paid := &accountManagementProfile{StorageMode: AccountStoragePaid,
		AutopayContract: defaults.AutopayContract}
	mutation, err := accountStateMutation(paid, root, stateKey, []byte("state"))
	if err != nil {
		t.Fatal(err)
	}
	mutation.IssueHeight = 3500
	record, err := (&dkvsManager{}).buildValueRecord(mutation, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTestnetSeq2PaidRecords(record); err != nil {
		t.Fatal(err)
	}
	proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
	if err != nil || proof.Mode != dkvsindexer.FeeModeAutopay ||
		proof.PoolContract != defaults.AutopayContract || record.TTL != 0 {
		t.Fatalf("paid record policy mismatch: ttl=%d proof=%+v err=%v", record.TTL, proof, err)
	}
	freeProof, err := dkvsindexer.NewFreeLocalFeeProof(record.Key, "personal",
		uint32(swire.MaxDKVSRecordSize), dkvsindexer.RecordExpiryHeight(record))
	if err != nil {
		t.Fatal(err)
	}
	record.FeeProof, err = dkvsindexer.EncodeFeeProof(freeProof)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateTestnetSeq2PaidRecords(record); err == nil {
		t.Fatal("FREE_LOCAL proof accepted for paid repair")
	}
}

func TestTestnetSeq2RepairResumeCommitsPaidPolicyWithoutChangingRemoteRevision(t *testing.T) {
	restore := enterTestnetSeq2RepairDBScope()
	defer restore()
	walletValue := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", &chaincfg.TestNet4Params)
	if walletValue == nil {
		t.Fatal("create repair wallet")
	}
	walletID := int64(1786424671107000)
	fingerprint := walletFingerprint(walletValue)
	database := newMemoryKVDB()
	profile := &accountManagementProfile{
		Version: accountManagementProfileVersion, AccountID: testnetSeq2RepairAccountID,
		RootFingerprint: fingerprint, StorageMode: AccountStorageTemporary,
		RecordTTL: 0, StateSeq: 3, StateEnvelope: []byte("verified-A-state"),
		ManagedDataRevision: 1, ManagedDataGeneration: 1,
	}
	manager := &Manager{
		db: database, accountProfile: profile, wallet: walletValue,
		status: &Status{CurrentWallet: walletID, CurrentAccount: 0,
			BlockHashMapL1: map[int]string{}, BlockHashMapL2: map[int]string{}},
		walletInfoMap: map[int64]*WalletInfo{walletID: {
			WalletInDB: WalletInDB{Id: walletID, Accounts: 1, Type: WALLET_TYPE_MNEMONIC,
				Name: "Root", AccountNames: map[uint32]string{0: "Account 1"},
				AccountDIDs: map[uint32]string{}},
			Wallet: walletValue,
		}},
	}
	remoteWallet := syncTestWallet(fingerprint, "Root")
	snapshot := &accountManagementSyncSnapshot{
		profile: *profile, wallets: map[string]account.ManagedWallet{fingerprint: remoteWallet},
	}
	state := account.ManagedState{
		Version: account.ManagedStateVersion, RootFingerprint: fingerprint, Revision: 3,
		Wallets: []account.ManagedWallet{remoteWallet}, DataRevision: 1, DataHash: "managed-hash",
	}
	managed := &accountManagedDataSnapshot{
		Bundle: account.ManagedDataBundle{Version: account.ManagedDataBundleVersion, Revision: 1},
		Hash:   "managed-hash", Envelope: []byte("verified-A-blob"),
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	authorization := AccountStorageAuthorization{
		Mode: AccountStoragePaid, RecordOptions: dkvsindexer.RecordOptions{TTL: 0},
		Autopay: &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(),
			PoolContract: defaults.AutopayContract},
		Location: AccountIndexerLocation{Scheme: "https", Host: "apiprd.ordx.market",
			Proxy: "satsnet/testnet"},
	}
	if err := commitTestnetSeq2VerifiedLocalProfile(manager, state, snapshot,
		[]byte("verified-A-state"), managed, authorization); err != nil {
		t.Fatal(err)
	}
	encoded, err := database.Read(accountManagementProfileKey())
	if err != nil {
		t.Fatal(err)
	}
	var stored accountManagementProfile
	if err := DecodeFromBytes(encoded, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.StateSeq != 3 || stored.StorageMode != AccountStoragePaid || stored.RecordTTL != 0 ||
		stored.AutopayContract != defaults.AutopayContract ||
		stored.Location != authorization.Location || stored.ManagedDataRevision != 1 {
		t.Fatalf("resume profile metadata mismatch: %+v", stored)
	}
	wrapperKey, err := accountRootWrapperKey(walletValue)
	if err != nil {
		t.Fatal(err)
	}
	mutation, err := accountRootWrapperMutation(&stored, walletValue,
		wrapperKey, []byte("sealed-wrapper"))
	if err != nil {
		t.Fatal(err)
	}
	mutation.IssueHeight = 3452
	wrapper, err := (&dkvsManager{}).buildValueRecord(mutation, 1)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := dkvsindexer.ParseFeeProof(wrapper.FeeProof)
	if err != nil || wrapper.Seq != 1 || wrapper.TTL != 0 ||
		proof.Mode != dkvsindexer.FeeModeAutopay || proof.PoolContract != defaults.AutopayContract {
		t.Fatalf("wrapper did not use paid Seq1 policy: record=%+v proof=%+v err=%v",
			wrapper, proof, err)
	}
}

func TestTestnetSeq2RepairDBScopeAndFailureRestore(t *testing.T) {
	oldMode, oldEnv, oldChain := _mode, _env, _chain
	_mode, _env, _chain = "scope-test-mode", "scope-test-env", "scope-test-chain"
	defer func() { _mode, _env, _chain = oldMode, oldEnv, oldChain }()

	restore := enterTestnetSeq2RepairDBScope()
	if _mode != LIGHT_NODE || _env != "prd" || _chain != "testnet" ||
		string(accountManagementProfileKey()) != "prd-testnet-"+accountManagementProfileDBKey {
		t.Fatalf("repair DB scope not installed: mode=%s env=%s chain=%s key=%s",
			_mode, _env, _chain, accountManagementProfileKey())
	}
	restore()
	if _mode != "scope-test-mode" || _env != "scope-test-env" || _chain != "scope-test-chain" {
		t.Fatalf("repair DB scope not restored: mode=%s env=%s chain=%s", _mode, _env, _chain)
	}

	_, _, _, leakedRestore, err := loadTestnetSeq2RepairManager(
		"/private/tmp/sat20-account-repair-snapshot-does-not-exist")
	if err == nil || leakedRestore != nil {
		t.Fatalf("missing snapshot loader result: restore=%v err=%v", leakedRestore != nil, err)
	}
	if _mode != "scope-test-mode" || _env != "scope-test-env" || _chain != "scope-test-chain" {
		t.Fatalf("failed loader leaked DB scope: mode=%s env=%s chain=%s", _mode, _env, _chain)
	}
}

func TestTestnetSeq2RepairCASPlanBindsBothETags(t *testing.T) {
	stateKey := "/personal/" + testnetSeq2RepairAccountID + "/account/state"
	blobKey := "/blob/" + testnetSeq2RepairAccountID + "/account-managed-data"
	makeRecord := func(key string, seq, height uint64, value string) *swire.DKVSRecord {
		return &swire.DKVSRecord{Version: dkvsindexer.Version, Key: key, Seq: seq,
			IssueHeight: height, Value: []byte(value), Signature: make([]byte, 64)}
	}
	bState := makeRecord(stateKey, 2, 3445, "b-state")
	bBlob := makeRecord(blobKey, 2, 3445, "b-blob")
	aState := makeRecord(stateKey, 3, 3500, "a-state")
	aBlob := makeRecord(blobKey, 3, 3500, "a-blob")
	pair := testnetSeq2RemotePair{State: bState, Blob: bBlob,
		VerificationHeight: 3500, EndpointID: "test-endpoint"}
	approvedState, approvedBlob := dkvsindexer.RecordHash(bState).String(), dkvsindexer.RecordHash(bBlob).String()
	if err := validateTestnetSeq2ApprovedRemoteHashes(pair, stateKey, blobKey,
		approvedState, approvedBlob); err != nil {
		t.Fatal(err)
	}
	plan, err := buildTestnetSeq2CASPlanHashes(pair, aState, aBlob, approvedState, approvedBlob)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Mutations) != 2 ||
		plan.Mutations[0].Record.Seq != 3 || plan.Mutations[1].Record.Seq != 3 ||
		plan.Mutations[0].Precondition.ExpectedHash == nil ||
		plan.Mutations[1].Precondition.ExpectedHash == nil ||
		plan.Mutations[0].Precondition.ExpectedHash.String() != approvedState ||
		plan.Mutations[1].Precondition.ExpectedHash.String() != approvedBlob {
		t.Fatalf("unsafe ETag CAS plan: %+v", plan)
	}
}
func TestTestnetSeq2RepairNeverCommitsBeforeVerification(t *testing.T) {
	commits := 0
	err := testnetSeq2CommitAfterVerification(func() error { return errors.New("remote invalid") },
		func() error { commits++; return nil })
	if err == nil || commits != 0 {
		t.Fatalf("invalid remote committed: commits=%d err=%v", commits, err)
	}
	err = testnetSeq2CommitAfterVerification(func() error { return nil },
		func() error { commits++; return nil })
	if err != nil || commits != 1 {
		t.Fatalf("verified remote did not commit once: commits=%d err=%v", commits, err)
	}
}

func TestTestnetSeq2RepairPatchRequiresOriginalProfileHash(t *testing.T) {
	original := []byte("encrypted-profile-a")
	hash := sha256.Sum256(original)
	patch := testnetSeq2RepairPatch{Origin: "http://localhost:5173",
		Key:         "prd-testnet-account-management-profile-v2",
		ExpectedSHA: hex.EncodeToString(hash[:]), Value: base64.StdEncoding.EncodeToString([]byte("updated"))}
	verify := func(current []byte) error {
		got := sha256.Sum256(current)
		if !strings.EqualFold(hex.EncodeToString(got[:]), patch.ExpectedSHA) {
			return errors.New("local profile changed")
		}
		return nil
	}
	if err := verify(original); err != nil {
		t.Fatal(err)
	}
	if err := verify([]byte("third-profile")); err == nil {
		t.Fatal("third local profile was accepted")
	}
}
