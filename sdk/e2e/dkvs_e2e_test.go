package e2e

import (
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetDKVSAutopayNameSync(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	f := newTemplateFixtureWithArgs(t, map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	gas := contractcommon.GetGasAssetName()
	actorA := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	actorB := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))
	require.Equal(t, defaults.AutopayDeployer, actorA.Address)

	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000},
		[]int64{10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{actorA, actorA, actorB, actorB})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName], defaults.AutopayFeeAssetName,
		[]int64{5000, 5000},
		[]int64{10000, 10000},
		[]*dkvsKeyPathActor{actorA, actorB})

	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	deployAssets := txAsset(gas, 290000)
	deployAssets = append(deployAssets, txAsset(defaults.AutopayFeeAssetName, 5000)...)
	deployA, contractA := buildDKVSKeyPathTemplateDeploy(t, actorA,
		contractcommon.TemplateAutopay, content, actorA.Address, defaults.AutopayDeployNonce,
		[]dkvsPrevOut{gasOuts[0], feeOuts[0]},
		wire.TxOut{Value: 10000, Assets: deployAssets})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{deployA}, 0)

	fundB := buildDKVSKeyPathTemplateDefaultInvoke(t, actorB, contractA,
		[]dkvsPrevOut{feeOuts[1]},
		wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 5000)})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{fundB}, 0)

	heartbeatA := buildDKVSKeyPathAssetTransfer(t, actorA, gasOuts[1], gas, 290000, 9000, actorA)
	heartbeatB := buildDKVSKeyPathAssetTransfer(t, actorB, gasOuts[3], gas, 290000, 9000, actorB)
	f.Network.sendManyAndMine(t, []*wire.MsgTx{heartbeatA, heartbeatB}, 0)
	state := fetchTemplateAutopayView(t, f.Network.Bootstrap, contractA.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
	require.Contains(t, state.Delegates, actorA.Address)
	require.Contains(t, state.Delegates, actorB.Address)
	coreState := fetchTemplateAutopayView(t, f.Network.Core, contractA.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusActive, coreState.Status)
	require.Contains(t, coreState.Delegates, actorA.Address)
	require.Contains(t, coreState.Delegates, actorB.Address)
	require.Equal(t, templateruntime.AutopayStatusActive, coreState.Delegates[actorB.Address].Status)

	fakeL1 := f.NetworkFakeL1()
	fakeL1.setNameOwner(dkvsE2EName, actorA.Address)
	clientA := dkvsClientForNode(t, f.Network.Bootstrap)
	nameKey, err := dkvsindexer.NameKey(dkvsE2EName)
	require.NoError(t, err)
	autopayA := wallet.DKVSAutopayOptions{
		AddressParams: &chaincfg.TestNetParams,
		PoolContract:  contractA.MustEncode(),
	}
	if _, err := clientA.PutSignedRecordWithAutopay(actorA.Wallet, nameKey, []byte("owner-a"),
		dkvsindexer.RecordOptions{Seq: 10, TTL: 60_000, ExpiryHeight: 1000}, autopayA); err != nil {
		t.Fatal(err)
	}
	requireDKVSValue(t, f.Network.Core, nameKey, []byte("owner-a"))
	require.NoError(t, connectNode(f.Network.Miner, f.Network.Core))
	requireDKVSValue(t, f.Network.Miner, nameKey, []byte("owner-a"))

	fakeL1.setNameOwner(dkvsE2EName, actorB.Address)
	clientB := dkvsClientForNode(t, f.Network.Core)
	actorBRecordPayer, err := dkvsindexer.P2TRAddressFromPubKeyBytes(actorB.Wallet.GetPubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	require.Equal(t, actorB.Address, actorBRecordPayer)
	autopayB := wallet.DKVSAutopayOptions{
		AddressParams: &chaincfg.TestNetParams,
		PoolContract:  contractA.MustEncode(),
	}
	if _, err := clientB.PutSignedRecordWithAutopay(actorB.Wallet, nameKey, []byte("owner-b"),
		dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 1000}, autopayB); err != nil {
		t.Fatal(err)
	}
	requireDKVSValue(t, f.Network.Bootstrap, nameKey, []byte("owner-b"))
	requireDKVSValue(t, f.Network.Miner, nameKey, []byte("owner-b"))
}

func dkvsMinerArgs(t *testing.T) []string {
	t.Helper()
	minerKey := keyFromMnemonic(t, minerMnemonic, 0)
	return []string{
		"--generate",
		"--miningpubkey=" + hex.EncodeToString(minerKey.PubKey().SerializeCompressed()),
	}
}

const dkvsClientMnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
const dkvsE2EName = "8888.btc"

type dkvsKeyPathActor struct {
	Key      *btcec.PrivateKey
	Wallet   *wallet.InternalWallet
	PkScript []byte
	Address  string
}

type dkvsPrevOut struct {
	Point  wire.OutPoint
	Output *wire.TxOut
}

func waitForDKVSPeerReady(t *testing.T, network *realSatoshiNet) {
	t.Helper()
	require.NotNil(t, network)
	deadline := time.Now().Add(15 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		bootstrapCount, errBootstrap := network.Bootstrap.Client.GetConnectionCount()
		coreCount, errCore := network.Core.Client.GetConnectionCount()
		minerCount, errMiner := network.Miner.Client.GetConnectionCount()
		if errBootstrap == nil && errCore == nil && errMiner == nil &&
			bootstrapCount >= 2 && coreCount >= 1 && minerCount >= 1 {
			return
		}
		last = strings.Join([]string{
			connectionCountStatus("bootstrap", bootstrapCount, errBootstrap),
			connectionCountStatus("core", coreCount, errCore),
			connectionCountStatus("miner", minerCount, errMiner),
		}, " ")
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("satoshinet peers were not ready for dkvs broadcast: %s", last)
}

func connectionCountStatus(name string, count int64, err error) string {
	if err != nil {
		return name + "=" + err.Error()
	}
	return name + "=" + strconv.FormatInt(count, 10)
}

func newDKVSKeyPathActor(t *testing.T, key *btcec.PrivateKey) *dkvsKeyPathActor {
	t.Helper()
	pkScript, err := wallet.PubKeyToPkScript(key.PubKey())
	require.NoError(t, err)
	address, err := wallet.AddrFromPkScript_SatsNet(pkScript)
	require.NoError(t, err)
	payer, err := dkvsindexer.P2TRAddressFromPubKeyBytes(key.PubKey().SerializeCompressed(), &chaincfg.TestNetParams)
	require.NoError(t, err)
	require.Equal(t, payer, address)
	w, _, err := wallet.NewInternalWalletWithPrivKey(key.Serialize(), wallet.GetChainParam())
	require.NoError(t, err)
	return &dkvsKeyPathActor{
		Key:      key,
		Wallet:   w,
		PkScript: pkScript,
		Address:  address,
	}
}

func splitToDKVSKeyPathActors(t *testing.T, f *templateFixture, tx *wire.MsgTx, asset string,
	amounts []int64, values []int64, recipients []*dkvsKeyPathActor) []dkvsPrevOut {

	t.Helper()
	require.Len(t, values, len(amounts))
	require.Len(t, recipients, len(amounts))
	outputs := make([]*wire.TxOut, 0, len(amounts))
	for i := range amounts {
		outputs = append(outputs, wire.NewTxOut(values[i], txAsset(asset, amounts[i]), recipients[i].PkScript))
	}
	splitTx := wire.NewMsgTx(2)
	splitTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: tx.TxHash(), Index: 0}})
	for _, output := range outputs {
		splitTx.AddTxOut(output)
	}
	signTaprootInputs(t, splitTx, f.A.Key, f.A.RedeemScript, f.A.ControlBlock)
	f.Network.sendAndMine(t, splitTx, 1)
	return collectDKVSPrevOuts(t, splitTx, outputs)
}

func collectDKVSPrevOuts(t *testing.T, tx *wire.MsgTx, outputs []*wire.TxOut) []dkvsPrevOut {
	t.Helper()
	points := collectSpendableOutPoints(t, tx, outputs)
	require.Len(t, points, len(outputs))
	txHash := tx.TxHash()
	prevs := make([]dkvsPrevOut, 0, len(points))
	for _, point := range points {
		require.Equal(t, txHash, point.Hash)
		require.Less(t, int(point.Index), len(tx.TxOut))
		prevs = append(prevs, dkvsPrevOut{
			Point:  point,
			Output: cloneDKVSTxOut(tx.TxOut[point.Index]),
		})
	}
	return prevs
}

func cloneDKVSTxOut(out *wire.TxOut) *wire.TxOut {
	if out == nil {
		return nil
	}
	assets := out.Assets.Clone()
	pkScript := append([]byte(nil), out.PkScript...)
	return wire.NewTxOut(out.Value, assets, pkScript)
}

func buildDKVSKeyPathTemplateDeploy(t *testing.T, actor *dkvsKeyPathActor, templateName string, content []byte,
	deployer string, nonce uint64, inputs []dkvsPrevOut, funding wire.TxOut) (*wire.MsgTx, contractcommon.ContractAddress) {

	t.Helper()
	tx, address, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  contractcommon.TestnetContractPrefix,
		Type:            contractcommon.ContractTypeTemplate,
		SubType:         templateName,
		Version:         contractcommon.CurrentTemplateVersion,
		ContractContent: content,
		Deployer:        deployer,
		DeployNonce:     nonce,
		GasLimit:        contractcommon.DeployBaseGas,
		Funding:         funding,
		Inputs:          dkvsPrevOutPoints(inputs),
	})
	require.NoError(t, err)
	signDKVSKeyPathInputs(t, tx, actor, inputs)
	return tx, address
}

func buildDKVSKeyPathTemplateDefaultInvoke(t *testing.T, actor *dkvsKeyPathActor,
	contract contractcommon.ContractAddress, inputs []dkvsPrevOut, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	pkScript, err := contractcommon.ContractPkScript(contract)
	require.NoError(t, err)
	funding.PkScript = pkScript
	tx := wire.NewMsgTx(wire.TxVersion)
	for _, input := range inputs {
		tx.AddTxIn(wire.NewTxIn(&input.Point, nil, nil))
	}
	tx.AddTxOut(&funding)
	signDKVSKeyPathInputs(t, tx, actor, inputs)
	return tx
}

func buildDKVSKeyPathAssetTransfer(t *testing.T, actor *dkvsKeyPathActor, input dkvsPrevOut,
	asset string, amount int64, value int64, recipient *dkvsKeyPathActor) *wire.MsgTx {

	t.Helper()
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&input.Point, nil, nil))
	tx.AddTxOut(wire.NewTxOut(value, txAsset(asset, amount), recipient.PkScript))
	signDKVSKeyPathInputs(t, tx, actor, []dkvsPrevOut{input})
	return tx
}

func dkvsPrevOutPoints(inputs []dkvsPrevOut) []wire.OutPoint {
	points := make([]wire.OutPoint, 0, len(inputs))
	for _, input := range inputs {
		points = append(points, input.Point)
	}
	return points
}

func signDKVSKeyPathInputs(t *testing.T, tx *wire.MsgTx, actor *dkvsKeyPathActor, inputs []dkvsPrevOut) {
	t.Helper()
	require.Len(t, tx.TxIn, len(inputs))
	prevs := make(map[wire.OutPoint]*wire.TxOut, len(inputs))
	for _, input := range inputs {
		prevs[input.Point] = cloneDKVSTxOut(input.Output)
	}
	fetcher := txscript.NewMultiPrevOutFetcher(prevs)
	for i := range tx.TxIn {
		require.NoError(t, wallet.SignTxIn_P2TR(tx, i, actor.Key, fetcher))
	}
}

func (f *templateFixture) NetworkFakeL1() *fakeL1Indexer {
	if f == nil || f.Network == nil {
		return nil
	}
	return f.Network.fakeL1
}

func dkvsClientForNode(t *testing.T, node *testHarness) *wallet.SatsNetDKVSClient {
	t.Helper()
	base, err := node.IndexerURL("testnet")
	require.NoError(t, err)
	parsed, err := url.Parse(base)
	require.NoError(t, err)
	return wallet.NewSatsNetDKVSClient(parsed.Scheme, parsed.Host, strings.Trim(parsed.Path, "/"), nil)
}

func requireDKVSValue(t *testing.T, node *testHarness, key string, value []byte) {
	t.Helper()
	client := dkvsClientForNode(t, node)
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		record, err := client.GetRecord(key)
		if err == nil && record != nil && string(record.Value) == string(value) {
			return
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	record, err := client.GetRecord(key)
	require.NoError(t, err)
	require.Equal(t, string(value), string(record.Value))
}
