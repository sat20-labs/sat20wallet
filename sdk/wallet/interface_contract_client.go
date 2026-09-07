package wallet

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
)

///////////////////////////////
// 客户端使用的接口
// TODO 需要跟wallet中的代码同步，后续改为同一套代码

// 返回服务端支持的合约（包括已经部署和未部署）
// templateName->contract content (json)
func (p *Manager) GetSupportContractInServer() ([]string, error) {
	return p.serverNode.client.GetSupportedContractsReq()
}

// GetChannelOpenFeeInServer returns the service node's current channel opening
// configuration without creating a reservation or changing wallet state.
func (p *Manager) GetChannelOpenFeeInServer() (*wwire.ChannelOpenFeeInfo, error) {
	if p.serverNode == nil || p.serverNode.client == nil {
		return nil, fmt.Errorf("server node is not configured")
	}
	client, ok := p.serverNode.client.(ChannelOpenFeeRPCClient)
	if !ok {
		return nil, fmt.Errorf("server node does not support channel fee preview")
	}
	return client.GetChannelOpenFeeReq()
}

// 返回服务端已经部署的合约
// contractURL list
func (p *Manager) GetDeployedContractInServer() ([]string, error) {
	return p.serverNode.client.GetDeployedContractsReq()
}

// 返回服务端合约的运行状态
// contractURL->contract status (json)
func (p *Manager) GetContractStatusInServer(url string) (string, error) {
	return p.serverNode.client.GetContractStatusReq(url)
}

func (p *Manager) GetContractInvokeHistoryInServer(url string, start, limit int) (string, error) {
	return p.serverNode.client.GetContractInvokeHistoryReq(url, start, limit)
}

func (p *Manager) GetInvokeHistoryByAddressInContract(url, address string, start, limit int) (string, error) {
	return p.serverNode.client.GetContractInvokeHistoryByAddressReq(url, address, start, limit)
}

func (p *Manager) GetInvokeItemByInUtxoInContract(url, inUtxo string) (string, error) {
	return p.serverNode.client.GetContractInvokeItemByInUtxoReq(url, inUtxo)
}

func (p *Manager) GetAllAddressesInContract(url string, start, limit int) (string, error) {
	return p.serverNode.client.GetContractAllAddressesReq(url, start, limit)
}

func (p *Manager) GetContractAnalyticsInServer(url string) (string, error) {
	return p.serverNode.client.GetContractAnalyticsReq(url)
}

func (p *Manager) GetUserStatusInContract(url, address string) (string, error) {
	return p.serverNode.client.GetContractStatusByAddressReq(url, address)
}

func (p *Manager) GetTranscendContractWithAssetNameInServer(assetName string) (string, error) {
	urls, err := p.GetDeployedContractInServer()
	if err != nil {
		return "", err
	}
	var generic string
	for _, url := range urls {
		if !strings.HasSuffix(url, TEMPLATE_CONTRACT_TRANSCEND) {
			continue
		}
		if ExtractAssetName(url) == assetName {
			return url, nil
		}
		if ExtractAssetName(url) == "::" {
			generic = url
		}
	}
	if generic != "" && assetName == indexer.ASSET_PLAIN_SAT.String() {
		return generic, nil
	}
	return "", fmt.Errorf("can't find transcend contract")
}

func (p *Manager) HasContractInChannel(channelId string) bool {
	urls, err := p.GetDeployedContractInServer()
	if err != nil {
		Log.Warnf("GetDeployedContractInServer failed while checking channel contracts %s: %v", channelId, err)
		return false
	}
	for _, url := range urls {
		if ExtractChannelId(url) == channelId {
			return true
		}
	}
	return false
}

func (p *Manager) IsControlByContract(channelId string, assetName string) bool {
	urls, err := p.GetDeployedContractInServer()
	if err != nil {
		Log.Warnf("GetDeployedContractInServer failed while checking contract control %s/%s: %v", channelId, assetName, err)
		return false
	}
	for _, url := range urls {
		if ExtractChannelId(url) == channelId && strings.Contains(url, assetName) {
			return true
		}
	}
	return false
}

// 根据合约名称和配置参数，计算部署合约的费用（聪） feerate是主网费率
func (p *Manager) QueryFeeForDeployContract(templateName string, contractContent string,
	feeRate int64) (int64, error) {
	contract, err := ContractContentUnMarsh(templateName, contractContent)
	if err != nil {
		return 0, err
	}

	return contract.DeployFee(feeRate), nil
}

// 请求服务端部署一个合约（支付上面计算出来的费用），只支持在聪网调用
func (p *Manager) DeployContract_Remote(templateName, contractContent string,
	feeRate int64, sendTxInL1 bool) (string, int64, string, error) {
	if p.wallet == nil {
		return "", 0, "", fmt.Errorf("wallet is not created/unlocked")
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	contract, err := ContractContentUnMarsh(templateName, contractContent)
	if err != nil {
		return "", 0, "", err
	}
	buf, err := contract.Encode()
	if err != nil {
		return "", 0, "", err
	}

	param, err := txscript.NewScriptBuilder().
		AddData([]byte(templateName)).
		AddData([]byte(buf)).Script()
	if err != nil {
		return "", -1, "", err
	}

	doAction := func(resv *RemoteActionPerformReservation) (string, string, error) {
		signedScript, err := SignedPerformRemoteActionInvoice(resv.Action, resv.InvoiceSig)
		if err != nil {
			return "", "", fmt.Errorf("SignedPerformRemoteActionInvoice failed. %v", err)
		}

		nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_PERFORMACTION, signedScript)
		if err != nil {
			return "", "", fmt.Errorf("NullDataScript failed. %v", err)
		}

		var txHex, txId string
		if resv.SendTxInL1 {
			tx, err := p.SendAssets(resv.ServiceAddr, ASSET_PLAIN_SAT.String(),
				fmt.Sprintf("%d", resv.ServiceFee), resv.FeeRate, nullDataScript)
			if err != nil {
				Log.Errorf("SendAssets %s %d failed, %v", resv.ServiceAddr, resv.ServiceFee, err)
				return "", "", err
			}
			txId = tx.TxID()
			txHex, err = EncodeMsgTx(tx)
			if err != nil {
				return "", "", err
			}
		} else {
			tx, err := p.SendAssets_SatsNet(resv.ServiceAddr, ASSET_PLAIN_SAT.String(),
				fmt.Sprintf("%d", resv.ServiceFee), nullDataScript)
			if err != nil {
				Log.Errorf("SendAssets_SatsNet %s %d failed, %v", resv.ServiceAddr, resv.ServiceFee, err)
				return "", "", err
			}
			txId = tx.TxID()
			txHex, err = EncodeMsgTx_SatsNet(tx)
			if err != nil {
				return "", "", err
			}
		}

		return txHex, txId, nil
	}

	txId, resvId, result, err := p.PerformRemoteAction(doAction, REMOTE_ACTION_DEPLOY_CONTRACT, param, nil, feeRate, sendTxInL1, false)
	if err != nil {
		Log.Errorf("DeployContract_Remote %s failed: %v", templateName, err)
		return "", 0, "", err
	}
	Log.Infof("deploy contract %s with txId %s, reserveId %d", templateName, txId, resvId)

	return txId, resvId, string(result), nil
}

// 查询调用合约的参数模板 invokeParam
func (p *Manager) QueryParamForInvokeContract(templateName, action string) (string, error) {
	c := NewContract(templateName)
	if c == nil {
		return "", fmt.Errorf("contract not found")
	}
	return c.InvokeParam(action), nil
}

func (p *Manager) IsAmmContractExisting(coreChannelId, assetName string) bool {
	url := GenerateContractURl(coreChannelId, assetName, TEMPLATE_CONTRACT_AMM)
	r := p.getRemoteDeployedContract(url)
	return r != nil
}

// 合约状态经常变化，需要实时获取
func (p *Manager) getRemoteDeployedContract(url string) ContractRuntime {

	status, err := p.GetContractStatusInServer(url)
	if err != nil {
		Log.Errorf("GetContractStatusInServer failed %v", err)
		return nil
	}

	_, _, typeName, err := ParseContractURL(url)
	if err != nil {
		Log.Errorf("ParseContractURL failed %v", err)
		return nil
	}

	c := NewContractRuntime(p, typeName)
	if c == nil {
		Log.Errorf("NewContractRuntime failed %s", url)
		return nil
	}

	err = json.Unmarshal([]byte(status), &c)
	if err != nil {
		Log.Errorf("Unmarshal failed %v", err)
		return nil
	}

	return c
}

func (p *Manager) QueryFeeForInvokeContract(contractURL string, jsonInvokeParam string) (ContractRuntime, int64, error) {

	// client mode
	contract := p.getRemoteDeployedContract(contractURL)
	if contract == nil {
		return nil, 0, fmt.Errorf("contract not found")
	}

	// 检查调用参数是否有效。
	// TODO 以后直接到持有合约的节点上去检查
	fee, err := contract.CheckInvokeParam(jsonInvokeParam)
	if err != nil {
		return nil, 0, err
	}

	return contract, fee, nil
}

// 发送的TX包含调用该合约所需要的聪
func (p *Manager) InvokeContract_Satsnet(contractURL string, jsonInvokeParam string,
	feeRate int64) (txID string, err error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	logID := p.beginContractInvokeOperationLog(contractURL, jsonInvokeParam, "SatoshiNet", "", "")
	defer func() {
		if err != nil {
			p.failContractInvokeOperationLog(logID, err)
		} else if txID != "" {
			p.completeContractInvokeOperationLog(logID, txID)
		}
	}()

	channelAddr, _, _, err := ParseContractURL(contractURL)
	if err != nil {
		return "", err
	}

	runtime, fee, err := p.QueryFeeForInvokeContract(contractURL, jsonInvokeParam)
	if err != nil {
		return "", err
	}
	if !runtime.IsActive() {
		return "", fmt.Errorf("contract is not active")
	}

	wrapperParam, err := ConvertInvokeParam(jsonInvokeParam, false)
	if err != nil {
		return "", err
	}
	buf, err := wrapperParam.Encode()
	if err != nil {
		return "", err
	}

	invoke := sindexer.ContractInvokeData{
		ContractPath: contractURL,
		InvokeParam:  buf,
		PubKey:       p.wallet.GetPubKey().SerializeCompressed(),
	}

	invoice, err := UnsignedInvokeContractInvoice(&invoke)
	if err != nil {
		return "", err
	}
	sig, err := p.wallet.SignMessage(invoice)
	if err != nil {
		return "", err
	}
	signedInvoice, err := SignedInvokeContractInvoice(&invoke, sig)
	if err != nil {
		return "", err
	}
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKECONTRACT, signedInvoice)
	if err != nil {
		return "", err
	}

	tx, err := p.sendAssets_SatsNet(channelAddr, ASSET_PLAIN_SAT.String(), fmt.Sprintf("%d", fee), nullDataScript, false)
	if err != nil {
		Log.Errorf("sendAssets_SatsNet %s failed", channelAddr)
		return "", err
	}
	txID = tx.TxID()
	Log.Infof("invoke contract %s with txId %s", contractURL, txID)

	return txID, nil
}

// 调用合约的同时加入资产
func (p *Manager) InvokeContractV2_Satsnet(contractURL string, jsonInvokeParam string,
	assetName string, amt string, feeRate int64) (txID string, err error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	logID := p.beginContractInvokeOperationLog(contractURL, jsonInvokeParam, "SatoshiNet", assetName, amt)
	defer func() {
		if err != nil {
			p.failContractInvokeOperationLog(logID, err)
		} else if txID != "" {
			p.completeContractInvokeOperationLog(logID, txID)
		}
	}()

	channelAddr, _, _, err := ParseContractURL(contractURL)
	if err != nil {
		return "", err
	}

	// 调用合约的费用
	runtime, fee, err := p.QueryFeeForInvokeContract(contractURL, jsonInvokeParam)
	if err != nil {
		return "", err
	}
	if !runtime.IsActive() {
		return "", fmt.Errorf("contract is not active")
	}

	wrapperParam, err := ConvertInvokeParam(jsonInvokeParam, false)
	if err != nil {
		return "", err
	}
	buf, err := wrapperParam.Encode()
	if err != nil {
		return "", err
	}

	invoke := sindexer.ContractInvokeData{
		ContractPath: contractURL,
		InvokeParam:  buf,
		PubKey:       p.wallet.GetPubKey().SerializeCompressed(),
	}

	invoice, err := UnsignedInvokeContractInvoice(&invoke)
	if err != nil {
		return "", err
	}
	sig, err := p.wallet.SignMessage(invoice)
	if err != nil {
		return "", err
	}
	signedInvoice, err := SignedInvokeContractInvoice(&invoke, sig)
	if err != nil {
		return "", err
	}
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKECONTRACT, signedInvoice)
	if err != nil {
		return "", err
	}

	if amt == "" || amt == "0" { // 不需要携带资产
		tx, sendErr := p.sendAssets_SatsNet(channelAddr, ASSET_PLAIN_SAT.String(), fmt.Sprintf("%d", fee), nullDataScript, false)
		if sendErr != nil {
			Log.Errorf("sendAssets_SatsNet %s failed", channelAddr)
			return "", sendErr
		}
		txID = tx.TxID()
	} else {
		txID, err = p.SendAssetsV3_SatsNet(channelAddr, assetName, amt, fee, nullDataScript)
		if err != nil {
			Log.Errorf("SendAssetsV3_SatsNet %s failed", channelAddr)
			return "", err
		}
	}

	Log.Infof("invoke contract %s with txId %s", contractURL, txID)

	return txID, nil
}

// 调用合约的同时加入资产
func (p *Manager) InvokeContractV2(contractURL string, jsonInvokeParam string,
	assetName string, amt string, feeRate int64) (txID string, err error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	logID := p.beginContractInvokeOperationLog(contractURL, jsonInvokeParam, "Bitcoin", assetName, amt)
	defer func() {
		if err != nil {
			p.failContractInvokeOperationLog(logID, err)
		} else if txID != "" {
			p.completeContractInvokeOperationLog(logID, txID)
		}
	}()

	channelAddr, _, _, err := ParseContractURL(contractURL)
	if err != nil {
		return "", err
	}

	// 调用合约的费用
	runtime, fee, err := p.QueryFeeForInvokeContract(contractURL, jsonInvokeParam)
	if err != nil {
		return "", err
	}
	if !runtime.IsActive() {
		return "", fmt.Errorf("contract is not active")
	}

	var nullDataScript []byte
	asset := indexer.NewAssetNameFromString(assetName)
	if asset.Protocol != indexer.PROTOCOL_NAME_RUNES { // TODO 等主网支持多个op_return后打开
		wrapperParam, err := ConvertInvokeParam(jsonInvokeParam, true)
		if err != nil {
			return "", err
		}
		buf, err := wrapperParam.EncodeV2()
		if err != nil {
			return "", err
		}

		_, asssetName, tc, err := ParseContractURL(contractURL)
		if err != nil {
			return "", err
		}
		relativePath := GenerateContractRelativePath(asssetName, tc)

		invoke := sindexer.ContractInvokeData{
			ContractPath: relativePath, // 资产名字+tc
			InvokeParam:  buf,          // 这里的资产名字必须省略，减少字节数
			//PubKey:       p.wallet.GetPubKey().SerializeCompressed(),
		}

		invoice, err := AbbrInvokeContractInvoice(&invoke)
		if err != nil {
			return "", err
		}
		nullDataScript, err = sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKECONTRACT, invoice)
		if err != nil {
			return "", err
		}
	}

	name := indexer.NewAssetNameFromString(assetName)
	tickerInfo := p.getTickerInfo(name)
	if tickerInfo == nil {
		return "", fmt.Errorf("can't get ticker %s info", name)
	}

	dAmt, err := indexer.NewDecimalFromString(amt, tickerInfo.Divisibility)
	if err != nil {
		return "", err
	}

	value := fee
	if indexer.IsPlainAsset(name) {
		value += dAmt.Int64()
		dAmt = nil
	}

	dest := &SendAssetInfo{
		Address:   channelAddr,
		Value:     value,
		AssetName: name,
		AssetAmt:  dAmt,
	}

	// TODO 等主网支持多个op_return，就必须加上参数
	// 这是默认行为，在主网只要有交易往这里面转资产，就自动触发穿越行为
	// 原因：一方面op_return能写入的数据太少，另一方面runes还会占有，而主网只能有一个op_return
	txID, fee, err = p.BatchSendAssetsV3([]*SendAssetInfo{dest}, assetName, feeRate, nullDataScript, "", false)
	if err != nil {
		Log.Errorf("BatchSendAssetsV3 %s failed", channelAddr)
		return "", err
	}
	Log.Infof("invoke contract %s with txId %s %d", contractURL, txID, fee)

	return txID, nil
}

// 一个特殊的invoke
func (p *Manager) SendContractEnabledTx(url string, h1, h2 int) (txID string, err error) {

	var wrapperParam InvokeParam
	wrapperParam.Action = INVOKE_API_ENABLE

	var swapParam EnableInvokeParam
	swapParam.HeightL1 = h1
	swapParam.HeightL2 = h2
	innerParam, err := swapParam.Encode()
	if err != nil {
		return "", err
	}
	wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
	jsonInvokeParam, marshalErr := json.Marshal(wrapperParam)
	if marshalErr != nil {
		return "", marshalErr
	}
	logID := p.beginContractInvokeOperationLog(url, string(jsonInvokeParam), "SatoshiNet", "", "")
	defer func() {
		if err != nil {
			p.failContractInvokeOperationLog(logID, err)
		} else if txID != "" {
			p.completeContractInvokeOperationLog(logID, txID)
		}
	}()

	buf, err := wrapperParam.Encode()
	if err != nil {
		return "", err
	}

	invoke := sindexer.ContractInvokeData{
		ContractPath: url,
		InvokeParam:  buf,
		PubKey:       p.wallet.GetPubKey().SerializeCompressed(),
	}

	invoice, err := UnsignedInvokeContractInvoice(&invoke)
	if err != nil {
		return "", err
	}
	sig, err := p.wallet.SignMessage(invoice)
	if err != nil {
		return "", err
	}
	signedInvoice, err := SignedInvokeContractInvoice(&invoke, sig)
	if err != nil {
		return "", err
	}
	nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_INVOKECONTRACT, signedInvoice)
	if err != nil {
		return "", err
	}

	txID, err = p.SendNullData_SatsNet(nullDataScript)
	if err != nil {
		Log.Errorf("SendNullData_SatsNet %s failed", url)
		return "", err
	}
	Log.Infof("enable contract %s with txId %s", url, txID)
	return txID, nil
}

// 存款（充值）：在主网将资产转入流动性池子，流动性池子在聪网将对应资产转入destAddr
// 返回txid, msgId
func (p *Manager) DepositWithContract(destAddr string, assetName string, amt string,
	feeRate int64) (string, error) {

	Log.Infof("DepositWithContract %s %s", assetName, amt)
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	// if !p.IsReady() {
	// 	return "", fmt.Errorf("not ready")
	// }

	// 确保sln在线
	if !p.checkSuperNodeStatus() {
		return "", fmt.Errorf("peer is offline")
	}

	// 检查是否有对应的amm合约，如果存在，通过该合约处理资产进出
	coreChannelId := p.GetCoreChannelAddr()
	url := GenerateContractURl(coreChannelId, assetName, TEMPLATE_CONTRACT_AMM)
	r := p.getRemoteDeployedContract(url)
	if r == nil {
		url = GenerateContractURl(coreChannelId, assetName, TEMPLATE_CONTRACT_TRANSCEND)
		r = p.getRemoteDeployedContract(url)
		if r == nil {
			return "", fmt.Errorf("can't find a correct contract")
		}
	}
	if !r.IsActive() {
		return "", fmt.Errorf("contract not active")
	}

	depositPara := DepositInvokeParam{
		OrderType: ORDERTYPE_DEPOSIT,
		AssetName: assetName,
		Amt:       amt,
	}
	depositParaBytes, err := json.Marshal(depositPara)
	if err != nil {
		return "", err
	}
	invokeParam := InvokeParam{
		Action: INVOKE_API_DEPOSIT,
		Param:  string(depositParaBytes),
	}
	invokeJson, err := json.Marshal(invokeParam)
	if err != nil {
		return "", err
	}

	txId, err := p.InvokeContractV2(url, string(invokeJson), assetName, amt, feeRate)
	if err != nil {
		Log.Errorf("InvokeContractV2 %s failed, %v", url, err)
		return "", err
	}
	Log.Infof("DepositWithContract succeed. %s", txId)

	// 通知服务端（执行ascend操作）将txId中输出到通道地址的utxo锁定，否则有可能被withdraw或者其他操作用掉 （临时方案）
	// 如果合约不是该节点的服务端运行，这个就无效。需要方案2: 在withdraw时，不使用当前区块的utxo
	RESV_TYPE_DEPOSIT := "deposit"
	p.serverNode.client.SendActionResultNfty(p.GetWallet(), 0, RESV_TYPE_DEPOSIT, 0, txId)

	return txId, nil
}

// TODO 提取时，需要增加收费，除了固定的  DEFAULT_SERVICE_FEE_WITHDRAW 之外，
// 还需要支付提取资产的 DEFAULT_FEE_RATIO_WITHDRAW_WITH_CONTRACT
// 取款（提现）：在聪网将资产转入流动性池子，流动性池子在主网将对应的资产转给destAddr
// 返回txid
func (p *Manager) WithdrawWithContract(destAddr string, assetName string, amt string,
	feeRate int64) (string, error) {
	return p.withdrawWithContract(destAddr, assetName, amt, feeRate, "")
}

// withdrawWithContract performs a withdrawal through the selected contract.
// An empty contractURL preserves the public API's AMM-first selection; callers
// that already selected a contract (for example channel expansion) must pass
// it explicitly so a same-asset AMM cannot be selected accidentally.
func (p *Manager) withdrawWithContract(destAddr string, assetName string, amt string,
	feeRate int64, contractURL string) (string, error) {
	Log.Infof("WithdrawWithContract %s %s", assetName, amt)
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	// if !p.IsReady() {
	// 	return "", fmt.Errorf("not ready")
	// }

	// 确保sln在线
	if !p.checkSuperNodeStatus() {
		return "", fmt.Errorf("peer is offline")
	}

	if destAddr != "" && !IsBtcAddress(destAddr) {
		return "", fmt.Errorf("invalid dest address")
	}

	// 检查是否有对应的amm合约，如果存在，通过该合约处理资产进出
	coreChannelId := p.GetCoreChannelAddr()

	// 检查其主网地址是否有足够的资产
	name := indexer.NewAssetNameFromString(assetName)
	tickInfo := p.GetTickerInfo(name)
	if tickInfo == nil {
		return "", fmt.Errorf("can't find ticker info %s", assetName)
	}
	dAmt, err := indexer.NewDecimalFromString(amt, tickInfo.Divisibility)
	if err != nil {
		return "", err
	}
	total := p.GetAssetBalance(coreChannelId, indexer.NewAssetNameFromString(assetName))
	if total.Cmp(dAmt) < 0 {
		return "", fmt.Errorf("no enough asset %s in channel %s, require %s but only %s",
			assetName, coreChannelId, amt, total.String())
	}

	url := contractURL
	var r ContractRuntime
	if url != "" {
		r = p.getRemoteDeployedContract(url)
	} else {
		url = GenerateContractURl(coreChannelId, assetName, TEMPLATE_CONTRACT_AMM)
		r = p.getRemoteDeployedContract(url)
		if r == nil {
			url = GenerateContractURl(coreChannelId, assetName, TEMPLATE_CONTRACT_TRANSCEND)
			r = p.getRemoteDeployedContract(url)
		}
	}
	if r == nil {
		return "", fmt.Errorf("can't find a correct contract")
	}
	if !r.IsActive() {
		return "", fmt.Errorf("contract not active")
	}
	withdrawPara := WithdrawInvokeParam{
		OrderType: ORDERTYPE_WITHDRAW,
		AssetName: assetName,
		Amt:       amt,
		FeeRate:   feeRate,
		DestAddr:  destAddr,
	}
	withdrawParaBytes, err := json.Marshal(withdrawPara)
	if err != nil {
		return "", err
	}
	invokeParam := InvokeParam{
		Action: INVOKE_API_WITHDRAW,
		Param:  string(withdrawParaBytes),
	}
	invokeJson, err := json.Marshal(invokeParam)
	if err != nil {
		return "", err
	}

	// 修正白聪数量
	if assetName == indexer.ASSET_PLAIN_SAT.String() {
		total := p.GetAssetBalance_SatsNet("", indexer.NewAssetNameFromString(assetName))
		if total.Sign() == 0 {
			return "", fmt.Errorf("no any asset can be withdrawn")
		}
		if total.String() == amt {
			// 需要扣除调用费用和网络费用
			fee, err := r.CheckInvokeParam(string(invokeJson))
			if err != nil {
				return "", err
			}
			total = total.Sub(indexer.NewDefaultDecimal(fee + DEFAULT_FEE_SATSNET))

			amt = total.String()
			withdrawPara.Amt = amt
			withdrawParaBytes, err := json.Marshal(withdrawPara)
			if err != nil {
				return "", err
			}
			invokeParam.Param = string(withdrawParaBytes)
			invokeJson, err = json.Marshal(invokeParam)
			if err != nil {
				return "", err
			}
		}
	}

	txId, err := p.InvokeContractV2_Satsnet(url, string(invokeJson), assetName, amt, 0)
	if err != nil {
		Log.Errorf("InvokeContractV2_Satsnet %s failed, %v", url, err)
		return "", err
	}
	Log.Infof("WithdrawWithContract succeed. %s", txId)
	return txId, nil
}
