package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// TestDiagnosticAccountDKVSProfile is an opt-in, read-only field diagnostic.
// It only uses the final Wallet DKVS application protocol. The sensitive input
// snapshot is removed before the test exits.
func TestDiagnosticAccountDKVSProfile(t *testing.T) {
	snapshotPath := os.Getenv("SAT20_ACCOUNT_DIAG_SNAPSHOT")
	if snapshotPath == "" {
		t.Skip("SAT20_ACCOUNT_DIAG_SNAPSHOT is not set")
	}
	defer func() {
		if err := os.Remove(snapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove sensitive diagnostic snapshot: %v", err)
		}
	}()
	encoded, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Origin   string            `json:"origin"`
		Password string            `json:"password"`
		Values   map[string]string `json:"values"`
	}
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	zeroBytes(encoded)
	expectedOrigin := os.Getenv("SAT20_ACCOUNT_DIAG_ORIGIN")
	if expectedOrigin == "" {
		expectedOrigin = "http://localhost:5173"
	}
	if snapshot.Origin != expectedOrigin || snapshot.Password == "" {
		t.Fatalf("unexpected diagnostic snapshot origin=%q password=%t", snapshot.Origin, snapshot.Password != "")
	}
	password := snapshot.Password
	snapshot.Password = ""

	database := newMemoryKVDB()
	for key, value := range snapshot.Values {
		raw, decodeErr := base64.StdEncoding.DecodeString(value)
		if decodeErr != nil {
			t.Fatalf("decode selected database value for key class %s: %v", diagnosticKeyClass(key), decodeErr)
		}
		if err := database.Write([]byte(key), raw); err != nil {
			t.Fatal(err)
		}
		zeroBytes(raw)
		delete(snapshot.Values, key)
	}

	oldMode, oldEnv, oldChain := _mode, _env, _chain
	_mode, _env, _chain = LIGHT_NODE, "prd", "testnet"
	defer func() { _mode, _env, _chain = oldMode, oldEnv, oldChain }()

	manager := &Manager{db: database, cfg: &common.Config{
		Env: "prd", Chain: "testnet",
		IndexerL2: &common.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
	}}
	manager.walletInfoMap, err = diagnosticLoadWallets(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.loadAccountManagementProfileLocked(); err != nil {
		t.Fatal(err)
	}
	if manager.accountProfile == nil {
		t.Fatal("account management profile is absent")
	}
	if err := manager.unlockAccountManagementLocked(password); err != nil {
		t.Fatalf("profile secret unlock: %v", err)
	}
	defer manager.clearAccountManagementSession()

	profile := *manager.accountProfile
	localDigest := sha256.Sum256(profile.StateEnvelope)
	localHashOK := strings.EqualFold(profile.StateHash, hex.EncodeToString(localDigest[:]))
	localState, localStateErr := account.OpenManagedState(manager.accountSecret,
		profile.AccountID, profile.StateEnvelope)
	rootMatch := false
	var rootWallet common.Wallet
	if localStateErr == nil {
		for _, info := range manager.walletInfoMap {
			if info == nil || info.Type != WALLET_TYPE_MNEMONIC {
				continue
			}
			mnemonic, secretErr := manager.loadWalletSecret(info, password)
			if secretErr != nil {
				continue
			}
			candidate := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
			if candidate != nil && walletFingerprint(candidate) == profile.RootFingerprint &&
				localState.RootFingerprint == profile.RootFingerprint {
				rootMatch = true
				rootWallet = candidate
				break
			}
		}
	}
	localManaged, localManagedErr := openProfileManagedDataBundle(profile, manager.accountSecret)

	stateKey := "/personal/" + profile.AccountID + "/account/state"
	dataKey := "/blob/" + profile.AccountID + "/account-managed-data"
	client := NewSatsNetDKVSClient("https", "apiprd.ordx.market", "satsnet/testnet", nil)
	remoteStateRecord, stateReadErr := diagnosticExactRemoteRecord(client,
		"/personal/"+profile.AccountID+"/account", stateKey)
	remoteDataRecord, dataReadErr := diagnosticExactRemoteRecord(client, dataKey, dataKey)
	recoveryPrefix := "/personal/" + profile.AccountID + "/account/recovery"
	recoveryRecords, _, recoveryReadErr := client.ListRecords(recoveryPrefix, 0, 100)

	localGuardianKey, localGuardianKeyErr := manager.LoadAccountGuardianPrivateKey(password)
	defer zeroBytes(localGuardianKey)
	var wrapperRecord *swire.DKVSRecord
	var wrapperReadErr error
	var wrapperSecret []byte
	var wrapperKey string
	wrapperOpen := false
	wrapperValidatesRemote := false
	wrapperMatchesLocal := false
	if rootWallet != nil {
		wrapperKey, wrapperReadErr = accountRootWrapperKey(rootWallet)
		if wrapperReadErr == nil {
			wrapperRecord, wrapperReadErr = diagnosticExactRemoteRecord(client, wrapperKey, wrapperKey)
			if wrapperRecord != nil {
				payload, openErr := openAccountRootWrapper(rootWallet, _chain, profile.AccountID, wrapperRecord.Value)
				if openErr == nil {
					wrapperOpen = true
					wrapperSecret = append([]byte(nil), payload.Secret...)
					wrapperMatchesLocal = bytes.Equal(wrapperSecret, manager.accountSecret)
				}
			}
		}
	}

	var remoteState account.ManagedState
	var remoteStateErr error
	if remoteStateRecord != nil {
		remoteState, remoteStateErr = account.OpenManagedState(manager.accountSecret,
			profile.AccountID, remoteStateRecord.Value)
		if len(wrapperSecret) == 32 {
			wrappedState, wrappedErr := account.OpenManagedState(wrapperSecret,
				profile.AccountID, remoteStateRecord.Value)
			if wrappedErr == nil && wrappedState.RootFingerprint == profile.RootFingerprint {
				var wrappedData *dkvsValue
				if remoteDataRecord != nil {
					wrappedData = cloneDKVSValue(remoteDataRecord)
				}
				_, wrappedDataErr := openAccountManagedDataValue(wrapperSecret,
					profile.AccountID, wrappedData, wrappedState)
				wrapperValidatesRemote = wrappedDataErr == nil
			}
		}
	}
	zeroBytes(wrapperSecret)

	remoteManagedAttempted := false
	remoteManagedRevision := uint64(0)
	var remoteManagedErr error
	if remoteStateErr == nil && remoteStateRecord != nil {
		remoteManagedAttempted = true
		var value *dkvsValue
		if remoteDataRecord != nil {
			value = cloneDKVSValue(remoteDataRecord)
		}
		remoteManaged, openErr := openAccountManagedDataValue(manager.accountSecret,
			profile.AccountID, value, remoteState)
		remoteManagedErr = openErr
		if openErr == nil {
			remoteManagedRevision = remoteManaged.Bundle.Revision
		}
	}

	namespace := manager.dkvsReplicaNamespace()
	outbox, outboxErr := newDKVSReplicaStore(database).LoadOutbox(namespace)
	if outboxErr != nil {
		t.Fatal(outboxErr)
	}
	type accountOutboxSummary struct {
		RequestID, State, ErrorCode, OriginDomain string
		OriginGeneration                         uint64
		RecordSeqs                               []uint64
		KeyClasses                               []string
	}
	accountOutbox := make([]accountOutboxSummary, 0)
	for _, entry := range outbox {
		mutations, decodeErr := entry.DecodeMutations()
		if decodeErr != nil {
			continue
		}
		summary := accountOutboxSummary{
			RequestID: entry.RequestID, State: entry.State, ErrorCode: entry.LastErrorCode,
			OriginDomain: entry.OriginDomain, OriginGeneration: entry.OriginGeneration,
		}
		for _, mutation := range mutations {
			if mutation.Record == nil {
				continue
			}
			switch mutation.Record.Key {
			case stateKey:
				summary.KeyClasses = append(summary.KeyClasses, "account-state")
				summary.RecordSeqs = append(summary.RecordSeqs, mutation.Record.Seq)
			case dataKey:
				summary.KeyClasses = append(summary.KeyClasses, "account-blob")
				summary.RecordSeqs = append(summary.RecordSeqs, mutation.Record.Seq)
			}
		}
		if len(summary.KeyClasses) != 0 {
			accountOutbox = append(accountOutbox, summary)
		}
	}
	sort.Slice(accountOutbox, func(i, j int) bool { return accountOutbox[i].RequestID < accountOutbox[j].RequestID })

	decision := "stop-local-profile-invalid"
	if localHashOK && localStateErr == nil && rootMatch && localManagedErr == nil {
		switch {
		case stateReadErr != nil || dataReadErr != nil:
			decision = "stop-remote-read-failed"
		case remoteStateRecord == nil && remoteDataRecord == nil:
			decision = "local-valid-authoritative-remote-absent"
		case remoteStateRecord == nil || remoteDataRecord == nil:
			decision = "stop-remote-state-blob-incomplete"
		case remoteStateErr != nil || remoteManagedErr != nil:
			decision = "stop-remote-not-openable-by-local-secret"
		default:
			decision = "local-and-remote-valid"
		}
	}

	t.Logf("local profile: account=%s state_hash_ok=%t state_open=%t state_revision=%d profile_seq=%d root_match=%t managed_open=%t managed_revision=%d profile_managed_revision=%d",
		diagnosticAccountID(profile.AccountID), localHashOK, localStateErr == nil,
		localState.Revision, profile.StateSeq, rootMatch, localManagedErr == nil,
		localManaged.Revision, profile.ManagedDataRevision)
	t.Logf("remote state: exists=%t seq=%d issue_height=%d ttl=%d hash=%s open=%t revision=%d read_error=%t",
		remoteStateRecord != nil, diagnosticRecordSeq(remoteStateRecord), diagnosticRecordHeight(remoteStateRecord),
		diagnosticRecordTTL(remoteStateRecord), diagnosticRecordHash(remoteStateRecord),
		remoteStateErr == nil && remoteStateRecord != nil, remoteState.Revision, stateReadErr != nil)
	t.Logf("remote blob: exists=%t seq=%d issue_height=%d ttl=%d hash=%s open_attempted=%t open=%t revision=%d read_error=%t",
		remoteDataRecord != nil, diagnosticRecordSeq(remoteDataRecord), diagnosticRecordHeight(remoteDataRecord),
		diagnosticRecordTTL(remoteDataRecord), diagnosticRecordHash(remoteDataRecord), remoteManagedAttempted,
		remoteManagedAttempted && remoteManagedErr == nil && remoteDataRecord != nil,
		remoteManagedRevision, dataReadErr != nil)
	t.Logf("root wrapper: exists=%t seq=%d open=%t validates_remote=%t read_error=%t",
		wrapperRecord != nil, diagnosticRecordSeq(wrapperRecord), wrapperOpen,
		wrapperValidatesRemote, wrapperReadErr != nil)

	if os.Getenv("SAT20_ACCOUNT_DIAG_REPAIR_ROOT_WRAPPER") == "1" {
		repairAccountRootWrapperFromVerifiedState(t, client, manager, profile, rootWallet,
			wrapperKey, wrapperRecord, wrapperOpen, wrapperMatchesLocal, decision)
	}

	for _, record := range recoveryRecords {
		if record == nil {
			continue
		}
		pkg, decodeErr := account.DecodeRecoveryPackageStorage(record.Value)
		guardianLocalMatch := false
		packageID := "invalid"
		mode := account.RecoveryMode("")
		if decodeErr == nil {
			mode = pkg.Envelope.Locator.RecoveryMode
			packageID = diagnosticShort(pkg.Envelope.Locator.PackageID)
			if pkg.Manifest.Guardian != nil && localGuardianKeyErr == nil && len(localGuardianKey) == 32 {
				encodedCapsule, capsuleErr := manager.LoadAccountGuardianCapsule(
					AccountIndexerLocation{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
					pkg.Manifest.Guardian.MailboxID, pkg.Envelope.Locator.PackageID,
					pkg.Manifest.Guardian.ShareID)
				if capsuleErr == nil {
					var capsule account.GuardianShareCapsule
					if json.Unmarshal(encodedCapsule, &capsule) == nil {
						share, decryptErr := account.DecryptGuardianShare(capsule, localGuardianKey)
						guardianLocalMatch = decryptErr == nil && share.PackageID == pkg.Envelope.Locator.PackageID
					}
				}
			}
		}
		t.Logf("recovery package: package=%s mode=%s local_guardian_match=%t decode=%t",
			packageID, mode, guardianLocalMatch, decodeErr == nil)
	}
	if recoveryReadErr != nil {
		t.Log("recovery package read_error=true")
	}
	for _, entry := range accountOutbox {
		t.Logf("account outbox: request=%s state=%s error=%s origin=%s generation=%d keys=%v seqs=%v",
			entry.RequestID, entry.State, entry.ErrorCode, entry.OriginDomain,
			entry.OriginGeneration, entry.KeyClasses, entry.RecordSeqs)
	}
	t.Logf("decision: %s outbox_count=%d namespace=%s", decision, len(accountOutbox), namespace)
}

func repairAccountRootWrapperFromVerifiedState(t *testing.T, client *SatsNetDKVSClient,
	manager *Manager, profile accountManagementProfile, rootWallet common.Wallet,
	wrapperKey string, wrapperRecord *swire.DKVSRecord, wrapperOpen, wrapperMatchesLocal bool,
	decision string) {
	t.Helper()
	if decision != "local-and-remote-valid" {
		t.Fatalf("refusing root-wrapper repair: verified state/blob decision is %s", decision)
	}
	if rootWallet == nil || wrapperKey == "" {
		t.Fatal("refusing root-wrapper repair: root wallet or wrapper key is unavailable")
	}
	if wrapperRecord != nil && !wrapperOpen {
		t.Fatal("refusing root-wrapper repair: current remote wrapper cannot be opened")
	}
	if wrapperRecord != nil && wrapperMatchesLocal {
		t.Log("root-wrapper repair not needed: remote wrapper already contains the local account secret")
		return
	}
	if profile.StorageMode != AccountStoragePaid || profile.AutopayContract == "" {
		t.Fatalf("refusing root-wrapper repair: storage mode %q has no AUTOPAY contract", profile.StorageMode)
	}
	autopay := DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: profile.AutopayContract}
	state, err := client.GetKeyState(wrapperKey)
	if err != nil {
		t.Fatalf("read root-wrapper key state: %v", err)
	}
	height, err := client.GetBestHeight()
	if err != nil {
		t.Fatalf("read DKVS endpoint height: %v", err)
	}
	if wrapperRecord != nil {
		expected, err := chainHashFromETag(state.ETag)
		if err != nil {
			t.Fatal(err)
		}
		tombstone, err := newDKVSAccountSignedRecordWithAutopay(rootWallet, wrapperKey, nil,
			dkvsindexer.RecordOptions{Seq: state.Seq + 1, IssueHeight: height, Flags: dkvsindexer.FlagTombstone}, autopay)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.PutRecordCAS(tombstone, dkvsindexer.WritePrecondition{ExpectedHash: expected}); err != nil {
			t.Fatalf("tombstone root wrapper: %v", err)
		}
		state, err = client.GetKeyState(wrapperKey)
		if err != nil {
			t.Fatal(err)
		}
	}
	encoded, err := sealAccountRootWrapper(rootWallet, _chain, profile.AccountID,
		rootWrapperPayload(profile, manager.accountSecret), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(encoded)
	precondition := dkvsindexer.WritePrecondition{ExpectAbsent: true}
	seq := uint64(1)
	if state != nil && state.Status != dkvsindexer.KeyStateNeverSeen {
		seq = state.Seq + 1
		expected, parseErr := chainHashFromETag(state.ETag)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		precondition = dkvsindexer.WritePrecondition{ExpectedHash: expected}
	}
	rewritten, err := newDKVSAccountSignedRecordWithAutopay(rootWallet, wrapperKey, encoded,
		dkvsindexer.RecordOptions{Seq: seq, IssueHeight: height, TTL: profile.RecordTTL}, autopay)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.PutRecordCAS(rewritten, precondition)
	if err != nil {
		t.Fatalf("rewrite root wrapper: %v", err)
	}
	payload, err := openAccountRootWrapper(rootWallet, _chain, profile.AccountID, result.Value)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload.Secret)
	if !bytes.Equal(payload.Secret, manager.accountSecret) ||
		!accountRootWrapperMetadataMatchesProfile(payload, profile) {
		t.Fatal("rewritten root wrapper does not match verified local profile")
	}
	t.Logf("root-wrapper repair: rewritten_seq=%d", result.Seq)
}

func retryDiagnosticDKVSWrite(write func() (*swire.DKVSRecord, error)) (*swire.DKVSRecord, error) {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		record, err := write()
		if err == nil {
			return record, nil
		}
		lastErr = err
		if !IsDKVSErrorCode(err, dkvsindexer.ErrorCodeWriteConflict) {
			return nil, err
		}
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}
	return nil, lastErr
}

// TestDiagnosticGuardianPackageMatch checks whether an isolated PWA profile
// owns the guardian key for recovery packages without exposing that key or any
// recovered share.
func TestDiagnosticGuardianPackageMatch(t *testing.T) {
	snapshotPath := os.Getenv("SAT20_ACCOUNT_DIAG_SNAPSHOT")
	accountID := os.Getenv("SAT20_ACCOUNT_DIAG_ACCOUNT_ID")
	if snapshotPath == "" || len(accountID) != 64 {
		t.Skip("guardian diagnostic inputs are not set")
	}
	defer func() {
		if err := os.Remove(snapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove sensitive diagnostic snapshot: %v", err)
		}
	}()
	encoded, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Origin   string            `json:"origin"`
		Password string            `json:"password"`
		Values   map[string]string `json:"values"`
	}
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	zeroBytes(encoded)
	if snapshot.Origin != "http://localhost:5173" || snapshot.Password == "" {
		t.Fatal("unexpected guardian snapshot")
	}
	password := snapshot.Password
	snapshot.Password = ""
	database := newMemoryKVDB()
	for key, value := range snapshot.Values {
		raw, decodeErr := base64.StdEncoding.DecodeString(value)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if err := database.Write([]byte(key), raw); err != nil {
			t.Fatal(err)
		}
		zeroBytes(raw)
		delete(snapshot.Values, key)
	}
	manager := &Manager{db: database, cfg: &common.Config{
		Env: "prd", Chain: "testnet",
		IndexerL2: &common.Indexer{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
	}}
	manager.walletInfoMap, err = diagnosticLoadWallets(database)
	if err != nil {
		t.Fatal(err)
	}
	guardianKey, keyErr := manager.LoadAccountGuardianPrivateKey(password)
	defer zeroBytes(guardianKey)
	client := NewSatsNetDKVSClient("https", "apiprd.ordx.market", "satsnet/testnet", nil)
	records, _, err := client.ListRecords("/personal/"+accountID+"/account/recovery", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, record := range records {
		if record == nil {
			continue
		}
		pkg, decodeErr := account.DecodeRecoveryPackageStorage(record.Value)
		if decodeErr != nil || pkg.Manifest.Guardian == nil {
			continue
		}
		match := false
		if keyErr == nil && len(guardianKey) == 32 {
			encodedCapsule, capsuleErr := manager.LoadAccountGuardianCapsule(
				AccountIndexerLocation{Scheme: "https", Host: "apiprd.ordx.market", Proxy: "satsnet/testnet"},
				pkg.Manifest.Guardian.MailboxID, pkg.Envelope.Locator.PackageID,
				pkg.Manifest.Guardian.ShareID)
			if capsuleErr == nil {
				var capsule account.GuardianShareCapsule
				if json.Unmarshal(encodedCapsule, &capsule) == nil {
					share, decryptErr := account.DecryptGuardianShare(capsule, guardianKey)
					match = decryptErr == nil && share.PackageID == pkg.Envelope.Locator.PackageID
				}
			}
		}
		if match {
			matched++
		}
		t.Logf("guardian package: package=%s local_match=%t", diagnosticShort(pkg.Envelope.Locator.PackageID), match)
	}
	t.Logf("guardian key: available=%t package_count=%d matched=%d",
		keyErr == nil && len(guardianKey) == 32, len(records), matched)
}

func diagnosticExactRemoteRecord(client *SatsNetDKVSClient, prefix, key string) (*swire.DKVSRecord, error) {
	records, _, err := client.ListRecords(prefix, 0, 100)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record != nil && record.Key == key {
			return record, nil
		}
	}
	return nil, nil
}

func diagnosticLoadWallets(database *memoryKVDB) (map[int64]*WalletInfo, error) {
	wallets := make(map[int64]*WalletInfo)
	err := database.BatchRead([]byte(DB_KEY_WALLET), false, func(_, value []byte) error {
		var stored WalletInDB
		if err := DecodeFromBytes(value, &stored); err != nil {
			return err
		}
		wallets[stored.Id] = &WalletInfo{WalletInDB: stored}
		return nil
	})
	return wallets, err
}

func diagnosticKeyClass(key string) string {
	switch {
	case strings.HasSuffix(key, accountManagementProfileDBKey):
		return "account-profile"
	case strings.HasPrefix(key, DB_KEY_WALLET):
		return "encrypted-wallet"
	case strings.HasPrefix(key, string(dkvsOutboxPrefix)):
		return "dkvs-outbox"
	default:
		return "selected"
	}
}

func diagnosticAccountID(value string) string {
	if len(value) < 12 {
		return "invalid"
	}
	return value[:6] + "..." + value[len(value)-6:]
}

func diagnosticShort(value string) string {
	if len(value) < 10 {
		return "invalid"
	}
	return value[:5] + "..." + value[len(value)-5:]
}

func diagnosticRecordSeq(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.Seq
}

func diagnosticRecordHeight(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.IssueHeight
}

func diagnosticRecordTTL(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.TTL
}

func diagnosticRecordHash(record *swire.DKVSRecord) string {
	if record == nil {
		return ""
	}
	return dkvsindexer.RecordHash(record).String()
}
