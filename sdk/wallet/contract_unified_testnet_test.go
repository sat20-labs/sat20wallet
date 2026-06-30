package wallet

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	sbtcutil "github.com/sat20-labs/satoshinet/btcutil"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/sirupsen/logrus"
)

const liveTestnetBootstrapAddress = "tb1p62gjhywssq42tp85erlnvnumkt267ypndrl0f3s4sje578cgr79sekhsua"

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
		ContractType:    ContractTypeEVM,
		ContractContent: "6001600c60003960016000f300",
		ContentEncoding: "hex",
		GasLimit:        contractcommon.DeployBaseGas,
		GasAssetAmount:  20000,
		DeployNonce:     deployNonce,
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
		ContractType:    ContractTypeEVM,
		ContractAddress: deploy.ContractAddress,
		Action:          contractcommon.ContractInvokeAPICall,
		Param:           mustJSONParam(t, EVMCalldataInvokeParam{}),
		GasLimit:        contractcommon.InvokeBaseGas,
		GasAssetAmount:  20000,
		CallNonce:       deployNonce + 1,
	})
	if err != nil {
		t.Fatalf("InvokeUnifiedContract failed: %v", err)
	}
	t.Logf("invoke result: %+v", invoke)
	if invoke.TxID == "" {
		t.Fatalf("invalid invoke result: %+v", invoke)
	}
}

func TestEVMCloseProfit_testnet(t *testing.T) {
	if os.Getenv("SAT20WALLET_EVM_CLOSE_LIVE") != "1" {
		t.Skip("set SAT20WALLET_EVM_CLOSE_LIVE=1 to spend testnet gas and test EVM close profit")
	}
	manager := newContractTestnetManager(t)
	deployer := manager.wallet.GetAddress()
	bootstrap := liveTestnetBootstrapAddress
	startHeight := manager.l2IndexerClient.GetBestHeight()
	deploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType:    ContractTypeEVM,
		ContractContent: evmLiveInitCodeHex([]byte{0x60, 0x01, 0x60, 0x00, 0x55}),
		ContentEncoding: "hex",
		GasLimit:        contractcommon.DeployBaseGas,
		GasAssetAmount:  20000,
	})
	if err != nil {
		t.Fatalf("DeployUnifiedContract failed: %v", err)
	}
	t.Logf("evm close/profit deploy: %+v", deploy)
	if err := waitL2HeightAbove(t, manager, startHeight, 4*time.Minute); err != nil {
		t.Fatalf("deploy was not confirmed: %v", err)
	}
	if err := waitContractIndexed(t, manager, deploy.ContractAddress, 2*time.Minute); err != nil {
		t.Fatalf("contract was not indexed after deploy: %v", err)
	}

	fundStart := manager.l2IndexerClient.GetBestHeight()
	fund, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeEVM,
		ContractAddress: deploy.ContractAddress,
		DefaultInvoke:   true,
		Value:           100,
	})
	if err != nil {
		t.Fatalf("fund EVM contract failed: %v", err)
	}
	t.Logf("evm close/profit fund: %+v", fund)
	if err := waitL2HeightAbove(t, manager, fundStart, 4*time.Minute); err != nil {
		t.Fatalf("fund was not confirmed: %v", err)
	}

	closeStart := manager.l2IndexerClient.GetBestHeight()
	closeResult, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeEVM,
		ContractAddress: deploy.ContractAddress,
		Action:          contractcommon.ContractInvokeAPIClose,
		CallNonce:       uint64(time.Now().UnixNano()),
		GasLimit:        contractcommon.InvokeBaseGas,
		GasAssetAmount:  20000,
	})
	if err != nil {
		t.Fatalf("close EVM contract failed: %v", err)
	}
	t.Logf("evm close/profit close invoke: %+v", closeResult)
	if err := waitL2HeightAbove(t, manager, closeStart, 4*time.Minute); err != nil {
		t.Fatalf("close was not confirmed: %v", err)
	}
	closeHeight, err := waitL2TxHeight(t, manager, closeResult.TxID, 2*time.Minute)
	if err != nil {
		t.Fatalf("close tx height: %v", err)
	}
	resultTx := findL2ResultSpendingTx(t, manager, int(closeHeight), closeResult.TxID)
	values := txOutputValuesByAddress(t, resultTx)
	if values[deployer] != 60 {
		t.Fatalf("deployer profit mismatch: got %d want 60 outputs=%v", values[deployer], values)
	}
	if values[bootstrap] != 40 {
		t.Fatalf("bootstrap profit mismatch: got %d want 40 outputs=%v", values[bootstrap], values)
	}
	t.Logf("evm close/profit result tx=%s outputs=%v", resultTx.TxID(), values)
}

func evmLiveInitCodeHex(runtime []byte) string {
	init := []byte{
		0x60, byte(len(runtime)),
		0x60, 0x0c,
		0x60, 0x00,
		0x39,
		0x60, byte(len(runtime)),
		0x60, 0x00,
		0xf3,
	}
	return hex.EncodeToString(append(init, runtime...))
}

func TestUnifiedTemplateDefaultInvoke_testnet(t *testing.T) {
	if os.Getenv("SAT20WALLET_CONTRACT_TESTNET") != "1" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET=1 to spend testnet gas and test default template invokes")
	}
	manager := newContractTestnetManager(t)

	limitOrder := NewContract(TEMPLATE_CONTRACT_LIMITORDER)
	if limitOrder == nil {
		t.Fatalf("NewContract(%s) returned nil", TEMPLATE_CONTRACT_LIMITORDER)
	}
	limitOrder.GetContractBase().AssetName = *ParseAssetString(unifiedTemplateTestAsset)
	limitStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitDeploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType:    ContractTypeTemplate,
		SubType:         TEMPLATE_CONTRACT_LIMITORDER,
		ContractContent: mustUnifiedContractContent(t, ContractTypeTemplate, TEMPLATE_CONTRACT_LIMITORDER, string(limitOrder.Content())),
		ContentEncoding: "base64",
	})
	if err != nil {
		t.Fatalf("Deploy limitorder failed: %v", err)
	}
	t.Logf("limitorder deploy result: %+v", limitDeploy)
	if err := waitL2HeightAbove(t, manager, limitStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder deploy was not confirmed: %v", err)
	}
	if err := waitContractIndexed(t, manager, limitDeploy.ContractAddress, 2*time.Minute); err != nil {
		t.Fatalf("limitorder was not indexed after deploy: %v", err)
	}

	limitSellGas, _, err := manager.templateGasAssetAmount(contractcommon.InvokeBaseGas, true, 0)
	if err != nil {
		t.Fatalf("estimate limitorder sell gas failed: %v", err)
	}
	limitSellStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitSell, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: limitDeploy.ContractAddress,
		Action:          INVOKE_API_SWAP,
		Param: mustJSONParam(t, SwapInvokeParam{
			OrderType: ORDERTYPE_SELL,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			UnitPrice: "1",
		}),
		CallNonce:      uint64(time.Now().UnixNano()),
		GasAssetAmount: int64(limitSellGas + 1),
	})
	if err != nil {
		t.Fatalf("Invoke limitorder sell failed: %v", err)
	}
	t.Logf("limitorder sell result: %+v", limitSell)
	if err := waitL2HeightAbove(t, manager, limitSellStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder sell was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, limitDeploy.ContractAddress, 1, 2*time.Minute); err != nil {
		t.Fatalf("limitorder sell was not indexed: %v", err)
	}

	limitDefaultStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitDefaultBuy, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: limitDeploy.ContractAddress,
		DefaultInvoke:   true,
		Value:           1,
	})
	if err != nil {
		t.Fatalf("Default invoke limitorder buy failed: %v", err)
	}
	t.Logf("limitorder default buy result: %+v", limitDefaultBuy)
	if err := waitL2HeightAbove(t, manager, limitDefaultStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder default buy was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, limitDeploy.ContractAddress, 2, 2*time.Minute); err != nil {
		t.Fatalf("limitorder default buy was not indexed: %v", err)
	}

	limitExplicitBuyStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitExplicitBuy, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: limitDeploy.ContractAddress,
		Action:          INVOKE_API_SWAP,
		Param: mustJSONParam(t, SwapInvokeParam{
			OrderType: ORDERTYPE_BUY,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			UnitPrice: "1",
		}),
		CallNonce: uint64(time.Now().UnixNano()),
		Value:     1,
	})
	if err != nil {
		t.Fatalf("Invoke limitorder buy failed: %v", err)
	}
	t.Logf("limitorder explicit buy result: %+v", limitExplicitBuy)
	if err := waitL2HeightAbove(t, manager, limitExplicitBuyStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder explicit buy was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, limitDeploy.ContractAddress, 3, 2*time.Minute); err != nil {
		t.Fatalf("limitorder explicit buy was not indexed: %v", err)
	}

	limitDefaultSellStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitDefaultSell, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: limitDeploy.ContractAddress,
		DefaultInvoke:   true,
		Assets:          []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: "1"}},
	})
	if err != nil {
		t.Fatalf("Default invoke limitorder sell failed: %v", err)
	}
	t.Logf("limitorder default sell result: %+v", limitDefaultSell)
	if err := waitL2HeightAbove(t, manager, limitDefaultSellStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder default sell was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, limitDeploy.ContractAddress, 4, 2*time.Minute); err != nil {
		t.Fatalf("limitorder default sell was not indexed: %v", err)
	}

	amm := NewAmmContract()
	amm.AssetName = *ParseAssetString(unifiedTemplateTestAsset)
	amm.AssetAmt = "1"
	amm.SatValue = 1
	amm.K = "1"
	ammStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammDeploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType:    ContractTypeTemplate,
		SubType:         TEMPLATE_CONTRACT_AMM,
		ContractContent: mustUnifiedContractContent(t, ContractTypeTemplate, TEMPLATE_CONTRACT_AMM, string(amm.Content())),
		ContentEncoding: "base64",
		FundingValue:    amm.SatValue,
		Assets:          []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: amm.AssetAmt}},
	})
	if err != nil {
		t.Fatalf("Deploy AMM failed: %v", err)
	}
	t.Logf("amm deploy result: %+v", ammDeploy)
	if err := waitL2HeightAbove(t, manager, ammStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM deploy was not confirmed: %v", err)
	}
	if err := waitContractIndexed(t, manager, ammDeploy.ContractAddress, 2*time.Minute); err != nil {
		t.Fatalf("AMM was not indexed after deploy: %v", err)
	}

	ammAddLiqStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammAddLiq, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		Action:          INVOKE_API_ADDLIQUIDITY,
		Param: mustJSONParam(t, AddLiqInvokeParam{
			OrderType: ORDERTYPE_ADDLIQUIDITY,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			Value:     1,
		}),
		CallNonce: uint64(time.Now().UnixNano()),
		Assets:    []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: "1"}},
		Value:     1,
	})
	if err != nil {
		t.Fatalf("Invoke AMM add liquidity failed: %v", err)
	}
	t.Logf("amm add liquidity result: %+v", ammAddLiq)
	if err := waitL2HeightAbove(t, manager, ammAddLiqStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM add liquidity was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 1, 2*time.Minute); err != nil {
		t.Fatalf("AMM add liquidity was not indexed: %v", err)
	}

	ammDefaultBuyStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammDefaultBuy, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		DefaultInvoke:   true,
		Value:           1,
	})
	if err != nil {
		t.Fatalf("Default invoke AMM buy failed: %v", err)
	}
	t.Logf("amm default buy result: %+v", ammDefaultBuy)
	if err := waitL2HeightAbove(t, manager, ammDefaultBuyStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM default buy was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 2, 2*time.Minute); err != nil {
		t.Fatalf("AMM default buy was not indexed: %v", err)
	}

	ammDefaultSellStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammDefaultSell, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		DefaultInvoke:   true,
		Assets:          []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: "1"}},
	})
	if err != nil {
		t.Fatalf("Default invoke AMM sell failed: %v", err)
	}
	t.Logf("amm default sell result: %+v", ammDefaultSell)
	if err := waitL2HeightAbove(t, manager, ammDefaultSellStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM default sell was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 3, 2*time.Minute); err != nil {
		t.Fatalf("AMM default sell was not indexed: %v", err)
	}
}

func TestUnifiedTemplateDefaultInvokeExistingAMM_testnet(t *testing.T) {
	if os.Getenv("SAT20WALLET_CONTRACT_TESTNET") != "1" {
		t.Skip("set SAT20WALLET_CONTRACT_TESTNET=1 to spend testnet gas and test default AMM invokes")
	}
	manager := newContractTestnetManager(t)
	ammAddress := findTestnetTemplateContract(t, manager, TEMPLATE_CONTRACT_AMM)
	beforeInfo, err := manager.QueryContract(&ContractQueryRequest{Query: ContractQueryState, Contract: ammAddress})
	if err != nil {
		t.Fatalf("query AMM state failed: %v", err)
	}
	beforeCount, err := templateInvokeCount(beforeInfo)
	if err != nil {
		t.Fatalf("parse AMM invoke count failed: %v", err)
	}

	buyStartHeight := manager.l2IndexerClient.GetBestHeight()
	buy, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammAddress,
		DefaultInvoke:   true,
		Value:           1,
	})
	if err != nil {
		t.Fatalf("Default invoke existing AMM buy failed: %v", err)
	}
	t.Logf("existing AMM default buy result: %+v", buy)
	if err := waitL2HeightAbove(t, manager, buyStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("existing AMM default buy was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammAddress, beforeCount+1, 2*time.Minute); err != nil {
		t.Fatalf("existing AMM default buy was not indexed: %v", err)
	}

	sellStartHeight := manager.l2IndexerClient.GetBestHeight()
	sell, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammAddress,
		DefaultInvoke:   true,
		Assets:          []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: "1"}},
	})
	if err != nil {
		t.Fatalf("Default invoke existing AMM sell failed: %v", err)
	}
	t.Logf("existing AMM default sell result: %+v", sell)
	if err := waitL2HeightAbove(t, manager, sellStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("existing AMM default sell was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammAddress, beforeCount+2, 2*time.Minute); err != nil {
		t.Fatalf("existing AMM default sell was not indexed: %v", err)
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
	predictionJSON := mustJSONParam(t, prediction)
	deploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType:    ContractTypeAgent,
		SubType:         contractcommon.SubtypePrediction,
		ContractContent: mustUnifiedContractContent(t, ContractTypeAgent, contractcommon.SubtypePrediction, predictionJSON),
		ContentEncoding: "base64",
		GasLimit:        100000,
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
		ContractType:    ContractTypeAgent,
		SubType:         contractcommon.SubtypePrediction,
		ContractAddress: deploy.ContractAddress,
		Action:          contractcommon.AgentInvokeAPIBet,
		Param:           mustJSONParam(t, contractcommon.AgentPredictionBetParam{OutcomeID: "a"}),
		Assets:          []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: "1"}},
		GasLimit:        100000,
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
		ContractType:    ContractTypeTemplate,
		SubType:         TEMPLATE_CONTRACT_LIMITORDER,
		ContractContent: mustUnifiedContractContent(t, ContractTypeTemplate, TEMPLATE_CONTRACT_LIMITORDER, string(limitOrder.Content())),
		ContentEncoding: "base64",
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
		ContractType:    ContractTypeTemplate,
		ContractAddress: limitDeploy.ContractAddress,
		Action:          INVOKE_API_SWAP,
		Param: mustJSONParam(t, SwapInvokeParam{
			OrderType: ORDERTYPE_BUY,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			UnitPrice: "1",
		}),
		CallNonce: uint64(time.Now().UnixNano()),
		Value:     1,
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

	limitRefundStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitRefund, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: limitDeploy.ContractAddress,
		Action:          INVOKE_API_REFUND,
		Param:           mustEncodedTemplateInvokeParam(t, mustEncodeTemplateParam((&contractcommon.TemplateRefundInvokeParam{ItemIDs: []int64{1}}).Encode())),
		ParamEncoding:   "base64",
		CallNonce:       uint64(time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("Invoke limitorder refund failed: %v", err)
	}
	t.Logf("limitorder refund result: %+v", limitRefund)
	if err := waitL2HeightAbove(t, manager, limitRefundStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder refund was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, limitDeploy.ContractAddress, 2, 2*time.Minute); err != nil {
		t.Fatalf("limitorder refund was not indexed: %v", err)
	}

	limitSellGas, _, err := manager.templateGasAssetAmount(contractcommon.InvokeBaseGas, true, 0)
	if err != nil {
		t.Fatalf("estimate limitorder sell gas failed: %v", err)
	}
	limitSellStartHeight := manager.l2IndexerClient.GetBestHeight()
	limitSell, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: limitDeploy.ContractAddress,
		Action:          INVOKE_API_SWAP,
		Param: mustJSONParam(t, SwapInvokeParam{
			OrderType: ORDERTYPE_SELL,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			UnitPrice: "1",
		}),
		CallNonce:      uint64(time.Now().UnixNano()),
		GasAssetAmount: int64(limitSellGas + 1),
	})
	if err != nil {
		t.Fatalf("Invoke limitorder sell failed: %v", err)
	}
	t.Logf("limitorder sell result: %+v", limitSell)
	if err := waitL2HeightAbove(t, manager, limitSellStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("limitorder sell was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, limitDeploy.ContractAddress, 3, 2*time.Minute); err != nil {
		t.Fatalf("limitorder sell was not indexed: %v", err)
	}

	amm := NewAmmContract()
	amm.AssetName = *ParseAssetString(unifiedTemplateTestAsset)
	amm.AssetAmt = "10000"
	amm.SatValue = 1
	amm.K = "10000"
	ammStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammDeploy, err := manager.DeployUnifiedContract(&ContractDeployRequest{
		ContractType:    ContractTypeTemplate,
		SubType:         TEMPLATE_CONTRACT_AMM,
		ContractContent: mustUnifiedContractContent(t, ContractTypeTemplate, TEMPLATE_CONTRACT_AMM, string(amm.Content())),
		ContentEncoding: "base64",
		FundingValue:    amm.SatValue,
		Assets:          []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: amm.AssetAmt}},
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
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		Action:          INVOKE_API_SWAP,
		Param: mustJSONParam(t, SwapInvokeParam{
			OrderType: ORDERTYPE_BUY,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			UnitPrice: "1",
		}),
		CallNonce: uint64(time.Now().UnixNano()),
		Value:     1,
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

	ammSellGas, _, err := manager.templateGasAssetAmount(contractcommon.InvokeBaseGas, true, 0)
	if err != nil {
		t.Fatalf("estimate AMM sell gas failed: %v", err)
	}
	ammSellStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammSell, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		Action:          INVOKE_API_SWAP,
		Param: mustJSONParam(t, SwapInvokeParam{
			OrderType: ORDERTYPE_SELL,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			UnitPrice: "1",
		}),
		CallNonce:      uint64(time.Now().UnixNano()),
		GasAssetAmount: int64(ammSellGas + 1),
		Assets:         []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: "1"}},
	})
	if err != nil {
		t.Fatalf("Invoke AMM sell swap failed: %v", err)
	}
	t.Logf("amm sell result: %+v", ammSell)
	if err := waitL2HeightAbove(t, manager, ammSellStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM sell was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 2, 2*time.Minute); err != nil {
		t.Fatalf("AMM sell was not indexed: %v", err)
	}

	ammBuyAgainStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammBuyAgain, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		Action:          INVOKE_API_SWAP,
		Param: mustJSONParam(t, SwapInvokeParam{
			OrderType: ORDERTYPE_BUY,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			UnitPrice: "2",
		}),
		CallNonce: uint64(time.Now().UnixNano()),
		Value:     2,
	})
	if err != nil {
		t.Fatalf("Invoke AMM second buy swap failed: %v", err)
	}
	t.Logf("amm second buy result: %+v", ammBuyAgain)
	if err := waitL2HeightAbove(t, manager, ammBuyAgainStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM second buy was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 3, 2*time.Minute); err != nil {
		t.Fatalf("AMM second buy was not indexed: %v", err)
	}

	ammAddLiqStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammAddLiq, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		Action:          INVOKE_API_ADDLIQUIDITY,
		Param: mustJSONParam(t, AddLiqInvokeParam{
			OrderType: ORDERTYPE_ADDLIQUIDITY,
			AssetName: unifiedTemplateTestAsset,
			Amt:       "1",
			Value:     1,
		}),
		CallNonce: uint64(time.Now().UnixNano()),
		Assets:    []ContractFundingAsset{{AssetName: unifiedTemplateTestAsset, Amount: "1"}},
		Value:     1,
	})
	if err != nil {
		t.Fatalf("Invoke AMM add liquidity failed: %v", err)
	}
	t.Logf("amm add liquidity result: %+v", ammAddLiq)
	if err := waitL2HeightAbove(t, manager, ammAddLiqStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM add liquidity was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 4, 2*time.Minute); err != nil {
		t.Fatalf("AMM add liquidity was not indexed: %v", err)
	}

	ammRemoveLiqStartHeight := manager.l2IndexerClient.GetBestHeight()
	ammRemoveLiq, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		Action:          INVOKE_API_REMOVELIQUIDITY,
		Param: mustJSONParam(t, RemoveLiqInvokeParam{
			OrderType: ORDERTYPE_REMOVELIQUIDITY,
			AssetName: unifiedTemplateTestAsset,
			LptAmt:    "1",
		}),
		CallNonce: uint64(time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("Invoke AMM remove liquidity failed: %v", err)
	}
	t.Logf("amm remove liquidity result: %+v", ammRemoveLiq)
	if err := waitL2HeightAbove(t, manager, ammRemoveLiqStartHeight, 4*time.Minute); err != nil {
		t.Fatalf("AMM remove liquidity was not confirmed: %v", err)
	}
	if err := waitTemplateInvokeCount(t, manager, ammDeploy.ContractAddress, 5, 2*time.Minute); err != nil {
		t.Fatalf("AMM remove liquidity was not indexed: %v", err)
	}

	if _, err := manager.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType:    ContractTypeTemplate,
		ContractAddress: ammDeploy.ContractAddress,
		Action:          INVOKE_API_REFUND,
		Param:           mustEncodedTemplateInvokeParam(t, mustEncodeTemplateParam((&contractcommon.TemplateRefundInvokeParam{ItemIDs: []int64{1}}).Encode())),
		ParamEncoding:   "base64",
		CallNonce:       uint64(time.Now().UnixNano()),
	}); err == nil {
		t.Fatalf("AMM refund unexpectedly succeeded")
	} else {
		t.Logf("AMM refund rejected as expected: %v", err)
	}
}

func findTestnetTemplateContract(t *testing.T, manager *Manager, subtype string) string {
	t.Helper()
	result, err := manager.QueryContract(&ContractQueryRequest{Query: ContractQueryList, Start: 0, Limit: 50})
	if err != nil {
		t.Fatalf("QueryContract list failed: %v", err)
	}
	var root struct {
		Data []struct {
			Address string `json:"address"`
			Name    string `json:"name"`
			Subtype string `json:"subtype"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(result), &root); err != nil {
		t.Fatalf("unmarshal contract list: %v", err)
	}
	for _, item := range root.Data {
		if item.Address != "" && (item.Name == subtype || item.Subtype == subtype) {
			return item.Address
		}
	}
	t.Fatalf("missing testnet template contract %s in list: %s", subtype, result)
	return ""
}

func mustEncodedTemplateInvokeParam(t *testing.T, param []byte) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(param)
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

func waitL2TxHeight(t *testing.T, manager *Manager, txid string, timeout time.Duration) (int64, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		info, err := manager.l2IndexerClient.GetTxInfo(txid)
		if err == nil && info != nil && info.Confirmations > 0 {
			t.Logf("l2 tx confirmed: %s height=%d confirmations=%d", txid, info.BlockHeight, info.Confirmations)
			return info.BlockHeight, nil
		}
		lastErr = err
		time.Sleep(5 * time.Second)
	}
	return 0, fmt.Errorf("timeout waiting for tx %s confirmation: %v", txid, lastErr)
}

func findL2ResultSpendingTx(t *testing.T, manager *Manager, height int, invokeTxID string) *swire.MsgTx {
	t.Helper()
	block := l2BlockAtHeight(t, manager, height)
	var found *swire.MsgTx
	for _, tx := range block.Transactions() {
		msgTx := tx.MsgTx()
		if msgTx.TxID() == invokeTxID {
			continue
		}
		for _, in := range msgTx.TxIn {
			if in.PreviousOutPoint.Hash.String() != invokeTxID {
				continue
			}
			if found != nil && found.TxID() != msgTx.TxID() {
				t.Fatalf("multiple result txs spend invoke %s: %s and %s", invokeTxID, found.TxID(), msgTx.TxID())
			}
			found = msgTx
		}
	}
	if found == nil {
		t.Fatalf("result tx spending invoke %s not found at height %d", invokeTxID, height)
	}
	return found
}

func l2BlockAtHeight(t *testing.T, manager *Manager, height int) *sbtcutil.Block {
	t.Helper()
	hash, err := manager.l2IndexerClient.GetBlockHash(height)
	if err != nil {
		t.Fatalf("get L2 block hash %d: %v", height, err)
	}
	rawBlock, err := manager.l2IndexerClient.GetBlock(hash)
	if err != nil {
		t.Fatalf("get L2 block %d %s: %v", height, hash, err)
	}
	data, err := hex.DecodeString(rawBlock)
	if err != nil {
		t.Fatalf("decode L2 block %d: %v", height, err)
	}
	block, err := sbtcutil.NewBlockFromBytes(data)
	if err != nil {
		t.Fatalf("parse L2 block %d: %v", height, err)
	}
	return block
}

func txOutputValuesByAddress(t *testing.T, tx *swire.MsgTx) map[string]int64 {
	t.Helper()
	values := make(map[string]int64)
	for vout, out := range tx.TxOut {
		if out == nil || out.Value == 0 {
			continue
		}
		addr, err := AddrFromPkScript_SatsNet(out.PkScript)
		if err != nil {
			t.Logf("skip non-address output %s:%d value=%d: %v", tx.TxID(), vout, out.Value, err)
			continue
		}
		values[addr] += out.Value
	}
	return values
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
