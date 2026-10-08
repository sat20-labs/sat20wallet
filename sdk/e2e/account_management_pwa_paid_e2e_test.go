package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The SDK constructs, signs and submits the actual funding transaction. Only
// pool deployment and block production use the existing node fixture helpers.
func TestSDKAccountPWAPaidFundingWithChildSelected(t *testing.T) {
	defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 50000}, nil, nil, dkvsMinerArgs(t))
	network := fixture.Network
	waitForDKVSPeerReady(t, network)
	gas := contractcommon.GetGasAssetName()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	gasOuts := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{300000, 300000, 300000}, []int64{10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, owner})
	feeOuts := splitToDKVSKeyPathActors(t, fixture, fixture.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{5000, 45000}, []int64{10000, 10000},
		[]*dkvsKeyPathActor{owner, owner})
	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	assets := txAsset(gas, 290000)
	require.NoError(t, assets.Merge(txAsset(defaults.AutopayFeeAssetName, 5000)))
	deploy, address := buildDKVSKeyPathTemplateDeploy(t, owner, contractcommon.TemplateAutopay,
		content, owner.Address, defaults.AutopayDeployNonce, []dkvsPrevOut{gasOuts[0], feeOuts[0]},
		wire.TxOut{Value: 10000, Assets: assets})
	network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)
	param, err := (&contractcommon.TemplateAutopayConfigInvokeParam{
		AmountPerBlock: "1", GasFundingAmount: "280000",
	}).Encode()
	require.NoError(t, err)
	reserve := buildDKVSKeyPathTemplateInvoke(t, owner, address, 1, contractcommon.TemplateInvokeAPIConfig,
		param, []dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	network.sendManyAndMine(t, []*wire.MsgTx{reserve}, 0)
	f := prepareAccountReviewWithMnemonic(t, network, dkvsClientMnemonic, true)
	childID, _, err := f.manager.CreateWallet(accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, childID, f.manager.GetCurrentWalletId())

	// The fixture's POS miner mines nonempty blocks. Submit one ordinary
	// transfer after the SDK funding transaction to confirm its first payment.
	type fundingResult struct {
		authorization *wallet.AccountStorageAuthorization
		err           error
	}
	done := make(chan fundingResult, 1)
	go func() {
		authorization, err := f.manager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
		done <- fundingResult{authorization, err}
	}()
	require.Eventually(t, func() bool {
		state := fetchTemplateAutopayView(t, network.Core, address.MustEncode())
		return state.Delegates[owner.Address].AmountPerBlock == "10"
	}, 30*time.Second, 200*time.Millisecond, "the root wallet's actual funding transaction must confirm")
	heartbeat := buildDKVSKeyPathAssetTransfer(t, owner, gasOuts[2], gas, 290000, 9000, owner)
	network.sendManyAndMine(t, []*wire.MsgTx{heartbeat}, 0)
	funded := <-done
	authorization, err := funded.authorization, funded.err
	require.NoError(t, err)
	require.NotEmpty(t, authorization.TransactionID)
	require.Equal(t, childID, f.manager.GetCurrentWalletId(), "funding must preserve the PWA selection")
	state := fetchTemplateAutopayView(t, network.Core, address.MustEncode())
	require.Equal(t, "10", state.Delegates[owner.Address].AmountPerBlock)
	childAddress := wallet.PublicKeyToP2TRAddress_SatsNet(f.manager.GetWallet().GetPubKey())
	_, childPaid := state.Delegates[childAddress]
	require.False(t, childPaid, "the root account pays even when another wallet is selected")
	f.activate(t)
	status, err := f.manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.Required)
	require.True(t, status.Ready)
	reused, err := f.manager.FundAccountAutopay(*status)
	require.NoError(t, err)
	require.True(t, reused.Reused)
	require.Empty(t, reused.TransactionID)
}

// Crash only after the real database has committed signed bytes, before any
// broadcast. This wrapper does not fabricate transactions or node responses.
type accountPreparedCrashDB struct {
	indexercommon.KVDB
	armed        bool
	failPrepared bool
	captured     *wallet.OperationLogRecord
}

func (d *accountPreparedCrashDB) Write(key, value []byte) error {
	var record wallet.OperationLogRecord
	prepared := strings.Contains(string(key), wallet.DB_KEY_OPERATION_LOG) && wallet.DecodeFromBytes(value, &record) == nil &&
		record.Action == "account_autopay_fund" && len(record.PreparedTransaction) > 0 && record.Status == wallet.OperationLogPending
	if d.failPrepared && prepared {
		return fmt.Errorf("account E2E: signed transaction persistence failed")
	}
	if err := d.KVDB.Write(key, value); err != nil {
		return err
	}
	if d.armed && prepared {
		d.captured = &record
		d.armed = false
		panic("account E2E: stopped after durable transaction preparation")
	}
	return nil
}

type accountPreparedAckTransport struct {
	inner   wallet.HttpClient
	hide    bool
	loseAck bool
	bodies  [][]byte
}

func (h *accountPreparedAckTransport) SendGetRequest(u *wallet.URL) ([]byte, error) {
	if h.hide && (strings.Contains(u.Path, "/btc/tx/simpleinfo/") ||
		(len(h.bodies) > 0 && strings.Contains(u.Path, "/btc/rawtx/"))) {
		return nil, fmt.Errorf("account E2E: receipt temporarily unavailable")
	}
	return h.inner.SendGetRequest(u)
}
func (h *accountPreparedAckTransport) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	if strings.HasSuffix(u.Path, "/btc/tx") {
		h.bodies = append(h.bodies, append([]byte(nil), body...))
		raw, err := h.inner.SendPostRequest(u, body)
		if err == nil && h.loseAck {
			h.loseAck = false
			return nil, fmt.Errorf("account E2E: actual submission acknowledgement lost")
		}
		return raw, err
	}
	return h.inner.SendPostRequest(u, body)
}

func TestSDKAccountAutopayPreparedTransactionColdRetry(t *testing.T) {
	runAccountAutopayPreparedTransactionReview(t, false)
}

func TestSDKAccountGuardianPaidPWABatchReview(t *testing.T) {
	runAccountAutopayPreparedTransactionReview(t, true)
}

func runAccountAutopayPreparedTransactionReview(t *testing.T, browser bool) {
	defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 50000}, nil, nil, dkvsMinerArgs(t))
	network := fixture.Network
	waitForDKVSPeerReady(t, network)
	gas := contractcommon.GetGasAssetName()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	heartbeatOwner := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 0))
	gasOuts := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{300000, 300000, 300000}, []int64{10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, heartbeatOwner})
	feeOuts := splitToDKVSKeyPathActors(t, fixture, fixture.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{5000, 45000}, []int64{10000, 10000},
		[]*dkvsKeyPathActor{owner, owner})
	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	assets := txAsset(gas, 290000)
	require.NoError(t, assets.Merge(txAsset(defaults.AutopayFeeAssetName, 5000)))
	deploy, address := buildDKVSKeyPathTemplateDeploy(t, owner, contractcommon.TemplateAutopay,
		content, owner.Address, defaults.AutopayDeployNonce, []dkvsPrevOut{gasOuts[0], feeOuts[0]},
		wire.TxOut{Value: 10000, Assets: assets})
	network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)
	param, err := (&contractcommon.TemplateAutopayConfigInvokeParam{
		AmountPerBlock: "1", GasFundingAmount: "280000",
	}).Encode()
	require.NoError(t, err)
	reserve := buildDKVSKeyPathTemplateInvoke(t, owner, address, 1, contractcommon.TemplateInvokeAPIConfig,
		param, []dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	network.sendManyAndMine(t, []*wire.MsgTx{reserve}, 0)

	config, location := accountReviewConfig(t, network)
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	t.Cleanup(func() { database.Close() })
	crash := &accountPreparedCrashDB{KVDB: database}
	manager := wallet.NewManager(config, crash)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close() })
	_, err = manager.ImportWallet(dkvsClientMnemonic, accountReviewPassword)
	require.NoError(t, err)
	require.NoError(t, manager.InitializeAccountManagement(accountReviewPassword))
	require.NoError(t, manager.BindAccountToCurrentCoreNode())
	t.Run("PreparationWriteFailure", func(t *testing.T) {
		transport := &accountPreparedAckTransport{inner: wallet.NewHTTPClient()}
		manager.SetIndexerHttpClient_SatsNet(wallet.NewIndexerClient(location.Scheme, location.Host, location.Proxy, transport))
		crash.failPrepared = true
		defer func() { crash.failPrepared = false }()
		_, err := manager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
		require.ErrorContains(t, err, "signed transaction persistence failed")
		require.Empty(t, transport.bodies, "signed transaction was broadcast before durable preparation")
		logs, err := manager.GetOperationLogs()
		require.NoError(t, err)
		require.Len(t, logs, 1)
		require.Equal(t, wallet.OperationLogFailed, logs[0].Status)
		require.Empty(t, logs[0].TxID)
		require.Empty(t, logs[0].PreparedTransaction)
	})
	for _, mode := range []string{"BeforeBroadcast", "AcknowledgementLost"} {
		t.Run(mode, func(t *testing.T) {
			count := uint64(100)
			if mode == "AcknowledgementLost" {
				count = 200
			}
			transport := &accountPreparedAckTransport{inner: wallet.NewHTTPClient(), hide: true, loseAck: mode == "AcknowledgementLost"}
			manager.SetIndexerHttpClient_SatsNet(wallet.NewIndexerClient(location.Scheme, location.Host, location.Proxy, transport))
			if mode == "BeforeBroadcast" {
				crash.armed = true
				func() {
					defer func() { require.Equal(t, "account E2E: stopped after durable transaction preparation", recover()) }()
					_, _ = manager.ConfirmAccountStorage(wallet.AccountStoragePaid, count)
				}()
				require.Empty(t, transport.bodies, "broadcast occurred before simulated exit")
			} else {
				_, err := manager.ConfirmAccountStorage(wallet.AccountStoragePaid, count)
				var pending *wallet.AccountAutopayPendingError
				require.ErrorAs(t, err, &pending)
				require.Len(t, transport.bodies, 1, "original transaction was not actually submitted")
			}
			logs, err := manager.GetOperationLogs()
			require.NoError(t, err)
			var original *wallet.OperationLogRecord
			for _, log := range logs {
				if log.Action == "account_autopay_fund" && log.Status == wallet.OperationLogPending {
					original = log
					break
				}
			}
			require.NotNil(t, original)
			require.NotEmpty(t, original.PreparedTransaction)
			expected := wire.NewMsgTx(2)
			require.NoError(t, expected.Deserialize(bytes.NewReader(original.PreparedTransaction)))
			require.Equal(t, original.TxID, expected.TxID())
			manager.Close()
			// Database survives while the entire SDK runtime is recreated.
			manager = wallet.NewManager(config, database)
			require.NotNil(t, manager)
			_, err = manager.UnlockWallet(accountReviewPassword)
			require.NoError(t, err)
			manager.SetIndexerHttpClient_SatsNet(wallet.NewIndexerClient(location.Scheme, location.Host, location.Proxy, transport))
			before := len(transport.bodies)
			status, err := manager.GetAccountAutopayFundingStatus()
			require.NoError(t, err)
			require.True(t, status.FundingCanResume)
			require.Equal(t, before, len(transport.bodies), "status read submitted a payment")
			result, err := manager.FundAccountAutopay(*status)
			require.NoError(t, err)
			require.Equal(t, original.TxID, result.TransactionID)
			require.Len(t, transport.bodies, before+1)
			if before > 0 {
				require.Equal(t, transport.bodies[0], transport.bodies[1], "retry changed signed bytes or payment intent")
			}
			hash, err := chainhash.NewHashFromStr(original.TxID)
			require.NoError(t, err)
			require.Eventually(t, func() bool { _, err := network.Core.Client.GetRawTransaction(hash); return err == nil }, 30*time.Second, 200*time.Millisecond)
			actual, err := network.Core.Client.GetRawTransaction(hash)
			require.NoError(t, err)
			var observed bytes.Buffer
			require.NoError(t, actual.MsgTx().Serialize(&observed))
			require.Equal(t, original.PreparedTransaction, observed.Bytes())
			network.waitForTx(t, expected, 0)
			transport.hide = false
			// Production POS mines nonempty blocks; confirm initial payment with one transfer.
			remainingGas := int64(290000)
			if mode == "AcknowledgementLost" {
				remainingGas = 280000
			}
			heartbeat := buildDKVSKeyPathAssetTransfer(t, heartbeatOwner, gasOuts[2], gas, remainingGas, gasOuts[2].Output.Value-1000, heartbeatOwner)
			network.sendManyAndMine(t, []*wire.MsgTx{heartbeat}, 0)
			gasOuts[2] = dkvsPrevOut{Point: wire.OutPoint{Hash: heartbeat.TxHash(), Index: 0}, Output: cloneDKVSTxOut(heartbeat.TxOut[0])}
			require.EventuallyWithT(t, func(c *assert.CollectT) {
				status, err := manager.GetAccountAutopayFundingStatus()
				if !assert.NoError(c, err) {
					return
				}
				info, receiptErr := manager.GetIndexerRPCClient_SatsNet().GetTxInfo(original.TxID)
				assert.NoError(c, receiptErr)
				if assert.NotNil(c, info) {
					assert.Positive(c, info.BlockHeight, "original funding transaction did not confirm")
				}
				assert.False(c, status.FundingPending, "contract result did not finish original operation: %s", status.Reason)
			}, 30*time.Second, 200*time.Millisecond)
			completed, err := manager.GetOperationLog(original.ID)
			require.NoError(t, err)
			require.Equal(t, wallet.OperationLogSucceeded, completed.Status)
			finalLogs, err := manager.GetOperationLogs()
			require.NoError(t, err)
			require.Len(t, finalLogs, len(logs), "continuation created another operation")
			require.NoError(t, manager.SyncAccountManagementState(context.Background()))
		})
	}
	if browser {
		manager.Close()
		var mineMu sync.Mutex
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/mine" {
				http.NotFound(w, r)
				return
			}
			mineMu.Lock()
			defer mineMu.Unlock()
			heartbeat := buildDKVSKeyPathAssetTransfer(t, heartbeatOwner, gasOuts[2], gas, 280000,
				gasOuts[2].Output.Value-1000, heartbeatOwner)
			network.sendManyAndMine(t, []*wire.MsgTx{heartbeat}, 0)
			gasOuts[2] = dkvsPrevOut{Point: wire.OutPoint{Hash: heartbeat.TxHash(), Index: 0}, Output: cloneDKVSTxOut(heartbeat.TxOut[0])}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mined":true}`))
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, file, _, ok := runtime.Caller(0)
		require.True(t, ok)
		sdk := filepath.Dir(filepath.Dir(file))
		wasmPath, runtimePath := buildPWAWalletRuntime(t, ctx, sdk)
		configJSON, err := json.Marshal(config)
		require.NoError(t, err)
		fixtureJSON, err := json.Marshal(map[string]string{"mnemonic": dkvsClientMnemonic,
			"password": accountReviewPassword, "contract": address.MustEncode(), "control_url": server.URL})
		require.NoError(t, err)
		command := exec.CommandContext(ctx, "node", "scripts/verify/account-management-e2e.mjs", "--usage-cases",
			"usage: temporary Guardian renews paid hosting without changing its own recovery",
			"usage: original AUTOPAY resumes after prebroadcast exit and lost acknowledgement")
		command.Dir = filepath.Join(sdk, "..", "pwa")
		command.Env = append(os.Environ(), "SAT20_ACCOUNT_E2E_CONFIG="+string(configJSON),
			"SAT20_PWA_AUTOPAY_REVIEW="+string(fixtureJSON), "SAT20_PWA_E2E_WASM="+wasmPath, "SAT20_PWA_E2E_WASM_RUNTIME="+runtimePath)
		var output bytes.Buffer
		command.Stdout = io.MultiWriter(&output, os.Stdout)
		command.Stderr = command.Stdout
		require.NoError(t, command.Run(), "paid Guardian/continuation browser review: %s", output.String())
	}
}
