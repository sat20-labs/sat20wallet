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
)

var _client *Manager

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
		mnemonic := ""

		//mnemonic = "acquire pet news congress unveil erode paddle crumble blue fish match eye"
		mnemonic = "faith fluid swarm never label left vivid fetch scatter dilemma slight wear"
		_, err := manager.ImportWallet(mnemonic, "123456")
		if err != nil {
			t.Fatalf("ImportWallet failed. %v", err)
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

func toPsbt(psbtHex string) (*psbt.Packet, error) {
	hexBytes, _ := hex.DecodeString(psbtHex)
	return psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
}

func TestVerifyPsbtString(t *testing.T) {
	psbtStr := "70736274ff0100890200000001d71f33336e92a9e2a794c8a77ffd3b846c335bfc6ac2a0eb026e96d61a04b7220100000000fdffffff025a0b0000000000002251202fad5b1f0dfa1111ca54fb636e030846bd731dca4f2b7af48d8e5b9672d90b25ff630000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b000000000001012b94790000000000002251205ae432a8aa5e7aa98d47c74a28390db89edec262d4e2ca1f6b41704495c01d4b0113407da5f3313247877d7820cc5ef80de3895ff93fa40924744ed9fe834f71efd79aa99894be089ad1c7c14589828542271862413f4e6e12fa387a9b9d4b5fed4cf4011720d210be04396837b11f65eb42527de3f6a1c1c1d51de38ee907fc355c56ee5115000000"
	packet, err := toPsbt(psbtStr)
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

func TestSplitBatchSignedPsbt(t *testing.T) {
	psbt := "70736274ff01007701000000012a4b6c10fe9b369817650d222d9d7212abde1c9477b54ffdbd7e3e6626357fee0100000000ffffffff01200300000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380000000000010144900100000000000001046f7264780166097261726570697a7a61053430303a3001225120661a36d11cddce254ed8e38bd46c5ece87bd6fa913ee74f94d707591c817cb380103048300000001134062387e222f742ea1d6685adc9b9ee2d03c06167b4cfe4802c4eee0ac7013729c775604897d30ddd97b48156a7a07619e13eccd241572b54309cae7dbca09384c0000"
	result, err := SplitBatchSignedPsbt(psbt, "testnet")
	if err != nil {
		t.Fatal()
	}
	for _, psbt := range result {
		fmt.Printf("%s\n", psbt)
	}
}
