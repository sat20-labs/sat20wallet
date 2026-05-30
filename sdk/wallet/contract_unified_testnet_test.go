package wallet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sirupsen/logrus"
)

func newContractTestnetManager(t *testing.T) *Manager {
	t.Helper()
	mnemonic := os.Getenv("SAT20WALLET_CONTRACT_TESTNET_MNEMONIC")
	if mnemonic == "" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET_MNEMONIC to run live contract tests")
	}
	cfg := &common.Config{
		Env:   "prd",
		Chain: "testnet",
		Mode:  CLIENT_NODE,
		IndexerL1: &common.Indexer{
			Scheme: "https",
			Host:   "apiprd.sat20.org",
			Proxy:  "btc/testnet",
		},
		IndexerL2: &common.Indexer{
			Scheme: "https",
			Host:   "apiprd.sat20.org",
			Proxy:  "satsnet/testnet",
		},
		Log: "debug",
		DB:  t.TempDir(),
	}
	db := NewKVDB(cfg.DB)
	if db == nil {
		t.Fatalf("NewKVDB failed")
	}
	manager := NewManager(cfg, db)
	if manager == nil {
		t.Fatalf("NewManager failed")
	}
	lvl, err := logrus.ParseLevel(cfg.Log)
	if err != nil {
		lvl = logrus.DebugLevel
	}
	Log.SetLevel(lvl)
	if _, err := manager.ImportWallet(mnemonic, "123456"); err != nil {
		t.Fatalf("ImportWallet failed: %v", err)
	}
	return manager
}

func TestUnifiedContractQuery_testnet(t *testing.T) {
	if os.Getenv("SAT20WALLET_CONTRACT_TESTNET") != "1" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET=1 to query the live SatoshiNet testnet")
	}
	manager := newContractTestnetManager(t)
	result, err := manager.QueryContract(&ContractQueryRequest{Query: ContractQueryList, Start: 0, Limit: 10})
	if err != nil {
		t.Fatalf("QueryContract list failed: %v", err)
	}
	if result == "" {
		t.Fatalf("empty contract list response")
	}
	t.Logf("contracts: %s", result)
}

func TestUnifiedEVMDeployInvoke_testnet(t *testing.T) {
	if os.Getenv("SAT20WALLET_CONTRACT_TESTNET") != "1" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET=1 to spend testnet gas and deploy/invoke an EVM contract")
	}
	manager := newContractTestnetManager(t)
	startHeight := manager.l2IndexerClient.GetBestHeight()
	deployNonce := uint64(time.Now().Unix())
	deploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType: ContractTypeEVM,
		EVM: &EVMContractDeployRequest{
			// Init code returns a one-byte STOP runtime.
			InitCodeHex: "6001600c60003960016000f300",
			GasLimit:    150000,
			DeployNonce: deployNonce,
		},
	})
	if err != nil {
		t.Fatalf("DeployUnifiedContract failed: %v", err)
	}
	t.Logf("deploy result: %+v", deploy)
	if deploy.TxID == "" || deploy.ContractAddress == "" {
		t.Fatalf("invalid deploy result: %+v", deploy)
	}

	if err := waitL2HeightAbove(t, manager, startHeight, 4*time.Minute); err != nil {
		t.Fatalf("deploy was not confirmed in a new block: %v", err)
	}

	if err := waitContractIndexed(t, manager, deploy.ContractAddress, 2*time.Minute); err != nil {
		t.Fatalf("contract was not indexed after deploy: %v", err)
	}

	invoke, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeEVM,
		EVM: &EVMContractInvokeRequest{
			ContractAddress: deploy.ContractAddress,
			GasLimit:        30000,
			CallNonce:       deployNonce + 1,
		},
	})
	if err != nil {
		t.Fatalf("InvokeUnifiedContract failed: %v", err)
	}
	t.Logf("invoke result: %+v", invoke)
	if invoke.TxID == "" {
		t.Fatalf("invalid invoke result: %+v", invoke)
	}
}

func TestUnifiedAgentPredictionDeployBet_testnet(t *testing.T) {
	if os.Getenv("SAT20WALLET_CONTRACT_TESTNET") != "1" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET=1 to spend testnet gas and deploy/invoke an agent prediction contract")
	}
	manager := newContractTestnetManager(t)

	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Official final result report. Team Alpha wins the event. This maps to allowed outcome a: Team Alpha wins. The result is final and not pending."))
	}))
	defer resultServer.Close()

	startHeight := manager.l2IndexerClient.GetBestHeight()
	prediction := &AgentPredictionContract{
		Subtype:      "prediction",
		Title:        "Agent prediction live SDK test",
		Description:  "A deterministic test market. Resolve to outcome a when the source says Team Alpha won.",
		TimeBase:     "height",
		EventTime:    startHeight + 9,
		BetDeadline:  startHeight + 8,
		ConfirmAfter: startHeight + 10,
		SourceURL:    resultServer.URL,
		BetAsset:     unifiedTemplateTestAsset,
		MinBetUnit:   "1",
		Outcomes: []AgentPredictionOutcome{
			{ID: "a", Text: "Team Alpha wins"},
			{ID: "b", Text: "Team Beta wins"},
		},
	}
	deploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType: ContractTypeAgent,
		Agent: &AgentContractDeployRequest{
			Prediction: prediction,
			GasLimit:   100000,
		},
	})
	if err != nil {
		t.Fatalf("Deploy agent prediction failed: %v", err)
	}
	t.Logf("agent deploy result: %+v", deploy)
	if deploy.TxID == "" || deploy.ContractAddress == "" {
		t.Fatalf("invalid agent deploy result: %+v", deploy)
	}
	if err := waitL2HeightAbove(t, manager, startHeight, 4*time.Minute); err != nil {
		t.Fatalf("agent deploy was not confirmed: %v", err)
	}
	if err := waitContractIndexed(t, manager, deploy.ContractAddress, 2*time.Minute); err != nil {
		t.Fatalf("agent prediction was not indexed after deploy: %v", err)
	}
	if err := waitAgentPredictionStatus(t, manager, deploy.ContractAddress, "Ready", "Betting", 4*time.Minute); err != nil {
		t.Fatalf("agent prediction was not auto-readied by corenode: %v", err)
	}

	betStartHeight := manager.l2IndexerClient.GetBestHeight()
	bet, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeAgent,
		Agent: &AgentContractInvokeRequest{
			ContractAddress: deploy.ContractAddress,
			Bet:             &AgentPredictionBetParam{OutcomeID: "a"},
			BetAssetName:    unifiedTemplateTestAsset,
			BetAmount:       "1",
			GasLimit:        100000,
		},
	})
	if err != nil {
		t.Fatalf("Invoke agent bet failed: %v", err)
	}
	t.Logf("agent bet result: %+v", bet)
	if bet.TxID == "" {
		t.Fatalf("invalid agent bet result: %+v", bet)
	}
	if err := waitL2HeightAbove(t, manager, betStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("agent bet was not confirmed: %v", err)
	}
	if err := waitAgentPredictionBetCount(t, manager, deploy.ContractAddress, 1, 2*time.Minute); err != nil {
		t.Fatalf("agent bet was not indexed: %v", err)
	}
	if err := advanceL2HeightWithNullData(t, manager, prediction.ConfirmAfter+1, 5*time.Minute); err != nil {
		t.Fatalf("agent confirm height was not reached: %v", err)
	}
	if err := waitAgentPredictionStatus(t, manager, deploy.ContractAddress, "Completed", "Settled", 5*time.Minute); err != nil {
		t.Fatalf("agent prediction was not auto-confirmed/settled: %v", err)
	}
}

func TestUnifiedTemplateDeployInvoke_testnet(t *testing.T) {
	if os.Getenv("SAT20WALLET_CONTRACT_TESTNET") != "1" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET=1 to spend testnet gas and deploy/invoke template contracts")
	}
	manager := newContractTestnetManager(t)

	limitOrder := NewContract(TEMPLATE_CONTRACT_LIMITORDER)
	if limitOrder == nil {
		t.Fatalf("NewContract(%s) returned nil", TEMPLATE_CONTRACT_LIMITORDER)
	}
	limitOrder.GetContractBase().AssetName = *ParseAssetString(unifiedTemplateTestAsset)
	limitStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitDeploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType: ContractTypeTemplate,
		Template: &TemplateContractDeployRequest{
			TemplateName:    TEMPLATE_CONTRACT_LIMITORDER,
			ContractContent: string(limitOrder.Content()),
		},
	})
	if err != nil {
		t.Fatalf("Deploy limitorder failed: %v", err)
	}
	t.Logf("limitorder deploy result: %+v", limitDeploy)
	if limitDeploy.TxID == "" || limitDeploy.ContractAddress == "" {
		t.Fatalf("invalid limitorder deploy result: %+v", limitDeploy)
	}
	if err := waitL2HeightAbove(t, manager, limitStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder deploy was not confirmed: %v", err)
	}
	if err := waitContractIndexed(t, manager, limitDeploy.ContractAddress, 2*time.Minute); err != nil {
		t.Fatalf("limitorder was not indexed after deploy: %v", err)
	}

	limitInvokeStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitInvoke, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeTemplate,
		Template: &TemplateContractInvokeRequest{
			ContractAddress: limitDeploy.ContractAddress,
			JSONInvokeParam: mustInvokeJSON(t, INVOKE_API_SWAP, SwapInvokeParam{
				OrderType: ORDERTYPE_BUY,
				AssetName: unifiedTemplateTestAsset,
				Amt:       "1",
				UnitPrice: "1",
			}),
			CallNonce: uint64(time.Now().UnixNano()),
			Value:     11,
		},
	})
	if err != nil {
		t.Fatalf("Invoke limitorder swap failed: %v", err)
	}
	t.Logf("limitorder swap result: %+v", limitInvoke)
	if err := waitL2HeightAbove(t, manager, limitInvokeStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder invoke was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, limitDeploy.ContractAddress, 1, 2*time.Minute); err != nil {
		t.Fatalf("limitorder invoke was not indexed: %v", err)
	}

	amm := NewAmmContract()
	amm.AssetName = *ParseAssetString(unifiedTemplateTestAsset)
	amm.AssetAmt = "10000"
	amm.SatValue = 1
	amm.K = "10000"
	ammStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammDeploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType: ContractTypeTemplate,
		Template: &TemplateContractDeployRequest{
			TemplateName:    TEMPLATE_CONTRACT_AMM,
			ContractContent: string(amm.Content()),
		},
	})
	if err != nil {
		t.Fatalf("Deploy AMM failed: %v", err)
	}
	t.Logf("amm deploy result: %+v", ammDeploy)
	if ammDeploy.TxID == "" || ammDeploy.ContractAddress == "" {
		t.Fatalf("invalid AMM deploy result: %+v", ammDeploy)
	}
	if err := waitL2HeightAbove(t, manager, ammStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM deploy was not confirmed: %v", err)
	}
	if err := waitContractIndexed(t, manager, ammDeploy.ContractAddress, 2*time.Minute); err != nil {
		t.Fatalf("AMM was not indexed after deploy: %v", err)
	}

	ammInvokeStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammInvoke, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeTemplate,
		Template: &TemplateContractInvokeRequest{
			ContractAddress: ammDeploy.ContractAddress,
			JSONInvokeParam: mustInvokeJSON(t, INVOKE_API_SWAP, SwapInvokeParam{
				OrderType: ORDERTYPE_BUY,
				AssetName: unifiedTemplateTestAsset,
				Amt:       "1",
				UnitPrice: "1",
			}),
			CallNonce: uint64(time.Now().UnixNano()),
			Value:     11,
		},
	})
	if err != nil {
		t.Fatalf("Invoke AMM swap failed: %v", err)
	}
	t.Logf("amm invoke result: %+v", ammInvoke)
	if err := waitL2HeightAbove(t, manager, ammInvokeStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM invoke was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 1, 2*time.Minute); err != nil {
		t.Fatalf("AMM swap was not indexed: %v", err)
	}

	ammAddLiqStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammAddLiq, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeTemplate,
		Template: &TemplateContractInvokeRequest{
			ContractAddress: ammDeploy.ContractAddress,
			JSONInvokeParam: mustInvokeJSON(t, INVOKE_API_ADDLIQUIDITY, AddLiqInvokeParam{
				OrderType: ORDERTYPE_ADDLIQUIDITY,
				AssetName: unifiedTemplateTestAsset,
				Amt:       "1",
				Value:     1,
			}),
			CallNonce: uint64(time.Now().UnixNano()),
			Value:     1,
		},
	})
	if err != nil {
		t.Fatalf("Invoke AMM add liquidity failed: %v", err)
	}
	t.Logf("amm add liquidity result: %+v", ammAddLiq)
	if err := waitL2HeightAbove(t, manager, ammAddLiqStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM add liquidity was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 2, 2*time.Minute); err != nil {
		t.Fatalf("AMM add liquidity was not indexed: %v", err)
	}

	ammRemoveLiqStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammRemoveLiq, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeTemplate,
		Template: &TemplateContractInvokeRequest{
			ContractAddress: ammDeploy.ContractAddress,
			JSONInvokeParam: mustInvokeJSON(t, INVOKE_API_REMOVELIQUIDITY, RemoveLiqInvokeParam{
				OrderType: ORDERTYPE_REMOVELIQUIDITY,
				AssetName: unifiedTemplateTestAsset,
				LptAmt:    "1",
			}),
			CallNonce: uint64(time.Now().UnixNano()),
		},
	})
	if err != nil {
		t.Fatalf("Invoke AMM remove liquidity failed: %v", err)
	}
	t.Logf("amm remove liquidity result: %+v", ammRemoveLiq)
	if err := waitL2HeightAbove(t, manager, ammRemoveLiqStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM remove liquidity was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 3, 2*time.Minute); err != nil {
		t.Fatalf("AMM remove liquidity was not indexed: %v", err)
	}
}

func waitAgentPredictionStatus(t *testing.T, manager *Manager, contract string, wantContractStatus, wantPredictionStatus string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastInfo string
	var lastErr error
	for time.Now().Before(deadline) {
		info, err := manager.QueryContract(&ContractQueryRequest{Query: ContractQueryState, Contract: contract})
		if err == nil {
			lastInfo = info
			contractStatus, predictionStatus, err := agentPredictionStatuses(info)
			if err == nil && contractStatus == wantContractStatus && predictionStatus == wantPredictionStatus {
				t.Logf("agent contract %s status %s/%s", contract, contractStatus, predictionStatus)
				return nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for agent status %s/%s, last error: %v, last info: %s", wantContractStatus, wantPredictionStatus, lastErr, lastInfo)
}

func waitAgentPredictionBetCount(t *testing.T, manager *Manager, contract string, minCount int, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastInfo string
	var lastErr error
	for time.Now().Before(deadline) {
		info, err := manager.QueryContract(&ContractQueryRequest{Query: ContractQueryState, Contract: contract})
		if err == nil {
			lastInfo = info
			count, err := agentPredictionBetCount(info)
			if err == nil && count >= minCount {
				t.Logf("agent contract %s bet count %d >= %d", contract, count, minCount)
				return nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for agent bet count >= %d, last error: %v, last info: %s", minCount, lastErr, lastInfo)
}

func agentPredictionStatuses(info string) (string, string, error) {
	var root struct {
		State struct {
			Status     string `json:"status"`
			Prediction struct {
				Status string `json:"status"`
			} `json:"prediction"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(info), &root); err != nil {
		return "", "", err
	}
	return root.State.Status, root.State.Prediction.Status, nil
}

func agentPredictionBetCount(info string) (int, error) {
	var root struct {
		State struct {
			Prediction struct {
				Bets map[string]interface{} `json:"bets"`
			} `json:"prediction"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(info), &root); err != nil {
		return 0, err
	}
	return len(root.State.Prediction.Bets), nil
}

func advanceL2HeightWithNullData(t *testing.T, manager *Manager, height int64, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current := manager.l2IndexerClient.GetBestHeight()
		if current >= height {
			t.Logf("l2 height reached: %d >= %d", current, height)
			return nil
		}
		txid, err := manager.SendNullData_SatsNet([]byte(fmt.Sprintf("agent-height-%d-%d", height, time.Now().UnixNano())))
		if err != nil {
			return fmt.Errorf("send height advance tx at %d failed: %w", current, err)
		}
		t.Logf("height advance tx: %s", txid)
		if err := waitL2HeightAbove(t, manager, current, 2*time.Minute); err != nil {
			return err
		}
	}
	return fmt.Errorf("timeout advancing l2 height >= %d, last height %d", height, manager.l2IndexerClient.GetBestHeight())
}

func waitL2HeightAtLeast(t *testing.T, manager *Manager, height int64, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	lastHeight := manager.l2IndexerClient.GetBestHeight()
	for time.Now().Before(deadline) {
		lastHeight = manager.l2IndexerClient.GetBestHeight()
		if lastHeight >= height {
			t.Logf("l2 height reached: %d >= %d", lastHeight, height)
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for l2 height >= %d, last height %d", height, lastHeight)
}

func waitTemplateInvokeCount(t *testing.T, manager *Manager, contract string, minCount int, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastInfo string
	var lastErr error
	for time.Now().Before(deadline) {
		info, err := manager.QueryContract(&ContractQueryRequest{Query: ContractQueryState, Contract: contract})
		if err == nil {
			lastInfo = info
			count, err := templateInvokeCount(info)
			if err == nil && count >= minCount {
				t.Logf("contract %s invokeCount %d >= %d", contract, count, minCount)
				return nil
			}
			lastErr = err
		} else {
			lastErr = err
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s invokeCount >= %d, last error: %v, last info: %s", contract, minCount, lastErr, lastInfo)
}

func templateInvokeCount(info string) (int, error) {
	var root struct {
		Details struct {
			Template struct {
				RuntimeState struct {
					InvokeCount int `json:"invokeCount"`
				} `json:"runtimeState"`
			} `json:"template"`
		} `json:"details"`
		State struct {
			InvokeCount int `json:"invokeCount"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(info), &root); err != nil {
		return 0, err
	}
	if root.State.InvokeCount > 0 {
		return root.State.InvokeCount, nil
	}
	return root.Details.Template.RuntimeState.InvokeCount, nil
}

func waitL2HeightAbove(t *testing.T, manager *Manager, height int64, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	lastHeight := manager.l2IndexerClient.GetBestHeight()
	for time.Now().Before(deadline) {
		lastHeight = manager.l2IndexerClient.GetBestHeight()
		if lastHeight > height {
			t.Logf("l2 height advanced: %d -> %d", height, lastHeight)
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for l2 height > %d, last height %d", height, lastHeight)
}

func waitContractIndexed(t *testing.T, manager *Manager, contract string, timeout time.Duration) error {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		info, err := manager.QueryContract(&ContractQueryRequest{Query: ContractQueryState, Contract: contract})
		if err == nil && info != "" {
			t.Logf("contract info: %s", info)
			return nil
		}
		lastErr = err
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s, last error: %v", contract, lastErr)
}
