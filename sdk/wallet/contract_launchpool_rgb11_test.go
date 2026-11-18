package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/schemas"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func rgb11LaunchTestContent() *LaunchPoolContract {
	content := NewLaunchPoolContract()
	content.MaxSupply, content.MintAmtPerSat, content.LaunchRatio = 400_000, 1, 50
	return content
}

func TestRGB11LaunchImportFailure(t *testing.T) {
	manager, _, _, evidence, rpc := newRGB11GenericSendFixture(t)
	var issuancePoint string
	for _, out := range rpc.plain {
		if out.Value == 100_000 {
			issuancePoint = out.OutPoint
		}
	}
	point, err := wire.NewOutPointFromString(issuancePoint)
	if err != nil {
		t.Fatal(err)
	}
	original, err := evidence.GetRawTx(point.Hash.String())
	if err != nil {
		t.Fatal(err)
	}
	// The generic RGB fixture uses a simple in-memory store. This test needs a
	// real backend transaction so issuance indexes cannot become half durable.
	manager.stopAccountBackgroundTasks()
	manager.rgbManager.scopeStates.stopReconciliations()
	persistent := NewKVDB(t.TempDir())
	if persistent == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() {
		manager.stopAccountBackgroundTasks()
		if manager.rgbManager != nil && manager.rgbManager.scopeStates != nil {
			manager.rgbManager.scopeStates.stopReconciliations()
		}
		_ = persistent.Close()
	})
	if err := manager.db.Scan(indexer.ScanOptions{}, func(key, value []byte) error {
		return persistent.Write(key, value)
	}); err != nil {
		t.Fatal(err)
	}
	database := &rgb11SettlementFaultDB{KVDB: persistent}
	manager.db = database
	manager.utxoLockerL1 = NewUtxoLocker(database, rpc, L1_NETWORK_BITCOIN)
	manager.rgbManager, err = newRGB11Manager(manager, database, manager.utxoLockerL1, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.rgbManager.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	manager.resumeAccountBackgroundTasks()
	// The issuance batch has been assembled when this callback runs. Remove
	// Bitcoin evidence so the subsequent genuine core import fails; the fixed
	// genesis must already be durable and recoverable under the same request.
	database.beforeFlush = func() {
		evidence.mu.Lock()
		delete(evidence.rawTx, point.Hash.String())
		evidence.mu.Unlock()
	}
	content := rgb11LaunchTestContent()
	request := RGB11IssueRequest{Schema: "NIA", Ticker: "POOL", Name: "Fixed launchpool", Precision: 2}
	_, err = manager.PrepareRGB11LaunchPool(context.Background(), "recover-genesis", content, request)
	if err == nil {
		t.Fatal("missing issuance evidence did not fail genuine RGB import")
	}
	owner := hex.EncodeToString(manager.GetWallet().GetPaymentPubKey().SerializeCompressed())
	key := rgb11LaunchPoolKey("request", owner+"/recover-genesis")
	fixed, err := database.Read(key)
	if err != nil {
		t.Fatalf("genesis was not persisted before import: %v", err)
	}
	var record rgb11LaunchPoolGenesis
	if err := json.Unmarshal(fixed, &record); err != nil {
		t.Fatal(err)
	}
	if err := record.validate(); err != nil {
		t.Fatal(err)
	}
	if record.Source.Outpoint != issuancePoint || record.Source.Assets[0].Amount.String() != "400000" ||
		bytes.Contains(fixed, []byte("offsets")) {
		t.Fatal("genesis journal lost RGB11 source facts or leaked ordinal offsets")
	}
	for _, other := range [][]byte{rgb11LaunchPoolKey("asset", record.Contract.AssetName.String()), rgb11LaunchPoolKey("source", issuancePoint)} {
		raw, err := database.Read(other)
		if err != nil || !bytes.Equal(raw, fixed) {
			t.Fatalf("issuance indexes were not committed together: %v", err)
		}
	}
	database.beforeFlush = nil
	evidence.mu.Lock()
	evidence.rawTx[point.Hash.String()] = original
	evidence.mu.Unlock()
	recovered, err := manager.PrepareRGB11LaunchPool(context.Background(), "recover-genesis", content, request)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Content() != record.Contract.Content() {
		t.Fatal("retry generated a different genesis or signed deployment identity")
	}
	raw, err := database.Read(key)
	if err != nil || !bytes.Equal(raw, fixed) {
		t.Fatalf("retry rewrote the fixed genesis: %v", err)
	}
	balance, err := manager.GetRGB11AssetBalance(&recovered.AssetName)
	if err != nil || balance.String() != "400000" {
		t.Fatalf("recovered NIA supply was not imported: %s: %v", balance, err)
	}
	changed := *content
	changed.MaxSupply++
	if _, err := manager.PrepareRGB11LaunchPool(context.Background(), "recover-genesis", &changed, request); err == nil {
		t.Fatal("request ID allowed its signed supply to change")
	}
	for _, invalid := range []RGB11IssueRequest{
		{Schema: "IFA", Ticker: "POOL", Name: "Wrong schema"},
		{Schema: "NIA", Ticker: "POOL", Name: "Inflation", InflationAmounts: []uint64{1}},
		{Schema: "NIA", Ticker: "POOL", Name: "Partial supply", Amounts: []uint64{1}},
		{Schema: "NIA", Ticker: "POOL", Name: "Invalid precision", Precision: 255},
	} {
		if _, err := manager.PrepareRGB11LaunchPool(context.Background(), "invalid-genesis", content, invalid); err == nil {
			t.Fatal("unsupported issuance was accepted for RGB11 launchpool")
		}
	}
}

type rgb11LaunchWriteFailureDB struct {
	indexer.KVDB
	fail bool
}

func (d *rgb11LaunchWriteFailureDB) Write(key, raw []byte) error {
	if d.fail {
		return errRGB11SettlementWrite
	}
	return d.KVDB.Write(key, raw)
}

func TestRGB11LaunchFixedAnchor(t *testing.T) {
	db := NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	defer db.Close()
	database := &rgb11LaunchWriteFailureDB{KVDB: db, fail: true}
	pool := NewLaunchPoolContractRuntime(&Manager{db: database})
	pool.LaunchPoolContract = *rgb11LaunchTestContent()
	pool.AssetName = indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("a", 64)}
	pool.RGBPrecision, pool.RGBGenesisHash, pool.RGBSourcePoint = 2, strings.Repeat("b", 64), strings.Repeat("c", 64)+":0"
	pool.ChannelAddr = "rgb11-launchpool-fixed-anchor"
	tx := swire.NewMsgTx(swire.TxVersion)
	tx.AddTxIn(&swire.TxIn{PreviousOutPoint: swire.OutPoint{Index: swire.AnchorTxOutIndex}, SignatureScript: []byte("signed fixed invoice")})
	tx.AddTxOut(swire.NewTxOut(330, swire.TxAssets{{Name: pool.AssetName, Amount: *indexer.NewDecimal(400_000, 2)}}, []byte{0x51}))
	if err := pool.saveRGB11LaunchAnchor(tx); !errors.Is(err, errRGB11SettlementWrite) {
		t.Fatalf("anchor persistence failure was hidden: %v", err)
	}
	if loaded, err := pool.loadRGB11LaunchAnchor(); err != nil || loaded != nil {
		t.Fatalf("failed anchor write became visible: %v", err)
	}
	database.fail = false
	if err := pool.saveRGB11LaunchAnchor(tx); err != nil {
		t.Fatal(err)
	}
	loaded, err := pool.loadRGB11LaunchAnchor()
	if err != nil || loaded == nil || loaded.TxID() != tx.TxID() {
		t.Fatalf("fixed anchor was not recoverable: %v", err)
	}
	var before, after bytes.Buffer
	_ = tx.Serialize(&before)
	_ = loaded.Serialize(&after)
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("reloaded anchor changed its signed bytes")
	}
	if err := pool.saveRGB11LaunchAnchor(loaded); err != nil {
		t.Fatal(err)
	}
	loaded.TxOut[0].Value++
	if err := pool.saveRGB11LaunchAnchor(loaded); err == nil {
		t.Fatal("retry replaced the fixed anchor")
	}
	pool.MaxSupply += 100
	if _, err := pool.loadRGB11LaunchAnchor(); err == nil {
		t.Fatal("changed static deployment identity reused an old anchor")
	}
}

func TestRGB11LaunchConnectedEdges(t *testing.T) {
	root := schemas.InputRef{OperationID: [32]byte{1}, AssignmentType: 4000}
	middle := schemas.InputRef{OperationID: [32]byte{2}, AssignmentType: 4000}
	last := schemas.InputRef{OperationID: [32]byte{3}, AssignmentType: 4000}
	point := wire.OutPoint{Hash: [32]byte{4}, Index: 1}
	history := coreconsignment.HistoryReport{
		HistoricalStates: []coreconsignment.ValidatedHistoricalState{{Reference: last, Outpoint: coreconsignment.Outpoint{TxID: [32]byte(point.Hash), Vout: point.Index}}},
		ValidatedConsumers: []coreconsignment.ValidatedConsumer{
			{Input: root, ConsumerOperationID: middle.OperationID},
			{Input: middle, ConsumerOperationID: last.OperationID},
		},
	}
	if err := validateRGB11LaunchConsumption(history, root, point.String()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		edges []coreconsignment.ValidatedConsumer
	}{
		{"root only appears in history", history.ValidatedConsumers[1:]},
		{"missing intermediate consumer", history.ValidatedConsumers[:1]},
		{"conflicting consumer", append(append([]coreconsignment.ValidatedConsumer(nil), history.ValidatedConsumers...), coreconsignment.ValidatedConsumer{Input: root, ConsumerOperationID: [32]byte{8}})},
		{"disconnected cycle", []coreconsignment.ValidatedConsumer{{Input: root, ConsumerOperationID: [32]byte{8}}, {Input: middle, ConsumerOperationID: last.OperationID}, {Input: last, ConsumerOperationID: middle.OperationID}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := history
			candidate.ValidatedConsumers = test.edges
			if err := validateRGB11LaunchConsumption(candidate, root, point.String()); err == nil {
				t.Fatal("disconnected or ambiguous issuance consumption was accepted")
			}
		})
	}
}

func TestRGB11FractionalMint(t *testing.T) {
	pool := NewLaunchPoolContractRuntime(&Manager{db: newMemoryKVDB()})
	pool.LaunchPoolContract = *rgb11LaunchTestContent()
	pool.AssetName.Protocol, pool.RGBPrecision, pool.Divisibility, pool.Limit = rgb11wallet.Protocol, 2, 2, 200_000
	for _, test := range []struct {
		amount string
		value  int64
	}{
		{"0.25", 1}, {"100000", 100000}, {"100000.25", 100001},
	} {
		raw, err := json.Marshal(LaunchPoolInvokeParam{Action: INVOKE_API_MINT, Param: test.amount})
		if err != nil {
			t.Fatal(err)
		}
		value, err := pool.CheckInvokeParam(string(raw))
		if err != nil || value != test.value {
			t.Fatalf("mint %s cost %d, want %d: %v", test.amount, value, test.value, err)
		}
	}
	raw, _ := json.Marshal(LaunchPoolInvokeParam{Action: INVOKE_API_MINT, Param: "200000.01"})
	if _, err := pool.CheckInvokeParam(string(raw)); err == nil {
		t.Fatal("fractional excess above the wallet limit was accepted")
	}
	pool.MaxSupply++
	if pool.TotalAssetToMint().String() != "200000.5" || pool.MaxAssetToMint().String() != "380000.95" {
		t.Fatal("RGB11 launch percentages discarded fractional tokens")
	}
	pool.MintAmtPerSat = 2
	large, err := pool.rgb11LaunchMintAmount(math.MaxInt64)
	want := new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(200))
	if err != nil || large.Value.Cmp(want) != 0 {
		t.Fatalf("RGB11 payment conversion overflowed before quota checks: %v", err)
	}
	pool.MintAmtPerSat = 1
	pool.Status, pool.ReserveRatio, pool.Deployer = CONTRACT_STATUS_CLOSING, 5, "rgb11-launchpool-deployer"
	pool.AssetAmtInPool = pool.launchAssetAmount(pool.MaxSupply)
	pool.handleReserveRatio()
	var allocated *Decimal
	for _, minter := range pool.mintInfoMap {
		allocated = allocated.Add(minter.TotalAmt)
	}
	if allocated.String() != "20000.05" {
		t.Fatalf("reserve allocations changed their signed total: %s", allocated)
	}
}
