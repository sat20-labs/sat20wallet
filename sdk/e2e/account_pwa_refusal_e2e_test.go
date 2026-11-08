package e2e

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	core "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only the definite rejection/payment view at the HTTP boundary is controlled. Account
// activation, local edits, signed CAS, replica persistence and funding keep
// their real SDK/node paths; no profile, outbox or readiness state is injected.
type pwaRefusalHTTP struct {
	inner    wallet.HttpClient
	key      string
	code     dkvs.ErrorCode
	left     atomic.Int32
	hits     atomic.Int32
	pool     string
	payer    string
	depleted atomic.Bool
}

func (h *pwaRefusalHTTP) SendGetRequest(u *wallet.URL) ([]byte, error) {
	return h.SendGetRequestContext(context.Background(), u)
}

func (h *pwaRefusalHTTP) SendGetRequestContext(ctx context.Context, u *wallet.URL) ([]byte, error) {
	raw, err := h.inner.(wallet.ContextHttpClient).SendGetRequestContext(ctx, u)
	if err != nil || !h.depleted.Load() || !strings.HasSuffix(u.Path, "/v3/contracts/"+h.pool+"/state") {
		return raw, err
	}
	state, err := dkvs.DecodeAutopayContractState(raw, h.pool)
	if err != nil {
		return nil, err
	}
	delegate := state.Delegates[h.payer]
	delegate.Balance = "0"
	state.Delegates[h.payer] = delegate
	return json.Marshal(map[string]any{"code": 0, "data": state})
}

func (h *pwaRefusalHTTP) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	return h.SendPostRequestContext(context.Background(), u, body)
}

func (h *pwaRefusalHTTP) SendPostRequestContext(ctx context.Context, u *wallet.URL, body []byte) ([]byte, error) {
	if strings.HasSuffix(u.Path, "/v3/dkvs/records/batch-cas") && h.left.Load() > 0 {
		var batch core.BatchCASRequest
		if err := json.Unmarshal(body, &batch); err != nil {
			return nil, err
		}
		for _, mutation := range batch.Mutations {
			if mutation.Record != nil && mutation.Record.Key == h.key && h.left.Add(-1) >= 0 {
				h.hits.Add(1)
				if h.payer != "" {
					h.depleted.Store(true)
				}
				return json.Marshal(map[string]any{"code": -1, "error_code": h.code,
					"msg": "controlled definite PWA backup refusal"})
			}
		}
	}
	raw, err := h.inner.(wallet.ContextHttpClient).SendPostRequestContext(ctx, u, body)
	if err == nil && (strings.HasSuffix(u.Path, "/btc/tx") || strings.HasSuffix(u.Path, "/btc/txs")) {
		var response struct {
			Code int `json:"code"`
		}
		if json.Unmarshal(raw, &response) == nil && response.Code == 0 {
			h.depleted.Store(false)
		}
	}
	return raw, err
}

func checkPWAFundingAfterBackup(t *testing.T, f *accountReviewFixture) {
	t.Helper()
	rootID := f.manager.GetAccountManagementStatus().RootWalletID
	key, err := dkvs.AccountPersonalKey(f.manager.GetAccountManagementStatus().AccountID, "account/state")
	require.NoError(t, err)
	before, err := f.manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	h := &pwaRefusalHTTP{inner: wallet.NewHTTPClient(), key: key, code: dkvs.ErrorCodeInvalidRecord,
		pool: before.ContractAddress, payer: before.Payer}
	_, location := accountReviewConfig(t, f.network)
	f.manager.SetIndexerHttpClient_SatsNet(wallet.NewIndexerClient(location.Scheme, location.Host, location.Proxy, h))
	f.manager.SetDKVSHttpClient(h)
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	h.left.Store(1)
	require.NoError(t, f.manager.UpdateWalletName(rootID, "PWA rename before renewal"))
	_ = f.manager.SyncAccountManagementState(context.Background())
	require.Eventually(t, func() bool { return h.hits.Load() == 1 }, 10*time.Second, 20*time.Millisecond,
		"ordinary rename must reach automatic backup submission")
	entries, err := core.NewReplicaStore(f.manager.GetDB()).LoadOutbox("test:testnet")
	require.NoError(t, err)
	var rejected *core.BatchOutboxEntry
	for _, entry := range entries {
		if entry.OriginKey == key && entry.State == core.DKVSOutboxConflict {
			rejected = entry
		}
	}
	require.NotNil(t, rejected, "the failed public operation must persist its actual refusal")
	require.Equal(t, string(dkvs.ErrorCodeInvalidRecord), rejected.LastErrorCode)
	require.Contains(t, rejected.LastError, wallet.ErrAccountAutopayFundingRequired.Error())

	// An ordinary backup observes depleted payment. Only successful submission
	// of the real renewal transaction releases the controlled exhausted view.
	quote, err := f.manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, quote.Required && quote.CanFund)
	require.True(t, quote.NeedsFunding)
	funding, fundErr := f.manager.FundAccountAutopay(*quote)
	assert.NoError(t, fundErr, "a refused wallet-name backup must not block the PWA recharge action")
	if fundErr == nil {
		require.NotEmpty(t, funding.TransactionID, "funding must submit the real signed transaction")
		require.Eventually(t, func() bool {
			status, err := f.manager.GetAccountAutopayFundingStatus()
			return err == nil && status.Ready && !status.FundingPending
		}, 30*time.Second, 200*time.Millisecond, "actual renewal must confirm")
	}
	assert.NoError(t, f.manager.RetryDKVSSync())
	// PWA retry is queued and the UI follows status events. WaitAccountBackup
	// deliberately fails fast on the previous error until the new ACK arrives.
	require.Eventually(t, func() bool {
		status := f.manager.GetAccountManagementStatus()
		return status.BackupState == "backed_up" && !status.ManagedDataDirty
	}, 10*time.Second, 50*time.Millisecond, "PWA status must confirm the renamed wallet backup after retry")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assert.NoError(t, f.manager.WaitAccountManagedDataReady(ctx),
		"explicit PWA retry with valid payment must recover the refused backup")
	entries, err = core.NewReplicaStore(f.manager.GetDB()).LoadOutbox("test:testnet")
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotEqual(t, rejected.RequestID, entry.RequestID, "definitely refused request must not remain frozen")
	}
}

func TestSDKPWAWrapperRebase(t *testing.T) {
	network := newDKVSNoPluginUnfundedNetwork(t)
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReview(t, network, true)
	f.activate(t)
	key, err := dkvs.AccountPersonalKey(f.manager.GetAccountManagementStatus().AccountID, "account/root")
	require.NoError(t, err)
	h := &pwaRefusalHTTP{inner: wallet.NewHTTPClient(), key: key, code: dkvs.ErrorCodeStaleGeneration}
	f.manager.SetDKVSHttpClient(h)
	require.NoError(t, f.manager.SyncAccountManagementState(context.Background()))
	for round := 0; round < 2; round++ {
		_, err = f.manager.ConfirmAccountStorage(wallet.AccountStorageTemporary, 0)
		require.NoError(t, err, "PWA recovery reconfiguration must remain usable after a successful rebase")
		backup, exportErr := f.manager.ExportAccountBackupForPWA(accountReviewPassword, nil)
		require.NoError(t, exportErr)
		f.pkg, err = f.manager.CreateAccountRecoveryPackage(account.CreateOptions{
			AccountID: f.manager.GetAccountManagementStatus().AccountID, Backup: backup,
			RecoveryMode: account.RecoveryMode2Of2, Questions: e2eKnowledgeQuestions(),
		})
		require.NoError(t, err)
		if round == 0 {
			h.left.Store(1)
		}
		f.activate(t)
		require.Equal(t, int32(1), h.hits.Load(), "the first wrapper publication must encounter the refusal")
		entries, loadErr := core.NewReplicaStore(f.manager.GetDB()).LoadOutbox("test:testnet")
		require.NoError(t, loadErr)
		for _, entry := range entries {
			assert.False(t, entry.OriginDomain == "" && entry.State == core.DKVSOutboxConflict,
				"a successfully rebased explicit user action must not retain its rejected predecessor")
		}
	}
}
