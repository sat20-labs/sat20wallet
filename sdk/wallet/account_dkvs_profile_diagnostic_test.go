package wallet

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// TestDiagnosticAccountDKVSProfile is an opt-in, read-only field diagnostic.
// It is intentionally test-only: no diagnostic API or persistent production
// state is added. The sensitive input snapshot is removed before the test exits.
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
		Origin       string            `json:"origin"`
		PasswordHash string            `json:"passwordHash"`
		Values       map[string]string `json:"values"`
	}
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	for index := range encoded {
		encoded[index] = 0
	}
	if snapshot.Origin != "http://localhost:5173" {
		t.Fatalf("unexpected snapshot origin %q", snapshot.Origin)
	}
	if snapshot.PasswordHash == "" {
		t.Fatal("snapshot has no PWA-derived password")
	}
	password := snapshot.PasswordHash
	snapshot.PasswordHash = ""

	database := newMemoryKVDB()
	for key, value := range snapshot.Values {
		raw, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("decode selected database value for key class %s: %v", diagnosticKeyClass(key), err)
		}
		if err := database.Write([]byte(key), raw); err != nil {
			t.Fatal(err)
		}
		for index := range raw {
			raw[index] = 0
		}
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
	remoteStateRecord, stateListErr := diagnosticExactRemoteRecord(client,
		"/personal/"+profile.AccountID+"/account", stateKey)
	remoteDataRecord, dataListErr := diagnosticExactRemoteRecord(client, dataKey, dataKey)
	recoveryPrefix := "/personal/" + profile.AccountID + "/account/recovery"
	recoveryRecords, _, recoveryListErr := client.ListRecords(recoveryPrefix, 0, 100)
	localGuardianKey, localGuardianKeyErr := manager.LoadAccountGuardianPrivateKey(password)
	defer zeroBytes(localGuardianKey)
	var wrapperRecord *swire.DKVSRecord
	var wrapperListErr error
	var wrapperSecret []byte
	wrapperOpen := false
	wrapperValidatesRemote := false
	if rootWallet != nil {
		wrapperKey, keyErr := accountRootWrapperKey(rootWallet)
		if keyErr != nil {
			wrapperListErr = keyErr
		} else {
			wrapperRecord, wrapperListErr = diagnosticExactRemoteRecord(client, wrapperKey, wrapperKey)
			if wrapperRecord != nil {
				payload, openErr := openAccountRootWrapper(rootWallet, _chain, profile.AccountID, wrapperRecord.Value)
				if openErr == nil {
					wrapperOpen = true
					wrapperSecret = payload.Secret
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
	var remoteManagedErr error
	remoteManagedAttempted := false
	remoteManagedRevision := uint64(0)
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
	outbox, outboxErr := newDKVSReplicaStore(database).loadBatchOutbox(namespace)
	if outboxErr != nil {
		t.Fatal(outboxErr)
	}
	type terminalSummary struct {
		Key, State, ErrorCode, EndpointID, OriginDomain string
		Keys                                            []string
		Heights                                         []uint64
		TTLs                                            []uint64
		ExpectAbsent                                    []bool
	}
	terminals := make([]terminalSummary, 0)
	type accountOutboxSummary struct {
		Key, State, ErrorCode, OriginDomain string
		OriginGeneration                    uint64
		RecordSeqs                          []uint64
		KeyClasses                          []string
	}
	accountOutbox := make([]accountOutboxSummary, 0)
	for _, entry := range outbox {
		mutations, _, decodeErr := entry.decode()
		if decodeErr == nil {
			summary := accountOutboxSummary{Key: entry.Key, State: entry.State,
				ErrorCode: entry.LastErrorCode, OriginDomain: entry.OriginDomain,
				OriginGeneration: entry.OriginGeneration}
			matchesAccount := false
			for _, mutation := range mutations {
				if mutation.Record == nil {
					continue
				}
				matchedKey := false
				switch mutation.Record.Key {
				case stateKey:
					summary.KeyClasses = append(summary.KeyClasses, "account-state")
					matchedKey = true
				case dataKey:
					summary.KeyClasses = append(summary.KeyClasses, "account-blob")
					matchedKey = true
				}
				if matchedKey {
					matchesAccount = true
					summary.RecordSeqs = append(summary.RecordSeqs, mutation.Record.Seq)
				}
			}
			if matchesAccount {
				accountOutbox = append(accountOutbox, summary)
			}
		}
		if entry == nil || entry.State != dkvsSessionTerminal {
			continue
		}
		if decodeErr != nil {
			continue
		}
		summary := terminalSummary{Key: entry.Key, State: entry.State,
			ErrorCode:  entry.LastErrorCode,
			EndpointID: entry.EndpointID, OriginDomain: entry.OriginDomain}
		for _, mutation := range mutations {
			if mutation.Record == nil || mutation.Record.IssueHeight != 39791 {
				continue
			}
			summary.Keys = append(summary.Keys, mutation.Record.Key)
			summary.Heights = append(summary.Heights, mutation.Record.IssueHeight)
			summary.TTLs = append(summary.TTLs, mutation.Record.TTL)
			summary.ExpectAbsent = append(summary.ExpectAbsent, mutation.Precondition.ExpectAbsent)
		}
		if len(summary.Keys) != 0 {
			terminals = append(terminals, summary)
		}
	}
	sort.Slice(terminals, func(i, j int) bool { return terminals[i].Key < terminals[j].Key })

	decision := "stop-local-profile-invalid"
	if localHashOK && localStateErr == nil && rootMatch && localManagedErr == nil {
		switch {
		case stateListErr != nil || dataListErr != nil:
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
		remoteStateRecord != nil, diagnosticRecordSeq(remoteStateRecord),
		diagnosticRecordHeight(remoteStateRecord), diagnosticRecordTTL(remoteStateRecord),
		diagnosticRecordHash(remoteStateRecord), remoteStateErr == nil && remoteStateRecord != nil,
		remoteState.Revision, stateListErr != nil)
	t.Logf("remote blob: exists=%t seq=%d issue_height=%d ttl=%d hash=%s open_attempted=%t open=%t revision=%d read_error=%t",
		remoteDataRecord != nil, diagnosticRecordSeq(remoteDataRecord),
		diagnosticRecordHeight(remoteDataRecord), diagnosticRecordTTL(remoteDataRecord),
		diagnosticRecordHash(remoteDataRecord), remoteManagedAttempted,
		remoteManagedAttempted && remoteManagedErr == nil && remoteDataRecord != nil,
		remoteManagedRevision, dataListErr != nil)
	t.Logf("root wrapper: exists=%t seq=%d issue_height=%d open=%t validates_remote=%t read_error=%t",
		wrapperRecord != nil, diagnosticRecordSeq(wrapperRecord), diagnosticRecordHeight(wrapperRecord),
		wrapperOpen, wrapperValidatesRemote, wrapperListErr != nil)
	for _, record := range recoveryRecords {
		if record == nil {
			continue
		}
		pkg, decodeErr := account.DecodeRecoveryPackageStorage(record.Value)
		knowledgeMatch := false
		mode := account.RecoveryMode("")
		packageID := "invalid"
		guardian := false
		guardianLocalMatch := false
		questionIDs := make([]string, 0)
		unitAnswersMatch := false
		if decodeErr == nil {
			mode = pkg.Envelope.Locator.RecoveryMode
			packageID = diagnosticShort(pkg.Envelope.Locator.PackageID)
			guardian = pkg.Manifest.Guardian != nil
			for _, question := range pkg.KnowledgeBundle.QuestionShares {
				questionIDs = append(questionIDs, question.Question.ID)
			}
			_, recoverErr := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle,
				[]account.AnswerAttempt{
					{QuestionID: "account-e2e-a", Answer: "sat20 account recovery answer alpha"},
					{QuestionID: "account-e2e-b", Answer: "sat20 account recovery answer beta"},
					{QuestionID: "account-e2e-c", Answer: "sat20 account recovery answer gamma"},
				})
			knowledgeMatch = recoverErr == nil
			_, unitRecoverErr := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle,
				[]account.AnswerAttempt{
					{QuestionID: "book-page", Answer: "月光落在安静的旧桥上"},
					{QuestionID: "private-note", Answer: "yellow bicycle beside the winter river"},
				})
			unitAnswersMatch = unitRecoverErr == nil
			if guardian && localGuardianKeyErr == nil && len(localGuardianKey) == 32 {
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
		t.Logf("recovery package: package=%s mode=%s guardian=%t local_guardian_match=%t questions=%v seq=%d issue_height=%d decode=%t e2e_answers_match=%t unit_answers_match=%t",
			packageID, mode, guardian, guardianLocalMatch, questionIDs,
			diagnosticRecordSeq(record), diagnosticRecordHeight(record),
			decodeErr == nil, knowledgeMatch, unitAnswersMatch)
	}
	if recoveryListErr != nil {
		t.Logf("recovery package read_error=true")
	}
	for _, entry := range terminals {
		t.Logf("future terminal: key=%s state=%s error=%s keys=%v heights=%v ttls=%v expect_absent=%v endpoint=%s origin=%s",
			entry.Key, entry.State, entry.ErrorCode, entry.Keys, entry.Heights, entry.TTLs,
			entry.ExpectAbsent, entry.EndpointID, entry.OriginDomain)
	}
	for _, entry := range accountOutbox {
		t.Logf("account outbox: key=%s state=%s error=%s origin=%s generation=%d keys=%v seqs=%v",
			entry.Key, entry.State, entry.ErrorCode, entry.OriginDomain,
			entry.OriginGeneration, entry.KeyClasses, entry.RecordSeqs)
	}
	t.Logf("decision: %s future_terminal_count=%d namespace=%s", decision, len(terminals), namespace)
}

// TestDiagnosticGuardianPackageMatch checks whether an isolated PWA profile
// owns the guardian key for recovery packages without exposing that key or any
// recovered share. It is opt-in and removes its sensitive snapshot on exit.
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
		Origin       string            `json:"origin"`
		PasswordHash string            `json:"passwordHash"`
		Values       map[string]string `json:"values"`
	}
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		t.Fatal(err)
	}
	for index := range encoded {
		encoded[index] = 0
	}
	if snapshot.Origin != "http://localhost:5173" || snapshot.PasswordHash == "" {
		t.Fatal("unexpected guardian snapshot")
	}
	password := snapshot.PasswordHash
	snapshot.PasswordHash = ""
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
	prefix := "/personal/" + accountID + "/account/recovery"
	records, _, err := client.ListRecords(prefix, 0, 100)
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
	case strings.HasPrefix(key, string(dkvsBatchOutboxPrefix)):
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
