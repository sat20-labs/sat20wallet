package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Exercise the public SDK entry points used by PWA. Recovery must discover the
// root wrapper; these cases do not inject a recovered secret or decoded state.
func TestSDKAccountPWAEntryPoints(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	t.Run("UnavailableDiscoveryCannotAuthorizeNewAccountSecret", func(t *testing.T) {
		config, _ := accountReviewConfig(t, network)
		config.IndexerL2.Host = "127.0.0.1:1"
		db := indexerdb.NewKVDB(t.TempDir())
		require.NotNil(t, db)
		manager := wallet.NewManager(config, db)
		t.Cleanup(func() { manager.Close(); db.Close() })
		mnemonic := accountSyncMnemonic(t, 207)
		_, err := manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountDiscoveryPending)
		require.Empty(t, manager.GetWalletCatalog())
		_, err = manager.ImportWallet(mnemonic, accountReviewPassword)
		require.NoError(t, err)
		require.False(t, manager.GetAccountManagementStatus().Active)
	})

	t.Run("CreateFirstWalletAutomaticallyInitializesAccount", func(t *testing.T) {
		manager, _ := accountReviewDevice(t, network, "")
		id, mnemonic, err := manager.CreateWallet(accountReviewPassword)
		require.NoError(t, err)
		require.NotEmpty(t, mnemonic)
		status := manager.GetAccountManagementStatus()
		require.True(t, status.Active)
		require.False(t, status.RecoveryConfigured)
		require.Equal(t, id, status.RootWalletID)
		require.Equal(t, dkvs.AccountID(manager.GetWallet().GetPubKey().SerializeCompressed()), status.AccountID)
		_, err = manager.AccountPreflight("wrong-password", nil)
		require.Error(t, err)
		preflight, err := manager.AccountPreflight(accountReviewPassword, nil)
		require.NoError(t, err)
		require.Equal(t, status.AccountID, preflight.AccountID)
	})

	t.Run("AuthoritativeNotFoundThenImportInitializesOnlyThatRoot", func(t *testing.T) {
		manager, _ := accountReviewDevice(t, network, "")
		mnemonic := accountSyncMnemonic(t, 201)
		_, err := manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountNotFound)
		require.Empty(t, manager.GetWalletCatalog())
		require.False(t, manager.GetAccountManagementStatus().Active)
		require.NoError(t, manager.StartDKVSSync())
		require.Eventually(t, func() bool {
			_, err = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, accountReviewPassword)
			return errors.Is(err, wallet.ErrRootAccountNotFound)
		}, 30*time.Second, 100*time.Millisecond)
		id, err := manager.ImportWallet(mnemonic, accountReviewPassword)
		require.NoError(t, err)
		require.True(t, manager.GetAccountManagementStatus().Active)
		require.Equal(t, id, manager.GetAccountManagementStatus().RootWalletID)
	})

	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 202), true)
	f.activate(t)
	rootID := f.manager.GetAccountManagementStatus().RootWalletID
	childID, _, err := f.manager.CreateWallet(accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, f.manager.UpdateWalletName(childID, "Created after activation"))
	require.NoError(t, f.manager.EnsureAccount(childID, 1, "Before rename", "did:pwa:child"))
	require.NoError(t, f.manager.UpdateAccountMetadata(childID, 1, "Travel account", "did:pwa:travel"))
	require.NoError(t, f.manager.UpdateWalletName(rootID, "Latest root"))
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))

	t.Run("RootMnemonicRestoresLatestCatalogWithNewLocalPassword", func(t *testing.T) {
		fresh, _ := accountReviewDevice(t, network, "")
		require.NoError(t, fresh.StartDKVSSync())
		const newPassword = "pwa-new-device-password"
		requireAccountPWARootRecovery(t, fresh, f.rootMnemonic, newPassword)
		require.Equal(t, f.manager.GetWalletCatalog()[0].Fingerprint, fresh.GetWalletCatalog()[0].Fingerprint)
		require.Equal(t, "Latest root", fresh.GetWalletCatalog()[0].Name)
		require.Len(t, fresh.GetWalletCatalog(), 2)
		child := accountReviewFind(t, fresh, f.manager.GetWalletCatalog()[1].Fingerprint)
		require.Equal(t, "Created after activation", child.Name)
		require.Equal(t, "Travel account", child.Accounts[1].Name)
		require.Equal(t, "did:pwa:travel", child.Accounts[1].DID)
		require.Equal(t, f.manager.GetAccountManagementStatus().AccountID, fresh.GetAccountManagementStatus().AccountID)
		for _, entry := range fresh.GetWalletCatalog() {
			require.Empty(t, fresh.GetMnemonic(entry.ID, accountReviewPassword))
			require.NotEmpty(t, fresh.GetMnemonic(entry.ID, newPassword))
		}
	})

	t.Run("CurrentWalletDiscoveryUsesRootDespiteChildSelection", func(t *testing.T) {
		before := f.manager.GetWalletCatalog()
		_, err := f.manager.RecoverAccountManagementFromCurrentWallet(context.Background(), "wrong-password")
		require.Error(t, err)
		require.Equal(t, before, f.manager.GetWalletCatalog())
		_, err = f.manager.RecoverAccountManagementFromCurrentWallet(context.Background(), accountReviewPassword)
		require.NoError(t, err)
		require.Equal(t, rootID, f.manager.GetAccountManagementStatus().RootWalletID)
		require.Equal(t, before, f.manager.GetWalletCatalog())
		require.NoError(t, f.manager.UpdateAccountMetadata(childID, 1, "Pending rename", "did:pwa:pending"))
		_, err = f.manager.RecoverAccountManagementFromCurrentWallet(context.Background(), accountReviewPassword)
		require.NoError(t, err)
		require.Equal(t, "Pending rename", accountReviewFind(t, f.manager, before[1].Fingerprint).Accounts[1].Name)
		require.Zero(t, f.manager.GetAccountManagementStatus().PendingChanges)
	})

	t.Run("PublicLocatorAuthenticatesStorageAndNetwork", func(t *testing.T) {
		locator := wallet.AccountPublicLocator{Version: account.Version, Network: "testnet",
			StorageLocation: f.location, StorageMode: f.authorization.Mode, RecordTTL: f.authorization.RecordOptions.TTL,
			Locator: f.pkg.Envelope.Locator}
		require.NoError(t, f.manager.SignAccountPublicLocator(&locator))
		encoded, err := wallet.EncodeAccountPublicLocator(locator, "testnet")
		require.NoError(t, err)
		decoded, err := wallet.DecodeAccountPublicLocator(encoded, "testnet")
		require.NoError(t, err)
		require.Equal(t, locator, decoded)
		_, err = wallet.DecodeAccountPublicLocator(encoded, "mainnet")
		require.Error(t, err)
		locator.StorageLocation.Host = "different-core"
		require.Error(t, wallet.VerifyAccountPublicLocator(locator, "testnet"))
	})
	t.Run("ActiveAccountCannotBeReplacedByAnotherRoot", func(t *testing.T) {
		other := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 206), true)
		other.activate(t)
		otherRoot := keyFromMnemonic(t, other.rootMnemonic, 0)
		wrapperKey, err := dkvs.PersonalKey(otherRoot.PubKey().SerializeCompressed(), "account/root-key-wrapper/current")
		require.NoError(t, err)
		_, err = dkvsClientForNode(t, network.Core).GetRecordDirect(wrapperKey)
		require.NoError(t, err, "the other account really exists; an unsynchronized local directory cannot establish not-found")
		before := f.manager.GetWalletCatalog()
		identity := f.manager.GetAccountManagementStatus().AccountID
		_, err = f.manager.RecoverAccountManagementFromRootMnemonic(context.Background(), other.rootMnemonic, accountReviewPassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountWrapperInvalid)
		require.NotErrorIs(t, err, wallet.ErrRootAccountNotFound)
		require.Equal(t, identity, f.manager.GetAccountManagementStatus().AccountID)
		require.Equal(t, before, f.manager.GetWalletCatalog())
	})
}

func requireAccountPWARootRecovery(t *testing.T, manager *wallet.Manager, mnemonic, password string) {
	t.Helper()
	var lastErr error
	require.Eventually(t, func() bool {
		_, lastErr = manager.RecoverAccountManagementFromRootMnemonic(context.Background(), mnemonic, password)
		if lastErr != nil && !errors.Is(lastErr, wallet.ErrRootAccountDiscoveryPending) {
			t.Fatalf("root discovery failed: %v", lastErr)
		}
		return lastErr == nil
	}, 30*time.Second, 100*time.Millisecond, "root discovery did not complete: %v", lastErr)
}

func TestSDKAccountPWAOfflineRestart(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 203), true)
	f.activate(t)
	config, _ := accountReviewConfig(t, network)
	dir := t.TempDir()
	db := indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager := wallet.NewManager(config, db)
	t.Cleanup(func() { manager.Close(); db.Close() })
	require.NoError(t, manager.StartDKVSSync())
	requireAccountPWARootRecovery(t, manager, f.rootMnemonic, accountReviewPassword)
	manager.StopDKVSSync()
	root := manager.GetWalletCatalog()[0]
	require.NoError(t, manager.UpdateWalletName(root.ID, "Unsynced before restart"))
	require.NoError(t, manager.UpdateAccountMetadata(root.ID, 2, "Offline savings", "did:pwa:offline"))
	require.Greater(t, manager.GetAccountManagementStatus().PendingChanges, 0)
	manager.Close()
	db.Close()
	db = indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager = wallet.NewManager(config, db)
	require.Nil(t, manager.GetWallet())
	_, err := manager.UnlockWallet(accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, "Unsynced before restart", manager.GetWalletCatalog()[0].Name)
	require.Greater(t, manager.GetAccountManagementStatus().PendingChanges, 0)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	require.Equal(t, "Unsynced before restart", f.manager.GetWalletCatalog()[0].Name)
	require.Equal(t, "Offline savings", f.manager.GetWalletCatalog()[0].Accounts[2].Name)
	require.Equal(t, "did:pwa:offline", f.manager.GetWalletCatalog()[0].Accounts[2].DID)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
}

func TestSDKAccountPWAIndependentGuardian(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 204), true)
	guardian, guardianLocation := accountReviewDevice(t, network, accountSyncMnemonic(t, 205))
	require.NoError(t, guardian.InitializeAccountManagement(accountReviewPassword))
	require.NoError(t, guardian.BindAccountToCurrentCoreNode())
	identity, err := guardian.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
	require.NoError(t, err)
	require.NotEqual(t, f.manager.GetAccountManagementStatus().AccountID, identity.MailboxID)
	publicKey, err := base64.RawURLEncoding.DecodeString(identity.PublicKey)
	require.NoError(t, err)
	backup, err := f.manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
	require.NoError(t, err)
	pkg, err := f.manager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: f.manager.GetAccountManagementStatus().AccountID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of3, Questions: e2eKnowledgeQuestions(),
		GuardianMailboxID: identity.MailboxID, GuardianPublicKey: publicKey})
	require.NoError(t, err)
	repo, err := f.manager.NewAccountRepositoryForStorage(*f.authorization)
	require.NoError(t, err)
	require.NoError(t, account.NewManager(repo).Publish(context.Background(), *pkg))
	auth, err := guardian.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
	require.NoError(t, err)
	require.NoError(t, guardian.UseAccountStorageAuthorization(wallet.AccountStoragePurposeGuardian,
		func(value *wallet.AccountStorageAuthorization) error {
			return guardian.PutGuardianCapsuleForStorage(*value, identity.MailboxID, *pkg.GuardianCapsule)
		}))
	require.Equal(t, guardianLocation, auth.Location)
	_, err = guardian.GetOrCreateAccountGuardianIdentity("wrong-password")
	require.Error(t, err)
	stable, err := guardian.GetOrCreateAccountGuardianIdentity(accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, identity, stable)
	encoded, err := guardian.LoadAccountGuardianCapsule(guardianLocation, identity.MailboxID, pkg.Envelope.Locator.PackageID, pkg.Manifest.Guardian.ShareID)
	require.NoError(t, err)
	var capsule account.GuardianShareCapsule
	require.NoError(t, json.Unmarshal(encoded, &capsule))
	privateKey, err := guardian.LoadAccountGuardianPrivateKey(accountReviewPassword)
	require.NoError(t, err)
	defer clearBytes(privateKey)
	guardianShare, err := account.DecryptGuardianShare(capsule, privateKey)
	require.NoError(t, err)
	knowledge, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, e2eKnowledgeAnswers())
	require.NoError(t, err)
	_, secret, err := account.RecoverAccount(pkg.Envelope, knowledge, guardianShare)
	require.NoError(t, err)
	defer clearBytes(secret)
	require.Equal(t, f.secret, secret)
	require.NoError(t, f.manager.ActivateAccountManagement(secret, accountReviewPassword, *f.authorization, pkg.Envelope.Locator, ""))
	fresh, _ := accountReviewDevice(t, network, "")
	state, err := fresh.LoadAccountManagementStateForRecovery(f.location, pkg.Envelope.Locator, secret, f.rootMnemonic)
	require.NoError(t, err)
	_, err = fresh.RestoreAccountManagementState(*state, secret, accountReviewPassword, pkg.Envelope.Locator, f.restoreOptions())
	require.NoError(t, err)
	require.Equal(t, f.manager.GetWalletCatalog()[0].Accounts, fresh.GetWalletCatalog()[0].Accounts)
	key, err := dkvs.MailShareKey(identity.MailboxID, pkg.Envelope.Locator.PackageID, pkg.Manifest.Guardian.ShareID)
	require.NoError(t, err)
	requireDKVSAbsent(t, network.Bootstrap, key)
}

// The proxy forwards the production request to the real node and loses only
// its first successful ACK. Reopening the SDK must retry the original signed
// request, including its RequestID, authorization and CAS preconditions.
func TestSDKAccountPWACommitResponseLossRestart(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReviewWithMnemonic(t, network, accountSyncMnemonic(t, 208), true)
	f.activate(t)
	config, _ := accountReviewConfig(t, network)
	target, err := url.Parse("http://" + config.IndexerL2.Host)
	require.NoError(t, err)
	proxy := httputil.NewSingleHostReverseProxy(target)
	var loseACK atomic.Bool
	var requestMu sync.Mutex
	var requests [][]byte
	proxy.ModifyResponse = func(response *http.Response) error {
		if strings.HasSuffix(response.Request.URL.Path, "/records/batch-cas") && response.StatusCode == http.StatusOK &&
			response.Request.Header.Get("X-E2E-Account-State") == "1" && loseACK.Swap(false) {
			response.Body.Close()
			response.StatusCode = http.StatusServiceUnavailable
			body := []byte(`{"code":-1,"msg":"controlled ACK loss after real commit"}`)
			response.Body = io.NopCloser(bytes.NewReader(body))
			response.ContentLength = int64(len(body))
			response.Header.Del("Content-Length")
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/records/batch-cas") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "read request", http.StatusBadRequest)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			var batch struct {
				Mutations []struct{ Record struct{ Key string } }
			}
			if json.Unmarshal(body, &batch) == nil {
				for _, mutation := range batch.Mutations {
					if strings.HasSuffix(mutation.Record.Key, "/account/state") {
						r.Header.Set("X-E2E-Account-State", "1")
						requestMu.Lock()
						requests = append(requests, bytes.Clone(body))
						requestMu.Unlock()
						break
					}
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer server.Close()
	config.IndexerL2.Host = strings.TrimPrefix(server.URL, "http://")
	dir := t.TempDir()
	db := indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager := wallet.NewManager(config, db)
	t.Cleanup(func() { manager.Close(); db.Close() })
	require.NoError(t, manager.StartDKVSSync())
	requireAccountPWARootRecovery(t, manager, f.rootMnemonic, accountReviewPassword)
	manager.StopDKVSSync()
	requestMu.Lock()
	requests = nil
	requestMu.Unlock()
	require.NoError(t, manager.UpdateWalletName(manager.GetWalletCatalog()[0].ID, "Committed without client ACK"))
	loseACK.Store(true)
	require.Error(t, manager.SyncAccountManagementState(context.Background()))
	require.False(t, loseACK.Load(), "the node must have committed before ACK loss")
	stateKey, err := dkvs.PersonalKey(manager.GetWallet().GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	committed := waitForDKVSRecord(t, network.Core, stateKey)
	requestMu.Lock()
	committedRequest := bytes.Clone(requests[0])
	requestMu.Unlock()
	var committedBatch dkvscore.BatchCASRequest
	require.NoError(t, json.Unmarshal(committedRequest, &committedBatch))
	for _, mutation := range committedBatch.Mutations {
		if mutation.Record.Key == stateKey {
			require.Equal(t, dkvs.RecordHash(mutation.Record), dkvs.RecordHash(committed), "the lost response must follow a committed state")
		}
	}
	manager.Close()
	db.Close()
	db = indexerdb.NewKVDB(dir)
	require.NotNil(t, db)
	manager = wallet.NewManager(config, db)
	_, err = manager.UnlockWallet(accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.StartDKVSSync())
	require.Eventually(t, func() bool {
		return manager.SyncAccountManagementState(context.Background()) == nil &&
			manager.GetAccountManagementStatus().PendingChanges == 0
	}, 30*time.Second, 100*time.Millisecond)
	requestMu.Lock()
	captured := append([][]byte(nil), requests...)
	requestMu.Unlock()
	require.GreaterOrEqual(t, len(captured), 2)
	var original, retry dkvscore.BatchCASRequest
	require.NoError(t, json.Unmarshal(captured[0], &original))
	require.NoError(t, json.Unmarshal(captured[1], &retry))
	require.Equal(t, original.RequestID, retry.RequestID)
	require.Equal(t, original.EndpointID, retry.EndpointID)
	require.Equal(t, original.Authorization, retry.Authorization)
	require.Len(t, retry.Mutations, len(original.Mutations))
	for i, mutation := range original.Mutations {
		require.NotNil(t, mutation.Record)
		require.NotNil(t, retry.Mutations[i].Record)
		// Binary record hash includes the signature. Empty byte slices may
		// encode as null or "" in JSON after durable binary decoding.
		require.Equal(t, dkvs.RecordHash(mutation.Record), dkvs.RecordHash(retry.Mutations[i].Record))
		require.Equal(t, mutation.ExpectedETag, retry.Mutations[i].ExpectedETag)
		require.Equal(t, mutation.ExpectAbsent, retry.Mutations[i].ExpectAbsent)
	}
	current := waitForDKVSRecord(t, network.Core, stateKey)
	require.Equal(t, dkvs.RecordHash(committed), dkvs.RecordHash(current), "retry must not create another revision (Seq %d -> %d)", committed.Seq, current.Seq)
	require.Zero(t, manager.GetAccountManagementStatus().PendingChanges)
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	require.Equal(t, "Committed without client ACK", f.manager.GetWalletCatalog()[0].Name)
}
