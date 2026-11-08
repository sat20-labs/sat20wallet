package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	neturl "net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/sirupsen/logrus"
)

const coreRGB11TemporaryTTL = uint64(2)

// Enable only existing authorization metadata, never ordinary debug/HTTP data.
// R23 is serial; the original logging configuration is restored on exit.
type coreRGB11AuthorizationTraceFormatter struct {
	previous logrus.Formatter
}

func (f coreRGB11AuthorizationTraceFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	if entry.Level > logrus.InfoLevel {
		allowed := entry.Level == logrus.DebugLevel &&
			(strings.HasPrefix(entry.Message, "account authorization completion: ") ||
				strings.HasPrefix(entry.Message, "account managed write rejected: ") ||
				strings.HasPrefix(entry.Message, "DKVS paths rejected: ") ||
				strings.HasPrefix(entry.Message, "DKVS group rejected: "))
		if !allowed {
			return nil, nil
		}
	}
	return f.previous.Format(entry)
}

func coreTraceRGB11AccountAuthorization(t *testing.T) {
	t.Helper()
	level, formatter := Log.GetLevel(), Log.Formatter
	// Hooks run before formatting; do not let another sink bypass the filter.
	hooks := Log.ReplaceHooks(make(logrus.LevelHooks))
	Log.SetFormatter(coreRGB11AuthorizationTraceFormatter{previous: formatter})
	Log.SetLevel(logrus.DebugLevel)
	t.Cleanup(func() {
		Log.SetLevel(level)
		Log.SetFormatter(formatter)
		Log.ReplaceHooks(hooks)
	})
}

// coreRGB11TemporaryPolicyHTTP changes only the test client's advertised
// temporary-record maximum. All writes and reads still go to the real CoreNode.
type coreRGB11TemporaryPolicyHTTP struct {
	HttpClient
	maxTTL uint64
	hits   int
}

var _ ContextHttpClient = (*coreRGB11TemporaryPolicyHTTP)(nil)

// Classify a failed test request without printing its URL, message or payload.
func coreTraceRGB11TemporaryHTTPError(ctx context.Context, method string, target *URL, err error) {
	if err == nil {
		return
	}
	route := "other"
	if target != nil {
		for _, name := range []string{"config", "record", "active/sync", "active/watch", "records/batch-cas"} {
			if strings.HasSuffix(target.Path, "/v3/dkvs/"+name) {
				route = name
				break
			}
		}
	}
	var ctxErr error
	if ctx != nil {
		ctxErr = ctx.Err()
	}
	cause := err
	var urlErr *neturl.Error
	if errors.As(err, &urlErr) {
		cause = urlErr.Err
	}
	leaf := cause
	for depth := 0; depth < 8 && errors.Unwrap(leaf) != nil; depth++ {
		leaf = errors.Unwrap(leaf)
	}
	op := "none"
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		op = "other"
		switch opErr.Op {
		case "dial", "read", "write", "accept":
			op = opErr.Op
		}
	}
	var networkErr net.Error
	timeout := errors.As(err, &networkErr) && networkErr.Timeout()
	var errno syscall.Errno
	hasErrno := errors.As(err, &errno)
	Log.Infof("core-e2e: temporary HTTP failure: method=%s route=%s error_type=%T cause_type=%T leaf_type=%T net_op=%s timeout=%t ctx_canceled=%t ctx_deadline=%t error_canceled=%t error_deadline=%t eof=%t unexpected_eof=%t has_errno=%t errno=%d reset=%t refused=%t broken_pipe=%t address_unavailable=%t fd_limit=%t",
		method, route, err, cause, leaf, op, timeout,
		errors.Is(ctxErr, context.Canceled), errors.Is(ctxErr, context.DeadlineExceeded),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), hasErrno, uint64(errno),
		errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, syscall.EPIPE), errors.Is(err, syscall.EADDRNOTAVAIL),
		errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE))
}

func (h *coreRGB11TemporaryPolicyHTTP) SendGetRequest(url *URL) ([]byte, error) {
	return h.SendGetRequestContext(context.Background(), url)
}

func (h *coreRGB11TemporaryPolicyHTTP) SendGetRequestContext(ctx context.Context, url *URL) ([]byte, error) {
	raw, err := h.HttpClient.(ContextHttpClient).SendGetRequestContext(ctx, url)
	coreTraceRGB11TemporaryHTTPError(ctx, "GET", url, err)
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

// The TTL override must preserve the real client's lifecycle cancellation:
// account jobs wake the existing worker by cancelling its active long poll.
func (h *coreRGB11TemporaryPolicyHTTP) SendPostRequestContext(ctx context.Context, url *URL, body []byte) ([]byte, error) {
	raw, err := h.HttpClient.(ContextHttpClient).SendPostRequestContext(ctx, url, body)
	coreTraceRGB11TemporaryHTTPError(ctx, "POST", url, err)
	return raw, err
}

func coreInstallRGB11TemporaryTTLPolicy(manager *Manager) *coreRGB11TemporaryPolicyHTTP {
	http := &coreRGB11TemporaryPolicyHTTP{HttpClient: NewHTTPClient(), maxTTL: coreRGB11TemporaryTTL}
	manager.SetDKVSHttpClient(http)
	return http
}

func coreRGB11TemporaryRecoveryMaterial(t *testing.T, manager *Manager) *coreRecoveryMaterial {
	t.Helper()
	coreRequire(t, "initialize temporary account", manager.InitializeAccountManagement(coreE2EPassword))
	coreWaitAccountOnline(t, manager)
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
	coreWaitPrivateBackup(t, manager)
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
		material.locator, secret, string(backup.Wallets[0].Secret))
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
	state, rgb, wrapper, recovery *swire.DKVSRecord
	all                           map[string]*swire.DKVSRecord
}

func coreReadRGB11TemporaryRecords(t *testing.T, manager *Manager,
	cfg coreE2EConfig, material *coreRecoveryMaterial) coreRGB11TemporaryRecords {
	t.Helper()
	root, err := manager.accountManagementRootWallet()
	coreRequire(t, "resolve temporary managed root", err)
	stateKey, err := manager.accountManagedStateKey(root)
	coreRequire(t, "derive temporary wallet catalog key", err)
	indexKey, err := account.ManagedIndexKey(material.locator.AccountID)
	coreRequire(t, "derive temporary directory key", err)
	wrapperKey, err := accountRootWrapperKey(root)
	coreRequire(t, "derive temporary root-wrapper key", err)
	recoveryKey, err := dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(),
		accountPackagePath(material.locator.PackageID))
	coreRequire(t, "derive temporary recovery package key", err)
	direct := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	records := coreRGB11TemporaryRecords{all: make(map[string]*swire.DKVSRecord)}
	read := func(key string) *swire.DKVSRecord {
		value, err := direct.GetRecordDirect(key)
		coreRequire(t, "read signed temporary account record", err)
		coreRequire(t, "verify temporary record signature", dkvsindexer.VerifySignature(value))
		records.all[key] = value
		return value
	}
	indexRecord := read(indexKey)
	manager.mutex.RLock()
	secret := append([]byte(nil), manager.accountSecret...)
	network := manager.cfg.Chain
	manager.mutex.RUnlock()
	defer zeroBytes(secret)
	index, err := account.OpenManagedIndex(secret, material.locator.AccountID, network, indexRecord.Value)
	coreRequire(t, "open temporary account directory", err)
	for _, entry := range index.Entries {
		if entry.Deleted {
			continue
		}
		record := read(entry.Key)
		if entry.Provider == rgb11AccountManagedProviderID {
			coreAssert(t, records.rgb == nil, "temporary fixture must have exactly one RGB scope")
			records.rgb = record
		}
	}
	records.state = records.all[stateKey]
	records.wrapper, records.recovery = read(wrapperKey), read(recoveryKey)
	coreAssert(t, records.state != nil && records.rgb != nil,
		"temporary directory omitted wallet catalog or RGB module")
	return records
}

func coreAssertRGB11TemporaryRecordTTL(t *testing.T,
	records coreRGB11TemporaryRecords, want uint64) {
	t.Helper()
	for key, record := range records.all {
		coreAssert(t, record != nil && record.TTL == want && record.IssueHeight > 0,
			fmt.Sprintf("temporary %s did not read back as signed TTL %d at a real Core height", key, want))
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
	coreTraceRGB11AccountAuthorization(t)
	ctx := context.Background()
	dir := t.TempDir()
	database := indexerdb.NewKVDB(dir)
	coreAssert(t, database != nil, "open temporary account database")
	temporary := coreNewE2EManagerWithDB(t, cfg, chain, database, "")
	t.Cleanup(func() {
		if temporary != nil {
			temporary.Close()
		}
		if database != nil {
			coreRequire(t, "close temporary account database", database.Close())
		}
	})
	policyHTTP := coreInstallRGB11TemporaryTTLPolicy(temporary)
	_, _, err := temporary.CreateWallet(coreE2EPassword)
	coreRequire(t, "create independent temporary root wallet", err)
	chain.fund(t, temporary.GetWallet().GetAddress(), 8)
	material := coreRGB11TemporaryRecoveryMaterial(t, temporary)
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
	if err != nil {
		// Observe only after the original operation has failed. Do not retry,
		// refresh authorization, or print remote errors or account payloads.
		status := temporary.GetAccountManagementStatus()
		last := status.LastDKVSSyncError
		t.Logf("core-e2e: temporary prepare failure: actor=%d error_type=%T account_state=%q discovery=%q dkvs=%q writable=%t backup=%q pending_requests=%d error_code=%q error_at_ms=%d error_snapshot=%t error_unsynced=%t error_canceled=%t error_deadline=%t error_present=%t",
			temporary.debugID, err, status.AccountState, status.DiscoveryStatus, status.DKVSStatus,
			status.DKVSWriteAllowed, status.BackupState, status.DKVSPendingRequests,
			status.LastDKVSSyncErrorCode, status.LastDKVSSyncErrorAt,
			strings.Contains(last, errAccountSnapshotChanged.Error()), strings.Contains(last, ErrDKVSPathNotSynced.Error()),
			strings.Contains(last, context.Canceled.Error()), strings.Contains(last, context.DeadlineExceeded.Error()), last != "")
		temporary.mutex.RLock()
		coordinator := temporary.dkvs
		if coordinator != nil {
			client, clientErr := coordinator.primaryClient()
			if clientErr == nil {
				t.Logf("core-e2e: temporary prepare grants: read=%t write=%t account=%t",
					coordinator.scopeReady(coordinator.sessionKey(client, "read")),
					coordinator.scopeReady(coordinator.sessionKey(client, "write")),
					coordinator.scopeReady(temporary.accountWriteScopeLocked()))
			} else {
				t.Logf("core-e2e: temporary prepare grants: client_error_type=%T", clientErr)
			}
		}
		values, readErr := temporary.listAccountLocal(status.AccountID)
		temporary.mutex.RUnlock()
		if readErr != nil {
			t.Logf("core-e2e: temporary prepare modules: read_error_type=%T", readErr)
		} else {
			for _, value := range values {
				if value.Provider == rgb11AccountManagedProviderID {
					t.Logf("core-e2e: temporary prepare module: scope_tag=%s generation=%d captured=%d confirmed=%d baseline=%t restoring=%t conflict=%t has_error=%t",
						coreCASTag(value.Scope), value.Generation, value.Captured, value.Confirmed,
						value.Baseline != nil, value.Restoring, value.Conflict, value.LastError != "")
				}
			}
		}
	}
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

	// Publish the latest module closure without rotating the recovery package.
	coreRequire(t, "sync temporary pending recovery closure", temporary.SyncAccountManagementState(ctx))
	coreWaitPrivateBackup(t, temporary)
	records := coreReadRGB11TemporaryRecords(t, temporary, cfg, material)
	coreAssertRGB11TemporaryRecordTTL(t, records, coreRGB11TemporaryTTL)
	stableRGBBeforeExpiry := coreRemoteStableRGB(t, temporary, cfg)
	localRGBBeforeExpiry := coreLocalRGBBundleDigests(t, temporary)
	coreAssert(t, records.state.IssueHeight+records.state.TTL > records.rgb.IssueHeight+1 &&
		records.recovery.IssueHeight+records.recovery.TTL > records.rgb.IssueHeight+1,
		"state and recovery package cannot outlive the shortened RGB module for the first expiry boundary")

	temporary.StopDKVSSync()
	// Shorten only the already signed RGB module lease to one block. This produces a
	// real missing-module/live-state interval while every unmodified record keeps
	// its observed TTL-2 lease. The signed value and account state remain intact.
	direct := NewSatsNetDKVSClient(cfg.Core.Scheme, cfg.Core.Host, cfg.Core.Proxy, nil)
	root, err := temporary.accountManagementRootWallet()
	coreRequire(t, "resolve temporary account signer for expiry probe", err)
	shortRGB, err := direct.PutSignedRecordFreeLocal(root, records.rgb.Key, records.rgb.Value,
		dkvsindexer.RecordOptions{Seq: records.rgb.Seq + 1, TTL: 1})
	coreRequire(t, "resign real RGB module with a one-block expiry probe", err)
	coreAssert(t, shortRGB.TTL == 1 && shortRGB.IssueHeight > 0 &&
		records.state.IssueHeight+records.state.TTL > shortRGB.IssueHeight+shortRGB.TTL &&
		records.recovery.IssueHeight+records.recovery.TTL > shortRGB.IssueHeight+shortRGB.TTL,
		"signed expiry probe did not leave the state and recovery package live after the RGB module")

	stateKey, err := temporary.accountManagedStateKey(root)
	coreRequire(t, "derive temporary state key for expiry probe", err)
	rgbExpiryHeight := shortRGB.IssueHeight + shortRGB.TTL
	stateHeight := coreWaitRGB11TemporaryRecordExpired(t, cfg, paidWriter, shortRGB.Key)
	coreAssert(t, stateHeight >= rgbExpiryHeight,
		"temporary RGB module disappeared without advancing the actual Core height to its signed expiry")
	stateRecord, err := direct.GetRecordDirect(stateKey)
	coreRequire(t, "verify state remains live after RGB module expiry", err)
	recoveryRecord, err := direct.GetRecordDirect(records.recovery.Key)
	coreRequire(t, "verify old recovery package remains live after RGB module expiry", err)
	coreAssert(t, stateRecord.TTL == coreRGB11TemporaryTTL &&
		recoveryRecord.TTL == coreRGB11TemporaryTTL,
		"live state/recovery records no longer carry their original TTL-2 lease")

	partialCold := coreNewE2EManager(t, cfg, chain, "")
	coreInstallRGB11TemporaryTTLPolicy(partialCold)
	pkg, err := partialCold.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
	coreRequire(t, "load still-live old package for missing-module cold test", err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, coreAnswers())
	coreRequire(t, "recover share while RGB module is expired", err)
	backup, secret, err := account.RecoverAccount(pkg.Envelope, material.userShare, share)
	coreRequire(t, "recover secret while RGB module is expired", err)
	state, restoreErr := partialCold.LoadAccountManagementStateForRecovery(material.auth.Location,
		material.locator, secret, string(backup.Wallets[0].Secret))
	defer zeroBytes(secret)
	defer clearAccountBackup(&backup)
	coreRequire(t, "recover available wallet catalog despite missing RGB module", restoreErr)
	coreAssert(t, state != nil, "missing RGB module discarded the recoverable catalog")
	missingRGB := false
	for _, copy := range state.Copies {
		if copy.Entry.Key == records.rgb.Key {
			missingRGB = len(copy.Value) == 0 && copy.Record == nil
		} else {
			original := records.all[copy.Entry.Key]
			coreAssert(t, original != nil && bytes.Equal(original.Value, copy.Value),
				"missing RGB module changed an unrelated recovery copy")
		}
	}
	coreAssert(t, missingRGB, "cold load concealed the missing RGB module")
	_, err = partialCold.RestoreAccountManagementState(*state, secret, coreE2EPassword,
		material.locator, AccountManagementRestoreOptions{
			Location: material.auth.Location, StorageMode: AccountStorageTemporary,
			RecordTTL: material.auth.RecordOptions.TTL,
		})
	coreRequire(t, "restore available modules without fabricating RGB state", err)
	coreAssertMapEqual(t, "partial cold wallet catalog", coreCanonicalCatalog(temporary), coreCanonicalCatalog(partialCold))
	rgbUnavailable := false
	for _, module := range partialCold.GetAccountManagementStatus().Modules {
		if module.Provider == rgb11AccountManagedProviderID {
			rgbUnavailable = !module.WriteAllowed && module.LastError != ""
		} else {
			coreAssert(t, module.LastError == "", "RGB expiry disabled an unrelated module")
		}
	}
	coreAssert(t, rgbUnavailable, "missing RGB recovery data allowed new RGB business")
	// This device has no RGB copy and must not remain a writer during expiry.
	partialCold.Close()
	coreRequire(t, "resume original temporary backup worker", temporary.StartDKVSSync())
	coreRequire(t, "republish missing RGB module from complete local copy", temporary.SyncAccountManagementState(ctx))
	coreWaitPrivateBackup(t, temporary)
	repaired, err := direct.GetRecordDirect(records.rgb.Key)
	coreRequire(t, "read repaired RGB module", err)
	coreAssert(t, bytes.Equal(repaired.Value, records.rgb.Value),
		"module repair replaced its original recovery ciphertext")
	coreAssertRGB11TemporaryPending(t, temporary, prepared.State.TransferID,
		originalSignedTx, originalTxID, originalReservationID, originalInputs, true)

	// Close all account writers and the actual DB before advancing the real
	// chain to expire every directory, control and module record.
	records = coreReadRGB11TemporaryRecords(t, temporary, cfg, material)
	beforeStatus := temporary.GetAccountManagementStatus()
	beforeCatalog := coreCanonicalCatalog(temporary)
	temporary.Close()
	temporary = nil
	coreRequire(t, "close temporary database before real TTL expiry", database.Close())
	database = nil
	maxExpiryHeight := uint64(0)
	for _, record := range records.all {
		expiry := record.IssueHeight + record.TTL
		if expiry > maxExpiryHeight {
			maxExpiryHeight = expiry
		}
	}
	for key := range records.all {
		coreWaitRGB11TemporaryRecordExpired(t, cfg, paidWriter, key)
	}
	allExpiredHeight, err := direct.GetBestHeight()
	coreRequire(t, "read Core height after all temporary recovery records expired", err)
	coreAssert(t, allExpiredHeight >= maxExpiryHeight,
		"all temporary records disappeared without crossing their actual TTL-2 expiry heights")
	for key := range records.all {
		_, err := direct.GetRecordDirect(key)
		coreAssert(t, errors.Is(err, ErrDKVSRecordNotFound),
			"temporary recovery record remained available after its real expiry")
	}
	expiredCold := coreNewE2EManager(t, cfg, chain, "")
	coreInstallRGB11TemporaryTTLPolicy(expiredCold)
	_, oldPackageErr := expiredCold.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
	coreAssert(t, errors.Is(oldPackageErr, ErrDKVSRecordNotFound) &&
		len(expiredCold.GetWalletCatalog()) == 0,
		"cold recovery accepted a locator while all temporary records were missing")
	expiredCold.Close()

	// Unlock the persisted wallet in the same DB. Do not import a wallet,
	// activate again, create a package, or change the original user share.
	database = indexerdb.NewKVDB(dir)
	coreAssert(t, database != nil, "reopen original temporary account database")
	temporary = coreNewE2EManagerWithDB(t, cfg, chain, database, "",
		&coreRGB11TemporaryPolicyHTTP{HttpClient: NewHTTPClient(), maxTTL: coreRGB11TemporaryTTL})
	coreAssert(t, temporary.GetWallet() == nil, "cold reopen retained an unlocked wallet")
	_, err = temporary.UnlockWallet(coreE2EPassword)
	coreRequire(t, "unlock original persisted temporary wallet", err)
	coreRequire(t, "republish all expired records from persisted local copies", temporary.SyncAccountManagementState(ctx))
	coreWaitPrivateBackup(t, temporary)
	afterStatus := temporary.GetAccountManagementStatus()
	coreAssert(t, beforeStatus.AccountID == afterStatus.AccountID &&
		beforeStatus.RootFingerprint == afterStatus.RootFingerprint &&
		beforeStatus.PackageID == afterStatus.PackageID &&
		afterStatus.PackageID == material.locator.PackageID,
		"TTL repair changed the account, wrapper identity, or recovery package")
	coreAssertMapEqual(t, "same-DB temporary catalog", beforeCatalog, coreCanonicalCatalog(temporary))
	coreAssertRGB11TemporaryPending(t, temporary, prepared.State.TransferID,
		originalSignedTx, originalTxID, originalReservationID, originalInputs, true)
	coreAssertMapEqual(t, "temporary local RGB closure after cold republish",
		localRGBBeforeExpiry, coreLocalRGBBundleDigests(t, temporary))
	coreAssertMapEqual(t, "temporary authoritative RGB closure after cold republish",
		stableRGBBeforeExpiry, coreRemoteStableRGB(t, temporary, cfg))
	newRecords := coreReadRGB11TemporaryRecords(t, temporary, cfg, material)
	coreAssertRGB11TemporaryRecordTTL(t, newRecords, coreRGB11TemporaryTTL)
	coreAssert(t, len(newRecords.all) == len(records.all), "TTL repair changed the recovery record set")
	for key, original := range records.all {
		republished := newRecords.all[key]
		coreAssert(t, republished != nil && bytes.Equal(original.Value, republished.Value),
			"TTL repair changed an original control or module ciphertext")
	}
	temporary.Close()
	temporary = nil
	resume := coreRestoreRGB11TemporaryE2E(t, cfg, chain, material)
	coreAssertRGB11TemporaryPending(t, resume, prepared.State.TransferID,
		originalSignedTx, originalTxID, originalReservationID, originalInputs, true)
	coreAssert(t, chain.broadcastCount() == beforePrepareBroadcasts,
		"temporary cold restore automatically broadcast the never-delivered pending intent")
	coreRequire(t, "cancel only the never-delivered temporary intent", resume.CancelRGB11OutOfBandTransfer(prepared.State.TransferID))
	coreAssertRGB11Reservation(t, resume, prepared.State.TransferID, false, preparedPending)

	beforeFinalSend := chain.broadcastCount()
	coreSendRGB11Amount(t, chain, resume, receiver, issued.AssetName, "777", beforeFinalSend, 0, 777, nil)
	coreAssert(t, chain.broadcastCount() == beforeFinalSend+1,
		"resumed temporary account did not complete one real receiver-ACKed send")

	// Final settled history updates only its modules, not the recovery identity.
	coreWaitPrivateBackup(t, resume)
	coreAssert(t, resume.GetAccountManagementStatus().PackageID == material.locator.PackageID,
		"settled-history backup rotated the recovery package")
	finalClosure := coreLocalRGBBundleDigests(t, resume)
	resume.Close()
	finalCold := coreRestoreRGB11TemporaryE2E(t, cfg, chain, material)
	coreAssertMapEqual(t, "final temporary RGB recovery closure", finalClosure, coreLocalRGBBundleDigests(t, finalCold))
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
