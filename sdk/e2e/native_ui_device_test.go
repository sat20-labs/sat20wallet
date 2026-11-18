package e2e

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Transport faults refuse requests, never supply substitute chain or DKVS
// state. The same transport remains installed across device restarts.
type nativeUIHTTP struct {
	inner        wallet.HttpClient
	l1Offline    atomic.Bool
	l1Host       string
	dkvsOffline  atomic.Bool
	writeRefusal atomic.Pointer[url.URL]
	refused      atomic.Int64
	broadcasts   atomic.Int64
}

func (h *nativeUIHTTP) refuse(u *wallet.URL) error {
	if h.l1Offline.Load() && u.Host == h.l1Host || h.dkvsOffline.Load() && strings.Contains(u.Path, "/dkvs/") {
		h.refused.Add(1)
		return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("native UI injected transport disconnection")}
	}
	return nil
}

// A rejected upload is a server response, not a dial failure. Leave every
// other URL (including the read side of DKVS) on its original real backend.
func (h *nativeUIHTTP) rejectUploads(t *testing.T) {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/dkvs/records/batch-cas") {
			http.NotFound(w, r)
			return
		}
		h.refused.Add(1)
		http.Error(w, "batch CAS temporarily unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(s.Close)
	dst, err := url.Parse(s.URL)
	require.NoError(t, err)
	h.writeRefusal.Store(dst)
}

func (h *nativeUIHTTP) postURL(u *wallet.URL) *wallet.URL {
	if dst := h.writeRefusal.Load(); dst != nil && strings.HasSuffix(u.Path, "/dkvs/records/batch-cas") {
		req := *u
		req.Scheme, req.Host = dst.Scheme, dst.Host
		return &req
	}
	return u
}

func (h *nativeUIHTTP) SendGetRequest(u *wallet.URL) ([]byte, error) {
	if err := h.refuse(u); err != nil {
		return nil, err
	}
	return h.inner.SendGetRequest(u)
}
func (h *nativeUIHTTP) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	if strings.HasSuffix(u.Path, "btc/tx") || strings.HasSuffix(u.Path, "btc/txs") || strings.HasSuffix(u.Path, "bitcoin/tx/broadcast") {
		h.broadcasts.Add(1)
	}
	if err := h.refuse(u); err != nil {
		return nil, err
	}
	return h.inner.SendPostRequest(h.postURL(u), body)
}
func (h *nativeUIHTTP) SendGetRequestContext(ctx context.Context, u *wallet.URL) ([]byte, error) {
	if err := h.refuse(u); err != nil {
		return nil, err
	}
	return h.inner.(wallet.ContextHttpClient).SendGetRequestContext(ctx, u)
}
func (h *nativeUIHTTP) SendPostRequestContext(ctx context.Context, u *wallet.URL, body []byte) ([]byte, error) {
	if strings.HasSuffix(u.Path, "btc/tx") || strings.HasSuffix(u.Path, "btc/txs") || strings.HasSuffix(u.Path, "bitcoin/tx/broadcast") {
		h.broadcasts.Add(1)
	}
	if err := h.refuse(u); err != nil {
		return nil, err
	}
	return h.inner.(wallet.ContextHttpClient).SendPostRequestContext(ctx, h.postURL(u), body)
}

type nativeUIDevice struct {
	t        *testing.T
	mgr      *wallet.Manager
	cfg      *sdkcommon.Config
	dir      string
	db       indexercommon.KVDB
	http     *nativeUIHTTP
	password string
}

func newNativeUIDevice(t *testing.T, cfg *sdkcommon.Config, mnemonic string) *nativeUIDevice {
	t.Helper()
	d := &nativeUIDevice{t: t, cfg: cfg, dir: t.TempDir(), password: accountReviewPassword,
		http: &nativeUIHTTP{inner: wallet.NewHTTPClient()}}
	d.open(t)
	t.Cleanup(d.close)
	if mnemonic != "" {
		_, err := d.mgr.ImportWallet(mnemonic, d.password)
		require.NoError(t, err)
		nativeUIReady(t, d.mgr)
	}
	return d
}

// Only for a new root known to have no recovery on this fixture network.
// Finish its authenticated discovery before asking for a storage policy.
func newNativeUIInactive(t *testing.T, cfg *sdkcommon.Config, mnemonic string) *nativeUIDevice {
	t.Helper()
	d := newNativeUIDevice(t, cfg, "")
	_, err := d.mgr.ImportWallet(mnemonic, d.password)
	require.NoError(t, err)
	requireE2EInactive(t, d.mgr, d.password)
	nativeUIWritable(t, d.mgr)
	return d
}

func (d *nativeUIDevice) open(t *testing.T) {
	t.Helper()
	d.db = indexerdb.NewKVDB(d.dir)
	require.NotNil(t, d.db)
	d.mgr = wallet.NewManager(d.cfg, d.db)
	require.NotNil(t, d.mgr)
	d.mgr.SetDKVSHttpClient(d.http)
	for _, peer := range d.cfg.Peers {
		parts := strings.Split(peer, "@")
		if len(parts) == 3 && parts[0] == "s" {
			endpoint, err := url.Parse(parts[2])
			require.NoError(t, err)
			d.mgr.SetServerNodeHttpClient(wallet.NewNodeClient(endpoint.Scheme, endpoint.Host, endpoint.Path, d.http))
			break
		}
	}
	if e := d.cfg.IndexerL1; e != nil {
		d.http.l1Host = e.Host
		d.mgr.SetIndexerHttpClient(wallet.NewIndexerClient(e.Scheme, e.Host, e.Proxy, d.http))
	}
	if e := d.cfg.IndexerL2; e != nil {
		d.mgr.SetIndexerHttpClient_SatsNet(wallet.NewIndexerClient(e.Scheme, e.Host, e.Proxy, d.http))
	}
}
func (d *nativeUIDevice) close() {
	if d.mgr != nil {
		d.mgr.Close()
		d.mgr = nil
	}
	if d.db != nil {
		require.NoError(d.t, d.db.Close())
		d.db = nil
	}
}
func (d *nativeUIDevice) reopen(t *testing.T) {
	t.Helper()
	d.close()
	d.open(t)
	if len(d.mgr.GetWalletCatalog()) != 0 {
		_, err := d.mgr.UnlockWallet(d.password)
		require.NoError(t, err)
	}
}
func nativeUIReady(t *testing.T, m *wallet.Manager) {
	t.Helper()
	_, err := m.RefreshAccount(context.Background())
	require.NoError(t, err)
	nativeUIWritable(t, m)
}

func nativeUIWritable(t *testing.T, m *wallet.Manager) {
	t.Helper()
	require.Eventually(t, func() bool {
		view, err := m.GetAccountView(context.Background())
		if err != nil {
			return false
		}
		for _, module := range view.Modules {
			if module.Name == "rgb11" {
				return module.CanWrite
			}
		}
		return false
	}, 60*time.Second, 100*time.Millisecond, "the real account path must become writable")
}
func nativeUIBackup(t *testing.T, m *wallet.Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_, err := m.RefreshAccount(ctx)
	require.NoError(t, err)
	require.NoError(t, m.WaitAccountBackup(ctx, "", ""))
	view, err := m.GetAccountView(ctx)
	require.NoError(t, err)
	require.Equal(t, "backed_up", view.BackupState)
	require.Empty(t, view.BackupRisk)
}
func nativeUIQuestions() []wallet.QuestionInput {
	var result []wallet.QuestionInput
	for _, q := range e2eKnowledgeQuestions() {
		result = append(result, wallet.QuestionInput{Question: wallet.Question{
			ID: q.Question.ID, Prompt: q.Question.Prompt, CaseSensitive: q.Question.CaseSensitive,
			IgnorePunctuation: q.Question.IgnorePunctuation}, Answer: q.Answer, Confirmation: q.Confirmation})
	}
	return result
}
func nativeUIFactors(t *testing.T, setup *wallet.RecoverySetupView) wallet.RecoveryFactors {
	t.Helper()
	var factors wallet.RecoveryFactors
	for _, q := range nativeUIQuestions() {
		factors.Answers = append(factors.Answers, wallet.Answer{QuestionID: q.Question.ID, Text: q.Answer})
	}
	for _, f := range setup.Files {
		if f.Name == "user-share.txt" {
			factors.UserShare = string(f.Data)
		}
	}
	require.NotEmpty(t, factors.UserShare)
	return factors
}
func nativeUISetupReq(m *wallet.Manager, password string) wallet.RecoverySetup {
	request := wallet.RecoverySetup{Password: password, Mode: "2of2", Questions: nativeUIQuestions()}
	for _, entry := range m.GetWalletCatalog() {
		request.WalletIDs = append(request.WalletIDs, entry.ID)
	}
	return request
}
func nativeUIStorage(t *testing.T, m *wallet.Manager, id string, count uint64) wallet.StorageOption {
	t.Helper()
	options, err := m.GetStorageOptions(context.Background(), count)
	require.NoError(t, err)
	for _, option := range options {
		if option.ID == id {
			require.True(t, option.Available)
			return option
		}
	}
	t.Fatalf("missing storage option %s", id)
	return wallet.StorageOption{}
}
func nativeUIConfirm(t *testing.T, m *wallet.Manager, option wallet.StorageOption) *wallet.StorageView {
	t.Helper()
	var original string
	var view *wallet.StorageView
	require.Eventually(t, func() bool {
		var err error
		view, err = m.ConfirmStorage(context.Background(), option)
		require.NoError(t, err)
		if view.TxID != "" {
			if original != "" {
				require.Equal(t, original, view.TxID, "continue the original payment")
			}
			original = view.TxID
		}
		return view.Status == "ready"
	}, 90*time.Second, 200*time.Millisecond)
	return view
}
func nativeUIActivate(t *testing.T, d *nativeUIDevice, setup *wallet.RecoverySetupView) {
	t.Helper()
	view, err := d.mgr.ActivateRecovery(context.Background(), setup.SessionID, wallet.ActivationInput{
		Password: d.password, SavedLocator: setup.Locator, Factors: nativeUIFactors(t, setup)})
	require.NoError(t, err)
	require.True(t, view.Active)
	nativeUIBackup(t, d.mgr)
	nativeUIReady(t, d.mgr)
}
func nativeUILocalBackup(t *testing.T, d *nativeUIDevice) *wallet.RecoverySetupView {
	t.Helper()
	request := nativeUISetupReq(d.mgr, d.password)
	summary, err := d.mgr.PreflightRecovery(d.password, request.WalletIDs)
	require.NoError(t, err)
	require.Len(t, summary.Wallets, len(request.WalletIDs))
	nativeUIConfirm(t, d.mgr, nativeUIStorage(t, d.mgr, "temporary", 100))
	setup, err := d.mgr.CreateRecovery(context.Background(), request)
	require.NoError(t, err)
	nativeUIActivate(t, d, setup)
	return setup
}
func nativeUICatalog(t *testing.T, m *wallet.Manager, before []wallet.WalletCatalogEntry) {
	t.Helper()
	require.Len(t, m.GetWalletCatalog(), len(before))
	for _, expected := range before {
		found := accountReviewFind(t, m, expected.Fingerprint)
		require.Equal(t, expected.Name, found.Name)
		require.Equal(t, expected.Accounts, found.Accounts)
	}
}
func nativeUIRestore(t *testing.T, d *nativeUIDevice, setup *wallet.RecoverySetupView) *wallet.RecoveryResult {
	t.Helper()
	ctx := context.Background()
	loaded, err := d.mgr.LoadRecovery(ctx, setup.Locator, false)
	require.NoError(t, err)
	preview, err := d.mgr.PreviewRecovery(ctx, loaded.SessionID, nativeUIFactors(t, setup))
	require.NoError(t, err)
	require.True(t, preview.CanCommit)
	require.NotNil(t, preview.Summary)
	result, err := d.mgr.CommitRecovery(ctx, loaded.SessionID, d.password)
	require.NoError(t, err)
	require.NotNil(t, result)
	_, err = d.mgr.CommitRecovery(ctx, loaded.SessionID, d.password)
	require.EqualError(t, err, "recovery session expired")
	return result
}
