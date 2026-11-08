package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestTemporaryAccountCanMaintainExistingGuardianAutopay(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager := newAutopayWriteFixture(t, false)
	before, err := json.Marshal(manager.accountProfile)
	require.NoError(t, err)
	root, err := manager.accountManagementRootWallet()
	require.NoError(t, err)
	payer := PublicKeyToP2TRAddress_SatsNet(root.GetPubKey())
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	state := &dkvsindexer.AutopayContractState{Contract: defaults.AutopayContract,
		TemplateName: TEMPLATE_CONTRACT_AUTOPAY, Status: "active",
		ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName, CurrentBlock: 100,
		Delegates: map[string]dkvsindexer.AutopayDelegateState{}}
	manager.l2IndexerClient = NewIndexerRPCClientMgr()
	manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", &autopayReceiptTestHTTP{state: state}))
	status, err := manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.False(t, status.Required, "temporary account without an AUTOPAY delegate must not require funding")
	state.Delegates[payer] = dkvsindexer.AutopayDelegateState{Status: "active", AmountPerBlock: "100", Balance: "100000", LastPayHeight: 100}
	status, err = manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.Required, "paid Guardian hosting must remain maintainable without upgrading own backup")
	require.True(t, status.Ready)
	require.True(t, status.CanFund, "a ready AUTOPAY may still be explicitly renewed")
	beforeLogs, err := manager.operationLogManager().List()
	require.NoError(t, err)
	authorization, err := manager.ReusePaidAccountStorage(accountDefaultRecordCount)
	require.NoError(t, err)
	require.Equal(t, AccountStoragePaid, authorization.Mode)
	require.Empty(t, authorization.TransactionID)
	afterLogs, err := manager.operationLogManager().List()
	require.NoError(t, err)
	require.Equal(t, beforeLogs, afterLogs, "reusing the active Guardian delegate must not create a funding operation")
	delegate := state.Delegates[payer]
	delegate.Balance = "0"
	state.Delegates[payer] = delegate
	status, err = manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.Required)
	require.False(t, status.Ready)
	require.True(t, status.CanFund)
	after, err := json.Marshal(manager.accountProfile)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after), "maintenance must not change own recovery or storage mode")
}

type accountFundingRunningLogDB struct {
	indexer.KVDB
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	creates atomic.Int32
}

func (d *accountFundingRunningLogDB) Write(key, value []byte) error {
	var record OperationLogRecord
	if strings.Contains(string(key), DB_KEY_OPERATION_LOG) && DecodeFromBytes(value, &record) == nil &&
		record.Action == "account_autopay_fund" && record.Status == OperationLogRunning && record.TxID == "" {
		d.creates.Add(1)
		if err := d.KVDB.Write(key, value); err != nil {
			return err
		}
		d.once.Do(func() { close(d.entered); <-d.release })
		return fmt.Errorf("review: unsigned preparation interrupted")
	}
	return d.KVDB.Write(key, value)
}

func TestAccountGuardianIdentitySurvivesAccountRecovery(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	remote := newRGB11MemoryDKVSHTTP()
	makeActive := func(mnemonic string) (*Manager, *AccountStorageAuthorization, []byte) {
		manager := newAccountManagementAutoTestManager(t)
		configureRGB11DKVSTestManager(manager, remote)
		service := newRGB11MessageNodeClient(remote)
		manager.serverNode = NewNode(service, "message.test", SERVER_NODE, service.CoreNodePubKey(), service.CoreNodePubKey())
		_, err := manager.ImportWallet(mnemonic, "password")
		require.NoError(t, err)
		require.NoError(t, manager.InitializeAccountManagement("password"))
		require.NoError(t, manager.refreshDKVSRegistrations())
		_, err = manager.syncDKVSOnce()
		require.NoError(t, err)
		pkg, auth, secret := rereviewRecoveryPackage(t, manager)
		root, err := manager.accountManagementRootWallet()
		require.NoError(t, err)
		publicLocator, err := buildAccountPublicLocator(root, _chain, auth.Location,
			accountRootWrapperPayload{PackageID: pkg.Envelope.Locator.PackageID,
				StorageMode: auth.Mode, RecordTTL: auth.RecordOptions.TTL}, pkg.Envelope.Locator)
		require.NoError(t, err)
		require.NoError(t, manager.ActivateAccountManagement(secret, "password", *auth,
			pkg.Envelope.Locator, publicLocator))
		require.NoError(t, manager.SyncAccountManagementState(context.Background()))
		return manager, auth, secret
	}
	source, _, secret := makeActive(accountRootWrapperTestMnemonic)
	defer zeroBytes(secret)
	guardian, _, guardianSecret := makeActive("legal winner thank year wave sausage worth useful legal winner thank yellow")
	defer zeroBytes(guardianSecret)
	identity, err := source.GetOrCreateAccountGuardianIdentity("password")
	require.NoError(t, err)
	ownerKey, err := source.LoadAccountGuardianPrivateKey("password")
	require.NoError(t, err)
	defer zeroWalletBytes(ownerKey)
	guardianIdentity, err := guardian.GetOrCreateAccountGuardianIdentity("password")
	require.NoError(t, err)
	require.NotEqual(t, identity.MailboxID, guardianIdentity.MailboxID,
		"the recovery Guardian must have an independent account identity")
	guardianPrivate, err := guardian.LoadAccountGuardianPrivateKey("password")
	require.NoError(t, err)
	defer zeroWalletBytes(guardianPrivate)
	guardianPublic, err := base64.RawURLEncoding.DecodeString(guardianIdentity.PublicKey)
	require.NoError(t, err)

	backup, err := source.ExportAccountBackupForPWA("password", nil)
	require.NoError(t, err)
	bootstrap, err := account.RootBootstrapBackup(backup)
	clearAccountBackup(&backup)
	require.NoError(t, err)
	status := source.GetAccountManagementStatus()
	pkg, err := source.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: status.AccountID, Backup: bootstrap, RecoveryMode: account.RecoveryMode2Of3,
		Questions: coreQuestions(), GuardianMailboxID: guardianIdentity.MailboxID,
		GuardianPublicKey: guardianPublic,
	})
	clearAccountBackup(&bootstrap)
	require.NoError(t, err)
	require.NotNil(t, pkg.GuardianCapsule)
	require.NotNil(t, pkg.Manifest.Guardian)

	ownerStorage, err := source.ConfirmAccountStorage(AccountStorageTemporary, 0)
	require.NoError(t, err)
	location := ownerStorage.Location
	var publicLocator string
	var ownerAuthorization AccountStorageAuthorization
	err = source.UseAccountStorageAuthorization(AccountStoragePurposeRecovery, func(auth *AccountStorageAuthorization) error {
		repository, err := source.NewAccountRepositoryForStorage(*auth)
		if err != nil {
			return err
		}
		if err := account.NewManager(repository).Publish(context.Background(), *pkg); err != nil {
			return err
		}
		root, err := source.accountManagementRootWallet()
		if err != nil {
			return err
		}
		autopayContract := ""
		if auth.Autopay != nil {
			autopayContract = auth.Autopay.PoolContract
		}
		publicLocator, err = buildAccountPublicLocator(root, _chain, auth.Location,
			accountRootWrapperPayload{PackageID: pkg.Envelope.Locator.PackageID,
				StorageMode: auth.Mode, RecordTTL: auth.RecordOptions.TTL,
				AutopayContract: autopayContract}, pkg.Envelope.Locator)
		if err != nil {
			return err
		}
		ownerAuthorization = cloneAccountStorageAuthorization(*auth)
		return source.ActivateAccountManagement(secret, "password", *auth,
			pkg.Envelope.Locator, publicLocator)
	})
	require.NoError(t, err, "publish the recovery package at its real locator")
	require.Equal(t, ownerStorage.Location, ownerAuthorization.Location,
		"the locator and published recovery package must use the same confirmed storage location")
	require.NoError(t, source.SyncAccountManagementState(context.Background()))
	updated := source.GetAccountManagementStatus()
	require.Equal(t, pkg.Envelope.Locator.PackageID, updated.PackageID,
		"the source wrapper must reference the published two-of-three package")
	require.Equal(t, publicLocator, updated.PublicLocator)
	decodedLocator, err := DecodeAccountPublicLocator(updated.PublicLocator, _chain)
	require.NoError(t, err, "the public recovery locator must be canonical and signed")
	require.Equal(t, pkg.Envelope.Locator, decodedLocator.Locator)
	require.Equal(t, ownerAuthorization.Location, decodedLocator.StorageLocation)
	require.Equal(t, ownerAuthorization.Mode, decodedLocator.StorageMode)
	require.Equal(t, ownerAuthorization.RecordOptions.TTL, decodedLocator.RecordTTL)
	if ownerAuthorization.Autopay != nil {
		require.Equal(t, ownerAuthorization.Autopay.PoolContract, decodedLocator.AutopayContract)
	} else {
		require.Empty(t, decodedLocator.AutopayContract)
	}
	guardianStorage, err := guardian.ConfirmAccountStorage(AccountStorageTemporary, 0)
	require.NoError(t, err)
	guardianLocation, err := guardian.AccountIndexerLocation()
	require.NoError(t, err)
	require.Equal(t, guardianStorage.Location, guardianLocation,
		"the Guardian mailbox must be published under the independent Guardian account location")
	err = guardian.UseAccountStorageAuthorization(AccountStoragePurposeGuardian, func(auth *AccountStorageAuthorization) error {
		return guardian.PutGuardianCapsuleForStorage(*auth, guardianIdentity.MailboxID, *pkg.GuardianCapsule)
	})
	require.NoError(t, err, "publish the capsule through the independent Guardian account")

	target := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(target, remote)
	loaded, err := target.LoadAccountRecoveryPackage(location, pkg.Envelope.Locator)
	require.NoError(t, err)
	share, err := account.RecoverDKVSShare(loaded.DKVSShareCapsule, loaded.KnowledgeBundle, coreAnswers())
	require.NoError(t, err)
	ref := loaded.Manifest.Guardian
	require.NotNil(t, ref)
	capsuleBytes, err := target.LoadAccountGuardianCapsule(guardianStorage.Location, ref.MailboxID,
		loaded.Envelope.Locator.PackageID, ref.ShareID)
	require.NoError(t, err)
	var publishedCapsule account.GuardianShareCapsule
	require.NoError(t, json.Unmarshal(capsuleBytes, &publishedCapsule))
	guardianShare, err := account.DecryptGuardianShare(publishedCapsule, guardianPrivate)
	require.NoError(t, err, "the independently published Guardian capsule must be readable and decryptable")
	require.Equal(t, guardianIdentity.MailboxID, ref.MailboxID)
	recoveredBackup, recoveredSecret, err := account.RecoverAccount(loaded.Envelope, share, guardianShare)
	require.NoError(t, err)
	defer clearAccountBackup(&recoveredBackup)
	defer zeroBytes(recoveredSecret)
	require.Equal(t, secret, recoveredSecret, "published 2-of-3 recovery must restore the same account secret")
	require.NotEmpty(t, recoveredBackup.Wallets)
	state, err := target.LoadAccountManagementStateForRecovery(location, loaded.Envelope.Locator,
		recoveredSecret, string(recoveredBackup.Wallets[0].Secret))
	require.NoError(t, err)
	_, err = target.RestoreAccountManagementState(*state, recoveredSecret, "new-password", loaded.Envelope.Locator,
		AccountManagementRestoreOptions{Location: location, StorageMode: AccountStorageTemporary,
			RecordTTL: ownerStorage.RecordOptions.TTL})
	require.NoError(t, err)

	restoredOwnerKey, err := target.LoadAccountGuardianPrivateKey("new-password")
	require.NoError(t, err)
	defer zeroWalletBytes(restoredOwnerKey)
	_, err = target.LoadAccountGuardianPrivateKey("password")
	require.Error(t, err, "the pre-recovery password must not unlock the restored Guardian identity")
	require.Equal(t, ownerKey, restoredOwnerKey)
	stable, err := target.GetOrCreateAccountGuardianIdentity("new-password")
	require.NoError(t, err)
	require.Equal(t, identity, stable)
}

// Block the real REST state lookup before either public entry can construct a
// transaction. Navigation can start another request on the same PWA Manager.
type accountFundingPreparationHTTP struct {
	*autopayReceiptTestHTTP
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	queries atomic.Int32
}

func (h *accountFundingPreparationHTTP) SendGetRequest(url *URL) ([]byte, error) {
	if strings.HasSuffix(url.Path, "/state") {
		h.queries.Add(1)
		first := false
		h.once.Do(func() { first = true; close(h.entered) })
		if first {
			<-h.release
			return nil, fmt.Errorf("review: preparation lookup interrupted")
		}
	}
	return h.autopayReceiptTestHTTP.SendGetRequest(url)
}

func TestAccountAutopayConcurrentFundingSingleFlight(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	for _, stage := range []string{"lookup", "running-without-txid"} {
		for _, first := range []string{"fund", "confirm"} {
			for _, second := range []string{"fund", "confirm"} {
				t.Run(stage+"/"+first+"/"+second, func(t *testing.T) {
					manager := newAutopayWriteFixture(t, true)
					defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
					state := &dkvsindexer.AutopayContractState{Contract: defaults.AutopayContract,
						TemplateName: TEMPLATE_CONTRACT_AUTOPAY, Status: "active",
						ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient,
						FeeAssetName: defaults.AutopayFeeAssetName, CurrentBlock: 100,
						Delegates: map[string]dkvsindexer.AutopayDelegateState{}}
					lookup := &autopayReceiptTestHTTP{state: state}
					manager.l2IndexerClient = NewIndexerRPCClientMgr()
					manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", lookup))
					quote, err := manager.GetAccountAutopayFundingStatus()
					require.NoError(t, err)
					barrier := &accountFundingPreparationHTTP{autopayReceiptTestHTTP: lookup,
						entered: make(chan struct{}), release: make(chan struct{})}
					logBarrier := &accountFundingRunningLogDB{KVDB: manager.db, entered: make(chan struct{}), release: make(chan struct{})}
					entered, releaseCh := barrier.entered, barrier.release
					if stage == "lookup" {
						manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", barrier))
					} else {
						manager.db = logBarrier
						entered, releaseCh = logBarrier.entered, logBarrier.release
					}
					var release sync.Once
					finish := func() { release.Do(func() { close(releaseCh) }) }
					t.Cleanup(finish)
					invoke := func(kind string) error {
						if kind == "fund" {
							_, err := manager.FundAccountAutopay(*quote)
							return err
						}
						_, err := manager.ConfirmAccountStorage(AccountStoragePaid, 100)
						return err
					}
					done := make(chan error, 1)
					go func() { done <- invoke(first) }()
					select {
					case <-entered:
					case <-time.After(5 * time.Second):
						t.Fatal("first preparation did not enter")
					}
					// Cancellation invalidates a grant, not a running payment.
					manager.CancelPendingAccountStorageAuthorization()
					other := make(chan error, 1)
					go func() { other <- invoke(second) }()
					select {
					case err := <-other:
						require.ErrorIs(t, err, ErrAccountStorageAuthorizationBusy)
					case <-time.After(5 * time.Second):
						t.Fatal("duplicate request blocked instead of returning busy")
					}
					if stage == "lookup" {
						require.Equal(t, int32(1), barrier.queries.Load(), "duplicate preparation reached network lookup")
					} else {
						require.Equal(t, int32(1), logBarrier.creates.Load(), "duplicate funding intent was created")
					}
					finish()
					require.ErrorContains(t, <-done, "interrupted")
					// A failed preparation releases the existing reservation for retry.
					session, err := manager.beginAccountStoragePreparation()
					require.NoError(t, err)
					manager.abandonAccountStoragePreparation(session)
				})
			}
		}
	}
}

func TestAccountAutopayFundingPreservesConfirmedStorageGrant(t *testing.T) {
	manager := newAutopayWriteFixture(t, false)
	session, err := manager.beginAccountStoragePreparation()
	require.NoError(t, err)
	auth, err := manager.finishAccountStoragePreparation(session, &AccountStorageAuthorization{
		Mode: AccountStorageTemporary, Location: session.Authorization.Location,
	})
	require.NoError(t, err)
	finish, err := manager.beginAccountAutopayFunding()
	require.NoError(t, err)
	defer finish()
	observed, err := manager.PendingAccountStorageAuthorization()
	require.NoError(t, err)
	require.Equal(t, auth.ID, observed.ID)
	manager.CancelPendingAccountStorageAuthorization()
	_, err = manager.beginAccountAutopayFunding()
	require.ErrorIs(t, err, ErrAccountStorageAuthorizationBusy)
}

// Use the production transaction builder and signer; only node acceptance is
// recorded by this native boundary fixture. Real-node coverage is separate.
func accountAutopayPreparedFixture(t *testing.T, prepared ...*Manager) (*Manager, *OperationLogRecord, []byte) {
	t.Helper()
	var manager *Manager
	if len(prepared) == 0 {
		manager = newAutopayWriteFixture(t, true)
	} else {
		manager = prepared[0]
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	payer := PublicKeyToP2TRAddress_SatsNet(manager.GetWallet().GetPubKey())
	record, err := manager.BeginOperationLog(OperationLogCreate{Action: "account_autopay_fund", Title: "充值 AUTOPAY",
		Parameters: map[string]string{"network": _chain, "payer": payer, "contract": defaults.AutopayContract,
			"asset": defaults.AutopayFeeAssetName, "amount_per_block": "10", "amount": "10000", "blob_key_limit": "1"}})
	require.NoError(t, err)
	contract, err := contractcommon.DecodeContractAddress(defaults.AutopayContract)
	require.NoError(t, err)
	param, err := (&contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: "10", BlobKeyLimit: 1}).Encode()
	require.NoError(t, err)
	point := wire.OutPoint{Hash: chainhash.Hash{1}, Index: 0}
	assets := wire.TxAssets{{Name: *wire.NewAssetNameFromString(defaults.AutopayFeeAssetName), Amount: *indexer.NewDefaultDecimal(10000)}}
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{Contract: contract,
		GasLimit: contractcommon.InvokeBaseGas, CallNonce: 7, Action: contractcommon.TemplateInvokeAPIConfig,
		Param: param, Funding: wire.TxOut{Value: 9000, Assets: assets}, Inputs: []wire.OutPoint{point}})
	require.NoError(t, err)
	script, err := GetP2TRpkScript(manager.GetWallet().GetPaymentPubKey())
	require.NoError(t, err)
	fetcher := stxscript.NewMultiPrevOutFetcher(nil)
	fetcher.AddPrevOut(point, wire.NewTxOut(10000, assets, script))
	signed, err := SignTxWithWallet_SatsNet(manager.GetWallet(), tx, fetcher)
	require.NoError(t, err)
	var encoded bytes.Buffer
	require.NoError(t, signed.Serialize(&encoded))
	record.TxID = signed.TxID()
	return manager, record, encoded.Bytes()
}

type accountAutopayReplayHTTP struct {
	*autopayReceiptTestHTTP
	txid        string
	submissions [][]byte
	ackLost     bool
}

func (h *accountAutopayReplayHTTP) SendPostRequest(_ *URL, body []byte) ([]byte, error) {
	h.submissions = append(h.submissions, append([]byte(nil), body...))
	if h.ackLost {
		return nil, fmt.Errorf("review: acknowledgement lost")
	}
	return json.Marshal(map[string]any{"code": 0, "data": h.txid})
}

func TestAccountAutopaySignedTransactionColdContinuation(t *testing.T) {
	// The shared group owns explicit testnet setup/restoration and a real DB.
	source, remote, service, closeSource := newAccountGroup(t)
	_, err := source.ImportWallet(accountRootWrapperTestMnemonic, "password")
	require.NoError(t, err)
	require.NoError(t, source.InitializeAccountManagement("password"))
	discoverInactiveAccount(t, source)
	pkg, auth, secret := rereviewRecoveryPackage(t, source)
	paid := *auth
	paid.Mode, paid.RecordOptions.TTL = AccountStoragePaid, 0
	paidDefaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	paid.Autopay = &DKVSAutopayOptions{
		AddressParams: GetChainParam_SatsNet(), PoolContract: paidDefaults.AutopayContract,
	}
	require.NoError(t, source.ActivateAccountManagement(secret, "password", paid,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID))
	require.NoError(t, source.StartDKVSSync())
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer warmCancel()
	require.NoError(t, source.SyncAccountManagementState(warmCtx))
	require.NoError(t, source.WaitAccountManagedDataReady(warmCtx))
	warmCancel()
	require.Equal(t, AccountStoragePaid, source.GetAccountManagementStatus().StorageMode)
	_, record, raw := accountAutopayPreparedFixture(t, source)
	require.NoError(t, source.operationLogManager().prepareAccountAutopayTransaction(record.ID, record.TxID, raw))
	cfg := source.cfg
	identity := source.GetAccountManagementStatus().AccountID
	closeSource() // Manager.Close then DB.Close; no old Manager remains in use.
	cold, _ := openAccountGroup(t, cfg, remote, service)
	_, err = cold.UnlockWallet("password")
	require.NoError(t, err)
	require.NoError(t, cold.StartDKVSSync())
	coldCtx, coldCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer coldCancel()
	require.NoError(t, cold.SyncAccountManagementState(coldCtx), "cold restart must obtain its real online account baseline before resubmission")
	coldCancel()
	require.True(t, cold.GetAccountManagementStatus().AccountID == identity, "cold reopen changed the managed account identity")
	require.True(t, cold.GetAccountManagementStatus().DKVSWriteAllowed)
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	_, state := accountAutopayReadyFixture()
	state.Contract = defaults.AutopayContract
	state.Delegates = map[string]dkvsindexer.AutopayDelegateState{}
	http := &accountAutopayReplayHTTP{autopayReceiptTestHTTP: &autopayReceiptTestHTTP{state: state}, txid: record.TxID, ackLost: true}
	cold.l2IndexerClient = NewIndexerRPCClientMgr()
	cold.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", http))
	status, err := cold.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.FundingCanResume)
	require.Empty(t, http.submissions, "status observation broadcast a transaction")
	for retry := 0; retry < 2; retry++ {
		result, err := cold.FundAccountAutopay(*status)
		require.NoError(t, err)
		require.True(t, result.Pending)
		require.Equal(t, record.TxID, result.TransactionID)
		http.ackLost = false
	}
	require.Len(t, http.submissions, 2)
	require.Equal(t, http.submissions[0], http.submissions[1])
	var submitted map[string]any
	require.NoError(t, json.Unmarshal(http.submissions[0], &submitted))
	found := false
	for _, value := range submitted {
		if value == hex.EncodeToString(raw) {
			found = true
		}
	}
	require.True(t, found, "broadcast did not reuse the exact persisted signed bytes")
	logs, err := cold.operationLogManager().List()
	require.NoError(t, err)
	require.Len(t, logs, 1)
	encoded, err := json.Marshal(logs[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PreparedTransaction")
	require.NotContains(t, string(encoded), base64.StdEncoding.EncodeToString(raw))
}

func TestAccountAutopayPreparedTransactionRejectsBrokenBinding(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, record, raw := accountAutopayPreparedFixture(t)
	require.NoError(t, manager.operationLogManager().prepareAccountAutopayTransaction(record.ID, record.TxID, raw))
	stored, err := manager.operationLogManager().Get(record.ID)
	require.NoError(t, err)
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	for _, field := range []string{"network", "payer", "contract", "asset", "amount_per_block", "amount", "txid", "bytes", "trailing", "missing"} {
		t.Run(field, func(t *testing.T) {
			broken := cloneOperationLogRecord(stored)
			switch field {
			case "txid":
				broken.TxID = strings.Repeat("f", 64)
			case "bytes":
				broken.PreparedTransaction[0] ^= 1
			case "trailing":
				broken.PreparedTransaction = append(broken.PreparedTransaction, 0)
			case "missing":
				broken.PreparedTransaction = nil
			case "amount":
				broken.Parameters[field] = "99999999"
			default:
				broken.Parameters[field] = "different"
			}
			http := &accountAutopayReplayHTTP{autopayReceiptTestHTTP: &autopayReceiptTestHTTP{}}
			manager.l2IndexerClient = NewIndexerRPCClientMgr()
			manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "receipt-test", "testnet", http))
			_, err := manager.resumeAccountAutopayTransaction(broken, defaults, stored.Parameters["payer"])
			require.Error(t, err)
			require.Empty(t, http.submissions)
		})
	}
}
