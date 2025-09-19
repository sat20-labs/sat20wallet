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
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var _test_chain = "testnet"
var _client *Manager
var _client2 *Manager

func newTestConf(mode, dbPath string) *common.Config {
	ret := &common.Config{
		Env:   "test",
		Chain: _test_chain,
		Log:   "debug",
		DB:    dbPath,
		Peers: []string{
			"b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@seed.sat20.org:19529",
			//"s@02b8b9edca5c0c4b7fb2c2ee6ca7cc7a6e899ba36b182586724282d1d949a90397@127.0.0.1:9080",
			"s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@39.108.96.46:19529",
		},
		IndexerL1: &common.Indexer{
			Scheme: "http",
			Host:   "192.168.10.103:8009",
			Proxy:  "btc/testnet",
		},
		IndexerL2: &common.Indexer{
			Scheme: "http",
			Host:   "192.168.10.101:19528",
			Proxy:  "testnet",
		},
	}

	return ret
}

func createNode(t *testing.T, mode, dbPath string) *Manager {
	cfg := newTestConf(mode, dbPath)
	db := NewKVDB(cfg.DB)
	if db == nil {
		t.Fatalf("NewKVDB failed")
	}
	manager := NewManager(cfg, db)

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
		if mode == "client" {
			mnemonic := ""

			// tb1p62gjhywssq42tp85erlnvnumkt267ypndrl0f3s4sje578cgr79sekhsua
			// mnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
			
			// tb1pttjr9292tea2nr28ca9zswgdhz0dasnz6n3v58mtg9cyf9wqr49sv8zjep
			mnemonic = "faith fluid swarm never label left vivid fetch scatter dilemma slight wear"
			
			// tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv
			// mnemonic = "remind effort case concert skull live spoil obvious finish top bargain age"

			// tb1p6rk7tq5avpjmpudgut4vkhda5m8eetlzpqd6mrcr6u2022tdwfssfsra5x
			// mnemonic = "comfort very add tuition senior run eight snap burst appear exile dutch"

			// tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g
			// mnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
			_, err := manager.ImportWallet(mnemonic, "123456")
			if err != nil {
				t.Fatalf("ImportWallet failed. %v", err)
			}
		} else {
			mnemonic := ""

			mnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
			// mnemonic = "faith fluid swarm never label left vivid fetch scatter dilemma slight wear"
			// mnemonic = "remind effort case concert skull live spoil obvious finish top bargain age"
			// mnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
			_, err := manager.ImportWallet(mnemonic, "123456")
			if err != nil {
				t.Fatalf("ImportWallet failed. %v", err)
			}
		}
	}

	// tb1p62gjhywssq42tp85erlnvnumkt267ypndrl0f3s4sje578cgr79sekhsua
	// nodeId: 03258dd933765d50bc88630c6584726f739129d209bfeb21053c37a3b62e7a4ab1
	// pkscript: 5120d2912b91d0802aa584f4c8ff364f9bb2d5af103368fef4c61584b34f1f081f8b

	fmt.Printf("address: %s\n", manager.GetWallet().GetAddress())
	pkScript, _ := GetP2TRpkScript(manager.GetWallet().GetPaymentPubKey())
	fmt.Printf("pkscript: %s\n", hex.EncodeToString(pkScript))
	fmt.Printf("nodeId: %s\n", hex.EncodeToString(manager.GetWallet().GetNodePubKey().SerializeCompressed()))

	return manager
}

func prepare(t *testing.T) {
	err := os.RemoveAll("../db")
	if err != nil {
		t.Fatalf("RemoveAll failed: %v\n", err)
	}

	indexer.CHAIN = _test_chain

	_client = createNode(t, "client", "../db/clientDB")
	_client2 = createNode(t, "client2", "../db/client2DB")
}

func TestPsbt(t *testing.T) {
	prepare(t)

	// input address: tb1pttjr9292tea2nr28ca9zswgdhz0dasnz6n3v58mtg9cyf9wqr49sv8zjep
	psbtStr := "70736274ff01005e020000000114fe1543a720f178c472ad179224c268c287438477cba59d76b93e1fec4d68170100000000ffffffff0170170000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b000000000001012bb80b0000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b010304830000000000"
	signed, err := _client.SignPsbt(psbtStr, false)
	if err != nil {
		t.Fatal()
	}
	fmt.Printf("%s\n", signed)

	packet, err := toPsbt(signed)
	if err != nil {
		t.Fatal()
	}
	// 014112710444510b91aebabc1c7a01dfb732ddc178cbe9df5aa757667129acdf435801d3c3c5b22e5d536304bab4b4531e26b4c78ed7b31df6449a215cc91df4e4bf83
	fmt.Printf("%s\n", hex.EncodeToString(packet.Inputs[0].FinalScriptWitness))

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
}

func TestPsbt_SatsNet(t *testing.T) {
	prepare(t)

	psbtStr := "70736274ff0100fd1e0101000000042a4b6c10fe9b369817650d222d9d7212abde1c9477b54ffdbd7e3e6626357fee0100000000ffffffff9400974c50a5bb30f389e25696279f388ccf17a3d29a0506f8d8cb86895dcf150100000000ffffffff3f32941e8c34679cae707e31efa5ee8b39a0f4f10b748f70f48eb49c403148ca0100000000ffffffff2a4b6c10fe9b369817650d222d9d7212abde1c9477b54ffdbd7e3e6626357fee0200000000ffffffff02200300000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380c0300000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380000000000010144900100000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380001012ce80300000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380001012c640000000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380001012c5a0000000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb38000000"
	signed, err := _client.SignPsbt_SatsNet(psbtStr, false)
	if err != nil {
		t.Fatal()
	}
	fmt.Printf("%s\n", signed)

	hexBytes, _ := hex.DecodeString(signed)
	packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
	if err != nil {
		t.Fatal()
	}

	err = spsbt.MaybeFinalizeAll(packet)
	if err != nil {
		Log.Errorf("MaybeFinalizeAll failed, %v", err)
		t.Fatal()
	}

	finalTx, err := spsbt.Extract(packet)
	if err != nil {
		Log.Errorf("Extract failed, %v", err)
		t.Fatal()
	}
	PrintJsonTx_SatsNet(finalTx, "")

	prevFectcher := PsbtPrevOutputFetcher_SatsNet(packet)
	err = VerifySignedTx_SatsNet(finalTx, prevFectcher)
	if err != nil {
		Log.Errorf("VerifySignedTx_SatsNet failed, %v", err)
		t.Fatal(err)
	}

	txHex, err := EncodeMsgTx_SatsNet(finalTx)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("TX: %s\n", txHex)

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
	if err != nil {
		Log.Errorf("VerifySignedTx_SatsNet failed, %v", err)
		t.Fatal()
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

	// assset := indexer.DisplayAsset{
	// 	AssetName: AssetName{
	// 		Protocol: "ordx",
	// 		Type: "f",
	// 		Ticker: "rarepizza",
	// 	},
	// 	Amount: "400",
	// 	Precision: 0,
	// 	BindingSat: 1,
	// 	Offsets: nil,
	// }

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
			Precision: 0,
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

	// assset := indexer.DisplayAsset{
	// 	AssetName: AssetName{
	// 		Protocol: "ordx",
	// 		Type: "f",
	// 		Ticker: "rarepizza",
	// 	},
	// 	Amount: "400",
	// 	Precision: 0,
	// 	BindingSat: 1,
	// 	Offsets: nil,
	// }
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
	prepare(t)

	pkScript, _ := GetP2TRpkScript(_client.GetWallet().GetPaymentPubKey())
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

	sellerAddr := _client.wallet.GetAddress()

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

	sellPsbt := psbts[0]

	signedSellPsbt, err := _client.SignPsbt_SatsNet(sellPsbt, false)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SignPsbt_SatsNet: %s", signedSellPsbt)

	// buyer
	buyerAddr := _client2.wallet.GetAddress()
	pkScript2, _ := GetP2TRpkScript(_client2.GetWallet().GetPaymentPubKey())
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

	signedFinalPsbt, err := _client2.SignPsbt_SatsNet(finalPsbt, false)
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

	prevFectcher := PsbtPrevOutputFetcher_SatsNet(finalPacket)
	err = VerifySignedTx_SatsNet(finalTx, prevFectcher)
	if err != nil {
		Log.Errorf("VerifySignedTx_SatsNet failed, %v", err)
		t.Fatal()
	}

}


func TestChangePassword(t *testing.T) {
	prepare(t)

	oldPS := "123456"
	newPS := "abcdefgh"

	id := _client.status.CurrentWallet
	err := _client.ChangePassword(oldPS, newPS)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s\n", _client.GetMnemonic(id, oldPS))
	fmt.Printf("%s\n", _client.GetMnemonic(id, newPS))
}

func TestGetTxAssetInfoFromPsbt(t *testing.T) {
	prepare(t)

	// 部分签名的psbt
	psbtStr := "70736274ff01005e0200000001f18974422078033fbd084167f518ca0bae7d82eac3f492a84117eb94684e9abd0000000000ffffffff017602000000000000225120237771c78df654746be0f191b48c4acc782331cf1756c7b4fde5dd3eac43075d000000000001012bb701000000000000225120237771c78df654746be0f191b48c4acc782331cf1756c7b4fde5dd3eac43075d010304830000000000"

	// 完整的psbt
	// psbtStr := ""

	info, err := _client.GetTxAssetInfoFromPsbt(psbtStr)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%v\n", info)
}


func TestVerifyTx(t *testing.T) {
	prepare(t)

	txHex := "010000000001032f84604f0cb0991560925fcbf15c427ffd605a002800f1cd5f320a905904f46a0100000000ffffffff12cf5725e9116b8747c127fe15b4d69495a6f28b406defb7763ec99091d61dd50100000000ffffffff87790051681ed948664dfa6730eda17eae1656fc8ad36d407f6193ffbfa2eea50000000000ffffffff04940200000000000022002080631a064208b6d6df4b3762f59d12fb0f6e0f21bb57370aaa4f8866fba918d34a01000000000000225120237771c78df654746be0f191b48c4acc782331cf1756c7b4fde5dd3eac43075d0c8a00000000000022002080631a064208b6d6df4b3762f59d12fb0f6e0f21bb57370aaa4f8866fba918d30000000000000000106a5d0d00cffc030180c0f0978ef841010140552fd1c2c8ed3f240838f4455d4bee71c25426f0aa489e3628b2916700819d33f86685c1ce49f21bdea2a9f79a7f3d4e6a1d52baa8ed1854ba5712d17fb376c704004730440220418c89fdb2aff1de499e9640993a8986c9aaa0d689f6f2911bcaffd279f7e1eb02206d08fc8b32321fae4abe6329768ef0cf4a9c8d8d12d8371914b97890d442219701483045022100edb67dc62f0a591ee3195ff0625c1b3b09c3b16d16c53bb2a94e9d660c89607e02202433e439959a5ae431f50c30edb78ec3bad46c6ad11561eacdf1f6a6cf900402014752210331f024cdf284a7cc50769f0716c5efa8e37ac494db09f2d20e7008c5413def72210367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e52ae01401c7d41caccdd0717a633568157787ae39a09e46bbe210563a12d4d587ab2d105aaf85f7f49b4b7781a3920a00e25335c035001533a7d2f3fa095c51bf7c2be4000000000"

	tx, err := DecodeMsgTx(txHex)
	if err != nil {
		t.Fatal(err)
	}
	PrintJsonTx(tx, "tx")

	missingInputs := make([]string, 0)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for _, txIn := range tx.TxIn {
		utxo := txIn.PreviousOutPoint.String()
		txOut, err := _client.GetTxOutFromRawTx(utxo)
		if err != nil {
			missingInputs = append(missingInputs, utxo)
			continue
		}

		prevFetcher.AddPrevOut(txIn.PreviousOutPoint, &txOut.OutValue)
	}

	fmt.Printf("missing utxos: %v\n", missingInputs)

	if len(missingInputs) == 0 {
		fee, err := VerifySignedTxV2(tx, prevFetcher)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("fee: %d\n", fee)
		fmt.Printf("tx vsize: %d\n", GetTxVirtualSize2(tx))
	}

}


// func TestDeployContract_ORDX_Remote(t *testing.T) {
// 	prepare(t)

// 	supportedContracts, err := _client.GetSupportContractInServer()
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	fmt.Printf("supported contracts: %v\n", supportedContracts)

// 	deployedContracts, err := _client.GetDeployedContractInServer()
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	fmt.Printf("deployed contracts: %v\n", deployedContracts)

// 	assetName := &AssetName{
// 		AssetName: swire.AssetName{
// 			Protocol: "ordx",
// 			Type:     "f",
// 			Ticker:   "testTicker",
// 		},
// 		N: 1000,
// 	}
// 	launchPool := NewLaunchPoolContract()
// 	launchPool.AssetName = assetName.AssetName
// 	launchPool.BindingSat = assetName.N
// 	launchPool.MintAmtPerSat = assetName.N
// 	launchPool.Limit = 10000000
// 	launchPool.LaunchRatio = 70
// 	launchPool.MaxSupply = 40000000

// 	fmt.Printf("contract: %s\n", launchPool.Content())

// 	str := `{"contractType":"launchpool.tc","startBlock":0,"endBlock":0,"assetName":{"Protocol":"ordx","Type":"f","Ticker":"round2"},"mintAmtPerSat":100,"limit":100000,"maxSupply":10000000,"launchRation":70,"bindingSat":1000}`
// 	var launchPool2 LaunchPoolContract
// 	err = json.Unmarshal([]byte(str), &launchPool2)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	fmt.Printf("contract2: %s\n", launchPool2.Content())

// 	deployFee, err := _client.QueryFeeForDeployContract(TEMPLATE_CONTRACT_LAUNCHPOOL, (launchPool2.Content()), 1)
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	fmt.Printf("deploy contract %s need %d sats\n", TEMPLATE_CONTRACT_LAUNCHPOOL, deployFee)
// 	fmt.Printf("use RemoteDeployContract to deploy a contract on core channel in server node\n")

// 	invokeParam, err := _client.QueryParamForInvokeContract(TEMPLATE_CONTRACT_LAUNCHPOOL, "")
// 	if err != nil {
// 		t.Fatal(err)
// 	}
// 	fmt.Printf("use %s as template to invoke contract %s\n", invokeParam, TEMPLATE_CONTRACT_LAUNCHPOOL)
// }