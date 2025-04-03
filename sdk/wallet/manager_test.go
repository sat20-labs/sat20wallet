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
)

var _client *Manager
var _client2 *Manager

func newTestConf(mode, dbPath string) *Config {
	chain := "testnet4"
	ret := &Config{
		Chain: chain,
		Mode:  mode,
		Btcd: Bitcoin{
			Host:           "192.168.10.102:28332",
			User:           "jacky",
			Password:       "123456",
			Zmqpubrawblock: "tcp://192.168.10.102:58332",
			Zmqpubrawtx:    "tcp://192.168.10.102:58333",
		},
		IndexerL1: Indexer{
			Scheme: "http",
			Host:   "127.0.0.1:8009",
		},
		IndexerL2: Indexer{
			Scheme: "http",
			Host:   "127.0.0.1:8019",
		},
		Log: "debug",
		DB:  dbPath,
	}

	return ret
}

func createNode(t *testing.T, mode, dbPath string, quit chan struct{}) *Manager {
	cfg := newTestConf(mode, dbPath)
	manager := NewManager(cfg, quit)

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

	fmt.Printf("address: %s\n", manager.GetWallet().GetAddress(0))
	pkScript, _ := GetP2TRpkScript(manager.GetWallet().GetPaymentPubKey())
	fmt.Printf("pkscript: %s\n", hex.EncodeToString(pkScript))
	fmt.Printf("nodeId: %s\n", hex.EncodeToString(manager.GetWallet().GetNodePubKey().SerializeCompressed()))

	return manager
}

func prepare(t *testing.T) {

	lc := make(chan struct{})
	err := os.RemoveAll("../db")
	if err != nil {
		t.Fatalf("RemoveAll failed: %v\n", err)
	}

	_client = createNode(t, "client", "../db/clientDB", lc)
	_client2 = createNode(t, "client2", "../db/client2DB", lc)
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
	packet, err :=  spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
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
		t.Fatal()
	}

}


func toPsbt(psbtHex string) (*psbt.Packet, error) {
	hexBytes, _ := hex.DecodeString(psbtHex)
	return psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
}

func TestVerifyPsbtString(t *testing.T) {
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
}


func TestVerifyPsbtString_satsnet(t *testing.T) {
	psbtStr := "70736274ff0100fd4a0101000000042a4b6c10fe9b369817650d222d9d7212abde1c9477b54ffdbd7e3e6626357fee0100000000ffffffff9400974c50a5bb30f389e25696279f388ccf17a3d29a0506f8d8cb86895dcf150100000000ffffffff3f32941e8c34679cae707e31efa5ee8b39a0f4f10b748f70f48eb49c403148ca0100000000ffffffff2a4b6c10fe9b369817650d222d9d7212abde1c9477b54ffdbd7e3e6626357fee0200000000ffffffff03200300000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380c0300000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380000000000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b0000000000010144900100000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb38011340f5ee347b642ba06a2c45ca667bace0cd6cda1f94679faa634ecc60e0f476430873f66b7819cbabc9cc14269182bfb984a3dfca01d77aa3f81bf4b56be2bf5a410001012ce80300000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb3801030401000000011340cb9b9f356b3edb80489702e7a5d63301936d5872ed7084c047b692e7e3760c68e3e5d7923d00c8ea5c02e9e2624392918c2170fed3da76de937f2ec2c7b0d5450001012c640000000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380103040100000001134024791b634549dc25d8ee4b3604604aaf64354784f0ce5c09020948f8668d56a9b8d17e0b04bf4177f481a313254039092bb52eb5ad38f6487b3aefad0db0be3c0001012c5a0000000000000000225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb3801030401000000011340710b30193db2ed4bd92a9bebf5063bd80fcda3b9e29942d8f12a42a6ebfb64ca07d5d43cd1d63fa42149c96a87042ea9ed83c93a8a58ded003d77d642ad9873c00000000"
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

	BuildBatchSellOrder([]string{string(utxo)}, "tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv", "testnet")
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

	finalPsbt, err := FinalizeSellOrder(psbt, utxos, 
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
	result, err := SplitBatchSignedPsbt(psbt, "testnet")
	if err != nil {
		t.Fatal(err)
	}
	for _, psbt := range result {
		fmt.Printf("%s\n", psbt)
	}
}

func TestVerifySignedTx_SatsNet(t *testing.T) {
	txHex := "0100000000010c2e17fa0d036db6a813112330b648cb7cbc6990d1fbf8d1cff24f1611e75695460000000000ffffffff388f09bffc0b7ce08fd7591aeba2cbb9dbd08be753b92c200e7708d43f571d710100000000ffffffff1774368f86eef6fa5b96a71e3e7992e13ab6dfb39250884b5d47a4c3e63c43b50100000000ffffffff1eb0a1d7e09e630bb58199ac5045bd73687b2b3a906d6f018229e23bb45c09610000000000ffffffff81be784da7280d2e71e27c477bbd623d49fc201fdde94084347fdee7241237270100000000fffffffff834a000e1f5da16895f772722822e44fea1008632a92c9f38369c569c15b6c90100000000ffffffff28e2348685183d48eba46bad7f9bfc1808285351470f8d9c54136e84d378bce40100000000ffffffff7fbbd5d271527729b5d39e7511de13d33f60186c0ab6ac9836c7662f3c84d7360100000000ffffffffc91c2833eac3ca7209ec333b6be27a23d8d99c2ebede55fef23eae11a7b9a22b0100000000ffffffff1774368f86eef6fa5b96a71e3e7992e13ab6dfb39250884b5d47a4c3e63c43b50000000000ffffffff388f09bffc0b7ce08fd7591aeba2cbb9dbd08be753b92c200e7708d43f571d710000000000fffffffffd74b635d1cd89df4379535c2f99354f97f055bb063261c7cbfb521d8ed436a60100000000ffffffff03e803000000000000002251208c4a6b130077db156fb22e7946711377c06327298b4c7e6e19a6eaa808d19ebad60b00000000000001046f726478016607646f67636f696e053130303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380a00000000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b01419b28a3ee4f459d651f3c080d94e5bb184c44de7360e72339f114927e98d3e38fa2c5b60d863c258d5b349407e0e082fb0e8651eaeae1fd7015b17b81a6d8b2b48301402cacf82165f2610d48143aee4502cb88b62d67abdc24c4bea02e18a29f2f7d56c593f07b522d688566290b3646f3e636ff8708f04344e1945549b7006902539a01405b55593af4b8882887f7dfc4313ffa46de6f7d25c585b2157fed553a5c48adb94e7eba3a665c89a0239b5a38c4abbd21ee3d90a68e8edbf0374030081f38a6210140e74afd4718f8a4c3405490957e62796c16df71fea928926924a355c2fa796f394d7f3571bd35227a5d54ca855c5738fd813e4757c3082f3a8994e351e0a1eda30140509454795a532b2a04bbac270720b16f5f01e1f46180678d2aa5716a79ad9e88e882f77296544ac16cfd8440711455f8bee0fb9879d4933ab9e7b644ff711a5d0140da7a17dcd7ba2c9eaa3f5d2bfe7bb07b40f0f832bfd7cff28abffa527603b93fb311d136ba40e3f9eeaefb802851faaebba3d89baca18adfd3d6c0b90b8dcd050140368bd1ea1e51a11900e40d18dd6a6f15b4e81d26bb2d9306a0c829a2a020e9f0a8c70c070838c7a1f7d1bdfe5368f53794ba7f58b270f35d3ea840fcf179604f0140ae7453635e6f7a134fb47e733ab5d7977d1c132680e569dd1b16a907464e128c97def818e9b22ad81e31df039a5f5ee7c49ecaa3d06386f33caa5125e5a2816c0140dae2f65285a200d033d94d5caad6d59b2e34b5d1ab7f03d9a5cbd2ecb2653236c6ff1bd1be153027d2f917d10917ec215df797f3289223de2670621eba7f798a01402b2c5ca14e6c31ebd2dd6f4d519062902b76b2d223278281eb1511421ad564b345f2750eca4003ddba142908ae103808e9b5bb6f61c019ad0720302952d99d880140f27dd47c2447eacf4be5c35add6bffda371108e1b9d1188a50e9172a0e0959af41413630adb1897864f8c416264d13b7dceb0c36ac43f6c4ada040acae1a2e950140c7317c929957f6ce46f15aec8507d8ca7c67891a6ddb63c762be56dabb15ff44fabc6996998443b32c06e4626d3395b90bfc599e6c6e0c36a4859c656aea6af000000000"
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
			Type: "f",
			Ticker: "rarepizza",
		},
		Amount: "100",
		Precision: 0,
		BindingSat: 1,
		Offsets: nil,
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

	sellerAddr := _client.wallet.GetAddress(0)

	psbt, err := BuildBatchSellOrder([]string{string(utxo)}, 
		sellerAddr, "testnet",
	)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("BuildBatchSellOrder: %s", psbt)

	psbts, err := SplitBatchSignedPsbt(psbt, "testnet")
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
	buyerAddr := _client2.wallet.GetAddress(0)
	pkScript2, _ := GetP2TRpkScript(_client2.GetWallet().GetPaymentPubKey())
	info2 := UtxoInfo{
		AssetsInUtxo: common.AssetsInUtxo{
			UtxoId:   3985729912833,
			OutPoint: "84b9d49a9be9732ffa619a12cbf6dfb6f6e07a588bd7f7003f6bb8d85243482c:1",
			Value:    10000,
			PkScript: pkScript2,
			Assets:   nil,
		},
		Price: 0,
		AssetInfo: nil,
	}
	utxo2, _ := json.Marshal(info2)
	fmt.Printf("%s\n", string(utxo2))


	utxos := []string{
		string(utxo2),
	}

	finalPsbt, err := FinalizeSellOrder(signedSellPsbt, utxos, 
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
