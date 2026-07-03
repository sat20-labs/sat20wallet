package e2e

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	"github.com/sat20-labs/satoshinet/btcutil"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	localwire "github.com/sat20-labs/satoshinet/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetAgentPredictionTenBettors(t *testing.T) {
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/basketball/final", r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>Basketball match prediction: Home team vs away team. Home 101, Away 98. Final.</body></html>`))
	}))
	t.Cleanup(resultServer.Close)

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		var req map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		content := `{"result_type":"outcome","outcome_id":"home","reason":"Home team won 101-98"}`
		if llmRequestContains(req, "Review this prediction contract") {
			content = `{"ready":true,"reason":"basketball result is verifiable"}`
		}
		encoded, err := json.Marshal(content)
		require.NoError(t, err)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + string(encoded) + `}}]}`))
	}))
	t.Cleanup(llmServer.Close)

	const lockedValue = int64(300000)
	gas := contractcommon.GetGasAssetName()
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	deployer := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 1))
	bettors := make([]*templateActor, 0, 10)
	for i := 0; i < 10; i++ {
		bettors = append(bettors, newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, uint32(10+i))))
	}

	witnessScript, lockedPkScript, err := getP2WSHScript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)
	lockedUtxo := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb:0"
	fakeL1 := newFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), lockedPkScript,
		[]fakeL1Asset{{
			Utxo:  lockedUtxo,
			Value: lockedValue,
			Assets: map[string]string{
				gas: "4000000",
			},
		}})
	network := newRealSatoshiNetWithArgs(t, fakeL1, nil, []string{
		"--agentllmprovider=openai",
		"--agentllmendpoint=" + llmServer.URL,
		"--agentllmmodel=fake-agent",
		"--agentcheckinterval=1s",
	}, nil)

	anchorTx := buildAnchorTx(t, lockedUtxo, lockedValue,
		txAsset(gas, 4000000), gas+"-4000000-0-1",
		witnessScript, bootstrapKey, deployer.PkScript)
	network.sendAndMine(t, anchorTx, 1)

	const (
		heartbeatCount = 12
		heartbeatStart = 2
		bettorStart    = heartbeatStart + heartbeatCount
	)
	outputs := []*wire.TxOut{
		wire.NewTxOut(1000, txAsset(gas, 200000), deployer.PkScript),
		wire.NewTxOut(20000, txAsset(gas, 200000), p2trPkScriptFromKey(t, coreKey)),
	}
	for i := 0; i < heartbeatCount; i++ {
		outputs = append(outputs, wire.NewTxOut(10000, nil, deployer.PkScript))
	}
	for _, bettor := range bettors {
		outputs = append(outputs, wire.NewTxOut(1000, txAsset(gas, 200000), bettor.PkScript))
	}
	splitTx := wire.NewMsgTx(2)
	splitTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: anchorTx.TxHash(), Index: 0}})
	for _, output := range outputs {
		splitTx.AddTxOut(output)
	}
	signTaprootInputs(t, splitTx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
	network.sendAndMine(t, splitTx, 2)
	inputs := collectSpendableOutPoints(t, splitTx, outputs)

	_, height, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	contract := contractcommon.AgentPredictionContract{
		Subtype:      contractcommon.SubtypePrediction,
		Title:        "Home vs Away basketball prediction",
		Description:  "Home team vs away team",
		TimeBase:     contractcommon.TimeBaseHeight,
		BetDeadline:  int64(height) + 20,
		EventTime:    int64(height) + 21,
		ConfirmAfter: int64(height) + 22,
		SourceURL:    resultServer.URL + "/basketball/final",
		BetAsset:     gas,
		MinBetUnit:   "10000",
		Outcomes: []contractcommon.AgentPredictionOutcome{
			{ID: "home", Text: "Home team wins"},
			{ID: "away", Text: "Home team loses"},
			{ID: "draw", Text: "Draw"},
		},
	}
	deployTx, agentAddress := buildAgentDeployTx(t, contract, deployer.Address, inputs[0], outputs[0], deployer.PkScript)
	signTaprootInputs(t, deployTx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
	network.sendAndMine(t, deployTx, 3)
	waitForAgentPredictionReady(t, network.Bootstrap, agentAddress.EncodeAddress())

	resultGas, err := contractcommon.GasFeeAtHeight(contractcommon.ResultBaseGas, 0)
	require.NoError(t, err)
	outcomes := []string{"home", "away", "draw", "home", "away", "draw", "home", "away", "draw", "home"}
	winnerAddresses := make([]string, 0, 4)
	for i, bettor := range bettors {
		betTx := buildAgentInvokeTx(t, agentAddress, contractcommon.AgentInvokeAPIBet, mustAgentBetParam(t, outcomes[i]),
			inputs[bettorStart+i], txAsset(gas, 180000+resultGas), nil, bettor.PkScript)
		signTaprootInputs(t, betTx, bettor.Key, bettor.RedeemScript, bettor.ControlBlock)
		network.sendAndMine(t, betTx, int32(4+i))
		if outcomes[i] == "home" {
			winnerAddresses = append(winnerAddresses, bettor.Address)
		}
	}

	usedHeartbeats := 0
	for i := 0; i < heartbeatCount; i++ {
		heartbeatInput := inputs[heartbeatStart+i]
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(&wire.TxIn{PreviousOutPoint: heartbeatInput})
		tx.AddTxOut(wire.NewTxOut(9000, nil, deployer.PkScript))
		signTaprootInputs(t, tx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
		_, beforeHeight, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		network.sendAndMine(t, tx, beforeHeight+1)
		_, bestHeight, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		usedHeartbeats = i + 1
		if bestHeight >= int32(contract.ConfirmAfter) {
			break
		}
	}

	waitForAgentPredictionContractQueries(t, network.Bootstrap, agentAddress.EncodeAddress(), 10)
	if len(winnerAddresses) != 0 && usedHeartbeats < heartbeatCount {
		heartbeatInput := inputs[heartbeatStart+usedHeartbeats]
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(&wire.TxIn{PreviousOutPoint: heartbeatInput})
		tx.AddTxOut(wire.NewTxOut(9000, nil, deployer.PkScript))
		signTaprootInputs(t, tx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
		_, bestHeight, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		network.sendAndMine(t, tx, bestHeight+1)
	}
	for _, address := range winnerAddresses {
		requireAssetSummaryAtLeast(t, network.Bootstrap, address, gas, "405000")
	}
}

func TestRealSatoshiNetAgentPredictionPayoutByShare(t *testing.T) {
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	resultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/football/final", r.URL.Path)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>England 2, Croatia 1. Final.</body></html>`))
	}))
	t.Cleanup(resultServer.Close)

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		var req map[string]interface{}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		content := `{"result_type":"outcome","outcome_id":"home","result":"England 2-1 Croatia","reason":"England won the match"}`
		if llmRequestContains(req, "Review this prediction contract") {
			content = `{"ready":true,"reason":"match result is verifiable"}`
		}
		encoded, err := json.Marshal(content)
		require.NoError(t, err)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + string(encoded) + `}}]}`))
	}))
	t.Cleanup(llmServer.Close)

	const lockedValue = int64(300000)
	gas := contractcommon.GetGasAssetName()
	betAsset := "brc20:f:payout"
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	deployer := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))

	type betCase struct {
		outcome string
		amount  int64
	}
	bets := []betCase{
		{outcome: "home", amount: 100000},
		{outcome: "home", amount: 200000},
		{outcome: "home", amount: 300000},
		{outcome: "away", amount: 100000},
		{outcome: "away", amount: 200000},
		{outcome: "away", amount: 300000},
		{outcome: "draw", amount: 100000},
		{outcome: "draw", amount: 200000},
		{outcome: "draw", amount: 300000},
	}
	bettors := make([]*templateActor, 0, len(bets))
	for i := range bets {
		bettors = append(bettors, newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, uint32(40+i))))
	}

	witnessScript, lockedPkScript, err := getP2WSHScript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)
	gasLockedUtxo := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd:0"
	betLockedUtxo := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:0"
	fakeL1 := newFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), lockedPkScript,
		[]fakeL1Asset{
			{
				Utxo:  gasLockedUtxo,
				Value: lockedValue,
				Assets: map[string]string{
					gas: "10000000",
				},
			},
			{
				Utxo:  betLockedUtxo,
				Value: lockedValue,
				Assets: map[string]string{
					betAsset: "1800000",
				},
			},
		})
	network := newRealSatoshiNetWithArgs(t, fakeL1, nil, []string{
		"--agentllmprovider=openai",
		"--agentllmendpoint=" + llmServer.URL,
		"--agentllmmodel=fake-agent",
		"--agentcheckinterval=1s",
	}, nil)

	gasAnchorTx := buildAnchorTx(t, gasLockedUtxo, lockedValue,
		txAsset(gas, 10000000), gas+"-10000000-0-1",
		witnessScript, bootstrapKey, deployer.PkScript)
	network.sendAndMine(t, gasAnchorTx, 1)
	betAnchorTx := buildAnchorTx(t, betLockedUtxo, lockedValue,
		txAsset(betAsset, 1800000), betAsset+"-1800000-0-1",
		witnessScript, bootstrapKey, deployer.PkScript)
	network.sendAndMine(t, betAnchorTx, 2)

	const (
		heartbeatCount = 12
		heartbeatStart = 2
		gasBettorStart = heartbeatStart + heartbeatCount
	)
	gasOutputs := []*wire.TxOut{
		wire.NewTxOut(1000, txAsset(gas, 300000), deployer.PkScript),
		wire.NewTxOut(20000, txAsset(gas, 300000), p2trPkScriptFromKey(t, coreKey)),
	}
	for i := 0; i < heartbeatCount; i++ {
		gasOutputs = append(gasOutputs, wire.NewTxOut(10000, nil, deployer.PkScript))
	}
	for _, bettor := range bettors {
		gasOutputs = append(gasOutputs, wire.NewTxOut(1000, txAsset(gas, 200000), bettor.PkScript))
	}
	gasSplitTx := wire.NewMsgTx(2)
	gasSplitTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: gasAnchorTx.TxHash(), Index: 0}})
	for _, output := range gasOutputs {
		gasSplitTx.AddTxOut(output)
	}
	signTaprootInputs(t, gasSplitTx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
	network.sendAndMine(t, gasSplitTx, 3)
	gasInputs := collectSpendableOutPoints(t, gasSplitTx, gasOutputs)

	betOutputs := make([]*wire.TxOut, 0, len(bets))
	for i, bettor := range bettors {
		betOutputs = append(betOutputs, wire.NewTxOut(1000, txAsset(betAsset, bets[i].amount), bettor.PkScript))
	}
	betSplitTx := wire.NewMsgTx(2)
	betSplitTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: betAnchorTx.TxHash(), Index: 0}})
	for _, output := range betOutputs {
		betSplitTx.AddTxOut(output)
	}
	signTaprootInputs(t, betSplitTx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
	network.sendAndMine(t, betSplitTx, 4)
	betInputs := collectSpendableOutPoints(t, betSplitTx, betOutputs)

	_, height, err := network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	contract := contractcommon.AgentPredictionContract{
		Subtype:      contractcommon.SubtypePrediction,
		Title:        "England vs Croatia payout share test",
		Description:  "Validate winner payout is proportional to bet share",
		TimeBase:     contractcommon.TimeBaseHeight,
		BetDeadline:  int64(height) + 20,
		EventTime:    int64(height) + 21,
		ConfirmAfter: int64(height) + 22,
		SourceURL:    resultServer.URL + "/football/final",
		BetAsset:     betAsset,
		MinBetUnit:   "10000",
		Outcomes: []contractcommon.AgentPredictionOutcome{
			{ID: "home", Text: "England wins"},
			{ID: "away", Text: "Croatia wins"},
			{ID: "draw", Text: "Draw"},
		},
	}
	deployTx, agentAddress := buildAgentDeployTx(t, contract, deployer.Address, gasInputs[0], gasOutputs[0], deployer.PkScript)
	signTaprootInputs(t, deployTx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
	network.sendAndMine(t, deployTx, 5)
	waitForAgentPredictionReady(t, network.Bootstrap, agentAddress.EncodeAddress())

	for i, bettor := range bettors {
		funding := txAssets(
			templateFunding(t, gas, 199900),
			templateFunding(t, betAsset, bets[i].amount),
		)
		betTx := buildAgentInvokeTxWithInputs(t, agentAddress, contractcommon.AgentInvokeAPIBet, mustAgentBetParam(t, bets[i].outcome),
			[]wire.OutPoint{gasInputs[gasBettorStart+i], betInputs[i]}, funding, nil, bettor.PkScript)
		signTaprootInputs(t, betTx, bettor.Key, bettor.RedeemScript, bettor.ControlBlock)
		network.sendAndMine(t, betTx, int32(6+i))
		requireAssetSummaryZero(t, network.Bootstrap, bettor.Address, betAsset)
	}

	for i := 0; i < heartbeatCount; i++ {
		heartbeatInput := gasInputs[heartbeatStart+i]
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(&wire.TxIn{PreviousOutPoint: heartbeatInput})
		tx.AddTxOut(wire.NewTxOut(9000, nil, deployer.PkScript))
		signTaprootInputs(t, tx, deployer.Key, deployer.RedeemScript, deployer.ControlBlock)
		_, beforeHeight, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		network.sendAndMine(t, tx, beforeHeight+1)
		_, bestHeight, err := network.Bootstrap.Client.GetBestBlock()
		require.NoError(t, err)
		if bestHeight >= int32(contract.ConfirmAfter) {
			break
		}
	}

	waitForAgentPredictionContractQueries(t, network.Bootstrap, agentAddress.EncodeAddress(), len(bets))
	expected := map[int]string{
		0: "270000",
		1: "540000",
		2: "810000",
	}
	for bettorIndex, amount := range expected {
		requireAssetSummaryAmount(t, network.Bootstrap, bettors[bettorIndex].Address, betAsset, amount)
	}
	for _, loserIndex := range []int{3, 4, 5, 6, 7, 8} {
		requireAssetSummaryZero(t, network.Bootstrap, bettors[loserIndex].Address, betAsset)
	}
}

func buildAgentDeployTx(t *testing.T, contract contractcommon.AgentPredictionContract, deployer string,
	input wire.OutPoint, inputOut *wire.TxOut, spendScript []byte) (*wire.MsgTx, contractcommon.ContractAddress) {

	t.Helper()
	content, err := contract.Encode()
	require.NoError(t, err)
	deployFee, err := contractcommon.GasFeeAtHeight(contractcommon.DeployBaseGas, 0)
	require.NoError(t, err)
	tx, address, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  contractcommon.TestnetContractPrefix,
		Type:            contractcommon.ContractTypeAgent,
		SubType:         contractcommon.SubtypePrediction,
		Version:         contractcommon.CurrentAgentVersion,
		Deployer:        deployer,
		DeployNonce:     10,
		ContractContent: content,
		GasLimit:        contractcommon.DeployBaseGas,
		Inputs:          []wire.OutPoint{input},
		ExtraOutputs: []*wire.TxOut{
			wire.NewTxOut(inputOut.Value, txAsset(
				contractcommon.GetGasAssetName(),
				inputOut.Assets[0].Amount.Int64()-int64(deployFee),
			), spendScript),
		},
	})
	require.NoError(t, err)
	return tx, address
}

func buildAgentInvokeTx(t *testing.T, contract contractcommon.ContractAddress, action string, param []byte,
	input wire.OutPoint, fundingAssets wire.TxAssets, changeOut *wire.TxOut, spendScript []byte) *wire.MsgTx {

	return buildAgentInvokeTxWithInputs(t, contract, action, param, []wire.OutPoint{input}, fundingAssets, changeOut, spendScript)
}

func buildAgentInvokeTxWithInputs(t *testing.T, contract contractcommon.ContractAddress, action string, param []byte,
	inputs []wire.OutPoint, fundingAssets wire.TxAssets, changeOut *wire.TxOut, spendScript []byte) *wire.MsgTx {

	t.Helper()
	var funding wire.TxOut
	if fundingAssets != nil {
		funding.Assets = fundingAssets.Clone()
	}
	var changeOutputs []*wire.TxOut
	if changeOut != nil {
		changeOutputs = append(changeOutputs, wire.NewTxOut(changeOut.Value, changeOut.Assets.Clone(), spendScript))
	}
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:     contract,
		GasLimit:     100000,
		CallNonce:    uint64(time.Now().UnixNano()),
		Action:       action,
		Param:        param,
		Funding:      funding,
		Inputs:       inputs,
		ExtraOutputs: changeOutputs,
	})
	require.NoError(t, err)
	return tx
}

func mustAgentBetParam(t *testing.T, outcome string) []byte {
	t.Helper()
	data, err := (contractcommon.AgentPredictionBetParam{OutcomeID: outcome}).Encode()
	require.NoError(t, err)
	return data
}

func waitForAgentPredictionReady(t *testing.T, node *testHarness, contract string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		baseURL, err := node.IndexerURL("testnet")
		if err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		var history localwire.ContractHistoryResp
		if err := getIndexerJSON(baseURL+"/v3/contracts/"+contract+"/history", &history); err != nil {
			lastErr = err
			time.Sleep(300 * time.Millisecond)
			continue
		}
		for _, record := range history.Data {
			if record.Action == contractcommon.AgentInvokeAPIReady {
				return
			}
		}
		lastErr = fmt.Errorf("agent contract %s is not ready yet", contract)
		time.Sleep(300 * time.Millisecond)
	}
	require.NoError(t, lastErr)
}

func waitForAgentPredictionContractQueries(t *testing.T, node *testHarness, contract string, wantBets int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := checkAgentPredictionContractQueries(node, contract, wantBets); err == nil {
			return
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	require.NoError(t, lastErr)
}

func checkAgentPredictionContractQueries(node *testHarness, contract string, wantBets int) error {
	baseURL, err := node.IndexerURL("testnet")
	if err != nil {
		return err
	}
	var history localwire.ContractHistoryResp
	if err := getIndexerJSON(baseURL+"/v3/contracts/"+contract+"/history", &history); err != nil {
		return err
	}
	if history.Code != 0 {
		return fmt.Errorf("contract history code %d: %s", history.Code, history.Msg)
	}
	var bets, confirms int
	for _, record := range history.Data {
		switch record.Action {
		case contractcommon.AgentInvokeAPIBet:
			bets++
		case contractcommon.AgentInvokeAPIConfirm:
			confirms++
		}
	}
	if bets < wantBets || confirms == 0 {
		return fmt.Errorf("incomplete agent history: bets=%d confirms=%d total=%d", bets, confirms, history.Total)
	}

	var analytics localwire.ContractResp
	if err := getIndexerJSON(baseURL+"/v3/contracts/"+contract+"/analytics", &analytics); err != nil {
		return err
	}
	if analytics.Code != 0 {
		return fmt.Errorf("contract analytics code %d: %s", analytics.Code, analytics.Msg)
	}
	var data struct {
		TotalBets     int            `json:"totalBets"`
		Confirmations int            `json:"confirmations"`
		OutcomeBets   map[string]int `json:"outcomeBets"`
	}
	if err := json.Unmarshal(analytics.Data, &data); err != nil {
		return err
	}
	if data.TotalBets < wantBets || data.Confirmations == 0 ||
		data.OutcomeBets["home"] == 0 || data.OutcomeBets["away"] == 0 || data.OutcomeBets["draw"] == 0 {
		return fmt.Errorf("unexpected analytics: %+v", data)
	}
	return nil
}

func getIndexerJSON(endpoint string, target interface{}) error {
	resp, err := http.Get(endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d for %s", resp.StatusCode, endpoint)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func p2trPkScriptFromKey(t *testing.T, key *btcec.PrivateKey) []byte {
	t.Helper()
	tapKey := txscript.ComputeTaprootKeyNoScript(key.PubKey())
	addr, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(tapKey), &chaincfg.TestNetParams)
	require.NoError(t, err)
	pkScript, err := txscript.PayToAddrScript(addr)
	require.NoError(t, err)
	return pkScript
}

func llmRequestContains(req map[string]interface{}, needle string) bool {
	messages, ok := req["messages"].([]interface{})
	if !ok {
		return false
	}
	for _, msg := range messages {
		fields, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		content, ok := fields["content"].(string)
		if ok && strings.Contains(content, needle) {
			return true
		}
	}
	return false
}

func addAmountStrings(left, right string) string {
	var l, r big.Int
	if left != "" {
		l.SetString(left, 10)
	}
	r.SetString(right, 10)
	l.Add(&l, &r)
	return l.String()
}
