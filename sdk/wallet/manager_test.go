package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/sat20-labs/indexer/common"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var _client *Manager
var _client2 *Manager

func newTestConf(mode, dbPath string) *Config {
	chain := "testnet4"
	ret := &Config{
		Chain: chain,
		Log:   "debug",
		DB:    dbPath,
	}

	return ret
}

func createNode(t *testing.T, mode, dbPath string) *Manager {
	cfg := newTestConf(mode, dbPath)
	db := NewKVDB(cfg.DB)
	if db == nil {
		t.Fatalf("NewKVDB failed")
	}
	manager := NewManager(cfg.Chain, db)

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
			//mnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
			// mnemonic = "faith fluid swarm never label left vivid fetch scatter dilemma slight wear"
			// mnemonic = "remind effort case concert skull live spoil obvious finish top bargain age"
			mnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
			_, err := manager.ImportWallet(mnemonic, "123456")
			if err != nil {
				t.Fatalf("ImportWallet failed. %v", err)
			}
		} else {
			mnemonic := "comfort very add tuition senior run eight snap burst appear exile dutch"
			//mnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
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

	_client = createNode(t, "client", "../db/clientDB")
	_client2 = createNode(t, "client2", "../db/client2DB")
}

func TestPsbt(t *testing.T) {
	prepare(t)

	psbtStr := "70736274ff0100890200000001d71f33336e92a9e2a794c8a77ffd3b846c335bfc6ac2a0eb026e96d61a04b7220100000000fdffffff025a0b0000000000002251202fad5b1f0dfa1111ca54fb636e030846bd731dca4f2b7af48d8e5b9672d90b25ff630000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b000000000001012b94790000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b011720d210be04396837b11f65eb42527de3f6a1c1c1d51de38ee907fc355c56ee5115000000"
	signed, err := _client.SignPsbt(psbtStr, false)
	if err != nil {
		t.Fatal()
	}
	fmt.Printf("%s\n", signed)

	packet, err := toPsbt(signed)
	if err != nil {
		t.Fatal()
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
	psbtStr := "70736274ff01005f0100000001c2705e468dab059f54430bba8948f73099b3106b8f882d8e1799bdaa3786ea060000000000ffffffff010500000000000000002251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba0000000000010144640000000000000001046f72647801660970697a7a6174657374053130303a30012251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19eba01030483000000011341de619f109eec4ad8c9d7f59d6233281f077f8c40944837ebabd1363974f45281099bf5ac0abfaa64014e7649b41ca8982c5f8a3801d12176c10265dd10129226830000"
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

func TestBuildOrder(m *testing.T) {

	// assset := common.DisplayAsset{
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
		AssetsInUtxo: common.AssetsInUtxo{
			UtxoId:   1030792413185,
			OutPoint: "ee7f3526663e7ebdfd4fb577941cdeab12729d2d220d651798369bfe106c4b2a:1",
			Value:    10000,
			PkScript: []byte("USBmGjbRHN3OJU7Y44vUbF7Oh71vqRPudPlNcHWRyBfLOA=="),
			Assets:   nil,
		},
		Price: 800,
		AssetInfo: &common.AssetInfo{
			Name: common.AssetName{
				Protocol: "ordx",
				Type:     "f",
				Ticker:   "rarepizza",
			},
			Amount:     *common.NewDecimal(100, 0),
			BindingSat: 1,
		},
	}

	utxo, _ := json.Marshal(info)
	fmt.Printf("%s\n", string(utxo))

	BuildBatchSellOrder_SatsNet([]string{string(utxo)}, "tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv", "testnet")
}

func TestFinalizeOrder(t *testing.T) {

	// assset := common.DisplayAsset{
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
	assset := common.DisplayAsset{
		AssetName: AssetName{
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
		AssetsInUtxo: common.AssetsInUtxo{
			UtxoId:   1030792413185,
			OutPoint: "ee7f3526663e7ebdfd4fb577941cdeab12729d2d220d651798369bfe106c4b2a:1",
			Value:    100,
			PkScript: pkScript,
			Assets:   []*common.DisplayAsset{&assset},
		},
		Price: 6000,
		// AssetInfo: &common.AssetInfo{
		// 	Name: common.AssetName{
		// 		Protocol: "ordx",
		// 		Type:     "f",
		// 		Ticker:   "rarepizza",
		// 	},
		// 	Amount:     *common.NewDecimal(100, 0),
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
		AssetsInUtxo: common.AssetsInUtxo{
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
