package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const coreRGB11TemporaryTTL = uint64(2)

// coreRGB11TemporaryPolicyHTTP changes only the test client's advertised
// temporary-record maximum. All writes and reads still go to the real CoreNode.
type coreRGB11TemporaryPolicyHTTP struct {
	HttpClient
	maxTTL uint64
	hits   int
}

func (h *coreRGB11TemporaryPolicyHTTP) SendGetRequest(url *URL) ([]byte, error) {
	raw, err := h.HttpClient.SendGetRequest(url)
	if err != nil || url == nil || !strings.HasSuffix(url.Path, "/v3/dkvs/config") {
		return raw, err
	}
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	var config dkvsindexer.ClientConfig
	if err := json.Unmarshal(response["data"], &config); err != nil {
		return nil, err
	}
	config.FreeLocal.MaxTTL = h.maxTTL
	response["data"], err = json.Marshal(config)
	if err != nil {
		return nil, err
	}
	h.hits++
	return json.Marshal(response)
}

func coreInstallRGB11TemporaryTTLPolicy(manager *Manager) *coreRGB11TemporaryPolicyHTTP {
	http := &coreRGB11TemporaryPolicyHTTP{HttpClient: NewHTTPClient(), maxTTL: coreRGB11TemporaryTTL}
	manager.SetDKVSHttpClient(http)
	return http
}

func coreRGB11TemporaryRecoveryMaterial(t *testing.T, manager *Manager,
	previous *coreRecoveryMaterial) *coreRecoveryMaterial {
	t.Helper()
	coreRequire(t, "initialize temporary account", manager.InitializeAccountManagement(coreE2EPassword))
	authorization, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
	coreRequire(t, "confirm real temporary DKVS storage", err)
	coreAssert(t, authorization.RecordOptions.TTL == coreRGB11TemporaryTTL,
		"temporary authorization did not use the test-only TTL 2 policy")

	backup, err := manager.ExportAccountBackupForPWA(coreE2EPassword, nil)
	coreRequire(t, "export temporary account catalog", err)
	defer clearAccountBackup(&backup)
	bootstrap, err := account.RootBootstrapBackup(backup)
	coreRequire(t, "build temporary root bootstrap backup", err)
	defer clearAccountBackup(&bootstrap)
	accountID, err := manager.RootAccountID()
	coreRequire(t, "read temporary account identity", err)
	pkg, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: accountID, Backup: bootstrap, RecoveryMode: account.RecoveryMode2Of2,
		Questions: coreQuestions(),
	})
	coreRequire(t, "create temporary recovery material", err)
	material := &coreRecoveryMaterial{auth: *authorization,
		locator: pkg.Envelope.Locator, userShare: pkg.UserShare}
	err = manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
		func(storage *AccountStorageAuthorization) error {
			repository, err := manager.NewAccountRepositoryForStorage(*storage)
			if err != nil {
				return err
			}
			if err := account.NewManager(repository).Publish(context.Background(), *pkg); err != nil {
				return err
			}
			share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, coreAnswers())
			if err != nil {
				return err
			}
			recovered, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
			if err != nil {
				return err
			}
			defer zeroBytes(secret)
			defer clearAccountBackup(&recovered)
			return manager.ActivateAccountManagement(secret, coreE2EPassword,
				*storage, pkg.Envelope.Locator, "")
		})
	coreRequire(t, "publish and activate temporary recovery package", err)
	coreAssert(t, manager.GetAccountManagementStatus().RecoveryConfigured &&
		manager.GetAccountManagementStatus().StorageMode == AccountStorageTemporary,
		"temporary account activation did not commit")
	if previous != nil {
		coreAssert(t, material.locator.PackageID != previous.locator.PackageID,
			"temporary recovery package was not rotated during re-publication")
	}
	return material
}

func coreRestoreRGB11TemporaryE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, material *coreRecoveryMaterial) *Manager {
	t.Helper()
	target := coreNewE2EManager(t, cfg, chain, "")
	coreInstallRGB11TemporaryTTLPolicy(target)
	pkg, err := target.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
	coreRequire(t, "load temporary recovery package from real CoreNode", err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, coreAnswers())
	coreRequire(t, "recover temporary DKVS share", err)
	backup, secret, err := account.RecoverAccount(pkg.Envelope, material.userShare, share)
	coreRequire(t, "recover temporary account secret", err)
	defer zeroBytes(secret)
	defer clearAccountBackup(&backup)
	if len(backup.Wallets) == 0 {
		t.Fatal("core-e2e: temporary package omitted the root wallet")
	}
	state, err := target.LoadAccountManagementStateForRecovery(material.auth.Location,
		material.locator, secret, backup.Wallets[0].Mnemonic)
	coreRequire(t, "load temporary managed state and complete RGB closure", err)
	_, err = target.RestoreAccountManagementState(*state, secret, coreE2EPassword,
		material.locator, AccountManagementRestoreOptions{
			Location: material.auth.Location, StorageMode: AccountStorageTemporary,
			RecordTTL: material.auth.RecordOptions.TTL,
		})
	coreRequire(t, "restore independent temporary SDK instance", err)
	coreSynchronizeRestoredAccount(t, target)
	_, err = target.RefreshRGB11State(context.Background())
	coreRequire(t, "freshly reconcile restored temporary RGB state", err)
	return target
}

type coreRGB11TemporaryRecords struct {
	state, blob, wrapper, recovery *swire.DKVSRecord
}

func coreReadRGB11TemporaryRecords(t *testing.T, manager *Manager,
	cfg coreE2EConfig, material *coreRecoveryMaterial) coreRGB11TemporaryRecords {
	t.Helper()
	root, err := manager.accountManagementRootWallet()
	coreRequire(t, "resolve temporary managed root", err)
	stateKey, err := manager.accountManagedStateKey(root)
	coreRequire(t, "derive temporary account state key", err)
	blobKey, err := manager.accountManagedDataBlobKey(root)
	coreRequire(t, "derive temporary account data key", err)
	wrapperKey, err := accountRootWrapperKey(root)
	coreRequire(t, "derive temporary root-wrapper key", err)
	recoveryKey, err := dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(),
		accountPackagePath(material.locator.PackageID))
	coreRequire(t, "derive temporary recovery package key", err)
	direct := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	read := func(key, label string) *swire.DKVSRecord {
		value, err := direct.GetRecordDirect(key)
		coreRequire(t, label, err)
		return value
	}
	return coreRGB11TemporaryRecords{
		state:    read(stateKey, "read signed temporary state record"),
		blob:     read(blobKey, "read signed temporary data blob"),
		wrapper:  read(wrapperKey, "read signed temporary root wrapper"),
		recovery: read(recoveryKey, "read signed temporary recovery package"),
	}
}

func coreAssertRGB11TemporaryRecordTTL(t *testing.T,
	records coreRGB11TemporaryRecords, want uint64) {
	t.Helper()
	for name, record := range map[string]*swire.DKVSRecord{
		"state": records.state, "blob": records.blob,
		"root wrapper": records.wrapper, "recovery package": records.recovery,
	} {
		coreAssert(t, record != nil && record.TTL == want && record.IssueHeight > 0,
			fmt.Sprintf("temporary %s did not read back as signed TTL %d at a real Core height", name, want))
	}
}

func coreAssertRGB11TemporaryPending(t *testing.T, manager *Manager,
	transferID string, signedTx []byte, txid, reservationID string,
	inputs []string, wantLocked bool) {
	t.Helper()
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	coreRequire(t, "load retained temporary private pending transfer", err)
	coreAssert(t, pending != nil &&
		pending.State.TransferID == transferID && pending.State.WitnessTxID == txid &&
		pending.ReservationID == reservationID && bytes.Equal(pending.SignedTx, signedTx) &&
		coreDigest(pending.State.InputOutPoints) == coreDigest(inputs),
		"temporary transfer recovery changed its original signed transaction, txid, or inputs")
	coreAssertRGB11Reservation(t, manager, transferID, wantLocked)
}

// A DKVS write is off-chain and cannot advance SatoshiNet height. Reuse the
// existing SDK L2 transaction path with the funded paid fixture to mine a block.
func coreTemporaryL2HeightHeartbeat(t *testing.T, cfg coreE2EConfig,
	payer *Manager, afterHeight uint64) uint64 {
	t.Helper()
	script, err := stxscript.NullDataScript([]byte(fmt.Sprintf("rgb11-ttl-%d", time.Now().UnixNano())))
	coreRequire(t, "build L2 height heartbeat", err)
	_, err = payer.SendNullData_SatsNet(script)
	coreRequire(t, "broadcast real L2 height heartbeat", err)
	client := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		height, err := client.GetBestHeight()
		coreRequire(t, "read actual Core height after L2 heartbeat", err)
		if height > afterHeight {
			return height
		}
		time.Sleep(200 * time.Millisecond)
	}
	actual, err := client.GetBestHeight()
	coreRequire(t, "read final actual Core height", err)
	t.Fatalf("core-e2e: L2 heartbeat did not advance Core height past %d (height=%d)", afterHeight, actual)
	return actual
}

func coreWaitRGB11TemporaryRecordExpired(t *testing.T, cfg coreE2EConfig,
	payer *Manager, key string) uint64 {
	t.Helper()
	direct := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	for attempt := 0; attempt < 8; attempt++ {
		_, err := direct.GetRecordDirect(key)
		if errors.Is(err, ErrDKVSRecordNotFound) {
			height, heightErr := direct.GetBestHeight()
			coreRequire(t, "read Core height when temporary record expired", heightErr)
			return height
		}
		coreRequire(t, "read temporary record while waiting for real expiry", err)
		height, err := direct.GetBestHeight()
		coreRequire(t, "read actual Core height before temporary expiry", err)
		coreTemporaryL2HeightHeartbeat(t, cfg, payer, height)
	}
	t.Fatalf("core-e2e: temporary record did not expire on real Core height: key=%s", key)
	return 0
}

func coreRGB11TemporaryTTLExpiryColdRestoreE2E(t *testing.T,
	cfg coreE2EConfig, chain *coreE2EChain, paidWriter, receiver *Manager,
	receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	ctx := context.Background()
	temporary := coreNewE2EManager(t, cfg, chain, "")
	policyHTTP := coreInstallRGB11TemporaryTTLPolicy(temporary)
	_, _, err := temporary.CreateWallet(coreE2EPassword)
	coreRequire(t, "create independent temporary root wallet", err)
	chain.fund(t, temporary.GetWallet().GetAddress(), 8)
	material := coreRGB11TemporaryRecoveryMaterial(t, temporary, nil)
	coreAssert(t, policyHTTP.hits > 0, "temporary TTL test policy did not reach the real config endpoint")
	issued, err := temporary.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ET", Name: "E2E temporary recovery asset", Amounts: []uint64{777},
	})
	coreRequire(t, "issue NIA on isolated temporary account", err)
	endpoint, err := receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "ensure paid receiver Direct endpoint", err)
	coreAssert(t, endpoint != nil && !endpoint.Temporary,
		"temporary TTL send must terminate at the fixture's paid receiver endpoint")
	beforePrepareBroadcasts := chain.broadcastCount()
	// This pending is explicitly cancelled after recovery; use the matching
	// public OOB flow. The final complete spend below still exercises Direct.
	exported, err := temporary.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export temporary OOB contract", err)
	contract, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	coreRequire(t, "decode temporary OOB contract", err)
	_, err = receiver.ImportRGB11ContractFile(ctx, contract)
	coreRequire(t, "import temporary OOB contract on receiver", err)
	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "1", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	coreRequire(t, "create temporary OOB invoice", err)
	prepared, err := temporary.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	coreRequire(t, "prepare a never-delivered temporary transfer", err)
	coreAssert(t, chain.broadcastCount() == beforePrepareBroadcasts,
		"temporary transfer broadcast before delivery or receiver ACK")
	preparedPending, err := temporary.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	coreRequire(t, "capture temporary private signed pending", err)
	coreAssert(t, preparedPending != nil && len(preparedPending.SignedTx) > 0 &&
		preparedPending.State.WitnessTxID == prepared.TxID,
		"temporary prepared intent did not retain its private signed transaction")
	coreAssertRGB11Reservation(t, temporary, prepared.State.TransferID, true)
	originalSignedTx := append([]byte(nil), preparedPending.SignedTx...)
	originalTxID, originalReservationID := prepared.TxID, preparedPending.ReservationID
	originalInputs := append([]string(nil), preparedPending.State.InputOutPoints...)

	// Re-activate with a new recovery package after the intent exists so all
	// current recovery records cover the exact signed pending and share a fresh
	// TTL-2 lease before the expiry experiment starts.
	previousMaterial := material
	material = coreRGB11TemporaryRecoveryMaterial(t, temporary, previousMaterial)
	records := coreReadRGB11TemporaryRecords(t, temporary, cfg, material)
	coreAssertRGB11TemporaryRecordTTL(t, records, coreRGB11TemporaryTTL)
	stableRGBBeforeExpiry := coreRemoteStableRGB(t, temporary, cfg)
	localRGBBeforeExpiry := coreLocalRGBBundleDigests(t, temporary)
	coreAssert(t, records.state.IssueHeight+records.state.TTL > records.blob.IssueHeight+1 &&
		records.recovery.IssueHeight+records.recovery.TTL > records.blob.IssueHeight+1,
		"state and recovery package cannot outlive the shortened blob for the first expiry boundary")

	// Shorten only the already signed blob lease to one block. This produces a
	// real missing-blob/live-state interval while every unmodified record keeps
	// its observed TTL-2 lease. The signed value and account state remain intact.
	direct := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	root, err := temporary.accountManagementRootWallet()
	coreRequire(t, "resolve temporary account signer for expiry probe", err)
	shortBlob, err := direct.PutSignedRecordFreeLocal(root, records.blob.Key, records.blob.Value,
		dkvsindexer.RecordOptions{Seq: records.blob.Seq + 1, TTL: 1})
	coreRequire(t, "resign real account blob with a one-block expiry probe", err)
	coreAssert(t, shortBlob.TTL == 1 && shortBlob.IssueHeight > 0 &&
		records.state.IssueHeight+records.state.TTL > shortBlob.IssueHeight+shortBlob.TTL &&
		records.recovery.IssueHeight+records.recovery.TTL > shortBlob.IssueHeight+shortBlob.TTL,
		"signed expiry probe did not leave the state and recovery package live after the blob")

	stateKey, err := temporary.accountManagedStateKey(root)
	coreRequire(t, "derive temporary state key for expiry probe", err)
	blobExpiryHeight := shortBlob.IssueHeight + shortBlob.TTL
	stateHeight := coreWaitRGB11TemporaryRecordExpired(t, cfg, paidWriter, shortBlob.Key)
	coreAssert(t, stateHeight >= blobExpiryHeight,
		"temporary blob disappeared without advancing the actual Core height to its signed expiry")
	stateRecord, err := direct.GetRecordDirect(stateKey)
	coreRequire(t, "verify state remains live after data blob expiry", err)
	recoveryRecord, err := direct.GetRecordDirect(records.recovery.Key)
	coreRequire(t, "verify old recovery package remains live after data blob expiry", err)
	coreAssert(t, stateRecord.TTL == coreRGB11TemporaryTTL &&
		recoveryRecord.TTL == coreRGB11TemporaryTTL,
		"live state/recovery records no longer carry their original TTL-2 lease")

	partialCold := coreNewE2EManager(t, cfg, chain, "")
	coreInstallRGB11TemporaryTTLPolicy(partialCold)
	pkg, err := partialCold.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
	coreRequire(t, "load still-live old package for missing-blob cold test", err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, coreAnswers())
	coreRequire(t, "recover share while managed blob is expired", err)
	backup, secret, err := account.RecoverAccount(pkg.Envelope, material.userShare, share)
	coreRequire(t, "recover secret while managed blob is expired", err)
	state, restoreErr := partialCold.LoadAccountManagementStateForRecovery(material.auth.Location,
		material.locator, secret, backup.Wallets[0].Mnemonic)
	defer zeroBytes(secret)
	defer clearAccountBackup(&backup)
	coreAssert(t, state == nil && errors.Is(restoreErr, ErrDKVSRecordNotFound),
		"cold restore accepted a live state whose referenced RGB recovery blob expired")
	localSyncErr := temporary.SyncAccountManagementState(ctx)
	coreAssert(t, errors.Is(localSyncErr, ErrDKVSRecordNotFound),
		"original temporary device did not fail closed on its missing recovery blob")
	coreAssertRGB11TemporaryPending(t, temporary, prepared.State.TransferID,
		originalSignedTx, originalTxID, originalReservationID, originalInputs, true)

	// Continue using real L2 heartbeat transactions until every record belonging to
	// the old locator has expired. No record is deleted or clock is mocked.
	oldRecords := map[string]*swire.DKVSRecord{
		"state": stateRecord, "wrapper": records.wrapper, "recovery": recoveryRecord,
	}
	maxExpiryHeight := uint64(0)
	for _, record := range oldRecords {
		expiry := record.IssueHeight + record.TTL
		if expiry > maxExpiryHeight {
			maxExpiryHeight = expiry
		}
	}
	for _, key := range []string{stateRecord.Key, records.wrapper.Key, records.recovery.Key} {
		coreWaitRGB11TemporaryRecordExpired(t, cfg, paidWriter, key)
	}
	allExpiredHeight, err := direct.GetBestHeight()
	coreRequire(t, "read Core height after all temporary recovery records expired", err)
	coreAssert(t, allExpiredHeight >= maxExpiryHeight,
		"all temporary records disappeared without crossing their actual TTL-2 expiry heights")
	for _, key := range []string{stateRecord.Key, shortBlob.Key, records.wrapper.Key, records.recovery.Key} {
		_, err := direct.GetRecordDirect(key)
		coreAssert(t, errors.Is(err, ErrDKVSRecordNotFound),
			"old temporary recovery record remained available after its real expiry")
	}
	expiredCold := coreNewE2EManager(t, cfg, chain, "")
	coreInstallRGB11TemporaryTTLPolicy(expiredCold)
	_, oldPackageErr := expiredCold.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
	coreAssert(t, errors.Is(oldPackageErr, ErrDKVSRecordNotFound) &&
		len(expiredCold.GetWalletCatalog()) == 0,
		"cold recovery accepted an old locator after all temporary recovery records expired")

	// Once the authoritative state and blob themselves have expired, Sync can
	// republish from the still-authenticated local profile and complete closure.
	// This does not recreate the expired recovery locator; rotate that material
	// explicitly below before attempting another cold restore.
	coreRequire(t, "republish temporary state after all old records expire",
		temporary.SyncAccountManagementState(ctx))
	coreAssertRGB11TemporaryPending(t, temporary, prepared.State.TransferID,
		originalSignedTx, originalTxID, originalReservationID, originalInputs, true)
	coreAssertMapEqual(t, "temporary local RGB closure after state republish",
		localRGBBeforeExpiry, coreLocalRGBBundleDigests(t, temporary))
	coreAssertMapEqual(t, "temporary authoritative RGB closure after state republish",
		stableRGBBeforeExpiry, coreRemoteStableRGB(t, temporary, cfg))
	blobKey, err := temporary.accountManagedDataBlobKey(root)
	coreRequire(t, "derive republished temporary blob key", err)
	stateRecord, err = direct.GetRecordDirect(stateKey)
	coreRequire(t, "read republished temporary state record", err)
	republishedBlob, err := direct.GetRecordDirect(blobKey)
	coreRequire(t, "read republished temporary data blob", err)
	coreAssert(t, stateRecord.TTL == coreRGB11TemporaryTTL &&
		republishedBlob.TTL == coreRGB11TemporaryTTL,
		"ordinary sync did not republish state and blob with the actual TTL-2 policy")
	material = coreRGB11TemporaryRecoveryMaterial(t, temporary, material)
	coreAssert(t, material.locator.PackageID != previousMaterial.locator.PackageID,
		"full-expiry recovery did not publish a new package locator")
	newRecords := coreReadRGB11TemporaryRecords(t, temporary, cfg, material)
	coreAssertRGB11TemporaryRecordTTL(t, newRecords, coreRGB11TemporaryTTL)
	resume := coreRestoreRGB11TemporaryE2E(t, cfg, chain, material)
	coreAssertRGB11TemporaryPending(t, resume, prepared.State.TransferID,
		originalSignedTx, originalTxID, originalReservationID, originalInputs, true)
	coreAssert(t, chain.broadcastCount() == beforePrepareBroadcasts,
		"temporary cold restore automatically broadcast the never-delivered pending intent")
	coreRequire(t, "cancel only the never-delivered temporary intent", resume.CancelRGB11OutOfBandTransfer(prepared.State.TransferID))
	coreAssertRGB11Reservation(t, resume, prepared.State.TransferID, false)

	beforeFinalSend := chain.broadcastCount()
	coreSendRGB11Amount(t, chain, resume, receiver, issued.AssetName, "777", beforeFinalSend, 0, 777, nil)
	coreAssert(t, chain.broadcastCount() == beforeFinalSend+1,
		"resumed temporary account did not complete one real receiver-ACKed send")

	// Rotate a fresh temporary recovery package over the final settled history;
	// the subsequent cold check must use this actual locator, not an expired one.
	finalMaterial := coreRGB11TemporaryRecoveryMaterial(t, resume, material)
	coreAssert(t, finalMaterial.locator.PackageID != material.locator.PackageID,
		"post-send recovery package did not rotate to current settled history")
	finalCold := coreRestoreRGB11TemporaryE2E(t, cfg, chain, finalMaterial)
	coreAssert(t, chain.broadcastCount() == beforeFinalSend+1,
		"final temporary cold restore broadcast a settled transaction again")
	finalSourceBalance, err := finalCold.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read source balance after temporary cold restore", err)
	coreAssert(t, coreRGB11BalanceIsZero(finalSourceBalance),
		"final cold restore retained the already spent temporary source allocation")
	finalReceiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, finalReceiver)
	_, err = finalReceiver.RefreshRGB11State(ctx)
	coreRequire(t, "freshly reconcile final temporary receiver", err)
	finalReceiverBalance, err := finalReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read receiver balance after temporary source cold restore", err)
	coreAssert(t, finalReceiverBalance != nil && finalReceiverBalance.Value.Uint64() == 777,
		"temporary send did not conserve the complete issued balance across final cold restore")
}
