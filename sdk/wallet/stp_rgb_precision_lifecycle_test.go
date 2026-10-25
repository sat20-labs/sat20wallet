package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

type precisionSeparationNativeFixture struct {
	channel *Channel
	manager *Manager
	source  rgb11wallet.ChannelFunding
	output  *TxOutput
	wallet  common.Wallet
	remote  common.Wallet
}

// The allocation is issued by core and independently validated against a
// synthetic Bitcoin transaction. No indexer response contains an RGB balance.
func newPrecisionSeparationNativeFixture(t *testing.T, precision int) *precisionSeparationNativeFixture {
	t.Helper()
	previous := _chain
	_chain = "testnet4"
	t.Cleanup(func() { _chain = previous })
	local := NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "precision-local", GetChainParam())
	remote := NewInternalWalletWithMnemonic(channelHeartbeatTestMnemonic, "precision-remote", GetChainParam())
	redeem, err := utils.GenMultiSigScript(local.GetPubKey().SerializeCompressed(), remote.GetPubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	channel := &Channel{ChannelInDB: *NewChannelInDB()}
	channel.ChannelId, channel.RedeemScript, channel.AssetEntropy = "precision-channel", redeem, [32]byte{7, 11}
	channel.LocalCommitment, channel.RemoteCommitment = NewChannelCommitment(), NewChannelCommitment()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex}, []byte{1, 1}, nil))
	tx.AddTxOut(wire.NewTxOut(100_000, channel.GetChannelPkScript()))
	point := tx.TxID() + ":0"
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	supplyTokens := int64(400_000)
	if precision > 10 {
		// The native schema stores raw quantities in u64, even at 18 decimals.
		supplyTokens = 8
	}
	supply := indexer.NewDecimal(supplyTokens, precision)
	allocations, err := rgb11IssueAllocations([]string{point}, []uint64{supply.Value.Uint64()})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(GetChainParam()),
		Ticker: "NPREC", Name: "Native precision", Precision: uint8(precision), Allocations: allocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := nativeproof.BuildRGB11([]byte(issued.Armor), nil)
	if err != nil {
		t.Fatal(err)
	}
	material, err := nativeproof.Decode(proof)
	if err != nil {
		t.Fatal(err)
	}
	name, err := rgb11wallet.NewContractAssetKey(issued.ContractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		t.Fatal(err)
	}
	source := rgb11wallet.ChannelFunding{OutPoint: point, Value: tx.TxOut[0].Value, PkScript: tx.TxOut[0].PkScript,
		AssetName: name, Amount: *supply.Clone(), Consignment: material.Consignment}
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{point: {OutPoint: point, Value: source.Value, PkScript: source.PkScript, Confirmations: 1}},
		rawTx: map[string][]byte{tx.TxID(): raw.Bytes()},
		txStatuses: map[string]*rgb11wallet.BitcoinTxStatus{tx.TxID(): {TxID: tx.TxID(), Confirmed: true, Confirmations: 1,
			BlockHeight: 1, BlockHash: strings.Repeat("1", 64)}},
		spendingTx: make(map[string]string),
	}
	validated, err := rgb11wallet.ValidateChannelFunding(context.Background(), source, evidence, nativeproof.Current, GetChainParam().Name)
	if err != nil {
		t.Fatalf("validate actual core-issued source: %v", err)
	}
	output := indexer.GenerateTxOutput(tx, 0)
	output.Assets = indexer.TxAssets{validated.Asset}
	identity := GetAssetName2(&validated.Asset)
	channel.FundingUtxos[*identity] = []*TxOutput{output}
	inventory, err := rgb11wallet.EncodeChannelInventory([]rgb11wallet.ChannelFunding{source})
	if err != nil {
		t.Fatal(err)
	}
	channel.AssetFunding["rgb11"] = inventory
	// Match the production constructor's real reservation owner. Evidence is
	// synthetic, but the coasset consistency read still uses its actual gate.
	manager := &Manager{}
	manager.rgbManager = &rgb11Manager{Manager: manager, accountOwner: manager, evidence: evidence}
	return &precisionSeparationNativeFixture{channel: channel, source: source, output: output, wallet: local, remote: remote,
		manager: manager}
}

func TestRGBPrecisionSeparationFundingCommitmentExpansion(t *testing.T) {
	for _, precision := range []int{0, 2, 18} {
		t.Run(fmt.Sprint(precision), func(t *testing.T) {
			fixture := newPrecisionSeparationNativeFixture(t, precision)
			name := &AssetName{AssetName: fixture.source.AssetName, N: 0}
			protocol := rgb11STPProtocol{}
			empty := &Channel{ChannelInDB: *NewChannelInDB()}
			incoming := fixture.channel.AssetFunding["rgb11"]
			if _, err := protocol.ValidateChannelFunding(fixture.manager, empty, name, []*TxOutput{fixture.output.Clone()}, incoming); err != nil {
				t.Fatalf("N=0 funding with explicit precision %d rejected: %v", precision, err)
			}
			if _, err := protocol.BuildExpansionAnchorProof(fixture.manager, fixture.channel, name, fixture.output); err != nil {
				t.Fatalf("N=0 expansion with explicit precision %d rejected: %v", precision, err)
			}
			local := fixture.source.Amount.Clone()
			local.Value.Quo(local.Value, big.NewInt(4))
			remote := fixture.source.Amount.Sub(local)
			request := func() STPAssetCommitmentRequest {
				tx := wire.NewMsgTx(2)
				if _, err := rgb11wallet.ReserveChannelDBC(tx, 0); err != nil {
					t.Fatal(err)
				}
				return STPAssetCommitmentRequest{Channel: fixture.channel, Tx: tx, Operation: STPOpCommit, Weight: &utils.TxWeightEstimator{},
					LocalBalance: map[AssetName]*Decimal{*name: local.Clone()}, RemoteBalance: map[AssetName]*Decimal{*name: remote.Clone()},
					LocalScript: fixture.source.PkScript, RemoteScript: fixture.source.PkScript}
			}
			req := request()
			plan, err := protocol.BuildCommitment(fixture.manager, req)
			if err != nil {
				t.Fatalf("N=0 commitment with explicit precision %d rejected: %v", precision, err)
			}
			if len(plan.LocalOutputs) != 1 || len(plan.RemoteOutputs) != 1 ||
				plan.LocalOutputs[0].AssetName.N != 0 || plan.RemoteOutputs[0].AssetName.N != 0 ||
				plan.LocalOutputs[0].Amt.Precision != precision || plan.RemoteOutputs[0].Amt.Precision != precision ||
				plan.LocalOutputs[0].Amt.Add(plan.RemoteOutputs[0].Amt).Cmp(&fixture.source.Amount) != 0 {
				t.Fatal("commitment changed quantity precision, binding identity or total allocation")
			}
			if err := protocol.CompleteRecovery(fixture.manager, req, plan); err != nil {
				t.Fatalf("actual native commitment proof construction failed: %v", err)
			}
			if err := protocol.Verify(STPAssetVerification{Operation: STPOpCommit, Tx: req.Tx, Plan: plan}); err != nil {
				t.Fatalf("actual native commitment proof verification failed: %v", err)
			}
			bad := request()
			bad.LocalBalance[*name] = local.Clone()
			bad.LocalBalance[*name].Precision = (precision + 1) % 19
			if _, err := protocol.BuildCommitment(fixture.manager, bad); err == nil {
				t.Fatal("commitment accepted a balance in a different native quantity unit")
			}
			// A local shape check is not an authenticated genesis lookup. The
			// final funding/proof and commitment boundaries must reject a valid
			// Decimal carrying a different precision under the same asset ID.
			badSource := fixture.source
			badSource.Amount.Precision = (precision + 1) % 19
			badInventory, err := rgb11wallet.EncodeChannelInventory([]rgb11wallet.ChannelFunding{badSource})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := protocol.ValidateChannelFunding(fixture.manager, empty, name, []*TxOutput{fixture.output.Clone()}, badInventory); err == nil {
				t.Fatal("funding accepted a claimed precision different from actual genesis under the same asset ID")
			}
			badOutput := fixture.output.Clone()
			badOutput.Assets[0].Amount.Precision = (precision + 1) % 19
			if _, err := protocol.BuildExpansionAnchorProof(fixture.manager, fixture.channel, name, badOutput); err == nil {
				t.Fatal("expansion accepted a local view with a different native quantity precision")
			}
			badChannel := &Channel{ChannelInDB: fixture.channel.ChannelInDB}
			badChannel.FundingUtxos = map[AssetName][]*TxOutput{*name: {badOutput}}
			bad = request()
			bad.Channel = badChannel
			if _, err := protocol.BuildCommitment(fixture.manager, bad); err == nil {
				t.Fatal("commitment accepted a local input view with a different native quantity precision")
			}
		})
	}
}

type precisionSeparationLaunchManager struct {
	ContractManager
	database indexer.KVDB
	wallet   common.Wallet
}

func (m *precisionSeparationLaunchManager) GetDB() indexer.KVDB      { return m.database }
func (m *precisionSeparationLaunchManager) GetWallet() common.Wallet { return m.wallet }
func (m *precisionSeparationLaunchManager) GetTickerInfo(*indexer.AssetName) *indexer.TickerInfo {
	return nil
}
func (m *precisionSeparationLaunchManager) GetWalletMgr() *Manager { return nil }

func TestRGBPrecisionSeparationLaunchPoolInitAndRestore(t *testing.T) {
	for _, precision := range []int{0, 2} {
		t.Run(fmt.Sprint(precision), func(t *testing.T) {
			fixture := newPrecisionSeparationNativeFixture(t, precision)
			db := NewKVDB(t.TempDir())
			if db == nil {
				t.Fatal("NewKVDB failed")
			}
			t.Cleanup(func() { _ = db.Close() })
			manager := &precisionSeparationLaunchManager{database: db, wallet: fixture.wallet}
			static := nativeLaunchTestContent()
			static.AssetName, static.BindingSat, static.RGBPrecision = fixture.source.AssetName, 0, precision
			hash := sha256.Sum256(fixture.source.Consignment)
			static.RGBGenesisHash, static.RGBSourcePoint = hex.EncodeToString(hash[:]), fixture.source.OutPoint
			if err := static.CheckContent(); err != nil {
				t.Fatal(err)
			}
			content, err := static.Encode()
			if err != nil {
				t.Fatal(err)
			}
			runtime := NewLaunchPoolContractRuntime(manager)
			reservation := &rollbackContractReservation{contract: runtime, initiator: true, status: RS_DEPLOY_CONTRACT_RUNNING,
				channel: "precision-launch", localKey: fixture.wallet.GetPubKey().SerializeCompressed(),
				remoteKey: fixture.remote.GetPubKey().SerializeCompressed(), coreKey: fixture.wallet.GetPubKey().SerializeCompressed()}
			if err := runtime.InitFromContent(content, manager, reservation); err != nil {
				t.Fatal(err)
			}
			if runtime.N != 0 || runtime.Divisibility != precision || runtime.RGBPrecision != precision || runtime.GetAssetNameV2().N != 0 ||
				!runtime.nativeMetadataInitialized {
				t.Fatalf("new LaunchPool mixed binding and precision: N=%d runtime=%d static=%d", runtime.N, runtime.Divisibility, runtime.RGBPrecision)
			}
			if err := runtime.applyNativePublicMetadata(&nativePublicMetadata{Precision: precision + 1}); err == nil {
				t.Fatal("fixed LaunchPool precision, including true zero, was treated as unknown")
			}
			encoded, err := EncodeToBytes(&runtime.LaunchPoolContractRunTimeInDB)
			if err != nil {
				t.Fatal(err)
			}
			root := runtime.CalcRuntimeMerkleRoot()
			var restored LaunchPoolContractRunTime
			if err := DecodeFromBytes(encoded, &restored.LaunchPoolContractRunTimeInDB); err != nil {
				t.Fatal(err)
			}
			reservation.contract = &restored
			if err := restored.InitFromDB(manager, reservation); err != nil {
				t.Fatal(err)
			}
			if restored.N != 0 || restored.Divisibility != precision || restored.RGBPrecision != precision || !restored.nativeMetadataInitialized ||
				!bytes.Equal(root, restored.CalcRuntimeMerkleRoot()) {
				t.Fatal("new-format LaunchPool restore changed binding, precision or runtime root")
			}
			after, err := restored.LaunchPoolContract.Encode()
			if err != nil || !bytes.Equal(content, after) {
				t.Fatalf("new-format LaunchPool restore changed signed static content: %v", err)
			}
			var unknown LaunchPoolContractRunTime
			if err := DecodeFromBytes(encoded, &unknown.LaunchPoolContractRunTimeInDB); err != nil {
				t.Fatal(err)
			}
			unknown.RGBPrecision, unknown.RGBGenesisHash, unknown.RGBSourcePoint = 0, "", ""
			reservation.contract = &unknown
			if err := unknown.InitFromDB(manager, reservation); err == nil || unknown.nativeMetadataInitialized {
				t.Fatal("missing fixed RGB identity was accepted as known zero precision")
			}
		})
	}
}
