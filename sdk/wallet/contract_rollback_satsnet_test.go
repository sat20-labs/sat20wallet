package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	btcwire "github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/btcjson"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type rollbackContractManager struct {
	*Manager
	l1    IndexerRPCClient
	l2    IndexerRPCClient
	saves int
}

func (m *rollbackContractManager) SaveReservation(ContractDeployResvIF) error {
	m.saves++
	return nil
}

func (m *rollbackContractManager) SaveReservationWithLock(ContractDeployResvIF) error {
	return nil
}

func (m *rollbackContractManager) GetIndexerClient() IndexerRPCClient {
	if m.l1 != nil {
		return m.l1
	}
	return m.Manager.GetIndexerClient()
}

func (m *rollbackContractManager) GetIndexerClient_SatsNet() IndexerRPCClient {
	if m.l2 != nil {
		return m.l2
	}
	return m.Manager.GetIndexerClient_SatsNet()
}

type rollbackTxHeightClient struct {
	*TestIndexerClient
	height int
	err    error
	calls  int
	block  *swire.MsgBlock
}

func TestInvokeCompletedKeepsReorgPlaceholdersUntilReplay(t *testing.T) {
	l2Item := &InvokeItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Id: 1, Done: ITEM_STATUS_DEALT},
		InUtxo:                "reorg-l2:0",
		UtxoId:                REORG_UTXOID,
	}
	l1Item := &InvokeItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Id: 2, Done: ITEM_STATUS_DEALT},
		InUtxo:                "reorg-l1:0",
		UtxoId:                REORG_UTXOID,
		FromL1:                true,
	}
	base := &ContractRuntimeBase{
		CurrBlock:       100,
		CurrBlockL1:     100,
		InvokeCount:     2,
		lastInvokeCount: 2,
		history: map[string]*InvokeItem{
			l2Item.InUtxo: l2Item,
			l1Item.InUtxo: l1Item,
		},
	}

	base.invokeCompleted()
	if len(base.history) != 2 {
		t.Fatalf("reorg placeholders were cleaned before replay: history=%d", len(base.history))
	}
}

func (c *rollbackTxHeightClient) GetTxHeight(string) (int, error) {
	c.calls++
	return c.height, c.err
}

func (c *rollbackTxHeightClient) GetBlockHash(height int) (string, error) {
	if c.block == nil || height != c.height {
		return "", fmt.Errorf("block %d not found", height)
	}
	return "canonical-block", nil
}

func (c *rollbackTxHeightClient) GetBlock(blockHash string) (string, error) {
	if c.block == nil || blockHash != "canonical-block" {
		return "", fmt.Errorf("block %s not found", blockHash)
	}
	var buf bytes.Buffer
	if err := c.block.Serialize(&buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf.Bytes()), nil
}

type rollbackContractReservation struct {
	mutex      sync.RWMutex
	contract   ContractRuntime
	status     ResvStatus
	initiator  bool
	channel    string
	localKey   []byte
	remoteKey  []byte
	coreKey    []byte
	feeUtxos   []string
	feeInputs  []*TxOutput_SatsNet
	deployTx   *swire.MsgTx
	deployTxID string
}

func (r *rollbackContractReservation) GetId() int64                      { return 1 }
func (r *rollbackContractReservation) GetType() string                   { return "contract" }
func (r *rollbackContractReservation) GetStatus() ResvStatus             { return r.status }
func (r *rollbackContractReservation) SetStatus(status ResvStatus)       { r.status = status }
func (r *rollbackContractReservation) GetResult() []byte                 { return nil }
func (r *rollbackContractReservation) GetContract() ContractRuntime      { return r.contract }
func (r *rollbackContractReservation) GetMutex() *sync.RWMutex           { return &r.mutex }
func (r *rollbackContractReservation) LocalIsInitiator() bool            { return r.initiator }
func (r *rollbackContractReservation) SetInitiator(value bool)           { r.initiator = value }
func (r *rollbackContractReservation) GetChannelAddr() string            { return r.channel }
func (r *rollbackContractReservation) GetDeployer() string               { return "test-deployer" }
func (r *rollbackContractReservation) GetLocalPubKey() []byte            { return r.localKey }
func (r *rollbackContractReservation) GetRemotePubKey() []byte           { return r.remoteKey }
func (r *rollbackContractReservation) GetCoreNodePubKey() []byte         { return r.coreKey }
func (r *rollbackContractReservation) GetFeeRate() int64                 { return 0 }
func (r *rollbackContractReservation) GetFeeUtxos() []string             { return r.feeUtxos }
func (r *rollbackContractReservation) SetFeeUtxos(value []string)        { r.feeUtxos = value }
func (r *rollbackContractReservation) SetRequiredFee(int64)              {}
func (r *rollbackContractReservation) SetServiceFee(int64)               {}
func (r *rollbackContractReservation) GetDeployContractTx() *swire.MsgTx { return r.deployTx }
func (r *rollbackContractReservation) SetDeployContractTx(tx *swire.MsgTx) {
	r.deployTx = tx
}
func (r *rollbackContractReservation) GetDeployContractTxId() string { return r.deployTxID }
func (r *rollbackContractReservation) SetDeployContractTxId(id string) {
	r.deployTxID = id
}
func (r *rollbackContractReservation) SetHasSentDeployTx(int) {}
func (r *rollbackContractReservation) ResyncBlock(int, int)   {}
func (r *rollbackContractReservation) ResyncBlock_SatsNet(int, int) {
}
func (r *rollbackContractReservation) SignedDeployContractInvoice() ([]byte, error) {
	return nil, nil
}
func (r *rollbackContractReservation) SetFeeInputs(value []*TxOutput_SatsNet) {
	r.feeInputs = value
}

func TestContractInitFromDBPreservesResponderRole(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	defer database.Close()

	localWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	remoteWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		"", &chaincfg.TestNet4Params,
	)
	manager := &rollbackContractManager{Manager: &Manager{
		db: database, wallet: localWallet, cfg: &common.Config{Mode: "test"},
	}}
	runtime := NewSwapContractRuntime(manager)
	base := runtime.GetRuntimeBase()
	base.LocalPubKey = localWallet.GetPubKey().SerializeCompressed()
	base.RemotePubKey = remoteWallet.GetPubKey().SerializeCompressed()
	base.CoreNodePubKey = append([]byte(nil), base.RemotePubKey...)
	reservation := &rollbackContractReservation{
		contract: runtime, initiator: false, localKey: base.LocalPubKey,
		remoteKey: base.RemotePubKey, coreKey: base.CoreNodePubKey,
	}

	if err := runtime.InitFromDB(manager, reservation); err != nil {
		t.Fatal(err)
	}
	if reservation.LocalIsInitiator() || base.IsInitiator() {
		t.Fatal("responder was promoted to initiator during reload")
	}
	if manager.saves != 0 {
		t.Fatalf("InitFromDB persisted role unexpectedly: saves=%d", manager.saves)
	}
}

func TestContractRollbackSatsNetPreservesL1Height(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	defer database.Close()

	localWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	remoteWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		"", &chaincfg.TestNet4Params,
	)
	manager := &rollbackContractManager{Manager: &Manager{
		db:     database,
		wallet: localWallet,
		cfg:    &common.Config{Mode: "test"},
	}}
	runtime := NewSwapContractRuntime(manager)
	localKey := localWallet.GetPubKey().SerializeCompressed()
	remoteKey := remoteWallet.GetPubKey().SerializeCompressed()
	reservation := &rollbackContractReservation{
		contract:  runtime,
		status:    RS_DEPLOY_CONTRACT_RUNNING,
		initiator: true,
		channel:   "testchannel",
		localKey:  localKey,
		remoteKey: remoteKey,
		coreKey:   localKey,
	}

	base := runtime.GetRuntimeBase()
	base.ChannelAddr = reservation.channel
	base.LocalPubKey = localKey
	base.RemotePubKey = remoteKey
	base.CoreNodePubKey = localKey
	base.CurrBlock = 32
	base.CurrBlockL1 = 18
	base.Status = CONTRACT_STATUS_READY
	base.resv = reservation

	result, err := runtime.RollbackToHeight_SatsNet(8)
	if err != nil {
		t.Fatal(err)
	}
	if result.PreviousHeight != 32 || result.TargetHeight != 8 {
		t.Fatalf("unexpected rollback result: %+v", result)
	}
	if base.CurrBlock != 8 {
		t.Fatalf("L2 height after rollback = %d, want 8", base.CurrBlock)
	}
	if base.CurrBlockL1 != 18 {
		t.Fatalf("L1 height changed during L2 rollback: got %d, want 18", base.CurrBlockL1)
	}
}

func TestDeterministicRollbackTemplateSelection(t *testing.T) {
	for _, templateName := range []string{TEMPLATE_CONTRACT_SWAP, TEMPLATE_CONTRACT_LIMITORDER} {
		if !supportsDeterministicRollbackTemplate(templateName) {
			t.Fatalf("%s should use the deterministic order rollback", templateName)
		}
	}
	for _, templateName := range []string{
		TEMPLATE_CONTRACT_LAUNCHPOOL,
		TEMPLATE_CONTRACT_AMM,
		TEMPLATE_CONTRACT_TRANSCEND,
		TEMPLATE_CONTRACT_RECYCLE,
		TEMPLATE_CONTRACT_DAO,
		TEMPLATE_CONTRACT_FAUCET,
	} {
		if supportsDeterministicRollbackTemplate(templateName) {
			t.Fatalf("%s should use best-effort rollback", templateName)
		}
	}
}

func TestSwapRollbackRebuildsRetainedOrderState(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	defer database.Close()

	localWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	manager := &rollbackContractManager{Manager: &Manager{
		db:     database,
		wallet: localWallet,
		cfg:    &common.Config{Mode: "test"},
	}}
	runtime := NewSwapContractRuntime(manager)
	localKey := localWallet.GetPubKey().SerializeCompressed()
	reservation := &rollbackContractReservation{
		contract:  runtime,
		status:    RS_DEPLOY_CONTRACT_RUNNING,
		initiator: true,
		channel:   "testchannel",
		localKey:  localKey,
		remoteKey: localKey,
		coreKey:   localKey,
	}

	base := runtime.GetRuntimeBase()
	base.ChannelAddr = reservation.channel
	base.LocalPubKey = localKey
	base.RemotePubKey = localKey
	base.CoreNodePubKey = localKey
	base.CurrBlock = 10
	base.CurrBlockL1 = 18
	base.InvokeCount = 22
	base.Status = CONTRACT_STATUS_READY
	base.resv = reservation
	runtime.Contract.GetContractBase().AssetName = swire.AssetName{
		Protocol: indexer.PROTOCOL_NAME_BRC20,
		Type:     indexer.ASSET_TYPE_FT,
		Ticker:   "ordi",
	}

	newOrder := func(id int64, height, orderType int, address string) *SwapHistoryItem {
		item := &SwapHistoryItem{
			InvokeHistoryItemBase: InvokeHistoryItemBase{Id: id},
			OrderType:             orderType,
			UtxoId:                indexer.ToUtxoId(height, 0, 0),
			UnitPrice:             indexer.NewDecimal(10, MAX_PRICE_DIVISIBILITY),
			ExpectedAmt:           indexer.NewDecimal(10, runtime.Divisibility),
			Address:               address,
			InUtxo:                fmt.Sprintf("rollback-item-%d:0", id),
			InValue:               SWAP_INVOKE_FEE,
			InAmt:                 indexer.NewDecimal(0, runtime.Divisibility),
			RemainingAmt:          indexer.NewDecimal(0, runtime.Divisibility),
			OutAmt:                indexer.NewDecimal(0, runtime.Divisibility),
		}
		if orderType == ORDERTYPE_SELL {
			item.InAmt = indexer.NewDecimal(10, runtime.Divisibility)
			item.RemainingAmt = item.InAmt.Clone()
		} else {
			item.ServiceFee = SWAP_INVOKE_FEE
			item.InValue = 110
			item.RemainingValue = 100
		}
		return item
	}

	retainedSell := newOrder(0, 5, ORDERTYPE_SELL, "seller")
	removedBuy := newOrder(1, 9, ORDERTYPE_BUY, "buyer")
	runtime.addRollbackItem(retainedSell)
	runtime.addRollbackItem(removedBuy)
	if err := runtime.matchOrders(false); err != nil {
		t.Fatal(err)
	}
	if retainedSell.RemainingAmt.Sign() != 0 {
		t.Fatalf("fixture did not consume retained sell: %s", retainedSell.RemainingAmt.String())
	}
	for _, item := range []*SwapHistoryItem{retainedSell, removedBuy} {
		if err := SaveContractInvokeHistoryItem(database, runtime.URL(), item); err != nil {
			t.Fatal(err)
		}
	}

	result, err := runtime.RollbackToHeight_SatsNet(8)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedInvokeCount != 1 || result.InvokeCountAfter != 1 {
		t.Fatalf("unexpected rollback result: %+v", result)
	}
	if runtime.AssetAmtInPool.Cmp(indexer.NewDecimal(10, runtime.Divisibility)) != 0 {
		t.Fatalf("asset pool after rollback = %s, want 10", runtime.AssetAmtInPool.String())
	}
	loaded, err := loadContractInvokeHistoryItem(database, runtime.URL(), retainedSell.GetKey())
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := loaded.(*SwapHistoryItem)
	if rebuilt.Done != ITEM_STATUS_INIT || rebuilt.Reason != INVOKE_REASON_NORMAL ||
		rebuilt.RemainingAmt.Cmp(rebuilt.InAmt) != 0 || rebuilt.OutAmt.Sign() != 0 || rebuilt.OutValue != 0 {
		t.Fatalf("retained order was not reset to its initial state: %+v", rebuilt)
	}
	if _, err := loadContractInvokeHistoryItem(database, runtime.URL(), removedBuy.GetKey()); err == nil {
		t.Fatal("removed buy still exists after rollback")
	}
}

func TestSwapRollbackResultHeightUsesOutputChainAndStrictErrors(t *testing.T) {
	l1 := &rollbackTxHeightClient{TestIndexerClient: &TestIndexerClient{}, height: 7}
	l2 := &rollbackTxHeightClient{TestIndexerClient: &TestIndexerClient{}, height: 11}
	manager := &rollbackContractManager{Manager: &Manager{cfg: &common.Config{Mode: "test"}}, l1: l1, l2: l2}
	runtime := NewSwapContractRuntime(manager)

	height, found, err := runtime.rollbackResultHeight(&SwapHistoryItem{OutTxId: "l2-result"})
	if err != nil || !found || height != 11 || l2.calls != 1 || l1.calls != 0 {
		t.Fatalf("unexpected L2 lookup: height=%d found=%v err=%v l1=%d l2=%d", height, found, err, l1.calls, l2.calls)
	}
	height, found, err = runtime.rollbackResultHeight(&SwapHistoryItem{OutTxId: "l1-result", ToL1: true})
	if err != nil || !found || height != 7 || l2.calls != 1 || l1.calls != 1 {
		t.Fatalf("unexpected L1 lookup: height=%d found=%v err=%v l1=%d l2=%d", height, found, err, l1.calls, l2.calls)
	}

	l2.err = fmt.Errorf("not found")
	height, found, err = runtime.rollbackResultHeight(&SwapHistoryItem{OutTxId: "rolled-back"})
	if err != nil || found || height != -1 {
		t.Fatalf("not-found result must be classified as rolled back: height=%d found=%v err=%v", height, found, err)
	}
	l2.err = fmt.Errorf("rpc transport unavailable")
	if _, _, err := runtime.rollbackResultHeight(&SwapHistoryItem{OutTxId: "unknown"}); err == nil {
		t.Fatal("transport failure was incorrectly treated as a rolled-back transaction")
	}
}

func TestRollbackTxNotFoundClassification(t *testing.T) {
	realMessage := "-5: No information available about transaction a41e00000000000000000000000000000000000000000000000000000000b757"
	typed := btcjson.NewRPCError(btcjson.ErrRPCNoTxInfo,
		"No information available about transaction a41e00000000000000000000000000000000000000000000000000000000b757")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "real REST error", err: fmt.Errorf("%s", realMessage), want: true},
		{name: "typed RPC error", err: typed, want: true},
		{name: "wrapped typed RPC error", err: fmt.Errorf("get transaction: %w", typed), want: true},
		{name: "other RPC minus five", err: btcjson.NewRPCError(btcjson.ErrRPCBlockNotFound, "Block not found"), want: false},
		{name: "transport route", err: fmt.Errorf("transport endpoint not found"), want: false},
		{name: "transport timeout", err: fmt.Errorf("context deadline exceeded"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := rollbackTxNotFound(test.err); got != test.want {
				t.Fatalf("rollbackTxNotFound(%q)=%v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestRollbackResolvesCanonicalReorgInvokePosition(t *testing.T) {
	tx := swire.NewMsgTx(2)
	tx.AddTxIn(swire.NewTxIn(&swire.OutPoint{}, nil, nil))
	tx.AddTxOut(swire.NewTxOut(1, nil, []byte{0x51}))
	block := swire.NewMsgBlock(&swire.BlockHeader{})
	block.AddTransaction(tx)
	client := &rollbackTxHeightClient{TestIndexerClient: &TestIndexerClient{}, height: 7, block: block}
	manager := &rollbackContractManager{Manager: &Manager{cfg: &common.Config{Mode: "test"}}, l2: client}
	runtime := NewSwapContractRuntime(manager)
	item := &SwapHistoryItem{InUtxo: tx.TxID() + ":0", UtxoId: REORG_UTXOID}

	utxoID, found, err := runtime.resolveCanonicalInvokeUtxoID_SatsNet(item)
	if err != nil || !found {
		t.Fatalf("canonical invoke was not resolved: id=%d found=%v err=%v", utxoID, found, err)
	}
	want := indexer.ToUtxoId(7, 0, 0)
	if utxoID != want {
		t.Fatalf("canonical invoke id=%d, want %d", utxoID, want)
	}

	client.err = fmt.Errorf("-5: No information available about transaction %s", tx.TxID())
	if _, found, err := runtime.resolveCanonicalInvokeUtxoID_SatsNet(item); err != nil || found {
		t.Fatalf("orphan invoke classification: found=%v err=%v", found, err)
	}
	client.err = fmt.Errorf("indexer unavailable")
	if _, _, err := runtime.resolveCanonicalInvokeUtxoID_SatsNet(item); err == nil {
		t.Fatal("uncertain canonical lookup must fail closed")
	}
}

func TestAmmRollbackDropsReorgOrphanBeforeCanonicalReplay(t *testing.T) {
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	defer database.Close()

	localWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	client := &rollbackTxHeightClient{TestIndexerClient: &TestIndexerClient{}, err: fmt.Errorf("transaction not found")}
	manager := &rollbackContractManager{Manager: &Manager{
		db: database, wallet: localWallet, cfg: &common.Config{Mode: "test"},
	}, l2: client}
	runtime := NewAmmContractRuntime(manager)
	localKey := localWallet.GetPubKey().SerializeCompressed()
	reservation := &rollbackContractReservation{
		contract: runtime, status: RS_DEPLOY_CONTRACT_RUNNING, initiator: true,
		channel: "ammrollbackchannel", localKey: localKey, remoteKey: localKey, coreKey: localKey,
	}
	base := runtime.GetRuntimeBase()
	base.ChannelAddr = reservation.channel
	base.LocalPubKey, base.RemotePubKey, base.CoreNodePubKey = localKey, localKey, localKey
	base.CurrBlock = 3458
	base.CurrBlockL1 = 100
	base.InvokeCount = 2
	base.Status = CONTRACT_STATUS_READY
	base.resv = reservation
	base.Divisibility = 0
	contract := runtime.Contract.(*AmmContract)
	contract.AssetName = swire.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "ordxyz"}
	contract.AssetAmt = "1000"
	contract.SatValue = 10
	contract.K = "10000"
	if err := runtime.setOriginalValue(); err != nil {
		t.Fatal(err)
	}

	retainedAddLiquidity := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Id: 0, Done: ITEM_STATUS_DEALT},
		OrderType:             ORDERTYPE_ADDLIQUIDITY, UtxoId: indexer.ToUtxoId(100, 0, 0), InUtxo: "retained-add-liquidity:0",
		Address: "liquidity-provider", InValue: 100, InAmt: indexer.NewDecimal(500, 0),
		RemainingAmt: indexer.NewDecimal(0, 0), OutAmt: indexer.NewDecimal(500, 0), OutValue: 100,
		UnitPrice: indexer.NewDecimal(1, 6), ExpectedAmt: indexer.NewDecimal(0, 0), OutTxId: "retained-add-result",
	}
	retainedSell := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Id: 1, Done: ITEM_STATUS_DEALT},
		OrderType:             ORDERTYPE_SELL, UtxoId: indexer.ToUtxoId(101, 0, 0), InUtxo: "retained-sell:0",
		Address: "retained-seller", InValue: 10, InAmt: indexer.NewDecimal(100, 0),
		RemainingAmt: indexer.NewDecimal(0, 0), OutAmt: indexer.NewDecimal(0, 0), OutValue: 50,
		UnitPrice: indexer.NewDecimal(1, 6), ExpectedAmt: indexer.NewDecimal(0, 0), OutTxId: "retained-sell-result",
	}
	orphan := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Id: 20, Reason: INVOKE_REASON_INVALID, Done: ITEM_STATUS_DEALT},
		OrderType:             ORDERTYPE_SELL, UtxoId: REORG_UTXOID, InUtxo: "orphan-transaction:0",
		Address: "orphan-seller", InValue: 10, InAmt: indexer.NewDecimal(200, 0),
		RemainingAmt: indexer.NewDecimal(0, 0), OutAmt: indexer.NewDecimal(0, 0), OutValue: 20,
		UnitPrice: indexer.NewDecimal(1, 6), ExpectedAmt: indexer.NewDecimal(0, 0),
	}
	canonicalCurrent := &SwapHistoryItem{
		InvokeHistoryItemBase: InvokeHistoryItemBase{Id: 21},
		OrderType:             ORDERTYPE_BUY, UtxoId: indexer.ToUtxoId(3458, 1, 0), InUtxo: "canonical-current:0",
		Address: "canonical-buyer", InValue: 20, ServiceFee: 10, InAmt: indexer.NewDecimal(0, 0),
		RemainingAmt: indexer.NewDecimal(0, 0), RemainingValue: 0, OutAmt: indexer.NewDecimal(100, 0),
		UnitPrice: indexer.NewDecimal(1, 6), ExpectedAmt: indexer.NewDecimal(0, 0),
	}
	items := []*SwapHistoryItem{retainedAddLiquidity, retainedSell}
	for id := int64(2); id < 20; id++ {
		items = append(items, &SwapHistoryItem{
			InvokeHistoryItemBase: InvokeHistoryItemBase{Id: id, Reason: INVOKE_REASON_INVALID, Done: ITEM_STATUS_CLOSED_DIRECTLY},
			OrderType:             ORDERTYPE_UNUSED, UtxoId: indexer.ToUtxoId(100+int(id), 0, 0),
			InUtxo: fmt.Sprintf("retained-filler-%d:0", id), Address: "filler",
			InAmt: indexer.NewDecimal(0, 0), RemainingAmt: indexer.NewDecimal(0, 0),
			OutAmt: indexer.NewDecimal(0, 0), UnitPrice: indexer.NewDecimal(0, 6), ExpectedAmt: indexer.NewDecimal(0, 0),
		})
	}
	items = append(items, orphan, canonicalCurrent)
	for _, item := range items {
		if err := SaveContractInvokeHistoryItem(database, runtime.URL(), item); err != nil {
			t.Fatal(err)
		}
	}
	// Retained liquidity and retained sell produce the H=3068 baseline
	// 1600/60. The orphan sell and canonical buy then produce 1700/50.
	runtime.AssetAmtInPool = indexer.NewDecimal(1700, 0)
	runtime.SatsValueInPool = 50
	runtime.TotalInputAssets = indexer.NewDecimal(800, 0)
	runtime.TotalInputSats = 150
	runtime.TotalDealSats = 70
	runtime.TotalDealAssets = canonicalCurrent.OutAmt.Clone()
	runtime.TotalDealCount = 3
	runtime.TotalLptAmt = indexer.NewDecimal(777, 0)
	runtime.BaseLptAmt = indexer.NewDecimal(333, 0)

	result, err := runtime.RollbackToHeight_SatsNet(3068)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedInvokeCount != 2 || result.InvokeCountAfter != 20 {
		t.Fatalf("unexpected rollback result: %+v", result)
	}
	if runtime.AssetAmtInPool.Cmp(indexer.NewDecimal(1600, 0)) != 0 || runtime.SatsValueInPool != 60 {
		t.Fatalf("AMM rollback retained non-canonical state: pool=%s/%d", runtime.AssetAmtInPool, runtime.SatsValueInPool)
	}
	if runtime.TotalLptAmt.Cmp(indexer.NewDecimal(777, 0)) != 0 || runtime.BaseLptAmt.Cmp(indexer.NewDecimal(333, 0)) != 0 {
		t.Fatalf("retained liquidity state was rebuilt from static values: total=%s base=%s", runtime.TotalLptAmt, runtime.BaseLptAmt)
	}
	if got := len(LoadContractInvokeHistory(database, runtime.URL(), false, false)); got != 20 {
		t.Fatalf("retained history count=%d, want 20", got)
	}

	// Replaying the canonical current order starts from the restored pool; the
	// orphan contribution must not reappear.
	replay := canonicalCurrent.Clone()
	replay.Id = 20
	replay.RemainingValue = 10
	replay.OutAmt = indexer.NewDecimal(0, 0)
	runtime.updateContractStatus(replay)
	runtime.addItem(replay)
	if !runtime.swap(indexer.NewDecimal(1600, 0), 60) {
		t.Fatal("canonical AMM order was not replayed")
	}
	if runtime.InvokeCount != 21 || runtime.SatsValueInPool != 70 || runtime.AssetAmtInPool.Cmp(indexer.NewDecimal(1600, 0)) >= 0 {
		t.Fatalf("canonical replay did not rebuild AMM state: pool=%s/%d", runtime.AssetAmtInPool, runtime.SatsValueInPool)
	}
}

// TestRecycleActivationOrdering verifies that either chain can accept inputs
// after its own activation height, without waiting for the other chain.
func TestRecycleActivationOrdering(t *testing.T) {
	t.Run("L1 input before L2 enable", func(t *testing.T) {
		runtime, manager := newRecycleActivationOrderingFixture(t)
		defer manager.GetDB().Close()

		runtime.SetEnableBlock(3, 2)
		deposit := newRecycleOrderingDeposit(runtime, 2)
		err := runtime.InvokeWithBlock(&InvokeDataInBlock{
			Height: 2, BlockHash: recycleOrderingBlockHash("independent-l1-h2"),
			InvokeTxVect: []*InvokeTx{deposit},
		})
		if err != nil {
			t.Fatalf("L1 deposit at its enable height failed before L2 activation: %v", err)
		}
		if runtime.CurrBlockL1 != 2 || runtime.CurrBlock != 1 {
			t.Fatalf("unexpected independent cursors: L1=%d L2=%d", runtime.CurrBlockL1, runtime.CurrBlock)
		}
		if runtime.InvokeCount != 1 || !runtime.IsActive() || runtime.IsActive_SatsNet() {
			t.Fatalf("wrong activation after L1 input: invokes=%d L1=%t L2=%t", runtime.InvokeCount, runtime.IsActive(), runtime.IsActive_SatsNet())
		}
		history := LoadContractInvokeHistory(manager.GetDB(), runtime.URL(), false, false)
		if len(history) != 1 {
			t.Fatalf("L1 deposit history count=%d, want 1", len(history))
		}
		var stored InvokeHistoryItem
		for _, item := range history {
			stored = item
		}
		item, ok := stored.(*SwapHistoryItem)
		if !ok || !item.FromL1 {
			t.Fatalf("expected persisted L1 deposit history, got %#v", stored)
		}

		replay := *deposit
		replay.Handled = false
		if err := runtime.InvokeWithBlock(&InvokeDataInBlock{
			Height: 2, BlockHash: recycleOrderingBlockHash("independent-l1-h2-replay"),
			InvokeTxVect: []*InvokeTx{&replay},
		}); err == nil {
			t.Fatal("same-height L1 replay was accepted")
		}
		if runtime.InvokeCount != 1 || len(LoadContractInvokeHistory(manager.GetDB(), runtime.URL(), false, false)) != 1 {
			t.Fatalf("same-height L1 replay changed accepted history: invokes=%d", runtime.InvokeCount)
		}
	})

	t.Run("L2 input before L1 enable", func(t *testing.T) {
		runtime, manager := newRecycleActivationOrderingFixture(t)
		defer manager.GetDB().Close()

		runtime.SetEnableBlock(2, 3)
		deposit := newRecycleOrderingDeposit_SatsNet(runtime, 2)
		err := runtime.InvokeWithBlock_SatsNet(&InvokeDataInBlock_SatsNet{
			Height: 2, BlockHash: recycleOrderingBlockHash("independent-l2-h2"),
			InvokeTxVect: []*InvokeTx_SatsNet{deposit},
		})
		if err != nil {
			t.Fatalf("L2 deposit at its enable height failed before L1 activation: %v", err)
		}
		if runtime.CurrBlockL1 != 2 || runtime.CurrBlock != 2 {
			t.Fatalf("unexpected independent cursors: L1=%d L2=%d", runtime.CurrBlockL1, runtime.CurrBlock)
		}
		if runtime.InvokeCount != 1 || runtime.IsActive() || !runtime.IsActive_SatsNet() {
			t.Fatalf("wrong activation after L2 input: invokes=%d L1=%t L2=%t", runtime.InvokeCount, runtime.IsActive(), runtime.IsActive_SatsNet())
		}
		history := LoadContractInvokeHistory(manager.GetDB(), runtime.URL(), false, false)
		if len(history) != 1 {
			t.Fatalf("L2 deposit history count=%d, want 1", len(history))
		}
		var stored InvokeHistoryItem
		for _, item := range history {
			stored = item
		}
		item, ok := stored.(*SwapHistoryItem)
		if !ok || item.FromL1 {
			t.Fatalf("expected persisted L2 deposit history, got %#v", stored)
		}

		replay := *deposit
		replay.Handled = false
		if err := runtime.InvokeWithBlock_SatsNet(&InvokeDataInBlock_SatsNet{
			Height: 2, BlockHash: recycleOrderingBlockHash("independent-l2-h2-replay"),
			InvokeTxVect: []*InvokeTx_SatsNet{&replay},
		}); err == nil {
			t.Fatal("same-height L2 replay was accepted")
		}
		if runtime.InvokeCount != 1 || len(LoadContractInvokeHistory(manager.GetDB(), runtime.URL(), false, false)) != 1 {
			t.Fatalf("same-height L2 replay changed accepted history: invokes=%d", runtime.InvokeCount)
		}

		duplicate := *deposit
		duplicate.Handled = false
		if err := runtime.InvokeWithBlock_SatsNet(&InvokeDataInBlock_SatsNet{
			Height: 3, BlockHash: recycleOrderingBlockHash("independent-l2-h3"),
			InvokeTxVect: []*InvokeTx_SatsNet{&duplicate},
		}); err != nil {
			t.Fatalf("next L2 block failed while checking duplicate: %v", err)
		}
		if runtime.InvokeCount != 1 || len(LoadContractInvokeHistory(manager.GetDB(), runtime.URL(), false, false)) != 1 {
			t.Fatalf("duplicate L2 input changed accepted history: invokes=%d", runtime.InvokeCount)
		}
	})
}

func TestContractActivationGatesAreChainSpecific(t *testing.T) {
	runtime, manager := newRecycleActivationOrderingFixture(t)
	defer manager.GetDB().Close()

	runtime.Status = CONTRACT_STATUS_READY
	runtime.EnableBlockL1 = 3
	runtime.CurrBlockL1 = 2
	runtime.EnableBlock = 4
	runtime.CurrBlock = 3
	if err := runtime.AllowInvoke(); err == nil {
		t.Fatal("L1 invoke was allowed before its enable height")
	}
	if err := runtime.AllowInvoke_SatsNet(); err == nil {
		t.Fatal("L2 invoke was allowed before its enable height")
	}

	runtime.CurrBlockL1 = 3
	runtime.StartBlock = 5
	runtime.EndBlock = 6
	if err := runtime.AllowInvoke(); err != nil {
		t.Fatalf("L1 invoke at its enable height must ignore the L2 business window: %v", err)
	}
	if err := runtime.AllowInvoke_SatsNet(); err == nil {
		t.Fatal("L2 invoke was allowed before its enable height")
	}

	runtime.CurrBlock = 4
	if err := runtime.AllowInvoke_SatsNet(); err == nil {
		t.Fatal("L2 invoke was allowed before StartBlock")
	}
	runtime.CurrBlock = 5
	if err := runtime.AllowInvoke_SatsNet(); err != nil {
		t.Fatalf("L2 invoke at StartBlock was rejected: %v", err)
	}
	runtime.CurrBlock = 6
	if err := runtime.AllowInvoke_SatsNet(); err != nil {
		t.Fatalf("L2 invoke at EndBlock was rejected: %v", err)
	}
	runtime.CurrBlock = 7
	if err := runtime.AllowInvoke_SatsNet(); err == nil {
		t.Fatal("L2 invoke after EndBlock was allowed")
	}
	if err := runtime.AllowInvoke(); err != nil {
		t.Fatalf("L1 invoke was incorrectly constrained by the L2 business window: %v", err)
	}

	runtime.StartBlock = 0
	runtime.EndBlock = 0
	runtime.Status = CONTRACT_STATUS_CLOSING
	if err := runtime.AllowInvoke(); err != nil {
		t.Fatalf("L1 AllowInvoke must not add a CLOSING gate: %v", err)
	}
	if err := runtime.AllowInvoke_SatsNet(); err != nil {
		t.Fatalf("L2 AllowInvoke must not add a CLOSING gate: %v", err)
	}
	if runtime.IsActive() || runtime.IsActive_SatsNet() {
		t.Fatal("closing contract reported active on one of its chains")
	}
}

func newRecycleActivationOrderingFixture(t *testing.T) (*RecycleContractRunTime, *rollbackContractManager) {
	t.Helper()
	database := NewKVDB(t.TempDir())
	localWallet := NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "recycle-ordering-local", GetChainParam())
	remoteWallet := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "recycle-ordering-remote", GetChainParam())
	manager := &rollbackContractManager{Manager: &Manager{
		db: database, wallet: localWallet, cfg: &common.Config{Mode: "test"},
	}}
	runtime := NewRecycleContractRunTime(manager)
	runtime.LocalPubKey = localWallet.GetPubKey().SerializeCompressed()
	runtime.RemotePubKey = remoteWallet.GetPubKey().SerializeCompressed()
	runtime.CoreNodePubKey = runtime.LocalPubKey
	runtime.ChannelAddr = localWallet.GetAddress()
	runtime.AssetName = indexer.ASSET_PLAIN_SAT
	runtime.NumberOfLastDigits = 1
	runtime.FirstPrizeMatchCount = 1
	runtime.FirstPrize = "1000"
	reservation := &rollbackContractReservation{
		contract: runtime, status: RS_DEPLOY_CONTRACT_RUNNING, initiator: false,
		channel: runtime.ChannelAddr, localKey: runtime.LocalPubKey,
		remoteKey: runtime.RemotePubKey, coreKey: runtime.CoreNodePubKey,
	}
	if err := runtime.InitFromDB(manager, reservation); err != nil {
		t.Fatalf("initialize responder runtime: %v", err)
	}
	runtime.Status = CONTRACT_STATUS_READY
	runtime.EnableBlock = INIT_ENABLE_BLOCK
	runtime.EnableBlockL1 = INIT_ENABLE_BLOCK
	runtime.CurrBlock = 1
	runtime.CurrBlockL1 = 1
	return runtime, manager
}

func recycleOrderingBlockHash(label string) string {
	h := sha256.Sum256([]byte(label))
	return hex.EncodeToString(h[:])
}

func newRecycleOrderingDeposit(runtime *RecycleContractRunTime, height int) *InvokeTx {
	tx := btcwire.NewMsgTx(2)
	tx.AddTxIn(btcwire.NewTxIn(&btcwire.OutPoint{}, nil, nil))
	tx.AddTxOut(btcwire.NewTxOut(100000, []byte{0x51}))
	output := indexer.NewTxOutput(100000)
	output.OutValue.PkScript = []byte{0x51}
	output.UtxoId = indexer.ToUtxoId(height, 0, 0)
	output.OutPointStr = fmt.Sprintf("%s:0", tx.TxHash())
	return &InvokeTx{
		Tx: tx, TxIndex: 0, InvokeVout: 0, TxOutput: output,
		Address: runtime.ChannelAddr, Invoker: "recycle-ordering-test-invoker",
	}
}

func newRecycleOrderingDeposit_SatsNet(runtime *RecycleContractRunTime, height int) *InvokeTx_SatsNet {
	tx := swire.NewMsgTx(swire.TxVersion)
	tx.AddTxIn(swire.NewTxIn(&swire.OutPoint{}, nil, nil))
	tx.AddTxOut(swire.NewTxOut(100000, nil, []byte{0x51}))
	output := sindexer.NewTxOutput(100000)
	output.OutValue.PkScript = []byte{0x51}
	output.UtxoId = indexer.ToUtxoId(height, 0, 0)
	output.OutPointStr = fmt.Sprintf("%s:0", tx.TxID())
	return &InvokeTx_SatsNet{
		Tx: tx, TxIndex: 0, InvokeVout: 0, TxOutput: output,
		Address: runtime.ChannelAddr, Invoker: "recycle-ordering-test-invoker",
	}
}

// Exercise the base's existing resync callback when the next L1 block arrives,
// rather than manually replaying the deferred block from the test.
type recycleOrderingResyncReservation struct {
	*rollbackContractReservation
	resync func(int, int)
}

func (r *recycleOrderingResyncReservation) ResyncBlock(start, end int) { r.resync(start, end) }

func TestRecycleActivationNextBlockReplaysDeferredDeposit(t *testing.T) {
	for _, initiallyUnknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown_enable_%t", initiallyUnknown), func(t *testing.T) {
			runtime, manager := newRecycleActivationOrderingFixture(t)
			defer manager.GetDB().Close()
			deferred := &InvokeDataInBlock{
				Height: 2, BlockHash: recycleOrderingBlockHash("next-block-l1-h2"),
				InvokeTxVect: []*InvokeTx{newRecycleOrderingDeposit(runtime, 2)},
			}
			if !initiallyUnknown {
				runtime.SetEnableBlock(3, 2)
				if err := runtime.InvokeWithBlock(deferred); err != nil {
					t.Fatalf("L1 deposit should run without L2 activation: %v", err)
				}
				if runtime.InvokeCount != 1 || runtime.CurrBlock != 1 {
					t.Fatalf("known L1 activation was incorrectly blocked by L2: invokes=%d L2=%d", runtime.InvokeCount, runtime.CurrBlock)
				}
				return
			}

			if err := runtime.InvokeWithBlock(deferred); err == nil {
				t.Fatal("L1 block was accepted before its enable height was known")
			}
			if runtime.InvokeCount != 0 {
				t.Fatal("deposit was accepted before L1 enable height was known")
			}
			runtime.SetEnableBlock(3, 2)
			calls := 0
			runtime.resv = &recycleOrderingResyncReservation{
				rollbackContractReservation: runtime.resv.(*rollbackContractReservation),
				resync: func(start, end int) {
					calls++
					if start != 2 || end != 2 {
						t.Fatalf("wrong replay range %d..%d", start, end)
					}
					if err := runtime.InvokeWithBlock(deferred); err != nil {
						t.Fatalf("deferred L1 replay: %v", err)
					}
				},
			}
			if err := runtime.InvokeWithBlock(&InvokeDataInBlock{
				Height: 3, BlockHash: recycleOrderingBlockHash("next-block-l1-h3"),
			}); err != nil {
				t.Fatalf("next L1 block: %v", err)
			}
			if calls != 1 || runtime.InvokeCount != 1 || runtime.CurrBlockL1 != 3 || runtime.CurrBlock != 1 {
				t.Fatalf("L1 resync did not independently recover deposit: callbacks=%d invokes=%d L1=%d L2=%d",
					calls, runtime.InvokeCount, runtime.CurrBlockL1, runtime.CurrBlock)
			}
			if got := len(LoadContractInvokeHistory(manager.GetDB(), runtime.URL(), false, false)); got != 1 {
				t.Fatalf("expected one persistent deposit, got %d", got)
			}

			duplicate := *deferred.InvokeTxVect[0]
			duplicate.Handled = false
			if err := runtime.InvokeWithBlock(&InvokeDataInBlock{
				Height: 4, BlockHash: recycleOrderingBlockHash("next-block-l1-h4"),
				InvokeTxVect: []*InvokeTx{&duplicate},
			}); err != nil {
				t.Fatalf("next L1 block with duplicate input: %v", err)
			}
			if runtime.InvokeCount != 1 || len(LoadContractInvokeHistory(manager.GetDB(), runtime.URL(), false, false)) != 1 {
				t.Fatalf("duplicate L1 input changed accepted history: invokes=%d", runtime.InvokeCount)
			}
		})
	}
}
