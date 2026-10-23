package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

var _test_chain = "testnet"
var _client *Manager
var _server *Manager
var _bootstrap *Manager

func newTestConfForClient(mode, dbPath string) *common.Config {
	ret := &common.Config{
		Env:   "test",
		Chain: _test_chain,
		Peers: []string{
			"b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@https://apiprd.sat20.org/stp/testnet",
			//"s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@192.168.10.103:19529",
			"s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@https://apiprd.ordx.market/stp/testnet",
		},
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
		DB:  dbPath,

		Mode: mode,
	}

	// if _test_chain == "mainnet" {
	// 	ret.Peers[0] = "b@03ab606f4dffd65965b4a9db957361800f8b03ed16acac11d5a4672801554596d0@https://apiprd.sat20.org/stp/mainnet"
	//     ret.Peers[1] = "s@022ab2945f61304f117f55d469c341d606ceb729de436c80c0e6ad7819cdd53ce7@https://apiprd.ordx.market/stp/mainnet"
	// }

	return ret
}

func newTestConfForServer(mode, dbPath string) *common.Config {
	ret := &common.Config{
		Env:   "test",
		Chain: _test_chain,
		Peers: []string{
			"b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@https://apiprd.sat20.org/stp/testnet",
		},
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
		DB:  dbPath,

		Mode: mode,
	}

	// if _test_chain == "mainnet" {
	// 	ret.Peers[0] = "b@03ab606f4dffd65965b4a9db957361800f8b03ed16acac11d5a4672801554596d0@https://apiprd.sat20.org/stp/mainnet"
	//    // ret.Peers[1] = "s@03ab606f4dffd65965b4a9db957361800f8b03ed16acac11d5a4672801554596d0@https://apiprd.sat20.org/stp/mainnet"
	// }

	return ret
}

func initNode(t *testing.T, mode, dbPath string) *Manager {

	var lcfg *common.Config
	if mode == SERVER_NODE || mode == BOOTSTRAP_NODE {
		lcfg = newTestConfForServer(mode, dbPath)
	} else {
		lcfg = newTestConfForClient(mode, dbPath)
	}
	db := NewKVDB(lcfg.DB)
	if db == nil {
		t.Fatalf("NewKVDB failed")
	}
	manager := NewManager(lcfg, db)

	lvl, err := logrus.ParseLevel(lcfg.Log)
	if err != nil {
		lvl = logrus.DebugLevel
	}
	Log.SetLevel(lvl)

	// mnemonice, err := manager.CreateWallet("123456")
	// if err != nil {
	// 	t.Fatalf("CreateWallet failed. %v", err)
	// }
	// fmt.Printf("mnemonice:%s\n", mnemonice)

	if manager.IsWalletExist() {
		_, err := manager.UnlockWallet("123456")
		if err != nil {
			t.Fatalf("UnlockWallet failed. %v", err)
		}
	} else {
		mnemonic := ""
		if manager.IsServerMode() {
			if manager.cfg.Mode == "bootstrap" {
				mnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
			} else {
				//mnemonic = "suit pitch annual bar plug pull comic response night debate evoke original" // server-1
				mnemonic = "uniform bulb body vital later special era tourist build chief devote annual"
			}
		} else {
			// 跑模拟测试用
			mnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
			// tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g

			// mnemonic = "comfort very add tuition senior run eight snap burst appear exile dutch"
			// tb1p6rk7tq5avpjmpudgut4vkhda5m8eetlzpqd6mrcr6u2022tdwfssfsra5x
		}
		_, err := manager.ImportWallet(mnemonic, "123456")
		if err != nil {
			t.Fatalf("ImportWallet failed. %v", err)
		}
	}
	// client:
	// tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g
	// nodeId: 02148cbe135aea8ee9b72f18ca6ddf0efc052e54b6d723cc473a0cc6011766d776
	// pkscript: 51208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba

	// server 1
	// tb1pz747l0qfnt3q2w3ppd45u607rzse0ga85l9vvjtcj8qhcajneqsszqg7z9
	// nodeId: 02b8b9edca5c0c4b7fb2c2ee6ca7cc7a6e899ba36b182586724282d1d949a90397
	// pkscript: 512017abefbc099ae2053a210b6b4e69fe18a197a3a7a7cac6497891c17c7653c821

	// server 2
	// tb1pdw8xjqphyntnvgl3w0vmzkzd7dx266jwcprzwt0qen62pyzpdqhsdvr26h
	// nodeId: 0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e
	// pkscript: 51206b8e69003724d73623f173d9b1584df34cad6a4ec046272de0ccf4a09041682f

	// bootstrap:
	// tb1p62gjhywssq42tp85erlnvnumkt267ypndrl0f3s4sje578cgr79sekhsua
	// nodeId: 025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad
	// pkscript: 5120d2912b91d0802aa584f4c8ff364f9bb2d5af103368fef4c61584b34f1f081f8b

	fmt.Printf("address: %s\n", manager.GetWallet().GetAddress())
	pkScript, _ := GetP2TRpkScript(manager.GetWallet().GetPaymentPubKey())
	fmt.Printf("pkscript: %s\n", hex.EncodeToString(pkScript))
	fmt.Printf("pubkey: %s\n", hex.EncodeToString(manager.GetWallet().GetPaymentPubKey().SerializeCompressed()))

	return manager
}

func createNode(t *testing.T, mode, dbPath string, server, bootstrap *Manager) *Manager {

	manager := initNode(t, mode, dbPath)

	indexerClient1 := NewTestIndexerClient(_network1)
	l1IndexerMgr := NewIndexerRPCClientMgr()
	l1IndexerMgr.Set(indexerClient1)

	indexerClient2 := NewTestIndexerClient(_network2)
	l2IndexerMgr := NewIndexerRPCClientMgr()
	l2IndexerMgr.Set(indexerClient2)

	manager.l1IndexerClient = l1IndexerMgr
	manager.l2IndexerClient = l2IndexerMgr
	nodeClient := NewTestNodeClient(server)
	manager.serverNode.client = nodeClient
	if bootstrap != nil {
		nodeClient := NewTestNodeClient(bootstrap)
		manager.bootstrapNode[0].client = nodeClient
	}

	manager.SetIndexerHttpClient(indexerClient1)
	manager.SetIndexerHttpClient_SatsNet(indexerClient2)
	manager.SetServerNodeHttpClient(nodeClient)

	// manager.utxoLockerL1.rpcClient = indexerClient1
	// manager.utxoLockerL2.rpcClient = indexerClient2

	return manager
}

func closeNode(stp *Manager) {
	stp.initResvMap()
	stp.status.SyncHeight = -1
	stp.status.SyncHeightL2 = -1
	stp.Close()
}

func createNode_TestNet4(t *testing.T, mode, dbPath string, server *Manager) *Manager {

	manager := initNode(t, mode, dbPath)
	manager.status.SyncHeight = int(manager.l1IndexerClient.GetSyncHeight())
	manager.status.SyncHeightL2 = int(manager.l2IndexerClient.GetSyncHeight())

	return manager
}

func clean(t *testing.T) {
	if _client != nil {
		closeNode(_client)
		_client = nil
	}
	if _server != nil {
		closeNode(_server)
		_server = nil
	}
	if _bootstrap != nil {
		closeNode(_bootstrap)
		_bootstrap = nil
	}
	err := os.RemoveAll("../db")
	if err != nil {
		t.Fatalf("RemoveAll failed: %v\n", err)
	}
}

func prepare(t *testing.T) {
	if os.Getenv("SAT20WALLET_LEGACY_INTEGRATION") != "1" {
		t.Skip("set SAT20WALLET_LEGACY_INTEGRATION=1 to run legacy integration tests")
	}
	ENABLE_TESTING = true
	clean(t)

	indexer.ENABLE_TESTING = true
	indexer.CHAIN = _test_chain

	_bootstrap = createNode(t, BOOTSTRAP_NODE, "../db/bootstrapDB", nil, nil)
	_server = createNode(t, SERVER_NODE, "../db/serverDB", _bootstrap, nil)
	_client = createNode(t, CLIENT_NODE, "../db/clientDB", _server, _bootstrap)

	tx, err := CreateGenesisTx(_network1)
	if err != nil {
		t.Fatal(err)
	}
	InitTickerInfo(tx.TxID())

	txId, err := _bootstrap.l1IndexerClient.BroadCastTx(tx)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("bitcoin net genesis tx broadcasted. %s\n", txId)
	_network1.InitBRC20AssetInfo()

	tx2, err := CreateGenesisTx_SatsNet(_network2)
	if err != nil {
		t.Fatal(err)
	}
	txId, err = _bootstrap.l2IndexerClient.BroadCastTx_SatsNet(tx2)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("satoshi net genesis tx broadcasted. %s\n", txId)

	txId, _, err = _server.BatchSendPlainSats(_server.wallet.GetAddress(), 330, 10, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("server stub tx broadcasted. %s\n", txId)
}

func prepare_TestNet4(t *testing.T) {
	//ENABLE_TESTING = true

	err := os.RemoveAll("../db")
	if err != nil {
		t.Fatalf("RemoveAll failed: %v\n", err)
	}

	_bootstrap = createNode_TestNet4(t, BOOTSTRAP_NODE, "../db/bootstrapDB", nil)
	_server = createNode_TestNet4(t, SERVER_NODE, "../db/serverDB", _bootstrap)
	_client = createNode_TestNet4(t, CLIENT_NODE, "../db/clientDB", _server)
}

type Env struct {
	network1 *network
	network2 *network
	tickInfo map[string]*indexer.TickerInfo
}

var _backupEnv Env

func backupEnv() *Env {

	_backupEnv.network1 = _network1.Clone()
	_backupEnv.network2 = _network2.Clone()

	_backupEnv.tickInfo = CloneTickerInfo()

	n := &Env{}
	*n = _backupEnv
	fmt.Println("env backup complete")
	return n
}

func restoreEnv() {
	restoreSpecEnv(&_backupEnv)
}

func stopNode(stp *Manager) {
	if stp == nil {
		return
	}
	stp.initResvMap()
}

func initUtxoLocker(stp *Manager) {
	DeleteAllLockedUtxo(stp.db, L2_NETWORK_SATOSHI)
	DeleteAllLockedUtxo(stp.db, L1_NETWORK_BITCOIN)
	stp.utxoLockerL1.Reload("")
	stp.utxoLockerL2.Reload("")
}

// 不要改动channels
func restoreMgr(stp *Manager) {
	initUtxoLocker(stp)
}

func restoreSpecEnv(backup *Env) {
	stopNode(_client)
	stopNode(_server)
	stopNode(_bootstrap)

	_network1.Set(backup.network1)
	_network2.Set(backup.network2)
	SetTickerInfo(backup.tickInfo)

	_client.status.SyncHeight = _network1.height - 1
	_client.status.SyncHeightL2 = _network2.height - 1
	_server.status.SyncHeight = _network1.height - 1
	_server.status.SyncHeightL2 = _network2.height - 1
	_bootstrap.status.SyncHeight = _network1.height - 1
	_bootstrap.status.SyncHeightL2 = _network2.height - 1

	restoreMgr(_bootstrap)
	restoreMgr(_server)
	restoreMgr(_client)

	fmt.Println("env restore complete")
}

func backupNetwork() *Env {
	n := &Env{
		network1: _network1.Clone(),
		network2: _network2.Clone(),
		tickInfo: CloneTickerInfo(),
	}
	fmt.Println("network backup complete")
	return n
}

func restoreNetwork(backup *Env) {
	_network1.Set(backup.network1)
	_network2.Set(backup.network2)
	SetTickerInfo(backup.tickInfo)

	if _client != nil {
		initUtxoLocker(_client)
	}

	if _server != nil {
		initUtxoLocker(_server)
	}

	if _bootstrap != nil {
		initUtxoLocker(_bootstrap)
	}

	fmt.Println("network restore complete")
}

// Signing and wallet tests need fresh encrypted wallet state, not the legacy
// three-node/channel fixture or its shared ../db directory.
func newManagerTestWallet(t *testing.T, mnemonic string) *Manager {
	t.Helper()
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	manager := newAccountManagementAutoTestManager(t)
	_, err := manager.ImportWallet(mnemonic, "123456")
	require.NoError(t, err)
	return manager
}

func managerTestPSBT(t *testing.T, manager *Manager) (*psbt.Packet, *txscript.MultiPrevOutFetcher) {
	t.Helper()
	script, err := GetP2TRpkScript(manager.wallet.GetPaymentPubKey())
	require.NoError(t, err)
	previous := wire.NewMsgTx(2)
	previous.AddTxIn(&wire.TxIn{SignatureScript: []byte("manager-signing-fixture")})
	previous.AddTxOut(wire.NewTxOut(10000, script))
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: previous.TxHash()}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(9000, script))
	packet, err := psbt.NewFromUnsignedTx(tx)
	require.NoError(t, err)
	packet.Inputs[0].WitnessUtxo = previous.TxOut[0]
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	fetcher.AddPrevOut(tx.TxIn[0].PreviousOutPoint, previous.TxOut[0])
	return packet, fetcher
}

func TestPsbt(t *testing.T) {
	manager := newManagerTestWallet(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire")
	packet, fetcher := managerTestPSBT(t, manager)
	var unsigned bytes.Buffer
	require.NoError(t, packet.Serialize(&unsigned))
	signed, err := manager.SignPsbt(hex.EncodeToString(unsigned.Bytes()), false)
	require.NoError(t, err)
	finalPacket, err := toPsbt(signed)
	require.NoError(t, err)
	require.True(t, finalPacket.IsComplete())
	finalTx, err := psbt.Extract(finalPacket)
	require.NoError(t, err)
	require.Equal(t, packet.UnsignedTx.TxHash(), finalTx.TxHash())
	fee, err := VerifySignedTxV2(finalTx, fetcher)
	require.NoError(t, err)
	require.Equal(t, int64(1000), fee)
	extracted, err := manager.SignPsbt(hex.EncodeToString(unsigned.Bytes()), true)
	require.NoError(t, err)
	decoded, err := DecodeMsgTx(extracted)
	require.NoError(t, err)
	require.Equal(t, finalTx.TxHash(), decoded.TxHash())
	require.NoError(t, VerifySignedTx(decoded, fetcher))
}

func TestPsbt_SatsNet(t *testing.T) {
	manager := newManagerTestWallet(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire")
	script, err := GetP2TRpkScript(manager.wallet.GetPaymentPubKey())
	require.NoError(t, err)
	packet := satsNetPrevoutPacket(script)
	packet.UnsignedTx.TxOut[0].Value -= 100
	var unsigned bytes.Buffer
	require.NoError(t, packet.Serialize(&unsigned))
	signed, err := manager.SignPsbt_SatsNet(hex.EncodeToString(unsigned.Bytes()), false)
	require.NoError(t, err)
	raw, err := hex.DecodeString(signed)
	require.NoError(t, err)
	finalPacket, err := spsbt.NewFromRawBytes(bytes.NewReader(raw), false)
	require.NoError(t, err)
	require.True(t, finalPacket.IsComplete())
	finalTx, err := spsbt.Extract(finalPacket)
	require.NoError(t, err)
	require.Equal(t, packet.UnsignedTx.TxHash(), finalTx.TxHash())
	require.Equal(t, packet.Inputs[0].WitnessUtxo.Assets, finalTx.TxOut[0].Assets)
	require.Equal(t, int64(100), packet.Inputs[0].WitnessUtxo.Value-finalTx.TxOut[0].Value)
	require.NoError(t, VerifySignedTx_SatsNet(finalTx, PsbtPrevOutputFetcher_SatsNet(finalPacket)))
}

// 验证挂单的psbt的签名有效
func TestVerifyPsbtString_satsnet(t *testing.T) {
	psbtStr := "70736274ff0100740100000001cba01af0880b39487cc6cc6a0d196fbde4eecfd4963a94af784df3cd92669f950000000000ffffffff010000000000000000010572756e657301660736353130335f3103313a3000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb38000000000001012c0a0000000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb38010304830000000000"
	hexBytes, _ := hex.DecodeString(psbtStr)
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		t.Fatal(err)
	}

	partSignedTx := packet.UnsignedTx
	partSignedTx.TxIn[0].Witness = swire.TxWitness{packet.Inputs[0].TaprootKeySpendSig}

	PrintJsonTx_SatsNet(partSignedTx, "")
	prevFectcher := PsbtPrevOutputFetcher_SatsNet(packet)
	err = VerifySignedTx_SatsNet(partSignedTx, prevFectcher)
	if err == nil {
		t.Fatal("PSBT without a taproot key-spend signature should be rejected")
	}
}

func toPsbt(psbtHex string) (*psbt.Packet, error) {
	hexBytes, _ := hex.DecodeString(psbtHex)
	return psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
}

func TestVerifySignedPsbtString(t *testing.T) {
	psbtStr := "70736274ff0100890200000001d71f33336e92a9e2a794c8a77ffd3b846c335bfc6ac2a0eb026e96d61a04b7220100000000fdffffff025a0b0000000000002251202fad5b1f0dfa1111ca54fb636e030846bd731dca4f2b7af48d8e5b9672d90b25ff630000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b000000000001012b94790000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b0113407da5f3313247877d7820cc5ef80de3895ff93fa40924744ed9fe834f71efd79aa99894be089ad1c7c14589828542271862413f4e6e12fa387a9b9d4b5fed4cf4011720d210be04396837b11f65eb42527de3f6a1c1c1d51de38ee907fc355c56ee5115000000"
	packet, err := toPsbt(psbtStr)
	if err != nil {
		t.Fatal(err)
	}

	err = psbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		t.Fatal()
	}

	finalTx, err := psbt.Extract(packet)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		t.Fatal()
	}

	PrintJsonTx(finalTx, "")
	prevFectcher := PsbtPrevOutputFetcher(packet)
	err = VerifySignedTx(finalTx, prevFectcher)
	if err != nil {
		Log.Errorf("VerifySignedTx failed, %v", err)
		t.Fatal()
	}
}

func TestVerifySignedPsbtString_satsnet(t *testing.T) {
	psbtStr := "70736274ff0100f801000000021eb0a1d7e09e630bb58199ac5045bd73687b2b3a906d6f018229e23bb45c09610100000000ffffffff2c484352d8b86b3f00f7d78b587ae0f6b6dff6cb129a61fa2f73e99b9ad4b9840100000000ffffffff03200300000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb38de2600000000000001046f7264780166097261726570697a7a61053430303a30002251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba0a00000000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b0000000000010144020300000000000001046f7264780166097261726570697a7a61053430303a3000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb3801030483000000011341436f5765bdbd7b793c3a0f56438d5abc72a9d03bd3829ead52c2b40fcd82511e565ac6e0a14e458be0f4dae7cc17595f2f749cb51bf517d2bee530056f1a136b830001012c1027000000000000002251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba0113406a3e45e58ae465cb668cd8a2b992cf79caf052e05316c3df16992a370a43388c660fe70fdd0f2154e10730e549e195306951f2aec57c09466ef0c83b987ae22500000000"
	hexBytes, _ := hex.DecodeString(psbtStr)
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		t.Fatal(err)
	}
	err = spsbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		t.Fatal(err)
	}

	finalTx, err := spsbt.Extract(packet)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		t.Fatal(err)
	}

	PrintJsonTx_SatsNet(finalTx, "")
	prevFectcher := PsbtPrevOutputFetcher_SatsNet(packet)
	err = VerifySignedTx_SatsNet(finalTx, prevFectcher)
	if err != nil {
		Log.Errorf("VerifySignedTx_SatsNet failed, %v", err)
		t.Fatal()
	}
}

func TestBuildOrder(t *testing.T) {

	info := UtxoInfo{
		AssetsInUtxo: indexer.AssetsInUtxo{
			UtxoId:   1030792413185,
			OutPoint: "ee7f3526663e7ebdfd4fb577941cdeab12729d2d220d651798369bfe106c4b2a:1",
			Value:    10000,
			PkScript: []byte("USBmGjbRHN3OJU7Y44vUbF7Oh71vqRPudPlNcHWRyBfLOA=="),
			Assets:   nil,
		},
		Price: 10000,
		AssetInfo: &BuyAssetInfo{
			AssetName: indexer.AssetName{
				Protocol: "ordx",
				Type:     "f",
				Ticker:   "rarepizza",
			},
			Amount:     "100",
			BindingSat: 1,
			Precision:  0,
		},
	}

	utxo, _ := json.Marshal(info)
	fmt.Printf("%s\n", string(utxo))

	psbt, err := BuildBatchSellOrder_SatsNet([]string{string(utxo)}, "tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv", "testnet")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("psbt: %s", psbt)
}

func TestFinalizeOrder(t *testing.T) {

	psbt := "70736274ff01005f01000000012a4b6c10fe9b369817650d222d9d7212abde1c9477b54ffdbd7e3e6626357fee0100000000ffffffff01200300000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380000000000010144900100000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380000"

	utxos := []string{
		"{\"UtxoId\":1580548227073,\"Outpoint\":\"15cf5d8986cbd8f806059ad2a317cf8c389f279656e289f330bba5504c970094:1\",\"Value\":1000,\"PkScript\":\"USBmGjbRHN3OJU7Y44vUbF7Oh71vqRPudPlNcHWRyBfLOA==\",\"Assets\":null}",
		"{\"UtxoId\":1065152151553,\"Outpoint\":\"ca4831409cb48ef4708f740bf1f4a0398beea5ef317e70ae9c67348c1e94323f:1\",\"Value\":100,\"PkScript\":\"USBmGjbRHN3OJU7Y44vUbF7Oh71vqRPudPlNcHWRyBfLOA==\",\"Assets\":null}",
		"{\"UtxoId\":1030792413186,\"Outpoint\":\"ee7f3526663e7ebdfd4fb577941cdeab12729d2d220d651798369bfe106c4b2a:2\",\"Value\":90,\"PkScript\":\"USBmGjbRHN3OJU7Y44vUbF7Oh71vqRPudPlNcHWRyBfLOA==\",\"Assets\":null}",
	}

	finalPsbt, err := FinalizeSellOrder_SatsNet(psbt, utxos,
		"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv",
		"tb1pttjr9292tea2nr28ca9zswgdhz0dasnz6n3v58mtg9cyf9wqr49sv8zjep",
		"testnet",
		10,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("final psbt: %s\n", finalPsbt)
}

func TestSplitBatchSignedPsbt(t *testing.T) {
	psbt := "70736274ff01007701000000012a4b6c10fe9b369817650d222d9d7212abde1c9477b54ffdbd7e3e6626357fee0100000000ffffffff01200300000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380000000000010144900100000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380103048300000001134062387e222f742ea1d6685adc9b9ee2d03c06167b4cfe4802c4eee0ac7013729c775604897d30ddd97b48156a7a07619e13eccd241572b54309cae7dbca09384c0000"
	result, err := SplitBatchSignedPsbt_SatsNet(psbt, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	for _, psbt := range result {
		fmt.Printf("%s\n", psbt)
	}
}

func TestVerifySignedTx_SatsNet(t *testing.T) {
	txHex := "010000000001021eb0a1d7e09e630bb58199ac5045bd73687b2b3a906d6f018229e23bb45c09610100000000ffffffff2c484352d8b86b3f00f7d78b587ae0f6b6dff6cb129a61fa2f73e99b9ad4b9840100000000ffffffff03200300000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb38de2600000000000001046f7264780166097261726570697a7a61053430303a30002251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba0a00000000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b0141436f5765bdbd7b793c3a0f56438d5abc72a9d03bd3829ead52c2b40fcd82511e565ac6e0a14e458be0f4dae7cc17595f2f749cb51bf517d2bee530056f1a136b8301406a3e45e58ae465cb668cd8a2b992cf79caf052e05316c3df16992a370a43388c660fe70fdd0f2154e10730e549e195306951f2aec57c09466ef0c83b987ae22500000000"
	tx, err := DecodeMsgTx_SatsNet(txHex)
	if err != nil {
		t.Fatal(err)
	}
	PrintJsonTx_SatsNet(tx, "")
	//VerifySignedTx_SatsNet()
}

func TestPsbtFullFlow(t *testing.T) {
	seller := newManagerTestWallet(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire")
	buyer := newManagerTestWallet(t, "uniform bulb body vital later special era tourist build chief devote annual")

	pkScript, _ := GetP2TRpkScript(seller.GetWallet().GetPaymentPubKey())
	assset := indexer.DisplayAsset{
		AssetName: indexer.AssetName{
			Protocol: "ordx",
			Type:     "f",
			Ticker:   "rarepizza",
		},
		Amount:     "100",
		Precision:  0,
		BindingSat: 1,
		Offsets:    nil,
	}
	info := UtxoInfo{
		AssetsInUtxo: indexer.AssetsInUtxo{
			UtxoId:   1030792413185,
			OutPoint: "ee7f3526663e7ebdfd4fb577941cdeab12729d2d220d651798369bfe106c4b2a:1",
			Value:    100,
			PkScript: pkScript,
			Assets:   []*indexer.DisplayAsset{&assset},
		},
		Price: 6000,
		// AssetInfo: &indexer.AssetInfo{
		// 	Name: indexer.AssetName{
		// 		Protocol: "ordx",
		// 		Type:     "f",
		// 		Ticker:   "rarepizza",
		// 	},
		// 	Amount:     *indexer.NewDefaultDecimal(100),
		// 	BindingSat: 1,
		// },
	}

	utxo, _ := json.Marshal(info)
	fmt.Printf("%s\n", string(utxo))

	sellerAddr := seller.wallet.GetAddress()

	psbt, err := BuildBatchSellOrder_SatsNet([]string{string(utxo)},
		sellerAddr, "testnet",
	)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("BuildBatchSellOrder: %s", psbt)

	psbts, err := SplitBatchSignedPsbt_SatsNet(psbt, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	for i, psbt := range psbts {
		fmt.Printf("SplitBatchSignedPsbt %d: %s\n", i, psbt)
	}

	require.Len(t, psbts, 1)
	sellPsbt := psbts[0]

	signedSellPsbt, err := seller.SignPsbt_SatsNet(sellPsbt, false)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SignPsbt_SatsNet: %s", signedSellPsbt)

	// buyer
	buyerAddr := buyer.wallet.GetAddress()
	pkScript2, _ := GetP2TRpkScript(buyer.GetWallet().GetPaymentPubKey())
	info2 := UtxoInfo{
		AssetsInUtxo: indexer.AssetsInUtxo{
			UtxoId:   3985729912833,
			OutPoint: "84b9d49a9be9732ffa619a12cbf6dfb6f6e07a588bd7f7003f6bb8d85243482c:1",
			Value:    10000,
			PkScript: pkScript2,
			Assets:   nil,
		},
		Price:     0,
		AssetInfo: nil,
	}
	utxo2, _ := json.Marshal(info2)
	fmt.Printf("%s\n", string(utxo2))

	utxos := []string{
		string(utxo2),
	}

	finalPsbt, err := FinalizeSellOrder_SatsNet(signedSellPsbt, utxos,
		buyerAddr,
		"tb1pttjr9292tea2nr28ca9zswgdhz0dasnz6n3v58mtg9cyf9wqr49sv8zjep",
		"testnet",
		10,
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("FinalizeSellOrder: %s\n", finalPsbt)

	signedFinalPsbt, err := buyer.SignPsbt_SatsNet(finalPsbt, false)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SignPsbt_SatsNet: %s", signedFinalPsbt)

	hexBytes, _ := hex.DecodeString(signedFinalPsbt)
	finalPacket, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		t.Fatal()
	}

	err = spsbt.MaybeFinalizeAll(finalPacket)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		t.Fatal()
	}

	finalTx, err := spsbt.Extract(finalPacket)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		t.Fatal()
	}
	PrintJsonTx_SatsNet(finalTx, "")

	require.True(t, finalPacket.IsComplete())
	require.Equal(t, finalPacket.UnsignedTx.TxHash(), finalTx.TxHash())
	var receivedAsset bool
	for _, output := range finalTx.TxOut {
		if bytes.Equal(output.PkScript, pkScript2) && len(output.Assets) == 1 &&
			output.Assets[0].Name == assset.AssetName && output.Assets[0].Amount.String() == "100" {
			receivedAsset = true
		}
	}
	require.True(t, receivedAsset, "buyer must receive the seller's exact asset amount")
	prevFectcher := PsbtPrevOutputFetcher_SatsNet(finalPacket)
	err = VerifySignedTx_SatsNet(finalTx, prevFectcher)
	if err != nil {
		Log.Errorf("VerifySignedTx_SatsNet failed, %v", err)
		t.Fatal()
	}

}

func TestChangePassword(t *testing.T) {
	const mnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
	manager := newManagerTestWallet(t, mnemonic)
	id := manager.status.CurrentWallet
	require.Equal(t, mnemonic, manager.GetMnemonic(id, "123456"))
	require.Error(t, manager.ChangePassword("wrong-password", "abcdefgh"))
	require.NoError(t, manager.ChangePassword("123456", "abcdefgh"))
	require.Empty(t, manager.GetMnemonic(id, "123456"))
	require.Equal(t, mnemonic, manager.GetMnemonic(id, "abcdefgh"))
}

func TestSwitchChain(t *testing.T) {
	manager := &Manager{}
	if err := manager.SwitchChain("mainnet", "123456"); err != ErrInPlaceChainSwitchDisabled {
		t.Fatalf("SwitchChain error=%v", err)
	}
}

func TestGetTxAssetInfoFromPsbt(t *testing.T) {
	manager := newManagerTestWallet(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire")
	packet, fetcher := managerTestPSBT(t, manager)
	point := packet.UnsignedTx.TxIn[0].PreviousOutPoint
	previous := &TxOutput{UtxoId: 1, OutPointStr: point.String(), OutValue: *fetcher.FetchPrevOutput(point)}
	client := atomicalsTestConfigure(manager)
	client.outputs[point.String()] = previous
	var unsigned bytes.Buffer
	require.NoError(t, packet.Serialize(&unsigned))
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%t", complete), func(t *testing.T) {
			encoded := hex.EncodeToString(unsigned.Bytes())
			if complete {
				var err error
				encoded, err = manager.SignPsbt(encoded, false)
				require.NoError(t, err)
			}
			info, err := manager.GetTxAssetInfoFromPsbt(encoded)
			require.NoError(t, err)
			require.Equal(t, packet.UnsignedTx.TxID(), info.TxId)
			expectedHex, err := EncodeMsgTx(packet.UnsignedTx)
			require.NoError(t, err)
			require.Equal(t, expectedHex, info.TxHex)
			require.Len(t, info.InputAssets, 1)
			require.Equal(t, previous.ToAssetsInUtxo(), info.InputAssets[0])
			require.Len(t, info.OutputAssets, 1)
			require.Equal(t, int64(9000), info.OutputAssets[0].Value)
			require.Empty(t, info.OutputAssets[0].Assets)
		})
	}
	delete(client.outputs, point.String())
	info, err := manager.GetTxAssetInfoFromPsbt(hex.EncodeToString(unsigned.Bytes()))
	require.Error(t, err, "missing previous outputs must never be silently accepted")
	require.Nil(t, info)
}

func TestVerifyTx(t *testing.T) {
	manager := newManagerTestWallet(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire")
	packet, fetcher := managerTestPSBT(t, manager)
	var unsigned bytes.Buffer
	require.NoError(t, packet.Serialize(&unsigned))
	signed, err := manager.SignPsbt(hex.EncodeToString(unsigned.Bytes()), true)
	require.NoError(t, err)
	tx, err := DecodeMsgTx(signed)
	require.NoError(t, err)
	for _, input := range tx.TxIn {
		require.NotNil(t, fetcher.FetchPrevOutput(input.PreviousOutPoint))
	}
	fee, err := VerifySignedTxV2(tx, fetcher)
	require.NoError(t, err)
	require.Equal(t, int64(1000), fee)
	tx.TxOut[0].Value++
	require.Error(t, VerifySignedTx(tx, fetcher), "a mutated signed output must fail verification")
}

func TestImportPrivKeyWallet(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	manager := newAccountManagementAutoTestManager(t)
	const privateKey = "cd8deaf4d6f66edca13370f823de0b36fa7e911d1391f3967479e02e066a7cd3"
	id, err := manager.ImportWalletWithPrivateKey(privateKey, "123456")
	require.NoError(t, err)
	require.Equal(t, "tb1prfcqz85l0x9r03fnxkfs3r78g2ap6afp5740m0c9h7g6xd0s5xkqgzjh4q", manager.wallet.GetAddress())
	require.NoError(t, manager.ChangePassword("123456", "abcdefgh"))
	require.Empty(t, manager.GetMnemonic(id, "123456"))
	require.Equal(t, privateKey, manager.GetMnemonic(id, "abcdefgh"))
}

func TestSend_privKeyWallet(t *testing.T) {
	manager := newManagerTestWallet(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire")
	previousScripts := append([]string(nil), _pkScripts...)
	previousTesting, previousNotSend := ENABLE_TESTING, NOT_SEND_TX
	ENABLE_TESTING, NOT_SEND_TX = true, false
	t.Cleanup(func() {
		_pkScripts = previousScripts
		ENABLE_TESTING, NOT_SEND_TX = previousTesting, previousNotSend
	})
	script, err := GetP2TRpkScript(manager.wallet.GetPaymentPubKey())
	require.NoError(t, err)
	// Reuse the existing component Indexer client with one fresh, owned funding
	// output. No shared network state, filesystem DB or public endpoint is used.
	chain := &network{
		name: "bitcoin", height: 1,
		blocks: make(map[int][]string), txBroadcasted: make(map[string]string),
		utxoUsed: make(map[string]string), utxoIndex: make(map[string]int),
		addrAssetMap: make(map[string]map[swire.AssetName]*Decimal),
		invalids:     make(map[string]map[swire.AssetName]bool),
		utxoValue:    []int64{20000}, utxoAssets: []indexer.TxAssets{nil},
		offsets: []map[swire.AssetName]indexer.AssetOffsets{nil}, utxoOwner: []int{insertPkScript(script)},
	}
	client := NewTestIndexerClient(chain)
	funding, err := CreateGenesisTx(chain)
	require.NoError(t, err)
	_, err = client.BroadCastTx(funding)
	require.NoError(t, err)
	configure := func(wallet *Manager) {
		wallet.l1IndexerClient = &IndexerRPCClientMgr{active: client}
		wallet.utxoLockerL1 = NewUtxoLocker(wallet.db, client, L1_NETWORK_BITCOIN)
		wallet.utxoLockerL1.Init()
	}
	configure(manager)
	require.NoError(t, manager.SwitchAccount(1))
	address := manager.wallet.GetAddress()
	key := manager.wallet.(*InternalWallet).getPaymentPrivKey().Serialize()
	defer zeroBytes(key)
	require.NoError(t, manager.SwitchAccount(0))
	plain := &indexer.AssetName{}
	_, err = manager.SendAssets(address, plain.String(), "10000", 1, nil)
	require.NoError(t, err)
	require.Equal(t, int64(10000), manager.GetAssetBalance(address, plain).Int64())

	// A managed mnemonic account cannot import private-key wallets. Use a
	// separate wallet catalog, as an actual private-key-only user does.
	privateManager := newAccountManagementAutoTestManager(t)
	_, err = privateManager.ImportWalletWithPrivateKey(hex.EncodeToString(key), "123456")
	require.NoError(t, err)
	require.Equal(t, address, privateManager.wallet.GetAddress())
	configure(privateManager)
	recipient := manager.wallet.GetAddress()
	before := manager.GetAssetBalance(recipient, plain).Int64()
	previous := txscript.NewMultiPrevOutFetcher(nil)
	for _, utxo := range client.GetAllUtxosWithAddress(address) {
		op, err := wire.NewOutPointFromString(utxo.OutPoint)
		require.NoError(t, err)
		previous.AddPrevOut(*op, wire.NewTxOut(utxo.Value, utxo.PkScript))
	}
	tx, err := privateManager.SendAssets(recipient, plain.String(), "1000", 1, nil)
	require.NoError(t, err)
	fee, err := VerifySignedTxV2(tx, previous)
	require.NoError(t, err)
	require.Positive(t, fee)
	require.Equal(t, int64(9000)-fee, privateManager.GetAssetBalance(address, plain).Int64())
	require.Equal(t, before+1000, manager.GetAssetBalance(recipient, plain).Int64())
}
