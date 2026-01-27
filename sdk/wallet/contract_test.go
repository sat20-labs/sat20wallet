package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func sendInBackground(run *bool) {
	addr := _client.wallet.GetAddress()
	_, err := _server.SendAssets(addr, ASSET_PLAIN_SAT.String(), "100000", 0, nil)
	if err != nil {
		fmt.Printf("SendAssets failed, %v", err)
		os.Exit(-1)
	}
	_, err = _server.SendAssets_SatsNet(addr, ASSET_PLAIN_SAT.String(), "10000", nil)
	if err != nil {
		fmt.Printf("SendAssets failed, %v", err)
		os.Exit(-1)
	}
	for *run {
		_client.SendAssets(addr, ASSET_PLAIN_SAT.String(), "330", 0, nil)
		_client.SendAssets_SatsNet(addr, ASSET_PLAIN_SAT.String(), "10", nil)
		time.Sleep(2 * time.Second)
	}
}

func waitContractReady(url string, stp *Manager) error {
	ct := ExtractContractType(url)
	i := 0
	for {
		status, err := stp.GetContractStatusInServer(url)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		//fmt.Printf("%s\n", status)
		contractRuntime := NewContractRuntime(stp, ct)
		err = json.Unmarshal([]byte(status), contractRuntime)
		if err != nil {
			return err
		}
		if contractRuntime.IsReady() {
			break
		}

		i++
		if i == 100 {
			fmt.Printf("%v\n", contractRuntime)
			Log.Panic("")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// 主网地址转为测试网地址
func convertAddr(addr string) (string, error) {
	address, err := btcutil.DecodeAddress(addr, &chaincfg.MainNetParams)
	if err != nil {
		return "", err
	}
	pkScript, err := txscript.PayToAddrScript(address)
	if err != nil {
		return "", err
	}
	_, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, &chaincfg.TestNet4Params)
	if err != nil {
		return "", err
	}

	if len(addresses) == 0 {
		return "", fmt.Errorf("can't generate address")
	}

	return addresses[0].EncodeAddress(), nil
}

// 用导出的交易历史记录，重现整个交易过程。注意需要将remote端的AllowPeerAction直接返回nil，才能正确签名走完流程
func TestLaunchPoolContract(t *testing.T) {
	prepare(t)

	runningData := `{"TotalMinted":{"Precision":0,"Value":15000000},"TotalInvalid":1,"AssetAmtInPool":{"Precision":0,"Value":6000000},"SatsValueInPool":-5,"TotalInputAssets":{"Precision":0,"Value":21000000},"TotalInputSats":16,"TotalOutputAssets":{"Precision":0,"Value":15000000},"TotalOutputSats":1,"IsLaunching":false,"LaunchTxIDs":["6369c367b0be5514425efcb1378ae68462d7e03fa333f25050959b0ae7e3ff87"],"RefundTxIDs":["3b72397d9f077f82b1e9df3cea870c2bf41b7d08596b7b6d25dac30cdd64cc24"],"AmmContractURL":"","AmmResvId":0}`
	var realRunningData SwapContractRunningData
	err := json.Unmarshal([]byte(runningData), &realRunningData)
	if err != nil {
		t.Fatal(err)
	}

	mainnetAddr := true
	itemHistory := `[{"Version":1,"Id":1,"Reason":"","Done":1,"OrderType":8,"UtxoId":25391846916096,"OrderTime":1755177540,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pzq4ag2uw2z6m4wlh0x2qza7atyq7edde40mvn4cd66k0e8n44v0s7dqunj","FromL1":false,"InUtxo":"c4ce3f09a1b18f2c76684c5a4b5463516b7cea95457597992387b541ea9d0740:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":0,"Reason":"","Done":1,"OrderType":8,"UtxoId":25151328747520,"OrderTime":1755171352,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pm29wefnlwmq8pvx897ufggr455yjwq45cesa9gedzn85s4qmcshq56qn4t","FromL1":false,"InUtxo":"6cbae428d013b9654fa88a7ffa4e5626939a57707b78a8d037a839695eebcbb2:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":4,"Reason":"","Done":1,"OrderType":8,"UtxoId":25494926131200,"OrderTime":1755177660,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pmsw59znjnza5c39p0edl2ypa820l4q5jah8x0lpkuh0scv9e974sla0p9e","FromL1":false,"InUtxo":"b28d82a3b0d9f13e6440e1a0352516992781fa9875fe44bdc2ebdc1c7471be23:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":3,"Reason":"","Done":1,"OrderType":8,"UtxoId":25460566392832,"OrderTime":1755177600,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pct5rtqvnxdn6v8rp0dezsn38u046walrkxh4ar0wth0jan2ljdxqv7hakc","FromL1":false,"InUtxo":"41e48c5be57c8e2f382b2849b8da9363b1ec560d40f0dc1e4e30889e2c829af5:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":2,"Reason":"","Done":2,"OrderType":8,"UtxoId":25426206654464,"OrderTime":1755177552,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pzq4ag2uw2z6m4wlh0x2qza7atyq7edde40mvn4cd66k0e8n44v0s7dqunj","FromL1":false,"InUtxo":"3ae09036b5d2000a54f1818af3acffd7a47bf55e3690a0e5c6c3e6bc47396956:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":1},{"Version":1,"Id":5,"Reason":"","Done":1,"OrderType":8,"UtxoId":26113401421824,"OrderTime":1755185704,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p2vlv80jmfa4adhjtwr2yhza2hzenk6w0jk5c67pxg3s5tvajz79qqvs05m","FromL1":false,"InUtxo":"ec50070cbf554d2c9c2a31d228376f7eb839482cc8f0fa35ac49ccb0df89aa12:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":6,"Reason":"","Done":1,"OrderType":8,"UtxoId":26147761160192,"OrderTime":1755185896,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pe6huqfgeagufcpcmsjyljdr7fzcfdla2675jz443endk09y6l7dqerh3wg","FromL1":false,"InUtxo":"594b8c8127fd538916512986301de474d8cbf9853024e63966b4e1ae2401c724:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":7,"Reason":"","Done":1,"OrderType":8,"UtxoId":26182120898560,"OrderTime":1755185932,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p58kd3rw7jwmze506upn7u9ttuzkn6edlx4nkwe3s2rzcceeqexwsn8z04a","FromL1":false,"InUtxo":"a0008f5e3b318b3a9dc40a43165ee2bf647e60924e9a4a859e6a0b0bca573af7:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":8,"Reason":"","Done":1,"OrderType":8,"UtxoId":26216480636928,"OrderTime":1755185992,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pxcufana97el7wnxjnru3djvcluc4h3v422l0nkxweclg86pcaz7qhk875u","FromL1":false,"InUtxo":"5de2fb3f6598f527c1432bba7aeeada7de57436f51eb1d0db2d887f99c142f6f:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":9,"Reason":"","Done":1,"OrderType":8,"UtxoId":26422639067136,"OrderTime":1755187276,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pnumea7cs3f7vl0p7d3676mh9e7m4nr2r8rmhj0cqzae4t5t33rrq42gstt","FromL1":false,"InUtxo":"471d54a69bce6233b035097af2b1a5afc14f229ac5179aeb8b01e5f5a2d79ec9:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":10,"Reason":"","Done":1,"OrderType":8,"UtxoId":26456998805504,"OrderTime":1755187294,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p7cnl5t6r5kauwx5fxll85u7a8ra2zrzkka8xg6hfkvdms3evh6vqgqvquq","FromL1":false,"InUtxo":"a0e9657a432331401e2bbe30fb0ee4dd6cfb1f7c546a3af9ceb399e510fd4ff9:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":11,"Reason":"","Done":1,"OrderType":8,"UtxoId":26491358543872,"OrderTime":1755187312,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1ptlm6a3a8hz6xlqnjt4tfzw48tuaqsw486d5hq23gw67xg5un5e6sg9utsr","FromL1":false,"InUtxo":"4b7c019325e6da1e50b37c970003d39df285ff07c8d579fc5a189443f481263f:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":12,"Reason":"","Done":1,"OrderType":8,"UtxoId":26525718282240,"OrderTime":1755187324,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pur8pjn3skuf388jqyyr9vkqq4dsdku35yup2n62430mdeugxa0qqlv3qq5","FromL1":false,"InUtxo":"3ffc842e85977c1ec0d510a85debd3bca35467bfa3eff229e7ee0cf239c6263f:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":13,"Reason":"","Done":1,"OrderType":8,"UtxoId":26560078020608,"OrderTime":1755187342,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p9h0v3ds8t558gwh9mewfclw873v8ven0k5skchw3hdsgdj7kx0qst5ljak","FromL1":false,"InUtxo":"2d7b533dcf9c3749487c972fba01cf3e40aaa952f6dccfe19587e9a5c1d0bc8c:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":14,"Reason":"","Done":1,"OrderType":8,"UtxoId":26594437758976,"OrderTime":1755187372,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p6anhc73aqz8vdvg7f9fdundm4htzh55lfmjs4a6agp90xdzp695sw3cedl","FromL1":false,"InUtxo":"76c2a3863b9c70dd1eb2dfc5be099d0dfee9ce9ffb24d4b4f765b1e9ee97bc39:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":1,"Id":15,"Reason":"","Done":1,"OrderType":8,"UtxoId":26628797497344,"OrderTime":1755187384,"AssetName":"ordx:f:pizza","ServiceFee":0,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p67rgvec8sln2knzknchuk7pgc4rkzhcccwpmwxxtdqq7vdhpcz9qxspx4h","FromL1":false,"InUtxo":"ca3296118c715c842ab44ffea522711f63764860f3263c7f5d414b612173ba76:0","InValue":1,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0}]`

	var inputs []*InvokeItem
	err = json.Unmarshal([]byte(itemHistory), &inputs)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(inputs, func(i, j int) bool {
		return inputs[i].Id < inputs[j].Id
	})

	assetName := &AssetName{
		AssetName: swire.AssetName{
			Protocol: "ordx",
			Type:     "f",
			Ticker:   "pizza2",
		},
		N: 10000,
	}

	launchPool := NewLaunchPoolContract()
	launchPool.AssetName = assetName.AssetName
	launchPool.AssetSymbol = 0
	launchPool.BindingSat = assetName.N
	launchPool.MintAmtPerSat = 1000000
	launchPool.Limit = 1000000
	launchPool.LaunchRatio = 70
	launchPool.MaxSupply = 21000000

	deployFee, err := _client.QueryFeeForDeployContract(launchPool.TemplateName, (launchPool.Content()), 1)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("deploy contract %s need %d sats\n", launchPool.TemplateName, deployFee)
	fmt.Printf("use RemoteDeployContract to deploy a contract on core channel in server node\n")

	invokeParam, err := _client.QueryParamForInvokeContract(launchPool.TemplateName, INVOKE_API_SWAP)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("use %s as template to invoke contract %s\n", invokeParam, launchPool.TemplateName)

	assetAmt := _server.GetAssetBalance_SatsNet("", &ASSET_PLAIN_SAT)
	fmt.Printf("plain sats: %d\n", assetAmt)
	txId, id, url, err := _client.DeployContract_Remote(launchPool.TemplateName,
		string(launchPool.Content()), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("RemoteDeployContract succeed, %s, %d, %s\n", txId, id, url)

	run := true
	go sendInBackground(&run) // 有时需要更新区块引起合约调用
	err = waitContractReady(url, _client)
	if err != nil {
		t.Fatal(err)
	}
	err = waitContractReady(url, _server)
	if err != nil {
		t.Fatal(err)
	}
	run = false

	channelId := ExtractChannelId(url)
	// txId, err = _client.walletMgr.SendAssetsV3_SatsNet(channelId, launchPool.GetAssetName().String(),
	// 	"100000000", 100000, nil)
	tx, err := _client.SendAssets_SatsNet(channelId, indexer.ASSET_PLAIN_SAT.String(),
		"1000", nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", tx.TxID())

	contractRuntime := _server.GetContract(url)
	if contractRuntime == nil {
		t.Fatal("")
	}
	launch, ok := contractRuntime.(*LaunchPoolContractRunTime)
	if !ok {
		t.Fatal("")
	}

	contractRuntime2 := _server.GetContract(url)
	if contractRuntime2 == nil {
		t.Fatal("")
	}
	launch2, ok := contractRuntime2.(*LaunchPoolContractRunTime)
	if !ok {
		t.Fatal("")
	}

	for _, item := range inputs {
		h, _, _ := indexer.FromUtxoId(item.UtxoId)
		fmt.Printf("|%d-%d", item.Id, h)
	}
	if !bytes.Equal(launch.StaticMerkleRoot, launch2.StaticMerkleRoot) {
		t.Fatal("static merkle root not inconsistent")
	}

	fmt.Printf("size %d\n", len(inputs))
	_not_invoke_block = true // 停止 InvokeWithBlock 和 InvokeWithBlock_SatsNet
	_not_send_tx = true      // 停止广播
	for _, item := range inputs {
		//for i := 179; i < len(inputs); i++ {
		//item := inputs[i]
		fmt.Printf("item: %v\n", item)
		// 去掉处理结果
		item.RemainingAmt = item.InAmt.Clone()
		item.RemainingValue = item.InValue
		item.Done = DONE_NOTYET
		if item.Reason != INVOKE_REASON_INVALID {
			item.Reason = INVOKE_REASON_NORMAL
		}
		if mainnetAddr {
			item.Address, err = convertAddr(item.Address)
			if err != nil {
				t.Fatal(err)
			}
		}

		if item.Id == 14 {
			Log.Infof("")
		}

		h, _, _ := indexer.FromUtxoId(item.UtxoId)
		launch.CurrBlock = h
		launch2.CurrBlock = h

		////////
		// 以下代码模拟正常调用过程
		item2 := item.Clone()
		switch item.OrderType {
		case ORDERTYPE_MINT:
			launch.InvokeCount++
			launch.TotalInputSats += item.InValue
			launch.SatsValueInPool += item.InValue
			if item.OutAmt.Sign() > 0 {
				launch.TotalMinted = launch.TotalMinted.Add(item.OutAmt)
			}
			if item.OutValue != 0 {
				launch.TotalInvalid += item.OutValue
			}
			launch.addItem(item)
			SaveContractInvokeHistoryItem(_client.db, url, item)

			launch2.InvokeCount++
			launch2.TotalInputSats += item2.InValue
			launch2.SatsValueInPool += item2.InValue
			if item2.OutAmt.Sign() > 0 {
				launch2.TotalMinted = launch2.TotalMinted.Add(item2.OutAmt)
			}
			if item2.OutValue != 0 {
				launch2.TotalInvalid += item2.OutValue
			}
			launch2.addItem(item2)
			SaveContractInvokeHistoryItem(_server.db, url, item2)

		default:
			fmt.Printf("invalid type %d\n", item.OrderType)
			t.Fatal()
		}

		if launch2.ReadyToLaunch() &&
			(launch2.Status == CONTRACT_STATUS_READY || launch2.Status == CONTRACT_STATUS_CLOSING) {
			launch2.IsLaunching = true
			launch2.Status = CONTRACT_STATUS_CLOSING
			//saveReservationWithLock(launch2.stp.db, &launch2.resv.ContractDeployDataInDB)

			launch2.launch()
		}

		if launch.ReadyToLaunch() &&
			(launch.Status == CONTRACT_STATUS_READY || launch.Status == CONTRACT_STATUS_CLOSING) {
			launch.IsLaunching = true
			launch.Status = CONTRACT_STATUS_CLOSING
			//saveReservationWithLock(launch.stp.db, &launch.resv.ContractDeployDataInDB)

			launch.launch()
		}

		if !bytes.Equal(launch.CurrAssetMerkleRoot, launch2.CurrAssetMerkleRoot) {
			t.Fatal("asset merkle root not inconsistent")
		}

		if !bytes.Equal(launch.CurrAssetMerkleRoot, launch2.CurrAssetMerkleRoot) {
			t.Fatal("asset merkle root not inconsistent")
		}
		////////

		// 检查每次处理的结果
		err = launch.checkSelf()
		if err != nil {
			t.Fatalf("swap1: %d %v", item.Id, err)
		}

		err = launch2.checkSelf()
		if err != nil {
			t.Fatalf("swap2: %d %v", item2.Id, err)
		}

		// 等待处理
		//time.Sleep(time.Second)
	}

	//
	fmt.Printf("realRunningData: %v\n", realRunningData)
	fmt.Printf("simuRunningData: %v\n", launch.LaunchPoolRunningData)
}

// 用导出的交易历史记录，重现整个交易过程。注意需要将remote端的AllowPeerAction直接返回nil，才能正确签名走完流程
func TestSwapContract(t *testing.T) {
	prepare(t)
	//prepareChannel(t)

	assetName := indexer.NewAssetNameFromString("runes:f:TEST•FIRST•TEST")
	runningData := `{"AssetAmtInPool":{"Precision":0,"Value":44977880},"SatsValueInPool":17147,"LowestSellPrice":null,"HighestBuyPrice":{"Precision":10,"Value":100000000000},"LastDealPrice":{"Precision":10,"Value":5000000},"HighestDealPrice":{"Precision":10,"Value":5000000},"LowestDealPrice":{"Precision":10,"Value":5000000},"TotalInputAssets":{"Precision":0,"Value":222599999},"TotalInputSats":112128,"TotalDealAssets":{"Precision":0,"Value":175030336},"TotalDealSats":87637,"TotalDealCount":121,"TotalDealTx":0,"TotalDealTxFee":0,"TotalRefundAssets":{"Precision":0,"Value":0},"TotalRefundSats":240,"TotalRefundTx":0,"TotalRefundTxFee":0,"TotalProfitAssets":null,"TotalProfitSats":0,"TotalProfitTx":0,"TotalProfitTxFee":0,"TotalDepositAssets":null,"TotalDepositTx":0,"TotalDepositTxFee":0,"TotalWithdrawAssets":null,"TotalWithdrawTx":0,"TotalWithdrawTxFee":0}`
	var realRunningData SwapContractRunningData
	err := json.Unmarshal([]byte(runningData), &realRunningData)
	if err != nil {
		t.Fatal(err)
	}

	mainnetAddr := true
	itemHistory := `[{"Version":0,"Id":55,"Reason":"","Done":0,"OrderType":3,"UtxoId":363148075073536,"OrderTime":1756689051,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pjphxkxcz66yjuhgz4kdzr2acjxathn004eh8l4v9aqfk0a0y5vcsglm85h","FromL1":false,"InUtxo":"42f8caa975ad443c68994b53241dede61c67005f013e9b0e1ad970c653776e7c:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":54,"Reason":"","Done":0,"OrderType":1,"UtxoId":362873197166592,"OrderTime":1756687941,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":1000000000000},"ExpectedAmt":{"Precision":0,"Value":50000},"Address":"bc1pd2877z3w7laa9xygqyctawmhvtly5w6v3vwtncn4328s589qp8uqmzzwd9","FromL1":false,"InUtxo":"1688110b985b779179a45508ab3eae02564f9f2f66423851ea92c288b923af24:0","InValue":10,"InAmt":{"Precision":0,"Value":50000},"RemainingAmt":{"Precision":0,"Value":50000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":53,"Reason":"","Done":0,"OrderType":3,"UtxoId":356276127399936,"OrderTime":1756670439,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pjphxkxcz66yjuhgz4kdzr2acjxathn004eh8l4v9aqfk0a0y5vcsglm85h","FromL1":false,"InUtxo":"53c67e0d8aa93be65124e19a9d63d2160d0bb1c8ddd881694a33772477f068c3:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":52,"Reason":"","Done":0,"OrderType":3,"UtxoId":356241767661568,"OrderTime":1756669941,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pjphxkxcz66yjuhgz4kdzr2acjxathn004eh8l4v9aqfk0a0y5vcsglm85h","FromL1":false,"InUtxo":"3e0a31a3749277a5ae1d98e810d9acf114848bb08cdeb69a20382c7b16a11744:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":51,"Reason":"","Done":0,"OrderType":3,"UtxoId":356207407923200,"OrderTime":1756669923,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pjphxkxcz66yjuhgz4kdzr2acjxathn004eh8l4v9aqfk0a0y5vcsglm85h","FromL1":false,"InUtxo":"9b8fd1171e4a2d779f68fb80bfeef7e846f55a2fe43276270d0a67f72136090f:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":50,"Reason":"","Done":0,"OrderType":1,"UtxoId":351946800365568,"OrderTime":1756658901,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":255000000000},"ExpectedAmt":{"Precision":0,"Value":1000},"Address":"bc1pxfuujjw88l4wcdjglmy43ra00q67xv7gcxkjyalnz9sy9nsx0fxqngn573","FromL1":false,"InUtxo":"a9d98486522887765bc8a591aedafd0aa8eb922f9b67b7313e216a2f3fd89f98:0","InValue":10,"InAmt":{"Precision":0,"Value":1000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":25500,"Padded":null},{"Version":0,"Id":49,"Reason":"","Done":0,"OrderType":3,"UtxoId":331743274467328,"OrderTime":1756645077,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p5e6zpfp62h0u7f63k49ruh2xrg29up0h4qt8hjms64q63lt48s9qn4j2t3","FromL1":false,"InUtxo":"1b786314471cd5f4fd70e1b23404ec2c9be0f73e9b62b4f9b8be6fa1e7a4aaa5:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":48,"Reason":"","Done":0,"OrderType":2,"UtxoId":328960135397376,"OrderTime":1756643265,"AssetName":"ordx:f:pearl","ServiceFee":138,"UnitPrice":{"Precision":10,"Value":320000000000},"ExpectedAmt":{"Precision":0,"Value":500},"Address":"bc1pd69p4e6k7a5ajqh45pct4g79cdl36plhjduyrav3eqpvecju698qsdf64y","FromL1":false,"InUtxo":"d859aeda0cf8bd8541981e6499eaf33937e79f4fef21687e590fa3b65d789a80:0","InValue":16138,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":500},"OutValue":0,"Padded":null},{"Version":0,"Id":47,"Reason":"","Done":0,"OrderType":3,"UtxoId":322603583799296,"OrderTime":1756636497,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p5e6zpfp62h0u7f63k49ruh2xrg29up0h4qt8hjms64q63lt48s9qn4j2t3","FromL1":false,"InUtxo":"0b38c1867aeac2087a6a5d78c6a5764466b20def225d19249072121a8dc8fc3b:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":46,"Reason":"","Done":0,"OrderType":3,"UtxoId":322534864322560,"OrderTime":1756636395,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1p5e6zpfp62h0u7f63k49ruh2xrg29up0h4qt8hjms64q63lt48s9qn4j2t3","FromL1":false,"InUtxo":"ceb9ca65b9fcc9adae58ba4a65db7001dcd9c84033a10c229fd1e95242dd9967:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":45,"Reason":"","Done":0,"OrderType":1,"UtxoId":318308616503296,"OrderTime":1756632765,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":320000000000},"ExpectedAmt":{"Precision":0,"Value":90000},"Address":"bc1ph06afluet3hv39le9z3j6e4cflckx7k779pcxd2a2xdv79ktgrkqs5uyn3","FromL1":false,"InUtxo":"37164400280726489c74c327658813135d480658845871bb9aad601663dc8f74:0","InValue":10,"InAmt":{"Precision":0,"Value":90000},"RemainingAmt":{"Precision":0,"Value":89500},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":16000,"Padded":null},{"Version":0,"Id":44,"Reason":"","Done":0,"OrderType":1,"UtxoId":315559837433856,"OrderTime":1756630929,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":270000000000},"ExpectedAmt":{"Precision":0,"Value":4900},"Address":"bc1pygl00c4etx8lfmqhatp72ca4euc2tzmtzck05z7tgdcfezeg03cq230qzc","FromL1":false,"InUtxo":"08808098edb09890890fe0fd528e3ee5affe16a683d1f822d638502c22aba128:0","InValue":10,"InAmt":{"Precision":0,"Value":4900},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":132300,"Padded":null},{"Version":0,"Id":43,"Reason":"","Done":0,"OrderType":1,"UtxoId":315388038742016,"OrderTime":1756630815,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":270000000000},"ExpectedAmt":{"Precision":0,"Value":597},"Address":"bc1pygl00c4etx8lfmqhatp72ca4euc2tzmtzck05z7tgdcfezeg03cq230qzc","FromL1":false,"InUtxo":"dc797a61c1621ef4259ad6f1e99ed1b4bfbfb40af5b52398c35abea86420dd7b:0","InValue":10,"InAmt":{"Precision":0,"Value":597},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":16119,"Padded":null},{"Version":0,"Id":42,"Reason":"refund","Done":0,"OrderType":2,"UtxoId":309272005312512,"OrderTime":1756626810,"AssetName":"ordx:f:pearl","ServiceFee":982,"UnitPrice":{"Precision":10,"Value":270000000000},"ExpectedAmt":{"Precision":0,"Value":4500},"Address":"bc1p5e6zpfp62h0u7f63k49ruh2xrg29up0h4qt8hjms64q63lt48s9qn4j2t3","FromL1":false,"InUtxo":"e5a9ce2269439ae87749ed1b298dcf3b5db3fae2abbcda89e7d1d0eacb98185b:0","InValue":122482,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":4500},"OutValue":0,"Padded":null},{"Version":0,"Id":41,"Reason":"","Done":0,"OrderType":2,"UtxoId":301334905749504,"OrderTime":1756621578,"AssetName":"ordx:f:pearl","ServiceFee":225,"UnitPrice":{"Precision":10,"Value":270000000000},"ExpectedAmt":{"Precision":0,"Value":997},"Address":"bc1pd69p4e6k7a5ajqh45pct4g79cdl36plhjduyrav3eqpvecju698qsdf64y","FromL1":false,"InUtxo":"1872866d360e068e9f2867e515fb0fb852fc38b8326162d199ffbeaa8cf6f603:0","InValue":27144,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":997},"OutValue":0,"Padded":null},{"Version":0,"Id":40,"Reason":"","Done":0,"OrderType":2,"UtxoId":291301862146048,"OrderTime":1756614529,"AssetName":"ordx:f:pearl","ServiceFee":214,"UnitPrice":{"Precision":10,"Value":255000000000},"ExpectedAmt":{"Precision":0,"Value":1000},"Address":"bc1pd69p4e6k7a5ajqh45pct4g79cdl36plhjduyrav3eqpvecju698qsdf64y","FromL1":false,"InUtxo":"a098a258b6972102a0832485875d85814a6bf91bb548ecab3de3264c5ee090c1:0","InValue":25714,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000},"OutValue":0,"Padded":null},{"Version":0,"Id":39,"Reason":"","Done":0,"OrderType":1,"UtxoId":289583875751936,"OrderTime":1756613127,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":260000000000},"ExpectedAmt":{"Precision":0,"Value":2000},"Address":"bc1pygl00c4etx8lfmqhatp72ca4euc2tzmtzck05z7tgdcfezeg03cq230qzc","FromL1":false,"InUtxo":"22d1d73f4ce7d43348f3da9bdc25957ee7be938f84118978124c0358773ebba3:0","InValue":10,"InAmt":{"Precision":0,"Value":2000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":52000,"Padded":null},{"Version":0,"Id":38,"Reason":"","Done":0,"OrderType":1,"UtxoId":289274637844480,"OrderTime":1756612956,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":270000000000},"ExpectedAmt":{"Precision":0,"Value":1000},"Address":"bc1pygl00c4etx8lfmqhatp72ca4euc2tzmtzck05z7tgdcfezeg03cq230qzc","FromL1":false,"InUtxo":"032c6de3ac5f6b4e2d43baef5c017b804893d2cbf65bf631cacd2a2c313d8046:0","InValue":10,"InAmt":{"Precision":0,"Value":1000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":27000,"Padded":null},{"Version":0,"Id":37,"Reason":"","Done":0,"OrderType":3,"UtxoId":284292475518976,"OrderTime":1756610599,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pd69p4e6k7a5ajqh45pct4g79cdl36plhjduyrav3eqpvecju698qsdf64y","FromL1":false,"InUtxo":"c1f4d66e450ea1d72af1bebc289e53c57555afed03b9252745c1ffdacd52ab9d:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":36,"Reason":"","Done":0,"OrderType":2,"UtxoId":283811439443968,"OrderTime":1756610401,"AssetName":"ordx:f:pearl","ServiceFee":226,"UnitPrice":{"Precision":10,"Value":270000000000},"ExpectedAmt":{"Precision":0,"Value":1000},"Address":"bc1p8jfsxjjwjcjd27qwfqglnrqhua0jeucu33msg3afda6afm978fvsmemcwh","FromL1":false,"InUtxo":"236308101077bb3a0482e58cedd34b5841edb9c4487aa63e32588c0f38f61d39:0","InValue":27226,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":1000},"OutValue":0,"Padded":null},{"Version":0,"Id":35,"Reason":"","Done":1,"OrderType":1,"UtxoId":276664613601280,"OrderTime":1756606205,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":260000000000},"ExpectedAmt":{"Precision":0,"Value":1000},"Address":"bc1ph06afluet3hv39le9z3j6e4cflckx7k779pcxd2a2xdv79ktgrkqs5uyn3","FromL1":false,"InUtxo":"1b66709f041df3ef2ba46cba2bb9ecfb2f87360d50188510ef6ccd27751a5797:0","InValue":10,"InAmt":{"Precision":0,"Value":1000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"fa8f629050e127087003395d82d1eb4a4128fa82142bcb38509cb685e80112fa","OutAmt":{"Precision":0,"Value":0},"OutValue":26000,"Padded":null},{"Version":0,"Id":34,"Reason":"","Done":0,"OrderType":1,"UtxoId":272129128136704,"OrderTime":1756601297,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":350000000000},"ExpectedAmt":{"Precision":0,"Value":4000},"Address":"bc1prys9eh7amf0hmqlxnqy52v3xxv6skt3d49cvu7acekev68d75elqtumavv","FromL1":false,"InUtxo":"4df73194aa80c46827eb41bc6ff0f32aff40fc77e7a92a9bc8d44b1099d6da02:0","InValue":10,"InAmt":{"Precision":0,"Value":4000},"RemainingAmt":{"Precision":0,"Value":4000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":33,"Reason":"","Done":1,"OrderType":3,"UtxoId":272060408659968,"OrderTime":1756601135,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1prys9eh7amf0hmqlxnqy52v3xxv6skt3d49cvu7acekev68d75elqtumavv","FromL1":false,"InUtxo":"c18a422ef8f11fb57bb6d2bc060c52b6b9854666b70ff26321a7b8102a8e537f:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"975011a23d305217193c6a6556d6c553d9f7c6d48f4ffce6217bbf755f9c1620","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":32,"Reason":"","Done":1,"OrderType":2,"UtxoId":254296423923712,"OrderTime":1756577765,"AssetName":"ordx:f:pearl","ServiceFee":430,"UnitPrice":{"Precision":10,"Value":350000000000},"ExpectedAmt":{"Precision":0,"Value":1500},"Address":"bc1pd69p4e6k7a5ajqh45pct4g79cdl36plhjduyrav3eqpvecju698qsdf64y","FromL1":false,"InUtxo":"094d9461884fa7f87ba0397c59f4f9ef99bfd852da40f9fd5e29ce2550123d47:0","InValue":52930,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"5650e522fab49cd947d515e72d553b75d69bfd3596ed62843e34d4f8abd24f60","OutAmt":{"Precision":0,"Value":1500},"OutValue":0,"Padded":null},{"Version":0,"Id":31,"Reason":"","Done":0,"OrderType":2,"UtxoId":232546710585344,"OrderTime":1756566455,"AssetName":"ordx:f:pearl","ServiceFee":634,"UnitPrice":{"Precision":10,"Value":260000000000},"ExpectedAmt":{"Precision":0,"Value":3000},"Address":"bc1pd69p4e6k7a5ajqh45pct4g79cdl36plhjduyrav3eqpvecju698qsdf64y","FromL1":false,"InUtxo":"ce3904cf581da3e53dc0c3320472471083fe1aa1a400a459d4f5b1dca7dcde22:0","InValue":78634,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":3000},"OutValue":0,"Padded":null},{"Version":0,"Id":30,"Reason":"","Done":1,"OrderType":1,"UtxoId":200076756779008,"OrderTime":1756549269,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":170000000000},"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"bc1pygl00c4etx8lfmqhatp72ca4euc2tzmtzck05z7tgdcfezeg03cq230qzc","FromL1":false,"InUtxo":"9f0aa2a60bb04094a1cfed157b2d8169580348954a060de036f3c3f0e9b09f60:0","InValue":10,"InAmt":{"Precision":0,"Value":10000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"3b893f03bbe58bca9376d1ceb80c672f0fe16a2bd355eb1e56c73141ad98f551","OutAmt":{"Precision":0,"Value":0},"OutValue":170000,"Padded":null},{"Version":0,"Id":29,"Reason":"","Done":1,"OrderType":3,"UtxoId":199939317825536,"OrderTime":1756549209,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pygl00c4etx8lfmqhatp72ca4euc2tzmtzck05z7tgdcfezeg03cq230qzc","FromL1":false,"InUtxo":"492e497fcab314dd4361f705fc8efcfaefb76c95c78e158a4e55f188c6b353dc:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"cdad8bd0aaa04f85a9c0cf07581b116967687960f33e163b7c8c3e18fa207937","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":28,"Reason":"refund","Done":2,"OrderType":1,"UtxoId":185405148495872,"OrderTime":1756535547,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":300000000000},"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"bc1pygl00c4etx8lfmqhatp72ca4euc2tzmtzck05z7tgdcfezeg03cq230qzc","FromL1":false,"InUtxo":"9512516923c331db5e5cc5b68e924543a61e967efdda80b4559ff4828c1edfaa:0","InValue":10,"InAmt":{"Precision":0,"Value":10000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"cdad8bd0aaa04f85a9c0cf07581b116967687960f33e163b7c8c3e18fa207937","OutAmt":{"Precision":0,"Value":10000},"OutValue":0,"Padded":null},{"Version":0,"Id":27,"Reason":"refund","Done":0,"OrderType":1,"UtxoId":177949085270016,"OrderTime":1756527694,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":990000000000},"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"bc1pjphxkxcz66yjuhgz4kdzr2acjxathn004eh8l4v9aqfk0a0y5vcsglm85h","FromL1":false,"InUtxo":"510b4ddd289e417c7feb535585d822050050ef1ef9805998b02b48f4d0ba3297:0","InValue":10,"InAmt":{"Precision":0,"Value":10000},"RemainingAmt":{"Precision":0,"Value":10000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":26,"Reason":"refund","Done":2,"OrderType":2,"UtxoId":174959788294144,"OrderTime":1756524044,"AssetName":"ordx:f:pearl","ServiceFee":1438,"UnitPrice":{"Precision":10,"Value":170000000000},"ExpectedAmt":{"Precision":0,"Value":10500},"Address":"bc1prys9eh7amf0hmqlxnqy52v3xxv6skt3d49cvu7acekev68d75elqtumavv","FromL1":false,"InUtxo":"36ae162b08bb1dd8c89f16cfe55c6f674c85705cac5beb141e979aef0373038f:0","InValue":179938,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"975011a23d305217193c6a6556d6c553d9f7c6d48f4ffce6217bbf755f9c1620","OutAmt":{"Precision":0,"Value":9979},"OutValue":8857,"Padded":null},{"Version":0,"Id":25,"Reason":"invalid","Done":1,"OrderType":3,"UtxoId":83906481356800,"OrderTime":1756467164,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pypkyst34gw0xggs6yta8p2a0mx3nkvrmx7f2pz4ljcjglft6vpwq8japuz","FromL1":false,"InUtxo":"4d5979cc1424ff1f86e8fdbc27275b1afaf6e5bc5d772705b5bcee7e68036df4:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":24,"Reason":"","Done":1,"OrderType":3,"UtxoId":83872121618432,"OrderTime":1756467153,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pypkyst34gw0xggs6yta8p2a0mx3nkvrmx7f2pz4ljcjglft6vpwq8japuz","FromL1":false,"InUtxo":"a826290da0c29ca1293799ad9c186c5499575a6db307c0c078551868caa90cda:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"f1a26909edc3b930ba107206793df96a38a03e13e19e779135f748e2890f5dfe","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":23,"Reason":"refund","Done":2,"OrderType":2,"UtxoId":83803402141696,"OrderTime":1756467056,"AssetName":"ordx:f:pearl","ServiceFee":3423,"UnitPrice":{"Precision":10,"Value":300000000000},"ExpectedAmt":{"Precision":0,"Value":14223},"Address":"bc1pypkyst34gw0xggs6yta8p2a0mx3nkvrmx7f2pz4ljcjglft6vpwq8japuz","FromL1":false,"InUtxo":"f471d6d6d48b8d89f363c382fea8e881408b13f698af0df2ace7bb535548ae62:0","InValue":430113,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"f1a26909edc3b930ba107206793df96a38a03e13e19e779135f748e2890f5dfe","OutAmt":{"Precision":0,"Value":4989},"OutValue":277020,"Padded":null},{"Version":0,"Id":22,"Reason":"","Done":1,"OrderType":3,"UtxoId":53979149238272,"OrderTime":1756439816,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pd2877z3w7laa9xygqyctawmhvtly5w6v3vwtncn4328s589qp8uqmzzwd9","FromL1":false,"InUtxo":"081b4d307aa03fddad9e51b1c85c9922e738813e7e8e01ec764748fc3c52517b:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b7ea4975fac1a34b43bc046902d825e650fc4bbaf8b176b2780e0575a48578fe","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":21,"Reason":"","Done":1,"OrderType":3,"UtxoId":53841710284800,"OrderTime":1756439384,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pelmtkxy6uu2nh4rx5yxc2l05fdr5ffjrjvwutqjxkwg5qvxf70vscpmeds","FromL1":false,"InUtxo":"8ab3cb5a677d10ed48e55d8000c92940e019edb437479636d0434eb9e4b619dc:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"701d9729b79c80e8fca03e42029b1413d8763fdf37a323f9f5c38617e50ae0f7","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":20,"Reason":"refund","Done":2,"OrderType":1,"UtxoId":53807350546432,"OrderTime":1756439276,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":10000000000000},"ExpectedAmt":{"Precision":0,"Value":407},"Address":"bc1pelmtkxy6uu2nh4rx5yxc2l05fdr5ffjrjvwutqjxkwg5qvxf70vscpmeds","FromL1":false,"InUtxo":"f1ccc3e31a50a465fe69496cc5a8a2ac2e3c39548e526770f2eebecb3be0d1c1:0","InValue":10,"InAmt":{"Precision":0,"Value":407},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"701d9729b79c80e8fca03e42029b1413d8763fdf37a323f9f5c38617e50ae0f7","OutAmt":{"Precision":0,"Value":407},"OutValue":0,"Padded":null},{"Version":0,"Id":19,"Reason":"","Done":1,"OrderType":2,"UtxoId":53566832377856,"OrderTime":1756438886,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":300000000000},"ExpectedAmt":{"Precision":0,"Value":1},"Address":"bc1pstdhlylsf0svdlgap37v8pytsd7xxy2vnxhzg4zmsgh868f7ex6say6x4x","FromL1":false,"InUtxo":"2e455c26c55f192128f929d1c1af40e0a48daeb54b0eca33c537bb6178f7517f:0","InValue":40,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"616538d1a5a1c49a2912671febd80b97bba3377bacfdbaa066ecf8416799ff4d","OutAmt":{"Precision":0,"Value":1},"OutValue":0,"Padded":null},{"Version":0,"Id":18,"Reason":"","Done":1,"OrderType":2,"UtxoId":53326314209280,"OrderTime":1756438478,"AssetName":"ordx:f:pearl","ServiceFee":12,"UnitPrice":{"Precision":10,"Value":170000000000},"ExpectedAmt":{"Precision":0,"Value":21},"Address":"bc1pstdhlylsf0svdlgap37v8pytsd7xxy2vnxhzg4zmsgh868f7ex6say6x4x","FromL1":false,"InUtxo":"31c102d761dbf0b51b45c802b28a7f977952da5b116ba2e86d60dc4a9648a8a0:0","InValue":369,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"3b893f03bbe58bca9376d1ceb80c672f0fe16a2bd355eb1e56c73141ad98f551","OutAmt":{"Precision":0,"Value":21},"OutValue":0,"Padded":null},{"Version":0,"Id":17,"Reason":"refund","Done":2,"OrderType":2,"UtxoId":53257594732544,"OrderTime":1756438106,"AssetName":"ordx:f:pearl","ServiceFee":214,"UnitPrice":{"Precision":10,"Value":160000000000},"ExpectedAmt":{"Precision":0,"Value":1600},"Address":"bc1pd2877z3w7laa9xygqyctawmhvtly5w6v3vwtncn4328s589qp8uqmzzwd9","FromL1":false,"InUtxo":"83869c9061ef01878c2823498a23b4af159766419e3daeacb00d30a9e93ca0e3:0","InValue":25814,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b7ea4975fac1a34b43bc046902d825e650fc4bbaf8b176b2780e0575a48578fe","OutAmt":{"Precision":0,"Value":0},"OutValue":25600,"Padded":null},{"Version":0,"Id":16,"Reason":"","Done":0,"OrderType":2,"UtxoId":53223234994176,"OrderTime":1756438028,"AssetName":"ordx:f:pearl","ServiceFee":138,"UnitPrice":{"Precision":10,"Value":160000000000},"ExpectedAmt":{"Precision":0,"Value":1000},"Address":"bc1pr0fcau2j0hy68n6z5udgqz3ydylldkssfhu8h6tkgd5uevlq946qqrpx8g","FromL1":false,"InUtxo":"554dc5d361151ed3d2c382990b8070730a4c73f21f7e9d6a1e8c110e92d3eedb:0","InValue":16138,"InAmt":null,"RemainingAmt":null,"RemainingValue":16000,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":15,"Reason":"","Done":1,"OrderType":2,"UtxoId":52707838918656,"OrderTime":1756437458,"AssetName":"ordx:f:pearl","ServiceFee":12,"UnitPrice":{"Precision":10,"Value":300000000000},"ExpectedAmt":{"Precision":0,"Value":10},"Address":"bc1pr0fcau2j0hy68n6z5udgqz3ydylldkssfhu8h6tkgd5uevlq946qqrpx8g","FromL1":false,"InUtxo":"8145433dc51ab21d8bd6a724e50618b14cb5ff075f762bfe178b659814f076eb:0","InValue":312,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"439c1ec30a7459a2c719e9a5d9528c4865ddc79c2ee9a1ab42489ebbd234a94e","OutAmt":{"Precision":0,"Value":10},"OutValue":0,"Padded":null},{"Version":0,"Id":14,"Reason":"","Done":0,"OrderType":1,"UtxoId":39032663048192,"OrderTime":1756388348,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":1000000000000},"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"bc1p8j7fw6a3k9cdc43dumgxzg4d26s64euckf2vt0mdudjr9zhswn6qr4clca","FromL1":false,"InUtxo":"c96d2b5db0a90055036bcbcb84d06955a8d46bc1a212e175d5104e36b6ae6a1f:0","InValue":10,"InAmt":{"Precision":0,"Value":10000},"RemainingAmt":{"Precision":0,"Value":10000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":13,"Reason":"","Done":1,"OrderType":3,"UtxoId":37280316391424,"OrderTime":1756294962,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"bc1pxfuujjw88l4wcdjglmy43ra00q67xv7gcxkjyalnz9sy9nsx0fxqngn573","FromL1":false,"InUtxo":"81b109978c04666239ebbdf3f4e5867853cb8e2a1c8e77d7a8bc218c0a7fe1cd:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"13096dd531e4e95aba4f5e7681bddd1c96f4b7cbdc250e50b076efedec66a988","OutAmt":null,"OutValue":0,"Padded":null},{"Version":0,"Id":12,"Reason":"","Done":1,"OrderType":2,"UtxoId":36936719007744,"OrderTime":1756282230,"AssetName":"ordx:f:pearl","ServiceFee":1010,"UnitPrice":{"Precision":10,"Value":250000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pxfuujjw88l4wcdjglmy43ra00q67xv7gcxkjyalnz9sy9nsx0fxqngn573","FromL1":false,"InUtxo":"95c3fe8b3c4fab60575a6cb2adfefcf8bd2aa7a03d46daef2197d270fc600d70:0","InValue":126010,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"a0ef9aca22c6c543988dac67e9f83cb3c0de6ef8aabd6860439d239b3e2d63fe","OutAmt":{"Precision":0,"Value":5000},"OutValue":0,"Padded":null},{"Version":0,"Id":11,"Reason":"","Done":0,"OrderType":2,"UtxoId":36902359269376,"OrderTime":1756278822,"AssetName":"ordx:f:pearl","ServiceFee":54,"UnitPrice":{"Precision":10,"Value":10000000000},"ExpectedAmt":{"Precision":0,"Value":5500},"Address":"bc1p9znyx59v7j8dkkvr8xrz8ef9rupqxlhw78fdcrakhrq3h5s2v3vsct70f4","FromL1":false,"InUtxo":"922627d8064d54c0951412ee420fea1127df9f944f3bee9e372b4f116a3a1264:0","InValue":5554,"InAmt":null,"RemainingAmt":null,"RemainingValue":5500,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":10,"Reason":"","Done":0,"OrderType":1,"UtxoId":34806415228928,"OrderTime":1756222314,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":650000000000},"ExpectedAmt":{"Precision":0,"Value":30000},"Address":"bc1pzvfj8gkc650k206nnmu5xfa9xkt8rxf3znsaq70cc5wkrht86xyqcalep6","FromL1":false,"InUtxo":"c7a1bf88a28a8a87556ceb2ef2916f7f167f18c4498f4c850cd541c2e45c2238:0","InValue":10,"InAmt":{"Precision":0,"Value":30000},"RemainingAmt":{"Precision":0,"Value":30000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":9,"Reason":"refund","Done":2,"OrderType":2,"UtxoId":34772055490560,"OrderTime":1756222308,"AssetName":"ordx:f:pearl","ServiceFee":810,"UnitPrice":{"Precision":10,"Value":100000000000},"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"bc1pxfuujjw88l4wcdjglmy43ra00q67xv7gcxkjyalnz9sy9nsx0fxqngn573","FromL1":false,"InUtxo":"adccf1e8fff96e50b55979a29f995c7d1768616d6b38143745ce7e52ed1b18d2:0","InValue":100810,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"13096dd531e4e95aba4f5e7681bddd1c96f4b7cbdc250e50b076efedec66a988","OutAmt":{"Precision":0,"Value":0},"OutValue":100000,"Padded":null},{"Version":0,"Id":8,"Reason":"","Done":1,"OrderType":2,"UtxoId":34703336013824,"OrderTime":1756221816,"AssetName":"ordx:f:pearl","ServiceFee":810,"UnitPrice":{"Precision":10,"Value":200000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pxfuujjw88l4wcdjglmy43ra00q67xv7gcxkjyalnz9sy9nsx0fxqngn573","FromL1":false,"InUtxo":"42b852db739633fd03317490530c94eac15075811092eb9676b6a18540c24e3c:0","InValue":100810,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b9112545797eed0fdf7ee73d838c73d3bfb35cbfceb6415dbaf4599a5b27d66b","OutAmt":{"Precision":0,"Value":5000},"OutValue":0,"Padded":null},{"Version":0,"Id":7,"Reason":"","Done":0,"OrderType":1,"UtxoId":34668976275456,"OrderTime":1756221678,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":660000000000},"ExpectedAmt":{"Precision":0,"Value":9186},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"70b7741a709f18ca79fde185a60dd580a331c19ad87b195ace9157cd050b6574:0","InValue":10,"InAmt":{"Precision":0,"Value":9186},"RemainingAmt":{"Precision":0,"Value":9186},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":6,"Reason":"","Done":0,"OrderType":1,"UtxoId":34634616537088,"OrderTime":1756221624,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":500000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"966d45f73da1e3ef98df6dd348f4cfbb8837f6045de918cf97be4acc9087cfe5:0","InValue":10,"InAmt":{"Precision":0,"Value":5000},"RemainingAmt":{"Precision":0,"Value":5000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":5,"Reason":"","Done":0,"OrderType":1,"UtxoId":34600256798720,"OrderTime":1756221594,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":450000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"d26bbc91e7ccacf4e9a15118eb03959b5ce496b29b4da784054d94ef236df198:0","InValue":10,"InAmt":{"Precision":0,"Value":5000},"RemainingAmt":{"Precision":0,"Value":5000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":4,"Reason":"","Done":0,"OrderType":1,"UtxoId":34565897060352,"OrderTime":1756221564,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":400000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"147fdf3298720595fe1d1160d7ba68f0d1bdc35f30dfe699905c74956e873592:0","InValue":10,"InAmt":{"Precision":0,"Value":5000},"RemainingAmt":{"Precision":0,"Value":5000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0,"Padded":null},{"Version":0,"Id":3,"Reason":"","Done":0,"OrderType":1,"UtxoId":34531537321984,"OrderTime":1756221534,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":350000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"686926eca6922463d76886b30b66abbd48a66d32fea22ce8093d7f8d55928852:0","InValue":10,"InAmt":{"Precision":0,"Value":5000},"RemainingAmt":{"Precision":0,"Value":3500},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":52500,"Padded":null},{"Version":0,"Id":2,"Reason":"","Done":1,"OrderType":1,"UtxoId":34497177583616,"OrderTime":1756221504,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":300000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"aaf789b972699a0e38bef2f4945726497d04be5c6fd12309d9870048fe4d653c:0","InValue":10,"InAmt":{"Precision":0,"Value":5000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"20edc1509b3119e205fbc2c8e91b6e7a5a65ab2f10e16eb6a2b010f592a3f0dd","OutAmt":{"Precision":0,"Value":0},"OutValue":150000,"Padded":null},{"Version":0,"Id":1,"Reason":"","Done":1,"OrderType":1,"UtxoId":34462817845248,"OrderTime":1756221468,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":250000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"1fe9afe8cb758f013baf8186f875f634f38ff5973c050ce5992f1505a266abaf:0","InValue":10,"InAmt":{"Precision":0,"Value":5000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"a0ef9aca22c6c543988dac67e9f83cb3c0de6ef8aabd6860439d239b3e2d63fe","OutAmt":{"Precision":0,"Value":0},"OutValue":125000,"Padded":null},{"Version":0,"Id":0,"Reason":"","Done":1,"OrderType":1,"UtxoId":34428458106880,"OrderTime":1756221414,"AssetName":"ordx:f:pearl","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":200000000000},"ExpectedAmt":{"Precision":0,"Value":5000},"Address":"bc1pea6mmfl37y2q7zr99lyq52u4gt8fev2tjvzkvwqv0wdwesjcvn9sz3axuq","FromL1":false,"InUtxo":"0ea3080a2992ef0b6a12d8fd28351d3ac3952ed1fbb7e28d1539d294b136c3a2:0","InValue":10,"InAmt":{"Precision":0,"Value":5000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b9112545797eed0fdf7ee73d838c73d3bfb35cbfceb6415dbaf4599a5b27d66b","OutAmt":{"Precision":0,"Value":0},"OutValue":100000,"Padded":null}]`

	//itemHistory := `[{"Version":0,"Id":27,"Reason":"","Done":0,"OrderType":2,"UtxoId":14224931946496,"OrderTime":1753720560,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":45000000},"ExpectedAmt":{"Precision":0,"Value":1000},"Address":"tb1ph9cw75y4kgw9ekqsx5puljqz3n4xl5305d4cpdmkad0c6knzd0sqka3xc8","FromL1":false,"InUtxo":"63c5604421fc3e14e121669bf6896e34fa094c9f05a236ea5a627d36bbf7200a:0","InValue":15,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":1,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":888},"OutValue":0},{"Version":0,"Id":26,"Reason":"","Done":1,"OrderType":3,"UtxoId":11888469737472,"OrderTime":1753713935,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"8d2307a6e0a419b577c899d97fba83a1c7ca1aa2341276537a606f7ea553a0e9:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":null,"OutValue":0},{"Version":0,"Id":25,"Reason":"","Done":1,"OrderType":1,"UtxoId":11647951568896,"OrderTime":1753713743,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":45000000},"ExpectedAmt":{"Precision":0,"Value":30000},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"89a954f7e5040938da8a0ac8de79c35cb1ef57b0f6bdbea8643db39cb0ee92c6:0","InValue":0,"InAmt":{"Precision":0,"Value":30000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"e2469c3c49ad271901f16c031840301be10f1996dfa8d883a97625459d3e8499","OutAmt":{"Precision":0,"Value":112},"OutValue":135},{"Version":0,"Id":24,"Reason":"","Done":1,"OrderType":2,"UtxoId":11579232092160,"OrderTime":1753713197,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":90,"UnitPrice":{"Precision":10,"Value":50000000},"ExpectedAmt":{"Precision":0,"Value":2000000},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"aa7ac7835061aa32c99d7bcb54087eeb0dffdef4fea4acdd72062ac76c22de3a:0","InValue":10090,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"e2469c3c49ad271901f16c031840301be10f1996dfa8d883a97625459d3e8499","OutAmt":{"Precision":0,"Value":2000000},"OutValue":0},{"Version":0,"Id":23,"Reason":"","Done":2,"OrderType":2,"UtxoId":11544872353792,"OrderTime":1753713065,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":46,"UnitPrice":{"Precision":10,"Value":45000000},"ExpectedAmt":{"Precision":0,"Value":1000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"3bdd3ae1c28d445963f1bd1ba78e4d9130cc6abeb9e117723b70a7af8e606310:0","InValue":4546,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":1000000},"OutValue":485},{"Version":0,"Id":22,"Reason":"","Done":2,"OrderType":2,"UtxoId":11510512615424,"OrderTime":1753712903,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":13,"UnitPrice":{"Precision":10,"Value":45000000},"ExpectedAmt":{"Precision":0,"Value":100000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"d5d97aa6904e4fad5085caf5d94c2ae6143669a23d560a62c02198c83e1c927c:0","InValue":463,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":100000},"OutValue":50},{"Version":0,"Id":21,"Reason":"","Done":0,"OrderType":2,"UtxoId":11476152877056,"OrderTime":1753712885,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":13,"UnitPrice":{"Precision":10,"Value":40000000},"ExpectedAmt":{"Precision":0,"Value":119000},"Address":"tb1pt07drd4lwpjl8hz0f8m0fahxw0sk6djgrw67kdpxqasqudgu4eeqspl234","FromL1":false,"InUtxo":"3e67bb750d147e5c9bb8e5a07abfc4a35b589b83bf61f1b1068390f5df653a62:0","InValue":489,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":119000},"OutValue":0},{"Version":0,"Id":20,"Reason":"","Done":2,"OrderType":2,"UtxoId":11441793138688,"OrderTime":1753712867,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":6000000},"ExpectedAmt":{"Precision":0,"Value":1000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"121e52c92240688615bd7b47c4e3de1965630c8f9eefc549dea51bd6a79d71cf:0","InValue":614,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":0},"OutValue":600},{"Version":0,"Id":19,"Reason":"","Done":2,"OrderType":2,"UtxoId":11407433400320,"OrderTime":1753712837,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":5000000},"ExpectedAmt":{"Precision":0,"Value":1000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"bc93d20ef0d95d4e9f2e849ca0b02bc320ef3eaf8ef61e5400c2cd7ac13b7213:0","InValue":514,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":0},"OutValue":500},{"Version":0,"Id":18,"Reason":"","Done":0,"OrderType":1,"UtxoId":11373073661952,"OrderTime":1753712783,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":50000000},"ExpectedAmt":{"Precision":0,"Value":400000},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"32862a612fb4a52ad79e72719f875ce8c30dbf5200ad45b6ec200541c426586a:0","InValue":0,"InAmt":{"Precision":0,"Value":400000},"RemainingAmt":{"Precision":0,"Value":400000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0},{"Version":0,"Id":17,"Reason":"","Done":1,"OrderType":1,"UtxoId":11338713923584,"OrderTime":1753712747,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":40000000},"ExpectedAmt":{"Precision":0,"Value":300000},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"db6a85d295650b508ec01a68c04ccb83ffb42014853704dbc8ded796a5d044ec:0","InValue":0,"InAmt":{"Precision":0,"Value":300000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"e2469c3c49ad271901f16c031840301be10f1996dfa8d883a97625459d3e8499","OutAmt":{"Precision":0,"Value":0},"OutValue":1200},{"Version":0,"Id":16,"Reason":"","Done":2,"OrderType":2,"UtxoId":11304354185216,"OrderTime":1753712705,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":50000000},"ExpectedAmt":{"Precision":0,"Value":100000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"6da060841a2932f8ae506c09afecb303f413a479fc7dbb699f863fae8bf117a5:0","InValue":514,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":100000},"OutValue":100},{"Version":0,"Id":15,"Reason":"","Done":2,"OrderType":2,"UtxoId":11269994446848,"OrderTime":1753712675,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":4000000},"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"ce47c62453b8c819e70b23539cbbf43f8be313a8e84b55bf02fce3207dfe3d83:0","InValue":14,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":0},"OutValue":4},{"Version":0,"Id":14,"Reason":"","Done":0,"OrderType":1,"UtxoId":11235634708480,"OrderTime":1753712663,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":60000000},"ExpectedAmt":{"Precision":0,"Value":300000},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"da312693b283d464e270f6664b663c34eaa51bf378ca95d3655023854fe7322e:0","InValue":0,"InAmt":{"Precision":0,"Value":300000},"RemainingAmt":{"Precision":0,"Value":300000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0},{"Version":0,"Id":13,"Reason":"","Done":2,"OrderType":1,"UtxoId":11201274970112,"OrderTime":1753712633,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":90000000},"ExpectedAmt":{"Precision":0,"Value":400000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"6475d2d75530ae4408ae07668317f3efbe930e53e75945eb627087d8d176c7b8:0","InValue":0,"InAmt":{"Precision":0,"Value":400000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":400000},"OutValue":0},{"Version":0,"Id":12,"Reason":"","Done":2,"OrderType":1,"UtxoId":11166915231744,"OrderTime":1753712615,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":80000000},"ExpectedAmt":{"Precision":0,"Value":300000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"0e9101dc39fa2f456875ec2a2e5c13b693fc087d65ad32697cd2c13622839075:0","InValue":0,"InAmt":{"Precision":0,"Value":300000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":300000},"OutValue":0},{"Version":0,"Id":11,"Reason":"","Done":2,"OrderType":1,"UtxoId":11132555493376,"OrderTime":1753712603,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":70000000},"ExpectedAmt":{"Precision":0,"Value":199999},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"a9e8a224f17f9ad98dd100efa7caa1d0ed08716ef6e024f01b211fa6dae9260a:0","InValue":0,"InAmt":{"Precision":0,"Value":199999},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":199999},"OutValue":0},{"Version":0,"Id":10,"Reason":"","Done":2,"OrderType":1,"UtxoId":11098195755008,"OrderTime":1753712555,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":60000000},"ExpectedAmt":{"Precision":0,"Value":100000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"959d733b260fc2c4d342d5789312975dd37e243560e91dbd4010b85ad5e0478c:0","InValue":0,"InAmt":{"Precision":0,"Value":100000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":100000},"OutValue":0},{"Version":0,"Id":9,"Reason":"","Done":1,"OrderType":3,"UtxoId":7730941394944,"OrderTime":1753551234,"AssetName":"","ServiceFee":10,"UnitPrice":null,"ExpectedAmt":null,"Address":"tb1pucha8pnx8wd74uwhn8f3ggaxjyl462s2pp2qv0wqg9wpuhnqq0fq2ufh9d","FromL1":false,"InUtxo":"3186fa1cff3e91863c43fe341228caf9929226cc01f5e6df9bfb719f7ac5fc42:0","InValue":0,"InAmt":null,"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b230553022e247a686d69d8e3040dccbe2841ffee49e70ee45b4a00b02b1c8b1","OutAmt":null,"OutValue":0},{"Version":0,"Id":8,"Reason":"","Done":1,"OrderType":2,"UtxoId":7662221918208,"OrderTime":1753551126,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":40000000},"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"tb1pucha8pnx8wd74uwhn8f3ggaxjyl462s2pp2qv0wqg9wpuhnqq0fq2ufh9d","FromL1":false,"InUtxo":"c636dd4abad7cdc49621b5748f00ef7ceedeea954d9fb7c08ba96ae8d00b2f49:0","InValue":50,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"b95d4b1360e23f40226ac553c1faa6a6e36eaf205452172b418b6d530c762198","OutAmt":{"Precision":0,"Value":10000},"OutValue":0},{"Version":0,"Id":7,"Reason":"refund","Done":2,"OrderType":2,"UtxoId":7593502441472,"OrderTime":1753551042,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":34,"UnitPrice":{"Precision":10,"Value":30000000},"ExpectedAmt":{"Precision":0,"Value":999999},"Address":"tb1pucha8pnx8wd74uwhn8f3ggaxjyl462s2pp2qv0wqg9wpuhnqq0fq2ufh9d","FromL1":false,"InUtxo":"7aa5cd65442c36ce39207c45d2a09358800980439d6a18c213a1058513ee3e6c:0","InValue":3034,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"b230553022e247a686d69d8e3040dccbe2841ffee49e70ee45b4a00b02b1c8b1","OutAmt":{"Precision":0,"Value":999666},"OutValue":1},{"Version":0,"Id":6,"Reason":"","Done":1,"OrderType":2,"UtxoId":7249905057792,"OrderTime":1753547646,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":34,"UnitPrice":{"Precision":10,"Value":30000000},"ExpectedAmt":{"Precision":0,"Value":1000000},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"b3e45d11f4a6039b7b953a8dc8254d5abecb511af90fbcb112454d51a8fc34c7:0","InValue":3034,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"ed7fc9da9433fe3f2aebbabee5dbc9f7858ef58a60496628ae22dc233054d0af","OutAmt":{"Precision":0,"Value":1000000},"OutValue":0},{"Version":0,"Id":5,"Reason":"","Done":2,"OrderType":2,"UtxoId":7043746627584,"OrderTime":1753546974,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":29,"UnitPrice":{"Precision":10,"Value":8000000},"ExpectedAmt":{"Precision":0,"Value":3000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"1ac07ff57c5fc7cf95d5832b7ca75446bbf38de01314318aa98921ae01516a8a:0","InValue":2429,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":0},"OutValue":2400},{"Version":0,"Id":4,"Reason":"","Done":2,"OrderType":2,"UtxoId":7009386889216,"OrderTime":1753546950,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":24,"UnitPrice":{"Precision":10,"Value":9000000},"ExpectedAmt":{"Precision":0,"Value":2000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"07d1407a215a673af8e7edb9efa6598ef9425cdcf7173d304ce6a52f44a4a346:0","InValue":1824,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":0},"OutValue":1800},{"Version":0,"Id":3,"Reason":"","Done":2,"OrderType":2,"UtxoId":6975027150848,"OrderTime":1753546914,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":10000000},"ExpectedAmt":{"Precision":0,"Value":1000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"be63a0d3d7ba3e21561a8d34eaa44ff3f73060d4f609612799c081a7f19f1cfa:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":0},"OutValue":1000},{"Version":0,"Id":2,"Reason":"","Done":1,"OrderType":1,"UtxoId":6940667412480,"OrderTime":1753546890,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":30000000},"ExpectedAmt":{"Precision":0,"Value":1999999},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"7b3f1e5a0d859e68c8d78e87347e45ee73e451eec1a67c1b97237a8de7edaf1c:0","InValue":0,"InAmt":{"Precision":0,"Value":1999999},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"002a8bee1617f460d462e825c7daa26daa7a421031a47a036dddfe6dbade3e7f","OutAmt":{"Precision":0,"Value":333},"OutValue":5999},{"Version":0,"Id":1,"Reason":"","Done":2,"OrderType":1,"UtxoId":6906307674112,"OrderTime":1753546866,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":40000000},"ExpectedAmt":{"Precision":0,"Value":1000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"c48eae0b7d4f1483c2d638d44c428cdf58d01c70f57999be8488190c81899089:0","InValue":0,"InAmt":{"Precision":0,"Value":1000000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":0},"OutValue":4000},{"Version":0,"Id":0,"Reason":"","Done":2,"OrderType":1,"UtxoId":6871947935744,"OrderTime":1753546830,"AssetName":"runes:f:BITCOIN•TESTNET","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":50000000},"ExpectedAmt":{"Precision":0,"Value":10000000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"ab77cba3ad713677eb73824b8eb12b99f7c1b46550a511e1bdd4c893646f3eed:0","InValue":0,"InAmt":{"Precision":0,"Value":10000000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"378ad9361f8b14c93d25fd6782b102eca4ab4ccf3114c4ea6ff85832fbe40a5e","OutAmt":{"Precision":0,"Value":8000000},"OutValue":10000}]`

	var inputs []*SwapHistoryItem
	err = json.Unmarshal([]byte(itemHistory), &inputs)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(inputs, func(i, j int) bool {
		return inputs[i].Id < inputs[j].Id
	})

	tickInfo := _client.getTickerInfo(assetName)
	if tickInfo == nil {
		t.Fatal("can't find ticker")
	}

	swapContract := NewSwapContract()
	swapContract.AssetName = *assetName

	deployFee, err := _client.QueryFeeForDeployContract(swapContract.TemplateName, (swapContract.Content()), 1)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("deploy contract %s need %d sats\n", swapContract.TemplateName, deployFee)
	fmt.Printf("use RemoteDeployContract to deploy a contract on core channel in server node\n")

	invokeParam, err := _client.QueryParamForInvokeContract(swapContract.TemplateName, INVOKE_API_SWAP)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("use %s as template to invoke contract %s\n", invokeParam, swapContract.TemplateName)

	assetAmt := _server.GetAssetBalance_SatsNet("", &ASSET_PLAIN_SAT)
	fmt.Printf("plain sats: %d\n", assetAmt)
	txId, id, url, err := _client.DeployContract_Remote(swapContract.TemplateName,
		string(swapContract.Content()), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("RemoteDeployContract succeed, %s, %d, %s\n", txId, id, url)

	run := true
	go sendInBackground(&run) // 有时需要更新区块引起合约调用
	err = waitContractReady(url, _client)
	if err != nil {
		t.Fatal(err)
	}
	err = waitContractReady(url, _server)
	if err != nil {
		t.Fatal(err)
	}
	run = false

	channelId := ExtractChannelId(url)
	txId, err = _client.SendAssetsV3_SatsNet(channelId, swapContract.GetAssetName().String(),
		"100000000", 1000000, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", txId)

	contractRuntime := _server.GetContract(url)
	if contractRuntime == nil {
		t.Fatal("")
	}
	swap, ok := contractRuntime.(*SwapContractRuntime)
	if !ok {
		t.Fatal("")
	}

	contractRuntime2 := _server.GetContract(url)
	if contractRuntime2 == nil {
		t.Fatal("")
	}
	swap2, ok := contractRuntime2.(*SwapContractRuntime)
	if !ok {
		t.Fatal("")
	}

	for _, item := range inputs {
		h, _, _ := indexer.FromUtxoId(item.UtxoId)
		fmt.Printf("|%d-%d", item.Id, h)
	}
	if !bytes.Equal(swap.StaticMerkleRoot, swap2.StaticMerkleRoot) {
		t.Fatal("static merkle root not inconsistent")
	}

	fmt.Printf("size %d\n", len(inputs))
	_not_invoke_block = true // 停止 InvokeWithBlock 和 InvokeWithBlock_SatsNet
	_not_send_tx = true      // 停止广播
	for _, item := range inputs {
		//for i := 179; i < len(inputs); i++ {
		//item := inputs[i]
		fmt.Printf("item: %v\n", item)
		// 去掉处理结果
		item.RemainingAmt = item.InAmt.Clone()
		item.RemainingValue = item.InValue - item.ServiceFee
		item.OutAmt = nil
		item.OutValue = 0
		item.Done = DONE_NOTYET
		if item.Reason != INVOKE_REASON_INVALID {
			item.Reason = INVOKE_REASON_NORMAL
		}
		if mainnetAddr {
			item.Address, err = convertAddr(item.Address)
			if err != nil {
				t.Fatal(err)
			}
		}

		if item.Id == 21 {
			Log.Infof("")
		}

		h, _, _ := indexer.FromUtxoId(item.UtxoId)
		swap.CurrBlock = h
		swap2.CurrBlock = h

		////////
		// 以下代码模拟正常调用过程
		item2 := item.Clone()
		switch item.OrderType {
		case ORDERTYPE_BUY, ORDERTYPE_SELL:
			// if item.OrderType == ORDERTYPE_BUY {
			// 	item.RemainingValue = item.GetTradingValue()
			// } else {
			// 	item.RemainingValue = 0
			// 	item.InValue = 0
			// }
			swap.updateContractStatus(item)
			swap.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			// if item2.OrderType == ORDERTYPE_BUY {
			// 	item2.RemainingValue = item2.GetTradingValue()
			// } else {
			// 	item2.RemainingValue = 0
			// 	item2.InValue = 0
			// }
			swap2.updateContractStatus(item2)
			swap2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)

		case ORDERTYPE_WITHDRAW, ORDERTYPE_DEPOSIT:
			swap.updateContractStatus(item)
			swap.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			swap2.updateContractStatus(item2)
			swap2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)
		case ORDERTYPE_REFUND:
			swap.updateContractStatus(item)
			swap.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			swap2.updateContractStatus(item2)
			swap2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)
		default:
			fmt.Printf("invalid type %d\n", item.OrderType)
			t.Fatal()
		}

		swap.swap()
		swap.invokeCompleted()
		swap2.swap()
		swap2.invokeCompleted()

		if !bytes.Equal(swap.CurrAssetMerkleRoot, swap2.CurrAssetMerkleRoot) {
			t.Fatal("asset merkle root not inconsistent")
		}

		for swap.isSending {
			time.Sleep(100 * time.Millisecond)
		}
		err := swap.sendInvokeResultTx()
		if err != nil {
			t.Fatal(err)
		}
		swap.sendInvokeResultTx_SatsNet()
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(swap.CurrAssetMerkleRoot, swap2.CurrAssetMerkleRoot) {
			t.Fatal("asset merkle root not inconsistent")
		}
		////////

		// 检查每次处理的结果
		err = swap.checkSelf()
		if err != nil {
			t.Fatalf("swap1: %d %v", item.Id, err)
		}

		err = swap2.checkSelf()
		if err != nil {
			t.Fatalf("swap2: %d %v", item2.Id, err)
		}

		// 等待处理
		//time.Sleep(time.Second)
	}

	//
	fmt.Printf("realRunningData: %v\n", realRunningData)
	fmt.Printf("simuRunningData: %v\n", swap.SwapContractRunningData)

}

func TestAmmContract(t *testing.T) {
	prepare(t)

	// 需要修改资产名称为模拟环境支持的币
	assetName := indexer.NewAssetNameFromString("ordx:f:satoshilpt")
	runningData := `{"AssetAmtInPool":{"Precision":0,"Value":3259459},"SatsValueInPool":64407,"LowestSellPrice":null,"HighestBuyPrice":null,"LastDealPrice":{"Precision":10,"Value":192953343},"HighestDealPrice":{"Precision":10,"Value":233553912},"LowestDealPrice":{"Precision":10,"Value":192953343},"TotalInputAssets":{"Precision":0,"Value":437847},"TotalInputSats":3933,"TotalDealAssets":{"Precision":0,"Value":178188},"TotalDealSats":9395,"TotalDealCount":8,"TotalDealTx":8,"TotalDealTxFee":80,"TotalRefundAssets":{"Precision":0,"Value":740801},"TotalRefundSats":440796,"TotalRefundTx":1,"TotalRefundTxFee":10,"TotalProfitAssets":null,"TotalProfitSats":0,"TotalProfitTx":0,"TotalProfitTxFee":0,"TotalDepositAssets":null,"TotalDepositTx":0,"TotalDepositTxFee":0,"TotalWithdrawAssets":null,"TotalWithdrawTx":0,"TotalWithdrawTxFee":0}`
	var realRunningData SwapContractRunningData
	err := json.Unmarshal([]byte(runningData), &realRunningData)
	if err != nil {
		t.Fatal(err)
	}

	itemHistory := `[{"Version":0,"Id":62,"Reason":"","Done":1,"OrderType":1,"UtxoId":14534169591808,"OrderTime":1753724313,"AssetName":"ordx:f:ordxyz","ServiceFee":59,"UnitPrice":{"Precision":10,"Value":165973333},"ExpectedAmt":{"Precision":0,"Value":6161},"Address":"tb1pucha8pnx8wd74uwhn8f3ggaxjyl462s2pp2qv0wqg9wpuhnqq0fq2ufh9d","FromL1":false,"InUtxo":"7fe7de8446b5ea354864e8dc6ecdfd64d6f34a2232a375438c9f38a9e983a379:0","InValue":0,"InAmt":{"Precision":0,"Value":375000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"fe1d43fe86ea0df7e5988e2a903c285695923583732cf92bdddb89722196580f","OutAmt":{"Precision":0,"Value":0},"OutValue":6175},{"Version":0,"Id":61,"Reason":"slippage protection","Done":2,"OrderType":1,"UtxoId":14465450115072,"OrderTime":1753724193,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":9424},"Address":"tb1pucha8pnx8wd74uwhn8f3ggaxjyl462s2pp2qv0wqg9wpuhnqq0fq2ufh9d","FromL1":false,"InUtxo":"7c90dc8d7e2b10df07c755142a7451bc26cedd44e3ff0dcb2855771a730af81b:0","InValue":0,"InAmt":{"Precision":0,"Value":500000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"9ec930f1077b4c5bd26210c26525ee4a96c7626b2d92eccfaf22d437bbccea12","OutAmt":{"Precision":0,"Value":500000},"OutValue":0},{"Version":0,"Id":60,"Reason":"","Done":1,"OrderType":1,"UtxoId":14396730638336,"OrderTime":1753724163,"AssetName":"ordx:f:ordxyz","ServiceFee":86,"UnitPrice":{"Precision":10,"Value":190380000},"ExpectedAmt":{"Precision":0,"Value":9329},"Address":"tb1pucha8pnx8wd74uwhn8f3ggaxjyl462s2pp2qv0wqg9wpuhnqq0fq2ufh9d","FromL1":false,"InUtxo":"c5071c9ef1ca9d8ee3feeb7dd0496726301d56d22753319c805a686750b2dade:0","InValue":0,"InAmt":{"Precision":0,"Value":500000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"472a1c9ad9e9b5799b86bf1426a32f7a070ea74eb56099fa57af89c244ab071d","OutAmt":{"Precision":0,"Value":0},"OutValue":9443},{"Version":0,"Id":59,"Reason":"","Done":1,"OrderType":1,"UtxoId":12884902412288,"OrderTime":1753714934,"AssetName":"ordx:f:ordxyz","ServiceFee":23,"UnitPrice":{"Precision":10,"Value":209125000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"62ccd5d9e6ac3427dbff23d2c594f0077f1f1600d591e338e939bea6f699818a:0","InValue":0,"InAmt":{"Precision":0,"Value":80000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"343b787613af8b8d53dd0e010afbfc6bfff8691219f3f71e80ddc033e84e6b3a","OutAmt":{"Precision":0,"Value":0},"OutValue":1660},{"Version":0,"Id":58,"Reason":"","Done":1,"OrderType":2,"UtxoId":12850542411776,"OrderTime":1753714925,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":210362454},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"4dcd3ae4547e5b61043ab7fc23dd0351361607274db1e8cb01e50464f319e123:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"a0c85836a1789c063d804788c511aed2ca2fca99585f1c20044b34c0b5ac4869","OutAmt":{"Precision":0,"Value":47537},"OutValue":0},{"Version":0,"Id":57,"Reason":"","Done":1,"OrderType":2,"UtxoId":12781823197184,"OrderTime":1753714874,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":206936511},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"c7afd005f5e0b4d393ed90b2da31c020b14fc05d2c3605829306b060417c0d35:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"330c858d10df86811da3b185277872d82d7ba33b8a0faeb5f73b411de75d719f","OutAmt":{"Precision":0,"Value":48324},"OutValue":0},{"Version":0,"Id":56,"Reason":"","Done":1,"OrderType":1,"UtxoId":12747463196672,"OrderTime":1753714865,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":207000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"242b84cdd163e2c534c675d165383b3e1bd994f750e467c909d63f75c736788e:0","InValue":0,"InAmt":{"Precision":0,"Value":50000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b042a9957456c88a320f6184aca81a1b3d9c1f000727e142abdc6fce926805aa","OutAmt":{"Precision":0,"Value":0},"OutValue":1027},{"Version":0,"Id":55,"Reason":"","Done":1,"OrderType":2,"UtxoId":12678743982080,"OrderTime":1753714835,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":207095077},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"e5daf4c868ad1b240202f7a94bc5df934bb065dd8b27b5f35aac9f75c7275e82:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"a5ba4b19c8dd978e501075769f936f90da6a0ed63a0d132007614ae798019d1e","OutAmt":{"Precision":0,"Value":48287},"OutValue":0},{"Version":0,"Id":54,"Reason":"","Done":1,"OrderType":2,"UtxoId":12644383981568,"OrderTime":1753714826,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":203657692},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"a7fd24478491fb212b21181458adc8461fb3327bcda095a418c7ceebb0484281:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"5d1533ace4d74846eda26560f1803b5541c7ae42b2e6572ca45be10e814d9a7f","OutAmt":{"Precision":0,"Value":49102},"OutValue":0},{"Version":0,"Id":53,"Reason":"","Done":1,"OrderType":1,"UtxoId":12575664766976,"OrderTime":1753714805,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":200000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pw4ytvg6h9pzaufu8jslxel597c75v0jl4sxcclz2d2l8amzdanpqtvr0w4","FromL1":false,"InUtxo":"ce36e6c5c5db2cd75d94cd8bac00730f2a64df66bef815c412363c09bbbdf5bb:0","InValue":0,"InAmt":{"Precision":0,"Value":1000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"35c6f160336240e0c3d5eba6e001ad81f0e7bcf8b120af8e4ff29ee214630bed","OutAmt":{"Precision":0,"Value":0},"OutValue":20},{"Version":0,"Id":52,"Reason":"","Done":1,"OrderType":1,"UtxoId":12541305028608,"OrderTime":1753714796,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":203800000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"025e4199c5a70db9cb000bdfcbe75d996d77d013c33ef7a72dd457c8274b3bd0:0","InValue":0,"InAmt":{"Precision":0,"Value":50000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"861bf5b2d849de0ec8b1401747d365dd48281fa431a69944f6956a68969d84bc","OutAmt":{"Precision":0,"Value":0},"OutValue":1011},{"Version":0,"Id":51,"Reason":"","Done":1,"OrderType":2,"UtxoId":12472585551872,"OrderTime":1753714775,"AssetName":"ordx:f:ordxyz","ServiceFee":11,"UnitPrice":{"Precision":10,"Value":205107168},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pw4ytvg6h9pzaufu8jslxel597c75v0jl4sxcclz2d2l8amzdanpqtvr0w4","FromL1":false,"InUtxo":"769367e6f606930e756c5b062169289902f4a832002b1eef49856047e022a2d9:0","InValue":211,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"19001d1b0cc75c0c23129b8b0ed47fccea700b74f56af2b8f347ecaefeb44fad","OutAmt":{"Precision":0,"Value":9751},"OutValue":0},{"Version":0,"Id":50,"Reason":"","Done":1,"OrderType":1,"UtxoId":12403865812992,"OrderTime":1753714742,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":206660776},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"9884414bc53c2056f6d4e8a42932abfb054cc8deaf64b38ddc501c609b38a75a:0","InValue":0,"InAmt":{"Precision":0,"Value":52066},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"4c40f5d82070e7a2e3e8e97ace065fd24cfae3de9665205193b64b34435704db","OutAmt":{"Precision":0,"Value":0},"OutValue":1068},{"Version":0,"Id":49,"Reason":"","Done":1,"OrderType":2,"UtxoId":12335146336256,"OrderTime":1753714703,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":206808123},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"5170e58e3f7c9ef81a264b3a107657fa03a36643676c6198d928e6b926cab90c:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"d6c1d57cd51d511ad8d5eabc6adf1eb151aa95fcd39d37b4facc66612e74a253","OutAmt":{"Precision":0,"Value":48354},"OutValue":0},{"Version":0,"Id":48,"Reason":"","Done":1,"OrderType":2,"UtxoId":12232067121152,"OrderTime":1753714304,"AssetName":"ordx:f:ordxyz","ServiceFee":11,"UnitPrice":{"Precision":10,"Value":204792135},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pw4ytvg6h9pzaufu8jslxel597c75v0jl4sxcclz2d2l8amzdanpqtvr0w4","FromL1":false,"InUtxo":"071af5250107de644bf65f5618e02297be58f5951e5cdea09a955149bd17cb1e:0","InValue":211,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"eedb61fc07c7265281edba014a0f54e5701387cafbc46b8f71bdb2fa69442318","OutAmt":{"Precision":0,"Value":9766},"OutValue":0},{"Version":0,"Id":47,"Reason":"","Done":1,"OrderType":2,"UtxoId":12163347644416,"OrderTime":1753714253,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":202782171},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pw4ytvg6h9pzaufu8jslxel597c75v0jl4sxcclz2d2l8amzdanpqtvr0w4","FromL1":false,"InUtxo":"8eb873107252230ff75507a30646d60ad06193b2c92264ebd127ac34096164d9:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"bc75cf45272bbad05110fbc425caad17908a6737aeb40bc50a906da07b74ed47","OutAmt":{"Precision":0,"Value":49314},"OutValue":0},{"Version":0,"Id":46,"Reason":"","Done":1,"OrderType":1,"UtxoId":12094628167680,"OrderTime":1753714163,"AssetName":"ordx:f:ordxyz","ServiceFee":34,"UnitPrice":{"Precision":10,"Value":206253714},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"25e5be4b20deeab75a52b840265a22be28c0d1906d64ac8e60e71ebef56b7af9:0","InValue":0,"InAmt":{"Precision":0,"Value":149767},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b3f1a35f641cb09a703e3b438b221e55c2620010cd8564b4f9f4fc7562ebb6f7","OutAmt":{"Precision":0,"Value":0},"OutValue":3065},{"Version":0,"Id":45,"Reason":"","Done":1,"OrderType":2,"UtxoId":12025908690944,"OrderTime":1753714025,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":209479918},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"c6c90c85a4c8466d1ba1cd07b2368f29da999f011e9e0ab841055af6b2f65f73:0","InValue":1118,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"fc16920ca2d34931e38b79a536427845ebd7ac24fa6e10c966f01f744a0f65a7","OutAmt":{"Precision":0,"Value":52511},"OutValue":0},{"Version":0,"Id":44,"Reason":"","Done":1,"OrderType":1,"UtxoId":11922829475840,"OrderTime":1753713974,"AssetName":"ordx:f:ordxyz","ServiceFee":26,"UnitPrice":{"Precision":10,"Value":211300000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"7ffa9deba9a102799a1fa338319d91b9161e1a81574c0837dc844af4d920da0b:0","InValue":0,"InAmt":{"Precision":0,"Value":100000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"406605b34662ce40e399e51f77a228934aa6130ea4278ffac87f52bcff82eecd","OutAmt":{"Precision":0,"Value":0},"OutValue":2097},{"Version":0,"Id":43,"Reason":"","Done":1,"OrderType":1,"UtxoId":11819750260736,"OrderTime":1753713914,"AssetName":"ordx:f:ordxyz","ServiceFee":11,"UnitPrice":{"Precision":10,"Value":215000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"49ad02e46b21a9bc444166ca82febd3b222cb814f77256fc5c2592ab21004a88:0","InValue":0,"InAmt":{"Precision":0,"Value":10000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"c14b0ef8192f08c8c96cab1fffc9233586dd18f24020e900e245a0f7fa695e78","OutAmt":{"Precision":0,"Value":0},"OutValue":214},{"Version":0,"Id":42,"Reason":"","Done":1,"OrderType":2,"UtxoId":11751030784000,"OrderTime":1753713863,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":213999871},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"5fb85c040c6aee569f6d39713e4c84de48b27c8ef710bd272110fb11e80aa721:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"2890677a5010294b169999b31189f9596f060516338e8ee3fef1f00edb816046","OutAmt":{"Precision":0,"Value":46729},"OutValue":0},{"Version":0,"Id":41,"Reason":"","Done":1,"OrderType":2,"UtxoId":11682311307264,"OrderTime":1753713755,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":211267605},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"ca24f35817372f5ee902b9b161ea6e37042c3e656881ee920b8bbbf322f90590:0","InValue":614,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"ea57c58db156b1928c59eca3060b9870b213185cffca8e15c23e35f23c02657f","OutAmt":{"Precision":0,"Value":28400},"OutValue":0},{"Version":0,"Id":40,"Reason":"","Done":1,"OrderType":2,"UtxoId":10960756801536,"OrderTime":1753711763,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":208554922},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pt07drd4lwpjl8hz0f8m0fahxw0sk6djgrw67kdpxqasqudgu4eeqspl234","FromL1":false,"InUtxo":"7a16f138db144175951fca05ae9405139a2675f3c72cfb115ab4a93b841ba96e:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"6a8ce63e29b314af9ce49b639dc1c84be803bd75ffac3fbe509eaf95fc37f83f","OutAmt":{"Precision":0,"Value":47949},"OutValue":0},{"Version":0,"Id":39,"Reason":"","Done":1,"OrderType":2,"UtxoId":10892037324800,"OrderTime":1753711745,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":205170291},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pt07drd4lwpjl8hz0f8m0fahxw0sk6djgrw67kdpxqasqudgu4eeqspl234","FromL1":false,"InUtxo":"3c95263519172586f4fea990b23479dc54ad35f0ee25c29ead89426bdb8892b2:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"01faa208f5bcc68061fc5d73d5bafb291615c380d81097a94986010424947509","OutAmt":{"Precision":0,"Value":48740},"OutValue":0},{"Version":0,"Id":38,"Reason":"","Done":1,"OrderType":1,"UtxoId":10617159417856,"OrderTime":1753691003,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":205000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g","FromL1":false,"InUtxo":"256cf3650744d17d827605a05dff34cd4dd616af84e35b2dbb44554d81d94179:0","InValue":0,"InAmt":{"Precision":0,"Value":4000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"5ea14bd110ddd0c569e28a0d6880a385edc3aae91f00779d14985849359260b8","OutAmt":{"Precision":0,"Value":0},"OutValue":82},{"Version":0,"Id":37,"Reason":"","Done":1,"OrderType":1,"UtxoId":10342281510912,"OrderTime":1753677266,"AssetName":"ordx:f:ordxyz","ServiceFee":13,"UnitPrice":{"Precision":10,"Value":204625903},"ExpectedAmt":{"Precision":0,"Value":418},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"f9b94786877a12919a1b9ee9b1f302da13934105aba020b3ebee983a1266f7b3:0","InValue":0,"InAmt":{"Precision":0,"Value":20623},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"d41dbf79de88b3d4c3fd714f1c3d6cecc67a73116b36228526c7666010a3f700","OutAmt":{"Precision":0,"Value":0},"OutValue":419},{"Version":0,"Id":36,"Reason":"","Done":1,"OrderType":1,"UtxoId":10273562034176,"OrderTime":1753677140,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":205840637},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"1e0585e1b083cc3a37d6e82403bc8418b0d619015101e65c9814b3d9e72f8b3e:0","InValue":0,"InAmt":{"Precision":0,"Value":27497},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"82e09c4ec5146c78cb75e2ac428f21566c7017c671ced3ab5c65b8c271aa2375","OutAmt":{"Precision":0,"Value":0},"OutValue":562},{"Version":0,"Id":35,"Reason":"","Done":1,"OrderType":1,"UtxoId":10204842557440,"OrderTime":1753677020,"AssetName":"ordx:f:ordxyz","ServiceFee":16,"UnitPrice":{"Precision":10,"Value":208384474},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"ddba9545a0c2a21532c8f3c739719019be7fe13313c56206509f1a03d1cdda57:0","InValue":0,"InAmt":{"Precision":0,"Value":36663},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"740ad5a25b7c35b1c9db8f6ea2dcb8e1bdf5eaeac8477840813bb96e1c990f9a","OutAmt":{"Precision":0,"Value":0},"OutValue":758},{"Version":0,"Id":34,"Reason":"","Done":1,"OrderType":2,"UtxoId":10136123080704,"OrderTime":1753676966,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":208673877},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"cbd16c126ce5168138c86d4d7a8dd757ffea3a9c38484a2f8888c96070ff6fde:0","InValue":614,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"cfcd3f2b422e1ba16bed2b68a79a81a4a415655dcd02b21ad06c6adfcc6fe81a","OutAmt":{"Precision":0,"Value":28753},"OutValue":0},{"Version":0,"Id":33,"Reason":"","Done":1,"OrderType":2,"UtxoId":10067403603968,"OrderTime":1753676930,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":206640033},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"0ec3a6b66afb9920297151c555ea2de3ab095b2d40cdcd9973e32613a24b343d:0","InValue":614,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"d25b32d5a8da577e9dd88f5c34fef0c611a0752676919be69edb4f59bfe3cd06","OutAmt":{"Precision":0,"Value":29036},"OutValue":0},{"Version":0,"Id":32,"Reason":"","Done":1,"OrderType":2,"UtxoId":9998684127232,"OrderTime":1753676870,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":204631492},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"47fe142388e9e1c8bde11fb25f05dea0b5f1985c7edcc5bafc577e6029a4e37d:0","InValue":614,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"1777e018eb8aa6a62895d009f46eb258122f7c5851372a1a3aef0d09934a0bc5","OutAmt":{"Precision":0,"Value":29321},"OutValue":0},{"Version":0,"Id":31,"Reason":"","Done":1,"OrderType":2,"UtxoId":9929964650496,"OrderTime":1753676816,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":202627401},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"7e005c458a45a387b34dbb530e57174c40b35007b4678a21292f018b6a192032:0","InValue":614,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"51b3d9a511bf55e49647d437c17ad83cff0d0881acab870dfcf2e425883e670b","OutAmt":{"Precision":0,"Value":29611},"OutValue":0},{"Version":0,"Id":30,"Reason":"","Done":1,"OrderType":2,"UtxoId":9861245173760,"OrderTime":1753676750,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":200454363},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pqy3ytpfhwktxmjuhhx6ppl3pdd99h76v3qc04902msdkdhjx83jsxhy03c","FromL1":false,"InUtxo":"625a89ef1331d7f7bf021e1c15c5124dbdefd1f65e57d82a7323f3f46802c190:0","InValue":614,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"8ec1e0a810fd23ee41b0b9ee74e24b74fdcc63ba60f22eba6f6d910a1399270a","OutAmt":{"Precision":0,"Value":29932},"OutValue":0},{"Version":0,"Id":29,"Reason":"","Done":1,"OrderType":1,"UtxoId":9277129621504,"OrderTime":1753675586,"AssetName":"ordx:f:ordxyz","ServiceFee":58,"UnitPrice":{"Precision":10,"Value":209600705},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"33214e80761bbf246015449ae5315560c194fdc042ecd36a9515ffd7e237614d:0","InValue":0,"InAmt":{"Precision":0,"Value":287833},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"1d3741bda54ebac1e676e4e24a131ab38f5686964eb904160fc92664e81a8653","OutAmt":{"Precision":0,"Value":0},"OutValue":5985},{"Version":0,"Id":28,"Reason":"","Done":1,"OrderType":1,"UtxoId":9208410406912,"OrderTime":1753675568,"AssetName":"ordx:f:ordxyz","ServiceFee":64,"UnitPrice":{"Precision":10,"Value":231906923},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"649341b9d7e2c54aa56cfb343d36960ec2d1c925a6605235bfa2b7efacf4089d:0","InValue":0,"InAmt":{"Precision":0,"Value":292833},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"0a45e363b2c095e33a4054964212adbf3ddbf43a232661b8e76dfd2a0bb54047","OutAmt":{"Precision":0,"Value":0},"OutValue":6737},{"Version":0,"Id":27,"Reason":"","Done":1,"OrderType":1,"UtxoId":9174050668544,"OrderTime":1753675556,"AssetName":"ordx:f:ordxyz","ServiceFee":13,"UnitPrice":{"Precision":10,"Value":245000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"e1ce239f4ab25d598532ad6cd1b6cf34ea138f9afc90699ded5a6fafad571448:0","InValue":0,"InAmt":{"Precision":0,"Value":20000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"8b16bdc5ec6fde440ec91993b9c1522986abb62ad5abcd28c87924ceff4103ad","OutAmt":{"Precision":0,"Value":0},"OutValue":487},{"Version":0,"Id":26,"Reason":"","Done":1,"OrderType":1,"UtxoId":9139690668032,"OrderTime":1753675550,"AssetName":"ordx:f:ordxyz","ServiceFee":13,"UnitPrice":{"Precision":10,"Value":247000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"e46835bde035867a7bc6550bb40b027539882bb7cabed580bc6c4474d9aacffa:0","InValue":0,"InAmt":{"Precision":0,"Value":20000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"27a621dafe1273f9129a12bfca939e4f1e0da5404f015151b146ba0cb610fb31","OutAmt":{"Precision":0,"Value":0},"OutValue":491},{"Version":0,"Id":25,"Reason":"","Done":1,"OrderType":2,"UtxoId":9070971191296,"OrderTime":1753675496,"AssetName":"ordx:f:ordxyz","ServiceFee":14,"UnitPrice":{"Precision":10,"Value":247011164},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"04ec922cfd0bf919fb1ecdfc1578fc5d5fea93d2c83f7c375270eb389f8e1c46:0","InValue":514,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"3f6ec3ed2652296e1c3fe564154d9fd5c82690bd3d82c2e131f4970d4c4753a4","OutAmt":{"Precision":0,"Value":20242},"OutValue":0},{"Version":0,"Id":24,"Reason":"","Done":1,"OrderType":2,"UtxoId":9002251714560,"OrderTime":1753670900,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":245920478},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g","FromL1":false,"InUtxo":"3fc0d447c8819d6e5503a90e06c2c17ab327d5d2e50dfccc485c724658e87b4d:0","InValue":117,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"41bfb861afef51663d357156bc9609ed1546e37ea561f766e96778f1c873ebbd","OutAmt":{"Precision":0,"Value":4351},"OutValue":0},{"Version":0,"Id":23,"Reason":"","Done":1,"OrderType":2,"UtxoId":8933532237824,"OrderTime":1753666016,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":245700245},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g","FromL1":false,"InUtxo":"2b6b53c1721960f007d2bd37b9acb176a8d2efda7e03b18bf7e57a4f9607b1f5:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"e5ed4bb8e3a0ce92c6bee56db25618dbaa90177125eb111f0dbe8e91cbc759fa","OutAmt":{"Precision":0,"Value":407},"OutValue":0},{"Version":0,"Id":22,"Reason":"","Done":1,"OrderType":2,"UtxoId":8864812761088,"OrderTime":1753665956,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":245459008},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g","FromL1":false,"InUtxo":"0a584875640ed9c95fad69ac20c847745ba3a310f95afa97748cb2e457a7fc3a:0","InValue":110,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"72502be31cc18db60a910aabb33c6245ded0f46279860d9a1cdd36016ae2d94a","OutAmt":{"Precision":0,"Value":4074},"OutValue":0},{"Version":0,"Id":21,"Reason":"","Done":1,"OrderType":2,"UtxoId":8796093284352,"OrderTime":1753665920,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":245700245},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p339xkycqwld32maj9eu5vugnwlqxxfef3dx8umse5m42szx3n6aq6qv65g","FromL1":false,"InUtxo":"562932b48b5f3e486f68762ea586981b0089cef003ac57cbfcaff1c6b39741ee:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"a9f21c62e03be1d8c6773f6df19602d140f541007412f0c3d4a817aa3e3d27dc","OutAmt":{"Precision":0,"Value":407},"OutValue":0},{"Version":0,"Id":20,"Reason":"","Done":1,"OrderType":2,"UtxoId":8727373807616,"OrderTime":1753634516,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":244997958},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1ph9cw75y4kgw9ekqsx5puljqz3n4xl5305d4cpdmkad0c6knzd0sqka3xc8","FromL1":false,"InUtxo":"fe0fe9756d0e5cb39d38b5a2770813fb06ad98a4588e06791868fe0f22dc06f3:0","InValue":130,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"06854070964911870f1e74c402ba568129a5554ae984b0bb8b783f89d62a9bf5","OutAmt":{"Precision":0,"Value":4898},"OutValue":0},{"Version":0,"Id":19,"Reason":"","Done":1,"OrderType":2,"UtxoId":8658654330880,"OrderTime":1753634426,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":244661921},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1ph9cw75y4kgw9ekqsx5puljqz3n4xl5305d4cpdmkad0c6knzd0sqka3xc8","FromL1":false,"InUtxo":"33e81818e3f1076c537467f63acb78bbc8c33565a1aa08fdd17022b6a3e34df3:0","InValue":120,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"aaedcae0f41c438b80b7bd7f4efaa73b20a74970af7efeb12a5193c15ccbc8d7","OutAmt":{"Precision":0,"Value":4496},"OutValue":0},{"Version":0,"Id":18,"Reason":"","Done":1,"OrderType":2,"UtxoId":8589934854144,"OrderTime":1753634336,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":242072137},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1ph9cw75y4kgw9ekqsx5puljqz3n4xl5305d4cpdmkad0c6knzd0sqka3xc8","FromL1":false,"InUtxo":"db042ed1fe7059230e76f1dfca56696b4372d8d075829326f33e5f26e7b86c43:0","InValue":110,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"f435aa547dda84c73fa11760bbd4208f7a92aa9350627058b102e51933df3832","OutAmt":{"Precision":0,"Value":4131},"OutValue":0},{"Version":0,"Id":17,"Reason":"","Done":1,"OrderType":1,"UtxoId":8452496162816,"OrderTime":1753625642,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":200000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"fa5b9c4be91478328cc6f91467afd72c616727be3070b7f752d69d123f0805a3:0","InValue":0,"InAmt":{"Precision":0,"Value":100},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"0ada832a7bc52db1a93e2700b9dbe4b78277700a08c6f30ca6e990421490f74c","OutAmt":{"Precision":0,"Value":0},"OutValue":2},{"Version":0,"Id":16,"Reason":"","Done":1,"OrderType":1,"UtxoId":8418136424448,"OrderTime":1753625630,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":200000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"8efcda1fb7454247411c93cf7088fbe5f3ee9db8fbb853b9cd806cecc03a8647:0","InValue":0,"InAmt":{"Precision":0,"Value":100},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"e2754f91b41ef035eca698a6429dac9fad72835249541d91e331aaa9f76adfb2","OutAmt":{"Precision":0,"Value":0},"OutValue":2},{"Version":0,"Id":15,"Reason":"","Done":1,"OrderType":2,"UtxoId":8383776686080,"OrderTime":1753625618,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":243902439},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"aef5d9d37689a4a31e6a3a504e202cfcc57c1b5992669d335faf4ba2723940ac:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"d8dbd91798639c00f8bba890e60d50165a55dd4118510e67ba40c8838bbd0ddc","OutAmt":{"Precision":0,"Value":410},"OutValue":0},{"Version":0,"Id":14,"Reason":"","Done":1,"OrderType":2,"UtxoId":8349416685568,"OrderTime":1753625606,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":244498777},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"189d96b0b67c661fa5e14cd445ba8f21d4dfe348eb006b22747d1d40a178c1b0:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"6a1ad9bdd4f02c297f7847b310869b565857da2d7be772816269841de0d2213c","OutAmt":{"Precision":0,"Value":409},"OutValue":0},{"Version":0,"Id":13,"Reason":"","Done":1,"OrderType":2,"UtxoId":8280697208832,"OrderTime":1753625486,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":233644859},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"1db3fae265d011778074ecc2dcf53fd697b5cae7d0ef079e80d56b8dbda5ab02:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"1045b5c3637cf80f5b911373a79370aaf6b36ed0da2efdc1db830ca1aca78da5","OutAmt":{"Precision":0,"Value":428},"OutValue":0},{"Version":0,"Id":12,"Reason":"","Done":1,"OrderType":1,"UtxoId":8211977732096,"OrderTime":1753624706,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":200000000},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"37e9a7a5ef0ab46f578a3c9d266e5648e7a727bddb3b9610e6dabc8c2ae162b7:0","InValue":0,"InAmt":{"Precision":0,"Value":100},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"e9b6bcbbda4601f99b432fb6826ee6de49ababc1a483171913cc5203df68643f","OutAmt":{"Precision":0,"Value":0},"OutValue":2},{"Version":0,"Id":11,"Reason":"","Done":1,"OrderType":2,"UtxoId":8143258255360,"OrderTime":1753624661,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":243783520},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"af70c2eaa6bc7cac83cc79a494991c404a6badf950cc69ef46050b2fd2585072:0","InValue":110,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"e8bf8f1c318080e3aa39698c1f527ce0241c0a5f1bb9a81eb54c11a77d173e36","OutAmt":{"Precision":0,"Value":4102},"OutValue":0},{"Version":0,"Id":10,"Reason":"","Done":1,"OrderType":2,"UtxoId":8074538778624,"OrderTime":1753623818,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":243358345},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1ph9cw75y4kgw9ekqsx5puljqz3n4xl5305d4cpdmkad0c6knzd0sqka3xc8","FromL1":false,"InUtxo":"37f13ce9193f3aaf3baf640277b70f63663ca767c01b222e38d7bf33b5f548ac:0","InValue":130,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"bd4372760400040fc88249c2fa718321045ce57270639d9b4bf2db4051e57ed0","OutAmt":{"Precision":0,"Value":4931},"OutValue":0},{"Version":0,"Id":9,"Reason":"","Done":1,"OrderType":2,"UtxoId":8005819301888,"OrderTime":1753623677,"AssetName":"ordx:f:ordxyz","ServiceFee":10,"UnitPrice":{"Precision":10,"Value":243013365},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1ph9cw75y4kgw9ekqsx5puljqz3n4xl5305d4cpdmkad0c6knzd0sqka3xc8","FromL1":false,"InUtxo":"257617d41ac10c794912f58cd5268e9d6aab962f1e1a8a2a56e7f0a81e7b9cbf:0","InValue":110,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"82ff1cf94daec4dc34d9850c94ed465b1036422aef1273f5fe2e0d5871790fdc","OutAmt":{"Precision":0,"Value":4115},"OutValue":0},{"Version":0,"Id":8,"Reason":"","Done":1,"OrderType":2,"UtxoId":7868380348416,"OrderTime":1753551762,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":240969661},"ExpectedAmt":{"Precision":0,"Value":40668},"Address":"tb1pc5e8nm4996pg22mn0ffqngpprzysd05pk67p3ey4k06s9h2y069q6ysqhd","FromL1":false,"InUtxo":"3b523b2a21fb9b5a061070fed44b30683d6edd3d964a2a9a773628e275001c56:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"dde5296bfcb894d740dab6f3b2fdfb22e97f4dce3afd22e486a28deb0916b394","OutAmt":{"Precision":0,"Value":41499},"OutValue":0},{"Version":0,"Id":7,"Reason":"","Done":1,"OrderType":2,"UtxoId":7799660871680,"OrderTime":1753551522,"AssetName":"ordx:f:ordxyz","ServiceFee":12,"UnitPrice":{"Precision":10,"Value":238076343},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pc5e8nm4996pg22mn0ffqngpprzysd05pk67p3ey4k06s9h2y069q6ysqhd","FromL1":false,"InUtxo":"72bd1994007dbe5b94e2f94360eb796be595242c7b772405f59fbd2d8a4ff1cf:0","InValue":312,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"e8bc2dfd401e3e75c8ba7f3bb5607094c4a2d3ec8ee004fdeeca8a98ed72ed98","OutAmt":{"Precision":0,"Value":12601},"OutValue":0},{"Version":0,"Id":6,"Reason":"","Done":1,"OrderType":1,"UtxoId":7524782964736,"OrderTime":1753549878,"AssetName":"ordx:f:ordxyz","ServiceFee":89,"UnitPrice":{"Precision":10,"Value":256119929},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"315ce96622f9a5bc22cd6d64667154a6bf85127dcf085777d7317725b1c981b1:0","InValue":0,"InAmt":{"Precision":0,"Value":390364},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"701ae2c8f1787596d20a2b13cbd04c962fbf1b61aa4d0ae0eb8088d31a3165cc","OutAmt":{"Precision":0,"Value":0},"OutValue":9919},{"Version":0,"Id":5,"Reason":"","Done":1,"OrderType":1,"UtxoId":7456063488000,"OrderTime":1753548162,"AssetName":"ordx:f:ordxyz","ServiceFee":34,"UnitPrice":{"Precision":10,"Value":281399493},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"590ea27fab9930e5f132eb51636a61f633ba909fe1596fceaf484cc5ddd53516:0","InValue":0,"InAmt":{"Precision":0,"Value":106610},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"7b84902476aabe6b1c856da63041acbb86a1054fe59dca0f0b7128cc73b4ccad","OutAmt":{"Precision":0,"Value":0},"OutValue":2976},{"Version":0,"Id":4,"Reason":"","Done":1,"OrderType":2,"UtxoId":7387344011264,"OrderTime":1753548018,"AssetName":"ordx:f:ordxyz","ServiceFee":32,"UnitPrice":{"Precision":10,"Value":281760553},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"85501e9a756561e5d5b211de8b5430644bcbf2694b9fdec91cc3c270c47eb6ab:0","InValue":2850,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"5959cdd93cfe793b5f0cd4c842b7d035a572c925e3034a05aaf20af9835097e0","OutAmt":{"Precision":0,"Value":100014},"OutValue":0},{"Version":0,"Id":3,"Reason":"","Done":1,"OrderType":2,"UtxoId":7318624534528,"OrderTime":1753547898,"AssetName":"ordx:f:ordxyz","ServiceFee":31,"UnitPrice":{"Precision":10,"Value":271005420},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"fd0ec8c884a7ca1565649660aa4b18f8de099858144ead863155922dc368466d:0","InValue":2741,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"ac801018a895db53ee0f18cba7113d79e2bef6a9caef2c7681cddd79e05ca824","OutAmt":{"Precision":0,"Value":99998},"OutValue":0},{"Version":0,"Id":2,"Reason":"","Done":1,"OrderType":2,"UtxoId":6803228459008,"OrderTime":1753546779,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":263692218},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"88c9dfff29b1d3b8dc68b14fca520d214360c8bf90400c1b2a30e687a8e93913:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"5b7262ae53cd4783c05a3a084d8da7c03d707c3ea99a3fc368e22b7caa49b4a7","OutAmt":{"Precision":0,"Value":37923},"OutValue":0},{"Version":0,"Id":1,"Reason":"","Done":1,"OrderType":1,"UtxoId":6734508982272,"OrderTime":1753546589,"AssetName":"ordx:f:ordxyz","ServiceFee":127,"UnitPrice":{"Precision":10,"Value":289911862},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"4d1b1486ad38dffa32821723c68a61d6f4b47f35754ee7c5048a45965d2f6f7b:0","InValue":0,"InAmt":{"Precision":0,"Value":507844},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"f6681c18ded1011e216abae2097478d14fea7b5a3ceb5f9a3dce295c1c4790f6","OutAmt":{"Precision":0,"Value":0},"OutValue":14606},{"Version":0,"Id":0,"Reason":"","Done":1,"OrderType":2,"UtxoId":6665789505536,"OrderTime":1753546493,"AssetName":"ordx:f:ordxyz","ServiceFee":18,"UnitPrice":{"Precision":10,"Value":318704783},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"b03436b2ae5a0154a0d6cc1eba9210ed71acb4c3625a0e8107e7862cae61295b:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"ae869f257f77ebb309636d8dcb0be679ced8717e7d025a690dafef74d6487969","OutAmt":{"Precision":0,"Value":31377},"OutValue":0}]`
	var inputs []*SwapHistoryItem
	err = json.Unmarshal([]byte(itemHistory), &inputs)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(inputs, func(i, j int) bool {
		return inputs[i].Id < inputs[j].Id
	})

	tickInfo := _client.getTickerInfo(assetName)
	if tickInfo == nil {
		t.Fatal("can't find ticker")
	}

	ammContract := NewAmmContract()
	ammContract.AssetName = *assetName
	ammContract.AssetAmt = "2999800"
	ammContract.SatValue = 69982
	ammContract.K = "209932003600"

	deployFee, err := _client.QueryFeeForDeployContract(ammContract.TemplateName, (ammContract.Content()), 1)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("deploy contract %s need %d sats\n", ammContract.TemplateName, deployFee)
	fmt.Printf("use RemoteDeployContract to deploy a contract on core channel in server node\n")

	invokeParam, err := _client.QueryParamForInvokeContract(ammContract.TemplateName, INVOKE_API_SWAP)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("use %s as template to invoke contract %s\n", invokeParam, ammContract.TemplateName)

	assetAmt := _server.GetAssetBalance_SatsNet("", &ASSET_PLAIN_SAT)
	fmt.Printf("plain sats: %d\n", assetAmt)
	txId, id, url, err := _client.DeployContract_Remote(ammContract.TemplateName,
		string(ammContract.Content()), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("RemoteDeployContract succeed, %s, %d, %s\n", txId, id, url)

	channelId := ExtractChannelId(url)
	txId, err = _client.SendAssetsV3_SatsNet(channelId, ammContract.GetAssetName().String(),
		ammContract.AssetAmt, ammContract.SatValue, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", txId)
	txId, err = _client.SendAssetsV3_SatsNet(channelId, ammContract.GetAssetName().String(),
		ammContract.AssetAmt, ammContract.SatValue, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", txId)
	tx, err := _client.SendAssets(channelId, ammContract.GetAssetName().String(),
		"3000000", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", tx.TxID())
	tx, err = _client.SendAssets(channelId,
		indexer.ASSET_PLAIN_SAT.String(),
		fmt.Sprintf("%d", ammContract.SatValue), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", tx.TxID())

	run := true
	go sendInBackground(&run) // 有时需要更新区块引起合约调用
	for {
		status, err := _client.GetContractStatusInServer(url)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		fmt.Printf("%s\n", status)
		contractRuntime := NewContractRuntime(_client, ammContract.TemplateName)
		err = json.Unmarshal([]byte(status), contractRuntime)
		if err != nil {
			t.Fatal(err)
		}
		swapRuntime, ok := contractRuntime.(*AmmContractRuntime)
		if !ok {
			t.Fatal()
		}
		if swapRuntime.IsActive() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	run = false

	contractRuntime := _server.GetContract(url)
	if contractRuntime == nil {
		t.Fatal("")
	}
	amm, ok := contractRuntime.(*AmmContractRuntime)
	if !ok {
		t.Fatal("")
	}
	contractRuntime2 := _client.GetContract(url)
	if contractRuntime2 == nil {
		t.Fatal("")
	}
	amm2, ok := contractRuntime2.(*AmmContractRuntime)
	if !ok {
		t.Fatal("")
	}
	if !bytes.Equal(amm.StaticMerkleRoot, amm2.StaticMerkleRoot) {
		t.Fatal("static merkle root not inconsistent")
	}
	_not_invoke_block = true // 停止 InvokeWithBlock 和 InvokeWithBlock_SatsNet
	for _, item := range inputs {
		fmt.Printf("item: %v\n", item)
		// 去掉处理结果
		item.RemainingAmt = item.InAmt.Clone()
		item.RemainingValue = item.InValue
		item.OutAmt = nil
		item.OutValue = 0
		item.Done = DONE_NOTYET
		item.UtxoId = indexer.ToUtxoId(_server.status.SyncHeightL2, 1, 0)
		if item.Reason != INVOKE_REASON_INVALID {
			item.Reason = INVOKE_REASON_NORMAL
		}

		if item.Id == 16 {
			Log.Infof("")
		}

		h, _, _ := indexer.FromUtxoId(item.UtxoId)
		amm.CurrBlock = h
		amm2.CurrBlock = h

		////////
		// 以下代码模拟正常调用过程
		item2 := item.Clone()
		beforeAmt := amm.AssetAmtInPool.Clone()
		beforeValue := amm.SatsValueInPool

		beforeAmt2 := amm2.AssetAmtInPool.Clone()
		beforeValue2 := amm2.SatsValueInPool

		switch item.OrderType {
		case ORDERTYPE_BUY, ORDERTYPE_SELL:
			if item.OrderType == ORDERTYPE_BUY {
				item.RemainingValue = item.GetTradingValueForAmm()
			} else {
				item.RemainingValue = 0
				item.InValue = 0
			}
			amm.updateContractStatus(item)
			amm.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			if item2.OrderType == ORDERTYPE_BUY {
				item2.RemainingValue = item2.GetTradingValueForAmm()
			} else {
				item2.RemainingValue = 0
				item2.InValue = 0
			}
			amm2.updateContractStatus(item2)
			amm2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)

		case ORDERTYPE_WITHDRAW, ORDERTYPE_DEPOSIT:
			if item.AssetName != indexer.ASSET_PLAIN_SAT.String() {
				item.RemainingValue = 0
				item2.RemainingValue = 0
			}

			_not_send_tx = false
			amm.updateContractStatus(item)
			amm.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			amm2.updateContractStatus(item2)
			amm2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)

		case ORDERTYPE_REFUND:
			amm.updateContractStatus(item)
			amm.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			amm2.updateContractStatus(item2)
			amm2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)

		default:
			fmt.Printf("invalid type %d\n", item.OrderType)
			t.Fatal()
		}

		amm.swap(beforeAmt, beforeValue)
		amm.invokeCompleted()
		amm2.swap(beforeAmt2, beforeValue2)
		amm2.invokeCompleted()

		if !bytes.Equal(amm.CurrAssetMerkleRoot, amm2.CurrAssetMerkleRoot) {
			t.Fatal("asset merkle root not inconsistent")
		}

		_not_send_tx = true // 停止广播
		amm.sendInvokeResultTx()
		amm.sendInvokeResultTx_SatsNet()
		_not_send_tx = false // 需要生成一个新的tx，不然可能影响测试结果
		txId, err := _server.CoBatchSend_SatsNet(nil, []string{channelId}, ASSET_PLAIN_SAT.String(),
			[]*Decimal{indexer.NewDecimal(10, 0)}, "testing", amm.URL(), 0, nil, amm.StaticMerkleRoot, amm.CurrAssetMerkleRoot)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("dummy tx %s", txId)

		////////

		// 检查每次处理的结果
		err = amm.checkSelf()
		if err != nil {
			t.Fatalf("amm1: %d %v", item.Id, err)
		}

		err = amm2.checkSelf()
		if err != nil {
			t.Fatalf("amm2: %d %v", item.Id, err)
		}

		// 等待处理
		//time.Sleep(time.Second)
	}

	//
	fmt.Printf("realRunningData: %v\n", realRunningData)
	fmt.Printf("simuRunningData: %v\n", amm.SwapContractRunningData)

}

func TestAmmContract_Runes(t *testing.T) {
	prepare(t)

	// 需要修改资产名称为模拟环境支持的币
	assetName := indexer.NewAssetNameFromString("runes:f:TEST•FIRST•TEST")
	runningData := `{"AssetAmtInPool":{"Precision":0,"Value":3419651},"SatsValueInPool":61401,"LowestSellPrice":null,"HighestBuyPrice":null,"LastDealPrice":{"Precision":10,"Value":178970917},"HighestDealPrice":{"Precision":10,"Value":3333333333},"LowestDealPrice":{"Precision":10,"Value":112449799},"TotalInputAssets":{"Precision":0,"Value":15406324},"TotalInputSats":153493,"TotalDealAssets":{"Precision":0,"Value":4676573},"TotalDealSats":151758,"TotalDealCount":40,"TotalDealTx":41,"TotalDealTxFee":410,"TotalRefundAssets":null,"TotalRefundSats":0,"TotalRefundTx":0,"TotalRefundTxFee":0,"TotalProfitAssets":null,"TotalProfitSats":0,"TotalProfitTx":0,"TotalProfitTxFee":0,"TotalDepositAssets":{"Precision":0,"Value":60000},"TotalDepositTx":1,"TotalDepositTxFee":806,"TotalWithdrawAssets":null,"TotalWithdrawTx":0,"TotalWithdrawTxFee":0}`
	var realRunningData SwapContractRunningData
	err := json.Unmarshal([]byte(runningData), &realRunningData)
	if err != nil {
		t.Fatal(err)
	}

	itemHistory := `[{"Version":0,"Id":0,"Reason":"","Done":1,"OrderType":2,"UtxoId":102151502430208,"OrderTime":1750780349,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":82511},"Address":"tb1ptjxn9km8xrqdpp4he469mpudp78ssxrehzlut8783gg703plj7cqms02r7","FromL1":false,"InUtxo":"1dc3c47bc188e3b2e77a67ba6195b524a7f3a0af68353bf44cc5e4956994f7e5:0","InValue":2026,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"0d6a00697d35ddac19031611b319374fc0f148552f51a0d93a802be38f7038d6","OutAmt":{"Precision":0,"Value":83345},"OutValue":0},{"Version":0,"Id":1,"Reason":"","Done":1,"OrderType":1,"UtxoId":102288941383680,"OrderTime":1750780445,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1ptjxn9km8xrqdpp4he469mpudp78ssxrehzlut8783gg703plj7cqms02r7","FromL1":false,"InUtxo":"a4cd8a3413627dccf14003fc81a87dab808779d67b173a176f096ba20bf8cc49:0","InValue":10,"InAmt":{"Precision":0,"Value":1083345},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"ddf8e18f9ead0948a99f4f8105d50bc0dbbdb2b76c2a90cc74cecc53ca245a52","OutAmt":{"Precision":0,"Value":0},"OutValue":19333},{"Version":0,"Id":2,"Reason":"","Done":1,"OrderType":2,"UtxoId":102357660860416,"OrderTime":1750780649,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"c406f098ffe0e5b142bc4911087f97a5d02fac88d3446df614941d273964d673:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"8c12ac1e6cabf7bb89573d15b618ae6290fe7ac2ddf2c8e7cc50282dbaf8cd91","OutAmt":{"Precision":0,"Value":74741},"OutValue":0},{"Version":0,"Id":3,"Reason":"","Done":1,"OrderType":2,"UtxoId":102426380337152,"OrderTime":1750780709,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"6db1d0f2af345eb0b04522b161c150fea1ad77901e452a060c321daed77baf17:0","InValue":2026,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"7b8e4926f9db9d50786b48d11724986dcee9fd15915be493936a2d2c9af87e4b","OutAmt":{"Precision":0,"Value":141471},"OutValue":0},{"Version":0,"Id":4,"Reason":"","Done":1,"OrderType":2,"UtxoId":102495099813888,"OrderTime":1750780781,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":59180},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"38bc371e47270e0f442dab1e1aa98686aa226de7007fcb071355032f76fb099f:0","InValue":917,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"f38b004b7072d56c59d21672a85775a69fb81128328957544736530a66e0eb7c","OutAmt":{"Precision":0,"Value":60388},"OutValue":0},{"Version":0,"Id":5,"Reason":"","Done":1,"OrderType":1,"UtxoId":102563819290624,"OrderTime":1750780841,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":3524},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"4c8bbb6f2cfcf9c9754ff1255ad63cb5546ad6f3e7f53cc3618a743ac1620238:0","InValue":10,"InAmt":{"Precision":0,"Value":265097},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"b4b6c6fe2aab6bea9aa0beeb0c1a249d1e1c9e65e7e20657ea0ab1dbf465fa58","OutAmt":{"Precision":0,"Value":0},"OutValue":3710},{"Version":0,"Id":6,"Reason":"","Done":1,"OrderType":2,"UtxoId":102632538767360,"OrderTime":1750780877,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":128995},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"8e5a72e0001b90a9ac971778a513e1d2c8a07e29bdabda13b2c7f997ae661b86:0","InValue":2026,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"52804afc33ad014974c6b035bc77f0b466117ab3825ac0c08568462e0ac699b1","OutAmt":{"Precision":0,"Value":145919},"OutValue":0},{"Version":0,"Id":7,"Reason":"","Done":1,"OrderType":2,"UtxoId":102701258244096,"OrderTime":1750780913,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":65639},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"b1a963d6cd20a9e272342e4591cdf345d7e21f25edb5cc56c3c8e0b51c549a10:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"cc24d4266f5c167c5bc327422d34183e96b76426a08f07f7f2988e06206e86c9","OutAmt":{"Precision":0,"Value":69058},"OutValue":0},{"Version":0,"Id":8,"Reason":"","Done":1,"OrderType":1,"UtxoId":102769977720832,"OrderTime":1750780973,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":731},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"f0c19d5bfc1093bd87750c06b447647290d604f99f61a6630d17155663c5618f:0","InValue":10,"InAmt":{"Precision":0,"Value":51577},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"1ef7939c54cf699af80898f9a52f1d4e03eb6eef1f0856fc12ab282d1e6d0cf2","OutAmt":{"Precision":0,"Value":0},"OutValue":735},{"Version":0,"Id":9,"Reason":"","Done":1,"OrderType":1,"UtxoId":102838697197568,"OrderTime":1750781057,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"c027f1805f0adea7e8b781b22a709330a7bda23bb2d0f6ed087a6f37e09c4e15:0","InValue":10,"InAmt":{"Precision":0,"Value":68841},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"6bd71a2c9b70583e5bb6ea591883236506b64af59c401f3ad73d92612be1aba2","OutAmt":{"Precision":0,"Value":0},"OutValue":953},{"Version":0,"Id":10,"Reason":"slippage protection","Done":1,"OrderType":1,"UtxoId":102873056935936,"OrderTime":1750781081,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":3350},"Address":"tb1psd7vljvmp5x03nrzl7x2aelswu6slny962zdnuhd4f3vkzc2k86squfg8v","FromL1":false,"InUtxo":"1f800f8f3b0653e4ba288581792aa2271fd773ff98819d021a443d876d6ba3b1:0","InValue":10,"InAmt":{"Precision":0,"Value":250000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"2a3535175c7647ea513769a0071f7d6bcd0e9c218be70385db3fc439debd6341","OutAmt":{"Precision":0,"Value":246250},"OutValue":0},{"Version":0,"Id":11,"Reason":"","Done":1,"OrderType":2,"UtxoId":102941776412672,"OrderTime":1750781165,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1psd7vljvmp5x03nrzl7x2aelswu6slny962zdnuhd4f3vkzc2k86squfg8v","FromL1":false,"InUtxo":"f83a68afefcb9ccab4d8524bc78a795dfbd0828862d798aaca216b0d0bde592f:0","InValue":30,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"f892bb9c300fc4cb15c738e95127834be9f90e67b5f5e190d6f73589fd67432b","OutAmt":{"Precision":0,"Value":1424},"OutValue":0},{"Version":0,"Id":12,"Reason":"","Done":1,"OrderType":1,"UtxoId":103010495889408,"OrderTime":1750781225,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"2349bd8138cfcc3759e804a5870a4d9d649eded068d1b8e3e414b43f179d0393:0","InValue":10,"InAmt":{"Precision":0,"Value":444925},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"4de6605c50bfa34a67b46abf7870e6889713a32baa0469f6f11360ebecc0eef7","OutAmt":{"Precision":0,"Value":0},"OutValue":5480},{"Version":0,"Id":13,"Reason":"","Done":1,"OrderType":1,"UtxoId":103044855627776,"OrderTime":1750781237,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1psd7vljvmp5x03nrzl7x2aelswu6slny962zdnuhd4f3vkzc2k86squfg8v","FromL1":false,"InUtxo":"b0a1a09e994ca5262800eb68e012b4c91a1c6ed8fc5e2fa4fc234876acbe4e5c:0","InValue":10,"InAmt":{"Precision":0,"Value":2490},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"0f399a5b60fe3719550673cecaf3c27f34d8aa4b438b9f951205092ee906ecc6","OutAmt":{"Precision":0,"Value":0},"OutValue":18},{"Version":0,"Id":14,"Reason":"","Done":1,"OrderType":2,"UtxoId":103113575104512,"OrderTime":1750781273,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"de399191196400d0557db4c297ae5b6f9eaa324573caf0d449a0d2e4fa441022:0","InValue":5050,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"026a44dccb11306a3e43a939840db370247d675d3a0f257dd0c6e7b3ccf25d9a","OutAmt":{"Precision":0,"Value":406434},"OutValue":0},{"Version":0,"Id":15,"Reason":"","Done":1,"OrderType":2,"UtxoId":103182294581248,"OrderTime":1750781333,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":139212},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"52ad798e78887c509326791be2c36f0f3f77ff6e07cebe586cc6cc695241c8cc:0","InValue":2026,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"30bc15809ce851933cb0e3201bbac5f1b6b8608ca556a007d51f2d2d8153959b","OutAmt":{"Precision":0,"Value":142055},"OutValue":0},{"Version":0,"Id":16,"Reason":"","Done":1,"OrderType":2,"UtxoId":103251014057984,"OrderTime":1750781429,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":129507},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"2290fa173a71206cf3587bfd8a4d57588f331e98f761bde728746e1b82a1c4b3:0","InValue":2026,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"e3fb445d28832b47f0de0b44df6940380ea509847a9eac3f59466bfc6f5139b8","OutAmt":{"Precision":0,"Value":132152},"OutValue":0},{"Version":0,"Id":17,"Reason":"","Done":1,"OrderType":1,"UtxoId":103319733534720,"OrderTime":1750781465,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"ac68b76f6a6f1f2122bd67f7d92e4bb4ff7f4bdb7d68d67b6bde48486dfa285a:0","InValue":10,"InAmt":{"Precision":0,"Value":50000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"2f5927a223d20cc7ab10b8bd2494ab539deb3a290d7ec7d80d18bea387f7ab9d","OutAmt":{"Precision":0,"Value":0},"OutValue":758},{"Version":0,"Id":18,"Reason":"","Done":1,"OrderType":1,"UtxoId":103388453011456,"OrderTime":1750781489,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pw4ytvg6h9pzaufu8jslxel597c75v0jl4sxcclz2d2l8amzdanpqtvr0w4","FromL1":false,"InUtxo":"787b5a72758e8f37da819158259f4e63025cd6ed4b1728e16bb55bbc606a5c73:0","InValue":10,"InAmt":{"Precision":0,"Value":10000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"6d8523d2d107ca4a3ac59fd0ab98dbb6d9299ce19d8255eea900ce3ed9743e62","OutAmt":{"Precision":0,"Value":0},"OutValue":141},{"Version":0,"Id":19,"Reason":"","Done":1,"OrderType":2,"UtxoId":103457172488192,"OrderTime":1750781525,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"4529d81805277955ac33e1315f78b83ea0be1fdabae00fc8351a91af9d9d588f:0","InValue":100810,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"95597bf5213fa7676c808f3d5051b0da89e03143187ac4a0b027791c8a088c2f","OutAmt":{"Precision":0,"Value":2377181},"OutValue":0},{"Version":0,"Id":20,"Reason":"","Done":1,"OrderType":1,"UtxoId":103525891964928,"OrderTime":1750781597,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"640729e0a99d5fdc705e284c997f30112cd97c239a784cce467ac68c131db0a6:0","InValue":10,"InAmt":{"Precision":0,"Value":1751374},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"610e9e9588e99329528e231f160c0c0b9458223f1678d07828cda0eb567e3a5d","OutAmt":{"Precision":0,"Value":0},"OutValue":87856},{"Version":0,"Id":21,"Reason":"","Done":1,"OrderType":1,"UtxoId":103594611441664,"OrderTime":1750781717,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pw4ytvg6h9pzaufu8jslxel597c75v0jl4sxcclz2d2l8amzdanpqtvr0w4","FromL1":false,"InUtxo":"206bf2770c7c7f957cb27e7ab60127b0213bf0b3270519e6e0e03f2e0f68bb32:0","InValue":10,"InAmt":{"Precision":0,"Value":90000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"5e39508e6f10a8ef832030d270d015eb2baae465012f8ed4f982cf0856da8f9b","OutAmt":{"Precision":0,"Value":0},"OutValue":1893},{"Version":0,"Id":22,"Reason":"","Done":1,"OrderType":2,"UtxoId":103594611703808,"OrderTime":1750781717,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":689956},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"94211dfa3fc28c686f381d17d6ff8e807fab0c8f845807f5c9b354b090aba144:0","InValue":20170,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"5e39508e6f10a8ef832030d270d015eb2baae465012f8ed4f982cf0856da8f9b","OutAmt":{"Precision":0,"Value":740692},"OutValue":0},{"Version":0,"Id":23,"Reason":"","Done":1,"OrderType":1,"UtxoId":103663330918400,"OrderTime":1750781777,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":19600},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"f9b80c99b9f15eb0d22e9c27d4d64a61031d11f60ece98637d278ee5a7722fd8:0","InValue":10,"InAmt":{"Precision":0,"Value":740745},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"1ffd3c149d541210c5f68f73c047d57b019d461e8a2496adf037cca6594d57f5","OutAmt":{"Precision":0,"Value":0},"OutValue":19830},{"Version":0,"Id":24,"Reason":"","Done":1,"OrderType":1,"UtxoId":103732050395136,"OrderTime":1750781885,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":7814},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"d43b0ad1f168e46abd7ec9631ca17f3ca30f70371a65d792fcf10cd3d3a17e39:0","InValue":10,"InAmt":{"Precision":0,"Value":437830},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"66f5b6bc7cee981080782ba87a80a1e39e51ae50eb168e1ad995e57c05b83e08","OutAmt":{"Precision":0,"Value":0},"OutValue":7902},{"Version":0,"Id":25,"Reason":"","Done":1,"OrderType":2,"UtxoId":103800769871872,"OrderTime":1750781993,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"9b4043d56153eeeb8867d8c1137610c1bd710ad38154e528084e81f213f04cdb:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"1b60b8addec07613ae85fe8281a5b28873e400b7637c3b3a7189ecb5459f8c23","OutAmt":{"Precision":0,"Value":61344},"OutValue":0},{"Version":0,"Id":26,"Reason":"","Done":1,"OrderType":2,"UtxoId":103869489348608,"OrderTime":1750782023,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"2d318ff9a3c909ff6c0506aa902d03d5dbcad4f8544a90bc82775fac8d8792aa:0","InValue":2026,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"fc9e14970b5e76e0190e56ecdeca16e03f1078b9fbdb0483ef861788ec482e07","OutAmt":{"Precision":0,"Value":116760},"OutValue":0},{"Version":0,"Id":27,"Reason":"","Done":1,"OrderType":2,"UtxoId":103903849086976,"OrderTime":1750782035,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1p7q2t454hg6r0scdaphud3rdhtc7ghav9gne09hj203ck2q2hjphqcs8vuz","FromL1":false,"InUtxo":"3e764c9d053faff1c4fec4cc6488d2e50c07683c66ed46d5eab46a9dd7178cae:0","InValue":2026,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"3e5f2cc60892f3a806f7d4667f900aba138dac4cd2c5736d2126d383497db3bc","OutAmt":{"Precision":0,"Value":109345},"OutValue":0},{"Version":0,"Id":28,"Reason":"slippage protection","Done":1,"OrderType":2,"UtxoId":103938208825344,"OrderTime":1750782053,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":61037},"Address":"tb1p30x9tc93c3rlfwxv5h5mcpxpwnxx0rvjt5x2f9ef9uukd9l98y4slp2ukr","FromL1":false,"InUtxo":"d775bf74f0e3e0c18383c8c73dd554040dc84e614f5ff895865fc4b2decfa866:0","InValue":1018,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"96ea5682f4a3082d345f2cad5e2b2862068a845c0205b3b65829ac1295ebf6a6","OutAmt":{"Precision":0,"Value":0},"OutValue":1000},{"Version":0,"Id":29,"Reason":"","Done":1,"OrderType":1,"UtxoId":104006928302080,"OrderTime":1750782143,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":1824},"Address":"tb1p30x9tc93c3rlfwxv5h5mcpxpwnxx0rvjt5x2f9ef9uukd9l98y4slp2ukr","FromL1":false,"InUtxo":"167ef13b3725682655550b2bbfb1adb845b90c3bdd0f5083477fdd7d7b5e1188:0","InValue":10,"InAmt":{"Precision":0,"Value":100000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"97ee0221ee28deef36bf3bf1030dc23a92930169b5f66f466ea158e3a39e4d87","OutAmt":{"Precision":0,"Value":0},"OutValue":1811},{"Version":0,"Id":30,"Reason":"","Done":0,"OrderType":6,"UtxoId":3019018473046016,"OrderTime":1750782519,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":null,"ExpectedAmt":{"Precision":0,"Value":10000000},"Address":"tb1pm9ttvg52p3jcz0rgcmrl76lv3e0amteu9sghmppf04ekgluzjyqq7g6rym","FromL1":true,"InUtxo":"a0ca6cd6ebd639335b3c3a3c6964fb67f7022328fe6ded5ca41b8d1dd7275727:0","InValue":659,"InAmt":{"Precision":0,"Value":10000000},"RemainingAmt":{"Precision":0,"Value":10000000},"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":{"Precision":0,"Value":0},"OutValue":0},{"Version":0,"Id":31,"Reason":"","Done":1,"OrderType":7,"UtxoId":104075647778816,"OrderTime":1750782575,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":null,"ExpectedAmt":{"Precision":0,"Value":10000},"Address":"tb1pw4ytvg6h9pzaufu8jslxel597c75v0jl4sxcclz2d2l8amzdanpqtvr0w4","FromL1":false,"InUtxo":"6cfb9b1631aae1a0e804e3e6c0dc5bda3a56013184d631d12f8140c3188b3bf3:0","InValue":3538,"InAmt":{"Precision":0,"Value":10000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"80ca0dc103ac3346576f016aaee06ca1756dca152517fc75ca4a7d889cf906de","OutAmt":{"Precision":0,"Value":10000},"OutValue":0},{"Version":0,"Id":32,"Reason":"","Done":1,"OrderType":7,"UtxoId":104110007517184,"OrderTime":1750784273,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":null,"ExpectedAmt":{"Precision":0,"Value":50000},"Address":"tb1pydmhr3ud7e28g6lq7xgmfrz2e3uzxvw0zatv0d8auhwnatzrqawshjhh34","FromL1":false,"InUtxo":"f22bd11f19c84a9e61393ec0eb632c7fdba03f0e20e4135df1e5369fc358755f:0","InValue":3538,"InAmt":{"Precision":0,"Value":50000},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"80ca0dc103ac3346576f016aaee06ca1756dca152517fc75ca4a7d889cf906de","OutAmt":{"Precision":0,"Value":50000},"OutValue":0},{"Version":0,"Id":33,"Reason":"","Done":1,"OrderType":2,"UtxoId":104178726993920,"OrderTime":1750817915,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"2c980f3ba3595000e5dcf88423cfa5b09e8edf72451660564edb38258dcc7815:0","InValue":11,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"0b0514531b02d3d0d500002c883693bb1d6e6c312b278fa4b73b3037374519c7","OutAmt":{"Precision":0,"Value":3},"OutValue":0},{"Version":0,"Id":34,"Reason":"","Done":1,"OrderType":2,"UtxoId":104247446470656,"OrderTime":1750818167,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"04b13b6010b4601476e13018f7928617656118ffd8223f67205fa1d9f9132281:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"abaaf0c63dab80b190a577b5a572a157b0a2c78ef789b54a11598d5c559cddd1","OutAmt":{"Precision":0,"Value":562},"OutValue":0},{"Version":0,"Id":35,"Reason":"","Done":1,"OrderType":2,"UtxoId":104316165947392,"OrderTime":1750818203,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"506a0219f194b341f0ca30474476d220b4f1280a270d5c67872345bc6e5788d9:0","InValue":11,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"3ca7780ddadf66b678c8bdf36695e1fb9e3588d4c32dda6dddc549b31db675f3","OutAmt":{"Precision":0,"Value":56},"OutValue":0},{"Version":0,"Id":36,"Reason":"","Done":1,"OrderType":2,"UtxoId":104384885424128,"OrderTime":1750818347,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"aa1fbf53e4eaeebaf5424f5421d80770ed9d5cd8ad200139538cb53fac35354e:0","InValue":11,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"063658cc313eebfa0552feef5b40a7738a005c010422ec57ef7a91c5d881a164","OutAmt":{"Precision":0,"Value":56},"OutValue":0},{"Version":0,"Id":37,"Reason":"","Done":1,"OrderType":2,"UtxoId":104453604900864,"OrderTime":1750818383,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"ed2c96bc58502fd4964e4ce3b3147d0a0a5597a68eecac980c7c74c2840c8b3e:0","InValue":11,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"ce6108346e793343167d4bd651bd1865ae3d4f9016b4852a14a35877156160a6","OutAmt":{"Precision":0,"Value":56},"OutValue":0},{"Version":0,"Id":38,"Reason":"","Done":1,"OrderType":2,"UtxoId":104522324377600,"OrderTime":1750818473,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"15aba01c6f3bb92de354ac3a1ec314e05f9e0a300e460eab325d5ae364f8f2a7:0","InValue":11,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"e0535c173958b12aa17d47b25aa335d2ab18cf01fad20f23ebc2651db295beb5","OutAmt":{"Precision":0,"Value":56},"OutValue":0},{"Version":0,"Id":39,"Reason":"","Done":1,"OrderType":2,"UtxoId":104591043854336,"OrderTime":1750818581,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"385e40a93a4800129e5e8677c412fcf978c2a1e0db99ad08ee425aade70f76df:0","InValue":11,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"fe5d92c1b9b2285a198aae0a1342682aabdb79a01f2f8bf8d7352466e8217208","OutAmt":{"Precision":0,"Value":56},"OutValue":0},{"Version":0,"Id":40,"Reason":"","Done":1,"OrderType":2,"UtxoId":104659763331072,"OrderTime":1750818797,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"e221593760d76a18b83423d7755136d8443f5903a93d2c15d484cac92c2da649:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"21b7685f245f2a4c3e6ab7401ce732effe9b8d170f3c923d00f36a22bc41da9a","OutAmt":{"Precision":0,"Value":562},"OutValue":0},{"Version":0,"Id":41,"Reason":"no enough asset","Done":3,"OrderType":1,"UtxoId":104728482807808,"OrderTime":1750819067,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"a734b388aeb37faf7a93a9ef4f5953b9cd7434ca6ab5ee6c88c0dac566b76421:0","InValue":10,"InAmt":{"Precision":0,"Value":100},"RemainingAmt":null,"RemainingValue":0,"ToL1":false,"OutTxId":"","OutAmt":null,"OutValue":0},{"Version":0,"Id":42,"Reason":"","Done":1,"OrderType":2,"UtxoId":104762842546176,"OrderTime":1750819187,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"6437bfa86bf729be22a79c079e2ffa6a47469b340ba07201e4b8a0ab74599719:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"567a9aeb5e1d0718cd1e9de159285b3f0199a8bfdd6784a4a590241e8eb61f4d","OutAmt":{"Precision":0,"Value":561},"OutValue":0},{"Version":0,"Id":43,"Reason":"","Done":1,"OrderType":2,"UtxoId":104831562022912,"OrderTime":1750819547,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"5ad15052152c63e57ea99b2a2d638a21a972ad23216d1e743560266dbbcd37a4:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"130e9dab1ab62c65116061585a4f55358281a9cf5e1280a20f2f94a02c544618","OutAmt":{"Precision":0,"Value":561},"OutValue":0},{"Version":0,"Id":44,"Reason":"","Done":1,"OrderType":2,"UtxoId":104900281499648,"OrderTime":1750819739,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":0},"Address":"tb1pvcdrd5gumh8z2nkcuw9agmz7e6rm6mafz0h8f72dwp6erjqhevuqf2uhtv","FromL1":false,"InUtxo":"22c6ac0901f31a467c0de4ea0c9072dd3a802d89e23f60126cdeda24bb3284b3:0","InValue":20,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"9d73051edf9b7c812818b0e07ff470a9b7acfec5ac9d477ef290b2bc7d4ebb1b","OutAmt":{"Precision":0,"Value":560},"OutValue":0},{"Version":0,"Id":45,"Reason":"","Done":1,"OrderType":2,"UtxoId":105072080191488,"OrderTime":1750826912,"AssetName":"runes:f:TEST•FIRST•TEST","UnitPrice":{"Precision":10,"Value":0},"ExpectedAmt":{"Precision":0,"Value":11063},"Address":"tb1pc5e8nm4996pg22mn0ffqngpprzysd05pk67p3ey4k06s9h2y069q6ysqhd","FromL1":false,"InUtxo":"ac049a393c6e0fb89ea9a8f49bfcd336ecef2a0606b637fe21af6cbe26816a71:0","InValue":211,"InAmt":{"Precision":0,"Value":0},"RemainingAmt":{"Precision":0,"Value":0},"RemainingValue":0,"ToL1":false,"OutTxId":"79db5bb12ef40d1c96f8f3881ba335b6996e0c7f7b3ea59f2853a1659d0b9c2e","OutAmt":{"Precision":0,"Value":11175},"OutValue":0}]`
	var inputs []*SwapHistoryItem
	err = json.Unmarshal([]byte(itemHistory), &inputs)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(inputs, func(i, j int) bool {
		return inputs[i].Id < inputs[j].Id
	})

	tickInfo := _client.getTickerInfo(assetName)
	if tickInfo == nil {
		t.Fatal("can't find ticker")
	}

	ammContract := NewAmmContract()
	ammContract.AssetName = *assetName
	ammContract.AssetAmt = "3000000"
	ammContract.SatValue = 69990
	ammContract.K = "209970000000"

	deployFee, err := _client.QueryFeeForDeployContract(ammContract.TemplateName, (ammContract.Content()), 1)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("deploy contract %s need %d sats\n", ammContract.TemplateName, deployFee)
	fmt.Printf("use RemoteDeployContract to deploy a contract on core channel in server node\n")

	invokeParam, err := _client.QueryParamForInvokeContract(ammContract.TemplateName, INVOKE_API_SWAP)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("use %s as template to invoke contract %s\n", invokeParam, ammContract.TemplateName)

	assetAmt := _server.GetAssetBalance_SatsNet("", &ASSET_PLAIN_SAT)
	fmt.Printf("plain sats: %d\n", assetAmt)
	txId, id, url, err := _client.DeployContract_Remote(ammContract.TemplateName,
		string(ammContract.Content()), 0, false)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("RemoteDeployContract succeed, %s, %d, %s\n", txId, id, url)

	channelId := ExtractChannelId(url)
	txId, err = _client.SendAssetsV3_SatsNet(channelId, ammContract.GetAssetName().String(),
		ammContract.AssetAmt, ammContract.SatValue, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", txId)
	txId, err = _client.SendAssetsV3_SatsNet(channelId, ammContract.GetAssetName().String(),
		ammContract.AssetAmt, ammContract.SatValue, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", txId)
	tx, err := _client.SendAssets(channelId, ammContract.GetAssetName().String(),
		ammContract.AssetAmt, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", tx.TxID())
	tx, err = _client.SendAssets(channelId,
		indexer.ASSET_PLAIN_SAT.String(),
		fmt.Sprintf("%d", ammContract.SatValue), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("txId %s\n", tx.TxID())

	run := true
	go sendInBackground(&run) // 有时需要更新区块引起合约调用
	for {
		status, err := _client.GetContractStatusInServer(url)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		fmt.Printf("%s\n", status)
		contractRuntime := NewContractRuntime(_client, ammContract.TemplateName)
		err = json.Unmarshal([]byte(status), contractRuntime)
		if err != nil {
			t.Fatal(err)
		}
		swapRuntime, ok := contractRuntime.(*AmmContractRuntime)
		if !ok {
			t.Fatal()
		}
		if swapRuntime.IsActive() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	run = false

	contractRuntime := _server.GetContract(url)
	if contractRuntime == nil {
		t.Fatal("")
	}
	amm, ok := contractRuntime.(*AmmContractRuntime)
	if !ok {
		t.Fatal("")
	}
	contractRuntime2 := _client.GetContract(url)
	if contractRuntime2 == nil {
		t.Fatal("")
	}
	amm2, ok := contractRuntime2.(*AmmContractRuntime)
	if !ok {
		t.Fatal("")
	}
	if !bytes.Equal(amm.StaticMerkleRoot, amm2.StaticMerkleRoot) {
		t.Fatal("static merkle root not inconsistent")
	}
	_not_invoke_block = true // 停止 InvokeWithBlock 和 InvokeWithBlock_SatsNet
	for _, item := range inputs {
		fmt.Printf("item: %v\n", item)
		// 去掉处理结果
		item.RemainingAmt = item.InAmt.Clone()
		item.RemainingValue = item.InValue
		item.OutAmt = nil
		item.OutValue = 0
		item.Done = DONE_NOTYET
		item.UtxoId = indexer.ToUtxoId(_server.status.SyncHeightL2, 1, 0)
		if item.Reason != INVOKE_REASON_INVALID {
			item.Reason = INVOKE_REASON_NORMAL
		}

		if item.Id == 30 {
			Log.Infof("")
		}

		h, _, _ := indexer.FromUtxoId(item.UtxoId)
		amm.CurrBlock = h
		amm2.CurrBlock = h

		////////
		// 以下代码模拟正常调用过程
		item2 := item.Clone()
		beforeAmt := amm.AssetAmtInPool.Clone()
		beforeValue := amm.SatsValueInPool

		beforeAmt2 := amm2.AssetAmtInPool.Clone()
		beforeValue2 := amm2.SatsValueInPool

		switch item.OrderType {
		case ORDERTYPE_BUY, ORDERTYPE_SELL:
			if item.OrderType == ORDERTYPE_BUY {
				item.RemainingValue = item.GetTradingValueForAmm()
			} else {
				item.RemainingValue = 0
				item.InValue = 0
			}
			amm.updateContractStatus(item)
			amm.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			if item2.OrderType == ORDERTYPE_BUY {
				item2.RemainingValue = item2.GetTradingValueForAmm()
			} else {
				item2.RemainingValue = 0
				item2.InValue = 0
			}
			amm2.updateContractStatus(item2)
			amm2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)

		case ORDERTYPE_WITHDRAW, ORDERTYPE_DEPOSIT:

			if item.AssetName != indexer.ASSET_PLAIN_SAT.String() {
				item.RemainingValue = 0
				item2.RemainingValue = 0
			}

			_not_send_tx = false
			amm.updateContractStatus(item)
			amm.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			amm2.updateContractStatus(item2)
			amm2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)

		case ORDERTYPE_REFUND:
			amm.updateContractStatus(item)
			amm.addItem(item)
			SaveContractInvokeHistoryItem(_server.db, url, item)

			amm2.updateContractStatus(item2)
			amm2.addItem(item2)
			SaveContractInvokeHistoryItem(_client.db, url, item2)

		default:
			fmt.Printf("invalid type %d\n", item.OrderType)
			t.Fatal()
		}

		amm.swap(beforeAmt, beforeValue)
		amm.invokeCompleted()
		amm2.swap(beforeAmt2, beforeValue2)
		amm2.invokeCompleted()

		if !bytes.Equal(amm.CurrAssetMerkleRoot, amm2.CurrAssetMerkleRoot) {
			t.Fatal("asset merkle root not inconsistent")
		}

		_not_send_tx = true // 停止广播
		amm.sendInvokeResultTx()
		amm.sendInvokeResultTx_SatsNet()
		_not_send_tx = false // 需要生成一个新的tx，不然可能影响测试结果
		txId, err := _server.CoBatchSend_SatsNet(nil, []string{channelId}, ASSET_PLAIN_SAT.String(),
			[]*Decimal{indexer.NewDecimal(10, 0)}, "testing", amm.URL(), 0, nil, amm.StaticMerkleRoot, amm.CurrAssetMerkleRoot)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("dummy tx %s", txId)

		////////

		// 检查每次处理的结果
		err = amm.checkSelf()
		if err != nil {
			t.Fatalf("amm1: %d %v", item.Id, err)
		}

		err = amm2.checkSelf()
		if err != nil {
			t.Fatalf("amm2: %d %v", item.Id, err)
		}

		// 等待处理
		//time.Sleep(time.Second)
	}

	//
	fmt.Printf("realRunningData: %v\n", realRunningData)
	fmt.Printf("simuRunningData: %v\n", amm.SwapContractRunningData)

}
