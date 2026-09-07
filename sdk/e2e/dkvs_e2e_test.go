package e2e

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

func TestRealSatoshiNetDKVSAutopayNameAndMailboxSync(t *testing.T) {
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
		dkvsindexer.RecordOptions{Seq: 1}, autopayA); err != nil {
		t.Fatal(err)
	}
	requireDKVSValue(t, f.Network.Core, nameKey, []byte("owner-a"))
	require.NoError(t, subscribeDKVSNodeInternal(t, f.Network.Miner, dkvsindexer.Subscription{
		Type: dkvsindexer.SubscriptionKey, Target: nameKey,
	}))
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
		dkvsindexer.RecordOptions{Seq: 2}, autopayB); err != nil {
		t.Fatal(err)
	}
	requireDKVSValue(t, f.Network.Bootstrap, nameKey, []byte("owner-b"))
	requireDKVSValue(t, f.Network.Miner, nameKey, []byte("owner-b"))
	deletedName, err := clientB.TombstoneSignedWithAutopay(actorB.Wallet, nameKey,
		dkvsindexer.RecordOptions{}, autopayB)
	require.NoError(t, err)
	require.Equal(t, uint64(3), deletedName.Seq)
	require.True(t, dkvsindexer.IsTombstone(deletedName.Flags))
	requireDKVSAbsent(t, f.Network.Bootstrap, nameKey)
	requireDKVSAbsent(t, f.Network.Miner, nameKey)
	rewrittenName, err := clientB.PutSignedRecordWithAutopay(actorB.Wallet, nameKey,
		[]byte("owner-b-rewritten"), dkvsindexer.RecordOptions{}, autopayB)
	require.NoError(t, err)
	require.Equal(t, uint64(4), rewrittenName.Seq)
	requireDKVSValue(t, f.Network.Bootstrap, nameKey, []byte("owner-b-rewritten"))
	requireDKVSValue(t, f.Network.Core, nameKey, []byte("owner-b-rewritten"))
	requireDKVSValue(t, f.Network.Miner, nameKey, []byte("owner-b-rewritten"))

	// Message entries are not ordinary DKVS SharedAppend records anymore. Drive
	// the real SDK -> CoreNode MessageService -> AccountBound mailbox path.
	senderManager, _ := newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
	require.NoError(t, senderManager.InitializeAccountManagement("123456"))
	recipientManager, _ := newWalletManagerForNode(t, f.Network.Core, bootstrapMnemonic)
	require.NoError(t, recipientManager.InitializeAccountManagement("123456"))
	require.NoError(t, recipientManager.BindAccountToCurrentCoreNode())
	recipientManager.Start()
	recipientID := dkvsindexer.AccountID(recipientManager.GetWallet().GetPubKey().SerializeCompressed())
	direct, err := senderManager.SendAccountDirectMessage(
		"e2e-message", wallet.AccountMessageKindGeneric, recipientID, []byte("sender-paid-message"),
	)
	require.NoError(t, err)
	require.Equal(t, uint64(0), direct.SenderMsgID)
	mailKey, err := dkvsindexer.MailMsgKey(recipientID, direct.SenderAccount, direct.MessageID)
	require.NoError(t, err)
	requireDKVSValue(t, f.Network.Core, mailKey, mustSerializeDirectForE2E(t, direct))
	// AccountBound data never mirrors to Bootstrap or selective miners.
	requireDKVSAbsent(t, f.Network.Bootstrap, mailKey)
	requireDKVSAbsent(t, f.Network.Miner, mailKey)

	var (
		messages []*wallet.AccountDirectMessage
		total    int
	)
	require.Eventually(t, func() bool {
		messages, total, err = recipientManager.ReadAccountDirectMessages(0, 10)
		return err == nil && total == 1 && len(messages) == 1
	}, 90*time.Second, time.Second, "periodic managed-prefix sync did not refresh mailbox")
	require.Equal(t, 1, total)
	require.Len(t, messages, 1)
	require.Equal(t, wallet.AccountMessageKindGeneric, messages[0].Payload.Kind)
	require.Equal(t, "sender-paid-message", string(messages[0].Payload.Body))
	require.NoError(t, recipientManager.DeleteMailboxMessage(recipientManager.GetWallet(), mailKey))
	requireDKVSAbsent(t, f.Network.Core, mailKey)

}

func mustSerializeDirectForE2E(t *testing.T, message *wire.DirectMessage) []byte {
	t.Helper()
	encoded, err := wire.SerializeDirectMessage(message, true)
	require.NoError(t, err)
	return encoded
}

func subscribeDKVSNodeInternal(t *testing.T, node *testHarness, sub dkvsindexer.Subscription) error {
	t.Helper()
	base, err := node.IndexerURL("testnet")
	if err != nil {
		return err
	}
	target, err := url.Parse(base)
	if err != nil {
		return err
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/v3/dkvs/subscriptions"
	body, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK || result.Code != 0 {
		return fmt.Errorf("node-internal DKVS subscription failed status=%d code=%d msg=%s",
			resp.StatusCode, result.Code, result.Msg)
	}
	return nil
}

func dkvsMinerArgs(t *testing.T) []string {
	t.Helper()
	minerKey := keyFromMnemonic(t, minerMnemonic, 0)
	return []string{
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

func requireDKVSAbsent(t *testing.T, node *testHarness, key string) {
	t.Helper()
	client := dkvsClientForNode(t, node)
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, err := client.GetRecord(key)
		if errors.Is(err, wallet.ErrDKVSRecordNotFound) {
			return
		}
		lastErr = err
		time.Sleep(200 * time.Millisecond)
	}
	require.ErrorIs(t, lastErr, wallet.ErrDKVSRecordNotFound)
}
