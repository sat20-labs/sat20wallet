package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/btcec"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

func TestRecycleRewardItemRemovedAfterSuccessfulSend(t *testing.T) {
	const (
		address = "reward-address"
		itemID  = int64(17)
		txID    = "reward-tx"
	)

	item := &InvokeItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{
			Id:   itemID,
			Done: ITEM_STATUS_INIT,
		},
		Address:        address,
		RemainingValue: 1000,
	}
	runtime := &RecycleContractRunTime{
		rewardMap: map[string]map[int64]*InvokeItem{
			address: {itemID: item},
		},
	}

	selected := runtime.findRewardItemByID(itemID)
	if selected != item {
		t.Fatal("reward lookup did not return the in-memory item")
	}
	markRewardItemDealtState(selected, txID)
	runtime.pruneFinishedRewardItems()

	if !item.Finished() || item.Done != ITEM_STATUS_DEALT {
		t.Fatalf("reward item was not marked dealt: %+v", item)
	}
	if item.OutTxId != txID || !item.ToL1 || item.RemainingValue != 0 {
		t.Fatalf("reward item completion state is incomplete: %+v", item)
	}
	if len(runtime.rewardMap) != 0 {
		t.Fatalf("finished reward item remains queued: %+v", runtime.rewardMap)
	}
}

// This temporary test exercises the actual sendTx -> height-aware batch builder -> UTXO selector path.
// Only the shared signature/broadcast transport is replaced by rewardCutoffTransport.
type rewardCutoffIndexer struct {
	IndexerRPCClient
	outputs     []*indexerwire.TxOutputInfo
	ordxOutputs []*indexerwire.TxOutputInfo
	stubTxID    string
	stubTxHex   string
}

func (c *rewardCutoffIndexer) GetUtxoListWithTicker(_ string, asset *indexer.AssetName) []*indexerwire.TxOutputInfo {
	outputs := c.outputs
	if asset != nil && asset.Protocol == indexer.PROTOCOL_NAME_ORDX {
		outputs = c.ordxOutputs
	}
	result := make([]*indexerwire.TxOutputInfo, 0, len(outputs))
	for _, output := range outputs {
		copy := *output
		copy.PkScript = append([]byte(nil), output.PkScript...)
		result = append(result, &copy)
	}
	return result
}

func (c *rewardCutoffIndexer) GetRawTx(txid string) (string, error) {
	if txid != c.stubTxID {
		return "", fmt.Errorf("unexpected stub transaction %s", txid)
	}
	return c.stubTxHex, nil
}

type rewardCutoffTransport struct {
	*rollbackContractManager
	sourceAddress      string
	builderCalls       int
	cutoffHeight       int
	invoiceURL         string
	invoiceReason      string
	invoiceHeight      int
	invoiceItemIDs     []int64
	invoiceErr         bool
	selectedInputCount int
	selectedOutpoints  []string
	ordxBindingN       int
	stubBuilderCalls   int
	stubCutoffHeight   int
	stubInputCount     int
	stubOutpoints      []string
}

func (m *rewardCutoffTransport) CoBatchSendV3_Height(localWallet common.Wallet, dest []*SendAssetInfo, assetNameStr string, feeRate int64,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock, payFeeByCurrentAddress bool, maxConfirmedInputHeight int) (string, int64, error) {
	m.builderCalls++
	m.cutoffHeight = maxConfirmedInputHeight
	if ctype, data, err := sindexer.ReadDataFromNullDataScript(memo); err != nil || ctype != sindexer.CONTENT_TYPE_INVOKERESULT {
		m.invoiceErr = true
	} else if url, invoiceReason, more, err := ParseContractResultInvoice(data); err != nil {
		m.invoiceErr = true
	} else if parsed, err := ParseInvokeResultMore(more); err != nil {
		m.invoiceErr = true
	} else {
		m.invoiceURL = url
		m.invoiceReason = invoiceReason
		m.invoiceHeight = parsed.Height
		m.invoiceItemIDs = append([]int64(nil), parsed.ItemIDs...)
	}

	tx, _, fee, err := m.Manager.buildBatchSendTxWithAddressHeight_btc(
		m.sourceAddress, dest, feeRate, memo, true, excludeRecentBlock, maxConfirmedInputHeight)
	if tx != nil {
		m.selectedInputCount = len(tx.TxIn)
		m.selectedOutpoints = m.selectedOutpoints[:0]
		for _, input := range tx.TxIn {
			m.selectedOutpoints = append(m.selectedOutpoints, input.PreviousOutPoint.String())
		}
	}
	if err != nil {
		return "", 0, err
	}
	// The builder and selector are real. This deterministic unit boundary replaces only remote signing/broadcast.
	return "temporary-reward-send", fee, nil
}

func (m *rewardCutoffTransport) CoSendOrdxWithStub(localWallet common.Wallet, dest string, assetNameStr string, amt int64, feeRate int64, stub string,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool) (string, int64, error) {
	return m.coSendOrdxWithStubAtHeight(localWallet, dest, assetNameStr, amt, feeRate, stub, reason, contractURL,
		invokeCount, memo, static, runtime, sendDeAnchorTx, excludeRecentBlock, 0)
}

func (m *rewardCutoffTransport) CoSendOrdxWithStub_Height(localWallet common.Wallet, dest string, assetNameStr string, amt int64, feeRate int64, stub string,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool, maxConfirmedInputHeight int) (string, int64, error) {
	return m.coSendOrdxWithStubAtHeight(localWallet, dest, assetNameStr, amt, feeRate, stub, reason, contractURL,
		invokeCount, memo, static, runtime, sendDeAnchorTx, excludeRecentBlock, maxConfirmedInputHeight)
}

func (m *rewardCutoffTransport) coSendOrdxWithStubAtHeight(localWallet common.Wallet, dest string, assetNameStr string, amt int64, feeRate int64, stub string,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool, maxConfirmedInputHeight int) (string, int64, error) {
	m.stubBuilderCalls++
	m.stubCutoffHeight = maxConfirmedInputHeight
	if ctype, data, err := sindexer.ReadDataFromNullDataScript(memo); err != nil || ctype != sindexer.CONTENT_TYPE_INVOKERESULT {
		m.invoiceErr = true
	} else if url, invoiceReason, more, err := ParseContractResultInvoice(data); err != nil {
		m.invoiceErr = true
	} else if parsed, err := ParseInvokeResultMore(more); err != nil {
		m.invoiceErr = true
	} else {
		m.invoiceURL = url
		m.invoiceReason = invoiceReason
		m.invoiceHeight = parsed.Height
		m.invoiceItemIDs = append([]int64(nil), parsed.ItemIDs...)
	}
	parsedName := ParseAssetString(assetNameStr)
	if parsedName == nil {
		return "", 0, fmt.Errorf("invalid ORDX asset name")
	}
	asset := &AssetName{AssetName: *parsedName, N: m.ordxBindingN}
	tx, _, fee, err := m.Manager.buildSendOrdxTxWithStubFromAddressWithHeight(
		localWallet, m.sourceAddress, dest, asset, amt, stub, feeRate, memo, false, excludeRecentBlock, maxConfirmedInputHeight)
	if tx != nil {
		m.stubInputCount = len(tx.TxIn)
		m.stubOutpoints = m.stubOutpoints[:0]
		for _, input := range tx.TxIn {
			m.stubOutpoints = append(m.stubOutpoints, input.PreviousOutPoint.String())
		}
	}
	if err != nil {
		return "", 0, err
	}
	return "temporary-ordx-reward-send", fee, nil
}

type rewardCutoffFixture struct {
	runtime          *RecycleContractRunTime
	manager          *Manager
	sourceAddress    string
	recipientAddress string
	height           int
	itemID           int64
	assetName        string
	indexer          *rewardCutoffIndexer
	fundingOutpoint  string
}

func newRewardCutoffFixture(t *testing.T, fundingHeight int) *rewardCutoffFixture {
	t.Helper()
	localPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal("create temporary local public key")
	}
	remotePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal("create temporary remote public key")
	}
	sourcePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal("create temporary source public key")
	}
	recipientPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal("create temporary recipient public key")
	}

	sourceAddress := PublicKeyToP2TRAddress(sourcePriv.PubKey())
	recipientAddress := PublicKeyToP2TRAddress(recipientPriv.PubKey())
	script, err := GetPkScriptFromAddress(sourceAddress)
	if err != nil {
		t.Fatal("create temporary source script")
	}
	point := fmt.Sprintf("%s:%d", chainhash.DoubleHashH([]byte("reward-cutoff-funding")).String(), 0)
	utxo := &indexerwire.TxOutputInfo{
		OutPoint: point,
		UtxoId:   indexer.ToUtxoId(fundingHeight, 0, 0),
		Value:    2_000_000,
		PkScript: append([]byte(nil), script...),
	}
	rpc := &rewardCutoffIndexer{outputs: []*indexerwire.TxOutputInfo{utxo}}
	db := indexerdb.NewKVDB(t.TempDir())
	manager := &Manager{db: db}
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l1IndexerClient.Set(rpc)
	manager.utxoLockerL1 = NewUtxoLocker(db, rpc, L1_NETWORK_BITCOIN)
	stp := &rewardCutoffTransport{
		rollbackContractManager: &rollbackContractManager{Manager: manager},
		sourceAddress:           sourceAddress,
	}
	runtime := NewRecycleContractRunTime(stp)
	runtime.ChannelAddr = sourceAddress
	runtime.AssetName = *indexer.NewAssetNameFromString(indexer.ASSET_PLAIN_SAT.String())
	runtime.localPubKey = localPriv.PubKey()
	runtime.remotePubKey = remotePriv.PubKey()
	runtime.CurrBlockL1 = 550
	itemID := int64(17)
	zeroAmount, err := indexer.NewDecimalFromString("0", MAX_ASSET_DIVISIBILITY)
	if err != nil {
		t.Fatal("create zero reward amount")
	}
	item := &InvokeItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Id: itemID, Done: ITEM_STATUS_INIT},
		UtxoId:                indexer.ToUtxoId(500, 0, 0),
		Address:               recipientAddress,
		OutAmt:                zeroAmount,
		OutValue:              300_000,
	}
	runtime.rewardMap = map[string]map[int64]*InvokeItem{recipientAddress: {itemID: item}}
	return &rewardCutoffFixture{
		runtime: runtime, manager: manager, sourceAddress: sourceAddress,
		recipientAddress: recipientAddress, height: 500, itemID: itemID,
		assetName: indexer.ASSET_PLAIN_SAT.String(), indexer: rpc, fundingOutpoint: point,
	}
}

func rewardDestinations(info *DealInfo) []*SendAssetInfo {
	destinations := make([]*SendAssetInfo, 0, len(info.SendInfo))
	for _, destination := range info.SendInfo {
		destinations = append(destinations, destination)
	}
	return destinations
}

func configureRewardCutoffOrdxStub(t *testing.T, fixture *rewardCutoffFixture, winnerHeight, processedTip int) (string, string) {
	t.Helper()
	const bindingN = 500
	fixture.runtime.AssetName = indexer.AssetName{
		Protocol: indexer.PROTOCOL_NAME_ORDX,
		Type:     indexer.ASSET_TYPE_FT,
		Ticker:   "stubcutofffixture",
	}
	fixture.runtime.N = bindingN
	fixture.runtime.stp.(*rewardCutoffTransport).ordxBindingN = bindingN
	fixture.runtime.rewardMap[fixture.recipientAddress][fixture.itemID].OutAmt = indexer.NewDecimal(5000, MAX_ASSET_DIVISIBILITY)

	assetScript, err := GetPkScriptFromAddress(fixture.sourceAddress)
	if err != nil {
		t.Fatal("create ORDX funding script")
	}
	assetOutpoint := fmt.Sprintf("%s:%d", chainhash.DoubleHashH([]byte("reward-cutoff-ordx-funding")).String(), 0)
	ordxName := fixture.runtime.AssetName
	fixture.indexer.ordxOutputs = []*indexerwire.TxOutputInfo{{
		OutPoint: assetOutpoint,
		UtxoId:   indexer.ToUtxoId(winnerHeight+1, 1, 0),
		Value:    1000,
		PkScript: append([]byte(nil), assetScript...),
		Assets: []*indexer.DisplayAsset{{
			AssetName:  ordxName,
			Amount:     "5000",
			Precision:  0,
			BindingSat: bindingN,
			Offsets:    []*indexer.OffsetRange{{Start: 0, End: 10}},
		}},
	}}

	stubTx := btcwire.NewMsgTx(btcwire.TxVersion)
	var stubPrevHash chainhash.Hash
	stubPrevHash[0] = 0x6f
	stubTx.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{Hash: stubPrevHash, Index: 7}, nil, nil))
	stubTx.AddTxOut(&btcwire.TxOut{Value: 330, PkScript: append([]byte(nil), assetScript...)})
	var stubBytes bytes.Buffer
	if err := stubTx.Serialize(&stubBytes); err != nil {
		t.Fatalf("serialize synthetic stub transaction: %v", err)
	}
	fixture.indexer.stubTxID = stubTx.TxHash().String()
	fixture.indexer.stubTxHex = hex.EncodeToString(stubBytes.Bytes())
	stubOutpoint := fmt.Sprintf("%s:%d", fixture.indexer.stubTxID, 0)
	fixture.indexer.outputs = append(fixture.indexer.outputs, &indexerwire.TxOutputInfo{
		OutPoint: stubOutpoint,
		UtxoId:   indexer.ToUtxoId(processedTip, 2, 0),
		Value:    330,
		PkScript: append([]byte(nil), assetScript...),
	})
	return assetOutpoint, stubOutpoint
}

func TestRecycleRewardHeightCutoff(t *testing.T) {
	const processedTip = 550

	t.Run("control_funding_at_processed_tip_is_selected", func(t *testing.T) {
		fixture := newRewardCutoffFixture(t, processedTip)
		info := fixture.runtime.genRewardInfo(processedTip)
		if info.Height != fixture.height || len(info.ItemIDs) != 1 || info.ItemIDs[0] != fixture.itemID {
			t.Fatalf("fixture deal fields mismatch: height=%d itemCount=%d", info.Height, len(info.ItemIDs))
		}
		tx, _, _, err := fixture.manager.buildBatchSendTxWithAddressHeight_btc(
			fixture.sourceAddress, rewardDestinations(info), info.FeeRate, nil, true, false, processedTip)
		if err != nil {
			t.Fatalf("eligible tip funding did not build: %T", err)
		}
		if tx == nil {
			t.Fatal("eligible tip funding returned a nil transaction")
		}
		if len(tx.TxIn) != 1 || tx.TxIn[0].PreviousOutPoint.String() != fixture.fundingOutpoint {
			t.Fatalf("eligible tip funding input mismatch: count=%d", len(tx.TxIn))
		}
	})

	t.Run("control_funding_after_processed_tip_is_excluded", func(t *testing.T) {
		fixture := newRewardCutoffFixture(t, processedTip+1)
		info := fixture.runtime.genRewardInfo(processedTip)
		_, _, _, err := fixture.manager.buildBatchSendTxWithAddressHeight_btc(
			fixture.sourceAddress, rewardDestinations(info), info.FeeRate, nil, true, false, processedTip)
		if err == nil {
			t.Error("selector accepted funding above the cutoff")
		}
	})

	t.Run("actual_reward_send_uses_processed_tip_funding", func(t *testing.T) {
		fixture := newRewardCutoffFixture(t, processedTip)
		transport := fixture.runtime.stp.(*rewardCutoffTransport)
		info := fixture.runtime.genRewardInfo(processedTip)
		if info.Height != fixture.height {
			t.Fatalf("winner height changed: got=%d", info.Height)
		}
		if fixture.runtime.CurrBlockL1 != processedTip {
			t.Fatalf("processed tip changed: got=%d", fixture.runtime.CurrBlockL1)
		}
		txID, _, _, err := fixture.runtime.sendTx(info, INVOKE_RESULT_REWARD, false, false, processedTip)
		if transport.builderCalls == 0 {
			t.Fatal("actual sendTx did not reach height-aware builder")
		}
		if transport.cutoffHeight != processedTip {
			t.Errorf("actual selector cutoff=%d, expected processed tip=%d", transport.cutoffHeight, processedTip)
		}
		if transport.invoiceErr {
			t.Error("sendTx invoice could not be decoded")
		}
		if transport.invoiceURL != fixture.runtime.RelativePath() || transport.invoiceReason != INVOKE_RESULT_REWARD ||
			transport.invoiceHeight != fixture.height || len(transport.invoiceItemIDs) != 1 || transport.invoiceItemIDs[0] != fixture.itemID {
			t.Errorf("sendTx changed reward invoice fields: reason=%s height=%d itemCount=%d", transport.invoiceReason, transport.invoiceHeight, len(transport.invoiceItemIDs))
		}
		if err != nil {
			t.Errorf("processed-tip funding did not build: builderCalls=%d selectedInputs=%d", transport.builderCalls, transport.selectedInputCount)
			return
		}
		if txID == "" {
			t.Error("successful actual reward send returned an empty transaction id")
		}
		if transport.selectedInputCount != 1 || len(transport.selectedOutpoints) != 1 || transport.selectedOutpoints[0] != fixture.fundingOutpoint {
			t.Errorf("successful actual reward send did not use the sole eligible funding input: count=%d", transport.selectedInputCount)
		}
	})

	t.Run("actual_reward_send_rejects_future_funding", func(t *testing.T) {
		fixture := newRewardCutoffFixture(t, processedTip+1)
		transport := fixture.runtime.stp.(*rewardCutoffTransport)
		info := fixture.runtime.genRewardInfo(processedTip)
		if info.Height != fixture.height {
			t.Fatalf("winner height changed: got=%d", info.Height)
		}
		txID, _, _, err := fixture.runtime.sendTx(info, INVOKE_RESULT_REWARD, false, false, processedTip)
		if transport.builderCalls == 0 {
			t.Fatal("actual sendTx did not reach height-aware builder")
		}
		if transport.invoiceErr || transport.invoiceHeight != fixture.height || len(transport.invoiceItemIDs) != 1 || transport.invoiceItemIDs[0] != fixture.itemID {
			t.Errorf("future-funding send changed winner invoice: height=%d itemCount=%d", transport.invoiceHeight, len(transport.invoiceItemIDs))
		}
		if err == nil || txID != "" || transport.selectedInputCount != 0 {
			t.Errorf("actual send accepted future funding: txidPresent=%t selectedInputs=%d", txID != "", transport.selectedInputCount)
		}
	})

	t.Run("actual_ordx_stub_send_uses_processed_tip_funding", func(t *testing.T) {
		const winnerHeight = 500
		fixture := newRewardCutoffFixture(t, winnerHeight+1)
		transport := fixture.runtime.stp.(*rewardCutoffTransport)
		assetOutpoint, stubOutpoint := configureRewardCutoffOrdxStub(t, fixture, winnerHeight, processedTip)

		info := fixture.runtime.genRewardInfo(processedTip)
		if info.Height != winnerHeight || len(info.ItemIDs) != 1 || info.ItemIDs[0] != fixture.itemID {
			t.Fatalf("winner deal fields changed: height=%d itemCount=%d", info.Height, len(info.ItemIDs))
		}
		if fixture.runtime.CurrBlockL1 != processedTip {
			t.Fatalf("processed tip changed: got=%d", fixture.runtime.CurrBlockL1)
		}
		if len(info.SendInfo) != 1 {
			t.Fatalf("expected one ORDX reward output, got %d", len(info.SendInfo))
		}
		var target *SendAssetInfo
		for _, sendInfo := range info.SendInfo {
			target = sendInfo
		}
		if target == nil || target.AssetAmt == nil || target.AssetAmt.IsZero() || target.AssetAmt.Int64()%int64(transport.ordxBindingN) != 0 {
			t.Fatal("fixture did not produce a valid ORDX reward output")
		}

		more, err := EncodeInvokeResultMore(info.Height, info.ItemIDs)
		if err != nil {
			t.Fatal("encode reward invoice fields")
		}
		invoice, err := UnsignedContractResultInvoice(fixture.runtime.RelativePath(), INVOKE_RESULT_REWARD, more)
		if err != nil {
			t.Fatal("create reward invoice")
		}
		memo, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKERESULT, invoice)
		if err != nil {
			t.Fatal("create reward invoice script")
		}
		parsedName := ParseAssetString(target.AssetName.String())
		if parsedName == nil {
			t.Fatal("parse ORDX reward asset")
		}
		asset := &AssetName{AssetName: *parsedName, N: transport.ordxBindingN}
		controlTx, _, _, err := fixture.manager.buildSendOrdxTxWithStubFromAddressWithHeight(
			nil, fixture.sourceAddress, target.Address, asset, target.AssetAmt.Int64(), stubOutpoint,
			info.FeeRate, memo, false, false, processedTip)
		if err != nil {
			t.Fatalf("processed-tip ORDX builder control failed: %v", err)
		}
		if controlTx == nil {
			t.Fatal("processed-tip ORDX builder returned no transaction")
		}
		controlSelectedAsset := false
		for _, input := range controlTx.TxIn {
			if input.PreviousOutPoint.String() == assetOutpoint {
				controlSelectedAsset = true
				break
			}
		}
		if !controlSelectedAsset {
			t.Fatal("processed-tip ORDX builder did not select the post-winner asset input")
		}

		txID, _, _, err := fixture.runtime.sendTx(info, INVOKE_RESULT_REWARD, false, false, processedTip)
		if transport.stubBuilderCalls == 0 {
			t.Fatal("actual sendTx did not reach the ORDX stub builder")
		}
		if transport.stubCutoffHeight != processedTip {
			t.Errorf("actual ORDX selector cutoff=%d, expected processed tip=%d", transport.stubCutoffHeight, processedTip)
		}
		if err != nil {
			t.Errorf("processed-tip ORDX funding did not build: %v", err)
			return
		}
		if txID == "" {
			t.Error("successful ORDX reward send returned an empty transaction id")
		}
		selectedAssetInput := false
		for _, outpoint := range transport.stubOutpoints {
			if outpoint == assetOutpoint {
				selectedAssetInput = true
				break
			}
		}
		if transport.stubInputCount == 0 || !selectedAssetInput {
			t.Errorf("successful ORDX reward send did not use the post-winner asset input: count=%d", transport.stubInputCount)
		}
		if transport.invoiceErr || transport.invoiceURL != fixture.runtime.RelativePath() || transport.invoiceReason != INVOKE_RESULT_REWARD ||
			transport.invoiceHeight != winnerHeight || len(transport.invoiceItemIDs) != 1 || transport.invoiceItemIDs[0] != fixture.itemID {
			t.Errorf("ORDX reward send changed winner invoice fields: reason=%s height=%d itemCount=%d", transport.invoiceReason, transport.invoiceHeight, len(transport.invoiceItemIDs))
		}
	})

	t.Run("zero_funding_height_keeps_unbounded_ORDX_selection", func(t *testing.T) {
		const winnerHeight = 500
		fixture := newRewardCutoffFixture(t, winnerHeight+1)
		transport := fixture.runtime.stp.(*rewardCutoffTransport)
		assetOutpoint, _ := configureRewardCutoffOrdxStub(t, fixture, winnerHeight, processedTip)
		info := fixture.runtime.genRewardInfo(processedTip)

		txID, _, _, err := fixture.runtime.sendTx(info, INVOKE_RESULT_WITHDRAW, false, false, 0)
		if transport.stubBuilderCalls == 0 {
			t.Fatal("zero-height sendTx did not reach the ordinary ORDX stub builder")
		}
		if transport.stubCutoffHeight != 0 {
			t.Errorf("zero-height sendTx unexpectedly imposed cutoff %d", transport.stubCutoffHeight)
		}
		if err != nil {
			t.Fatalf("zero-height sendTx rejected funding above the winner height: %v", err)
		}
		if txID == "" {
			t.Error("successful zero-height send returned an empty transaction id")
		}
		selectedAssetInput := false
		for _, outpoint := range transport.stubOutpoints {
			if outpoint == assetOutpoint {
				selectedAssetInput = true
				break
			}
		}
		if transport.stubInputCount == 0 || !selectedAssetInput {
			t.Errorf("zero-height send did not select the input above winner height: count=%d", transport.stubInputCount)
		}
		if transport.invoiceErr || transport.invoiceReason != INVOKE_RESULT_WITHDRAW || transport.invoiceHeight != winnerHeight {
			t.Errorf("zero-height send changed invoice fields: reason=%s height=%d", transport.invoiceReason, transport.invoiceHeight)
		}
	})
}
