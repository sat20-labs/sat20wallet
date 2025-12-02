package wallet

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

///////////////////////////////
// 客户端使用的接口
// TODO 需要跟wallet中的代码同步，后续改为同一套代码

// 返回服务端支持的合约（包括已经部署和未部署）
// templateName->contract content (json)
func (p *Manager) GetSupportContractInServer() ([]string, error) {
	return p.serverNode.client.GetSupportedContractsReq()
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

func (p *Manager) GetAllAddressesInContract(url string, start, limit int) (string, error) {
	return p.serverNode.client.GetContractAllAddressesReq(url, start, limit)
}

func (p *Manager) GetContractAnalyticsInServer(url string) (string, error) {
	return p.serverNode.client.GetContractAnalyticsReq(url)
}

func (p *Manager) GetUserStatusInContract(url, address string) (string, error) {
	return p.serverNode.client.GetContractStatusByAddressReq(url, address)
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
	return "", 0, "", fmt.Errorf("not implemented")
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

// 发送的TX包含调用该合约所需要的聪, invokeParam不支持复杂结构的参数
func (p *Manager) InvokeContract_Satsnet(contractURL string, jsonInvokeParam string,
	feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

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
	Log.Infof("invoke contract %s with txId %s", contractURL, tx.TxID())

	return tx.TxID(), nil
}

// 调用合约的同时加入资产，invokeParam支持复杂结构的参数
func (p *Manager) InvokeContractV2_Satsnet(contractURL string, jsonInvokeParam string,
	assetName string, amt string, feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

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

	var txId string
	if amt == "" || amt == "0" { // 不需要携带资产
		tx, err := p.sendAssets_SatsNet(channelAddr, ASSET_PLAIN_SAT.String(), fmt.Sprintf("%d", fee), nullDataScript, false)
		if err != nil {
			Log.Errorf("sendAssets_SatsNet %s failed", channelAddr)
			return "", err
		}
		txId = tx.TxID()
	} else {
		txId, err = p.SendAssetsV3_SatsNet(channelAddr, assetName, amt, fee, nullDataScript)
		if err != nil {
			Log.Errorf("SendAssetsV3_SatsNet %s failed", channelAddr)
			return "", err
		}
	}

	Log.Infof("invoke contract %s with txId %s", contractURL, txId)

	return txId, nil
}

// 调用合约的同时加入资产
func (p *Manager) InvokeContractV2(contractURL string, jsonInvokeParam string,
	assetName string, amt string, feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

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
			InvokeParam:  buf, // 这里的资产名字必须省略，减少字节数
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
	txId, fee, err := p.BatchSendAssetsV3([]*SendAssetInfo{dest}, assetName, feeRate, nullDataScript, "", false)
	if err != nil {
		Log.Errorf("BatchSendAssetsV3 %s failed", channelAddr)
		return "", err
	}
	Log.Infof("invoke contract %s with txId %s %d", contractURL, txId, fee)

	return txId, nil
}

// 一个特殊的invoke
func (p *Manager) SendContractEnabledTx(url string, h1, h2 int) (string, error) {

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

	txId, err := p.SendNullData_SatsNet(nullDataScript)
	if err != nil {
		Log.Errorf("SendNullData_SatsNet %s failed", url)
		return "", err
	}
	Log.Infof("enable contract %s with txId %s", url, txId)
	return txId, nil
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
	p.serverNode.client.SendActionResultNfty(0, RESV_TYPE_DEPOSIT, 0, txId)

	return txId, nil
}

// TODO 提取时，需要增加收费，除了固定的  DEFAULT_SERVICE_FEE_WITHDRAW 之外，
// 还需要支付提取资产的 DEFAULT_FEE_RATIO_WITHDRAW_WITH_CONTRACT
// 取款（提现）：在聪网将资产转入流动性池子，流动性池子在主网将对应资产转入destAddr
// 返回txid
func (p *Manager) WithdrawWithContract(destAddr string, assetName string, amt string,
	feeRate int64) (string, error) {
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
	withdrawPara := WithdrawInvokeParam{
		OrderType: ORDERTYPE_WITHDRAW,
		AssetName: assetName,
		Amt:       amt,
		FeeRate:   feeRate,
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

	txId, err := p.InvokeContractV2_Satsnet(url, string(invokeJson), assetName, amt, feeRate)
	if err != nil {
		Log.Errorf("InvokeContractV2_Satsnet %s failed, %v", url, err)
		return "", err
	}
	Log.Infof("WithdrawWithContract succeed. %s", txId)
	return txId, nil
}
