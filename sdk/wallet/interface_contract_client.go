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


// 查询调用合约的参数模板 invokeParam
func (p *Manager) QueryParamForInvokeContract(templateName, action string) (string, error) {
	c := NewContract(templateName)
	if c == nil {
		return "", fmt.Errorf("contract not found")
	}
	return c.InvokeParam(action), nil
}

func (p *Manager) QueryFeeForInvokeContract(contractURL string, invokeParam string) (int64, error) {
	
	// client mode
	contract := p.getRemoteDeployedContract(contractURL)
	if contract == nil {
		return 0, fmt.Errorf("contract not found")
	}
	
	// 检查调用参数是否有效。
	// TODO 以后直接到持有合约的节点上去检查
	fee, err := contract.CheckInvokeParam(invokeParam)
	if err != nil {
		return 0, err
	}

	return fee, nil
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

// 发送的TX包含调用该合约所需要的聪
func (p *Manager) InvokeContract_Satsnet(contractURL string, invokeParam string,
	feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	channelAddr, _, _, err := ParseContractURL(contractURL)
	if err != nil {
		return "", err
	}

	fee, err := p.QueryFeeForInvokeContract(contractURL, invokeParam)
	if err != nil {
		return "", err
	}

	// 将json结构转为script结构
	var param InvokeParam
	err = json.Unmarshal([]byte(invokeParam), &param)
	if err != nil {
		return "", err
	}
	buf, err := param.Encode()
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

	// 需要查询合约是否已经关闭?
	
	txId, err := p.SendAssets_SatsNet(channelAddr, ASSET_PLAIN_SAT.String(), fmt.Sprintf("%d", fee), nullDataScript)
	if err != nil {
		Log.Errorf("SendAssets_SatsNet %s failed", channelAddr)
		return "", err
	}
	Log.Infof("invoke contract %s with txId %s", contractURL, txId)

	return txId, nil
}

// 调用合约的同时加入资产
func (p *Manager) InvokeContractV2_Satsnet(contractURL string, invokeParam string,
	assetName string, amt string, feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	channelAddr, _, _, err := ParseContractURL(contractURL)
	if err != nil {
		return "", err
	}

	// 调用合约的费用
	fee, err := p.QueryFeeForInvokeContract(contractURL, invokeParam)
	if err != nil {
		return "", err
	}

	// 将json结构转为script结构
	var wrapperParam InvokeParam
	err = json.Unmarshal([]byte(invokeParam), &wrapperParam)
	if err != nil {
		return "", err
	}

	switch wrapperParam.Action {
	case INVOKE_API_SWAP:
		var swapParam SwapInvokeParam
		err = json.Unmarshal([]byte(wrapperParam.Param), &swapParam)
		if err != nil {
			return "", err
		}
		innerParam, err := swapParam.Encode()
		if err != nil {
			return "", err
		}
		wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
		
	case INVOKE_API_WITHDRAW:
		var param WithdrawInvokeParam
		err = json.Unmarshal([]byte(wrapperParam.Param), &param)
		if err != nil {
			return "", err
		}
		innerParam, err := param.Encode()
		if err != nil {
			return "", err
		}
		wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)

	case INVOKE_API_MINT:


	default:
		return "", fmt.Errorf("unsupport action %s", wrapperParam.Action)
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

	txId, err := p.SendAssetsV3_SatsNet(channelAddr, assetName, amt, fee, nullDataScript)
	if err != nil {
		Log.Errorf("SendAssetsV3_SatsNet %s failed", channelAddr)
		return "", err
	}
	Log.Infof("invoke contract %s with txId %s", contractURL, txId)

	return txId, nil
}


// 调用合约的同时加入资产
func (p *Manager) InvokeContractV2(contractURL string, invokeParam string,
	assetName string, amt string, feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	channelAddr, _, _, err := ParseContractURL(contractURL)
	if err != nil {
		return "", err
	}

	// 调用合约的费用
	fee, err := p.QueryFeeForInvokeContract(contractURL, invokeParam)
	if err != nil {
		return "", err
	}

	// 主网不需要invoice，但可以加上（符文就不能用）
	var nullDataScript []byte
	asset := indexer.NewAssetNameFromString(assetName)
	if asset.Protocol != indexer.PROTOCOL_NAME_RUNES {

		// 将json结构转为script结构
		var wrapperParam InvokeParam
		err = json.Unmarshal([]byte(invokeParam), &wrapperParam)
		if err != nil {
			return "", err
		}

		switch wrapperParam.Action {
		case INVOKE_API_SWAP:
			// var swapParam SwapInvokeParam
			// err = json.Unmarshal([]byte(wrapperParam.Param), &swapParam)
			// if err != nil {
			// 	return "", err
			// }
			// innerParam, err := swapParam.Encode()
			// if err != nil {
			// 	return "", err
			// }
			//wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
			wrapperParam.Param = ""
		case INVOKE_API_DEPOSIT:
			// var param DepositInvokeParam
			// err = json.Unmarshal([]byte(wrapperParam.Param), &param)
			// if err != nil {
			// 	return "", err
			// }
			// innerParam, err := param.Encode()
			// if err != nil {
			// 	return "", err
			// }
			//wrapperParam.Param = base64.StdEncoding.EncodeToString(innerParam)
			wrapperParam.Param = ""
		default:
			return "", fmt.Errorf("unsupport action %s", wrapperParam.Action)
		}

		buf, err := wrapperParam.Encode()
		if err != nil {
			return "", err
		}

		_, asssetName, tc, err := ParseContractURL(contractURL)
		if err != nil {
			return "", err
		}
		relativePath := GenerateContractRelativePath(asssetName, tc)

		invoke := sindexer.ContractInvokeData{
			ContractPath: relativePath,
			InvokeParam:  buf,
			PubKey:       p.wallet.GetPubKey().SerializeCompressed(),
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

	dest := &SendAssetInfo{
		Address: channelAddr,
		Value: fee,
		AssetName: indexer.NewAssetNameFromString(assetName),
		AssetAmt: dAmt,
	}

	// 这是默认行为，在主网只要有交易往这里面转资产，就自动触发穿越行为
	// 原因：一方面op_return能写入的数据太少，另一方面runes还会占有，而主网只能有一个op_return
	txId, err := p.BatchSendAssetsV2([]*SendAssetInfo{dest}, assetName, 0, nullDataScript, false)
	if err != nil {
		Log.Errorf("BatchSendAssetsV2 %s failed", channelAddr)
		return "", err
	}
	Log.Infof("invoke contract %s with txId %s", contractURL, txId)

	return txId, nil
}
