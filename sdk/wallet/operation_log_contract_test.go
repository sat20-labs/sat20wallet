package wallet

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

type depositLogNode struct {
	NodeRPCClient
	contract string
	item     InvokeItem
	status   string
	deposit  RGB11ContractDeposit
}

func (n *depositLogNode) GetContractStatusReq(url string) (string, error) {
	if url != n.contract {
		return "", errOperationLogTestFailure{}
	}
	return n.status, nil
}

func (n *depositLogNode) SendActionResultNfty(_ common.Wallet, _ int64, action string, _ int, data string) error {
	switch action {
	case "":
		return nil // Peer availability probe.
	case RGB11DepositPrepareAction:
		return json.Unmarshal([]byte(data), &n.deposit)
	default:
		return errOperationLogTestFailure{}
	}
}

func (n *depositLogNode) GetContractInvokeItemByInUtxoReq(url, point string) (string, error) {
	if url != n.contract {
		return "", errOperationLogTestFailure{}
	}
	if point != n.item.InUtxo {
		return "", indexer.ErrKeyNotFound
	}
	raw, err := json.Marshal(n.item)
	return string(raw), err
}

func TestContractInvokeOperationLogIsSimpleExplicitAction(t *testing.T) {
	mgr := &Manager{db: newMemoryKVDB()}
	address, err := btcutil.NewAddressWitnessScriptHash(make([]byte, 32), GetChainParam())
	require.NoError(t, err)
	script, err := GetPkScriptFromAddress(address.EncodeAddress())
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(&wire.TxIn{})
	tx.AddTxOut(wire.NewTxOut(0, []byte{0x6a}))
	tx.AddTxOut(wire.NewTxOut(546, script))
	tx.AddTxOut(wire.NewTxOut(700, script))
	raw, err := EncodeMsgTx(tx)
	require.NoError(t, err)
	txID := tx.TxID()
	mgr.l1IndexerClient = &IndexerRPCClientMgr{active: &recoveryRPC{raw: map[string]string{txID: raw}}}
	url := GenerateContractURl(address.EncodeAddress(), "rgb11:f:test", TEMPLATE_CONTRACT_TRANSCEND)
	node := &depositLogNode{contract: url, item: InvokeItem{InUtxo: txID + ":1", OutTxId: "payout"}}
	mgr.serverNode = &Node{client: node}
	logID := mgr.beginContractInvokeOperationLog(
		url,
		`{"Action":"deposit","Param":"{}"}`,
		"Bitcoin",
		"rgb11:f:test",
		"100",
	)
	if logID == "" {
		t.Fatal("contract invoke log was not explicitly created")
	}
	mgr.completeContractInvokeOperationLog(logID, txID)
	result, err := mgr.GetContractLogResult(logID)
	require.NoError(t, err)
	require.Equal(t, ContractLogResult{Status: "ready", TxID: "payout", Chain: "satnet"}, result)

	logs, err := mgr.GetOperationLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("unexpected log count: %d", len(logs))
	}
	log := logs[0]
	if log.Action != "invoke_contract" || log.Status != OperationLogSucceeded {
		t.Fatalf("unexpected contract invoke log: %+v", log)
	}
	if log.Parameters["contract"] != url ||
		log.Parameters["network"] != "Bitcoin" ||
		log.Parameters["asset"] != "rgb11:f:test" ||
		log.Parameters["amount"] != "100" {
		t.Fatalf("contract invoke parameters are incomplete: %+v", log.Parameters)
	}
	if log.Result["txid"] != txID || log.TxID != txID {
		t.Fatalf("contract invoke result is incomplete: %+v", log)
	}
	if len(log.History) != 2 || log.History[1].Message != "Contract invocation submitted" {
		t.Fatalf("unexpected contract invoke history: %+v", log.History)
	}

	t.Run("addliq-multiple-outputs", func(t *testing.T) {
		add := mgr.beginContractActionLog(url, INVOKE_API_ADDLIQUIDITY, "Bitcoin", "ordx:f:test", "200")
		mgr.completeContractInvokeOperationLog(add, txID)
		node.item = InvokeItem{}
		result, err := mgr.GetContractLogResult(add)
		require.NoError(t, err)
		require.Equal(t, ContractLogResult{Status: "pending"}, result)
		node.item = InvokeItem{InUtxo: txID + ":2", OutTxId: "settlement"}
		result, err = mgr.GetContractLogResult(add)
		require.NoError(t, err)
		require.Equal(t, ContractLogResult{Status: "ready", TxID: "settlement", Chain: "satnet"}, result)
	})
	t.Run("satsnet-withdraw", func(t *testing.T) {
		withdraw := mgr.beginContractActionLog(url, INVOKE_API_WITHDRAW, "SatoshiNet", "rgb11:f:test", "100")
		mgr.completeContractInvokeOperationLog(withdraw, txID)
		node.item = InvokeItem{InUtxo: txID + ":0", OutTxId: "btc-payout", ToL1: true}
		result, err := mgr.GetContractLogResult(withdraw)
		require.NoError(t, err)
		require.Equal(t, ContractLogResult{Status: "ready", TxID: "btc-payout", Chain: "btc"}, result)
	})
	t.Run("enable-has-no-result-tx", func(t *testing.T) {
		enable := mgr.beginContractActionLog(url, INVOKE_API_ENABLE, "SatoshiNet", "", "")
		mgr.completeContractInvokeOperationLog(enable, txID)
		mgr.serverNode = nil
		result, err := mgr.GetContractLogResult(enable)
		require.NoError(t, err)
		require.Equal(t, ContractLogResult{Status: "none"}, result)
		record, err := mgr.GetOperationLog(enable)
		require.NoError(t, err)
		require.Equal(t, txID, record.TxID)
	})
}

func TestContractInvokeOperationLogRecordsFailureWithoutReservation(t *testing.T) {
	mgr := &Manager{db: newMemoryKVDB()}
	logID := mgr.beginContractInvokeOperationLog(
		"channel:asset:transcend.tc",
		`{"Action":"withdraw","Param":"{}"}`,
		"SatoshiNet",
		"ordx:f:test",
		"50",
	)
	mgr.failContractInvokeOperationLog(logID, errOperationLogTestFailure{})

	logs, err := mgr.GetOperationLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Status != OperationLogFailed {
		t.Fatalf("failed contract invoke log missing: %+v", logs)
	}
	if logs[0].ReservationID != 0 || logs[0].ReservationType != "" {
		t.Fatalf("simple contract invoke unexpectedly acquired reservation relation: %+v", logs[0])
	}
	if logs[0].History[len(logs[0].History)-1].Message != "contract invoke failed" {
		t.Fatalf("failure message missing: %+v", logs[0].History)
	}
}

func TestRGB11DepositLog(t *testing.T) {
	mgr, recipient, issued, evidence, rpc := newRGB11GenericSendFixture(t)
	previousChain := indexer.CHAIN
	indexer.CHAIN = "testnet"
	t.Cleanup(func() { indexer.CHAIN = previousChain })

	// A different contract already owns the candidate short name. The
	// imported contract still works through its unambiguous native RGB key.
	id, err := rgb11wallet.RGB11ContractIDHex(issued.ContractID)
	require.NoError(t, err)
	wrongID := "0" + id[1:]
	if wrongID == id {
		wrongID = "1" + id[1:]
	}
	conflict := seedRGB11RegistryName(t, mgr, issued.AssetName, "SEND", wrongID)
	_, err = mgr.RGB11ChainAsset(&issued.AssetName, false)
	require.ErrorIs(t, err, dkvs.ErrWriteConflict)
	localBalance, err := mgr.GetRGB11AssetBalance(&issued.AssetName)
	require.NoError(t, err)
	require.Positive(t, localBalance.Sign())
	_, err = mgr.CreateRGBInvoice(context.Background(), RGBInvoiceRequest{
		Asset: issued.AssetName.String(), Amount: "1", ReceiveMode: "witness", Transport: "manual",
	})
	require.NoError(t, err)
	view, err := mgr.GetRGBView()
	require.NoError(t, err)
	require.Len(t, view.Assets, 1)
	require.Empty(t, view.Assets[0].ChainAsset)

	// For the deposit path, use a fixture where the actual contract holds
	// the binding. The rejected chain admission above published no local name.
	chainAsset := seedRGB11Name(t, mgr, issued.AssetName, "SEND")
	require.Equal(t, conflict, chainAsset)
	key, err := dkvs.RGB11NameKey(chainAsset.Ticker)
	require.NoError(t, err)
	remote := mgr.http.(*rgb11MemoryDKVSHTTP)
	public, err := mgr.ExportPublicRGB11Contract(issued.ContractID)
	require.NoError(t, err)
	file, err := base64.StdEncoding.DecodeString(public.ContractConsignmentBase64)
	require.NoError(t, err)
	imported, err := recipient.ImportRGB11ContractFile(context.Background(), file)
	require.NoError(t, err)
	require.Equal(t, issued.AssetName, imported.AssetName)
	recipientView, err := recipient.GetRGBView()
	require.NoError(t, err)
	require.Len(t, recipientView.Assets, 1)
	require.Equal(t, chainAsset.String(), recipientView.Assets[0].ChainAsset)
	reg, err := mgr.VerifyRGB11PublicContract(issued.ContractID, public.ContractConsignmentBase64)
	require.NoError(t, err)
	require.Equal(t, chainAsset.String(), reg.AssetName)
	view, err = mgr.GetRGBView()
	require.NoError(t, err)
	require.Len(t, view.Assets, 1)
	require.Equal(t, chainAsset.String(), view.Assets[0].ChainAsset)

	remote.mu.Lock()
	record := remote.records[key]
	delete(remote.records, key)
	remote.mu.Unlock()
	local := &Manager{db: mgr.db}
	resolved, err := local.rgb11WalletAsset(&chainAsset)
	require.NoError(t, err)
	require.Equal(t, issued.AssetName, *resolved)
	available, _, err := mgr.GetAssetAmountForJS(mgr.wallet.GetAddress(), chainAsset.String())
	require.NoError(t, err)
	direct, err := mgr.GetRGB11AssetBalance(&issued.AssetName)
	require.NoError(t, err)
	require.Positive(t, available.Sign())
	require.Zero(t, available.Cmp(direct))
	remote.mu.Lock()
	remote.records[key] = record
	remote.mu.Unlock()

	mgr.rgbManager.evidence = newRGB11AcceptedEvidence(evidence, rpc)
	mgr.l2IndexerClient = &IndexerRPCClientMgr{active: rpc}
	node := &depositLogNode{NodeRPCClient: mgr.serverNode.client}
	mgr.serverNode.client = node
	bridge := NewTranscendContractRuntime(mgr)
	*bridge.GetAssetName() = chainAsset
	bridge.ChannelAddr = mgr.GetCoreChannelAddr()
	bridge.LocalPubKey = mgr.serverNode.Pubkey.SerializeCompressed()
	bridge.RemotePubKey, err = hex.DecodeString(indexer.GetBootstrapPubKey())
	require.NoError(t, err)
	bridge.Status, bridge.EnableBlockL1 = CONTRACT_STATUS_READY, 0
	node.contract = bridge.URL()
	status, err := json.Marshal(bridge)
	require.NoError(t, err)
	node.status = string(status)

	asset, dest := issued.AssetName.String(), recipient.wallet.GetAddress()
	_, err = mgr.PrepareRGB11ContractDeposit(context.Background(), node.contract, dest, asset, "-1", 2)
	require.EqualError(t, err, "invalid RGB11 deposit quantity")
	_, err = mgr.DepositWithContract(dest, asset, "-1", 2)
	require.EqualError(t, err, "invalid RGB11 deposit quantity")
	txID, err := mgr.DepositWithContract(dest, asset, "100", 2)
	require.NoError(t, err)
	require.NotEmpty(t, txID)
	require.Equal(t, dest, node.deposit.Intent.Recipient)
	require.Equal(t, chainAsset.String(), node.deposit.Intent.AssetName)
	require.Equal(t, issued.AssetName, node.deposit.Source.Output.Assets[0].Name)
	logs, err := mgr.GetOperationLogs()
	require.NoError(t, err)
	deposits := make(map[string]*OperationLogRecord)
	for _, record := range logs {
		if record.Action == "invoke_contract" {
			amount := record.Parameters["amount"]
			require.NotContains(t, deposits, amount)
			deposits[amount] = record
		}
	}
	require.Len(t, deposits, 2)
	for _, record := range deposits {
		require.Equal(t, node.contract, record.Parameters["contract"])
		require.Equal(t, INVOKE_API_DEPOSIT, record.Parameters["action"])
		require.Equal(t, "Bitcoin", record.Parameters["network"])
		require.Equal(t, chainAsset.String(), record.Parameters["asset"])
	}
	require.Contains(t, deposits, "-1")
	require.Equal(t, OperationLogFailed, deposits["-1"].Status)
	require.Empty(t, deposits["-1"].TxID)
	require.Contains(t, deposits, "100")
	require.Equal(t, OperationLogSucceeded, deposits["100"].Status)
	require.Equal(t, txID, deposits["100"].TxID)
	raw, err := evidence.GetRawTx(txID)
	require.NoError(t, err)
	mgr.l1IndexerClient = &IndexerRPCClientMgr{active: &recoveryRPC{raw: map[string]string{txID: hex.EncodeToString(raw)}}}
	node.item = InvokeItem{InUtxo: node.deposit.Source.Output.OutPointStr, OutTxId: "settlement"}
	result, err := mgr.GetContractLogResult(deposits["100"].ID)
	require.NoError(t, err)
	require.Equal(t, ContractLogResult{Status: "ready", TxID: "settlement", Chain: "satnet"}, result)
}

func seedRGB11Name(t *testing.T, mgr *Manager, asset indexer.AssetName, ticker string) indexer.AssetName {
	t.Helper()
	info, err := loadTickerInfo(mgr.db, &asset)
	require.NoError(t, err)
	return seedRGB11RegistryName(t, mgr, asset, ticker, info.ContractID)
}

func seedRGB11RegistryName(t *testing.T, mgr *Manager, asset indexer.AssetName, ticker, id string) indexer.AssetName {
	t.Helper()
	short, err := dkvs.RGB11NameTicker(ticker, id)
	require.NoError(t, err)
	require.Equal(t, short, asset.Ticker)
	key, err := dkvs.RGB11NameKey(asset.Ticker)
	require.NoError(t, err)
	value, err := dkvs.RGB11NameValue(id)
	require.NoError(t, err)
	signer := NewInternalWalletWithMnemonic(testCoreMnemonic, "", &chaincfg.TestNet4Params)
	record, err := NewDKVSSignedRecord(signer, key, value, dkvs.RecordOptions{Seq: dkvs.RGB11NameProofSeq, IssueHeight: 1})
	require.NoError(t, err)
	remote := mgr.http.(*rgb11MemoryDKVSHTTP)
	remote.mu.Lock()
	remote.records[key] = record
	remote.mu.Unlock()
	return asset
}

type errOperationLogTestFailure struct{}

func (errOperationLogTestFailure) Error() string { return "contract invoke failed" }
