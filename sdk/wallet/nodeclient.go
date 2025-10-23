package wallet

import (
	"encoding/json"
	"fmt"

	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

const (
	QUERY_INFO_SUPPORT_CONTRACTS 		string = "/info/contracts/support"
	QUERY_INFO_DEPLOYED_CONTRACTS 		string = "/info/contracts/deployed"
	QUERY_INFO_CONTRACT 				string = "/info/contract"
	QUERY_INFO_CONTRACT_INVOKE_HISTORY 	string = "/info/contract/history"
	QUERY_INFO_CONTRACT_ALLUSER 		string = "/info/contract/alluser"
	QUERY_INFO_CONTRACT_ANALYTICS 		string = "/info/contract/analytics"
	QUERY_INFO_CONTRACT_USER 			string = "/info/contract/user"
	QUERY_INFO_CONTRACT_USERHISTORY 	string = "/info/contract/userhistory"
)

type NodeRPCClient interface {
	GetSupportedContractsReq() ([]string, error)
	GetDeployedContractsReq() ([]string, error)
	GetContractStatusReq(string) (string, error)
	GetContractAnalyticsReq(string) (string, error)
	GetContractInvokeHistoryReq(string, int, int) (string, error)
	GetContractInvokeHistoryByAddressReq(string, string, int, int)  (string, error)
	GetContractAllAddressesReq(string, int, int) (string, error)
	GetContractStatusByAddressReq(string, string) (string, error)

	SendSigReq(req *wwire.SignRequest,
		sig []byte) ([][][]byte, error)
	SendActionResultNfty(msgId int64, msg string, result int, reason string) error
	SendPingReq(*wwire.PingReq) (*wwire.PingResp, error)
	SendActionSyncReq(req *wwire.ActionSyncReq) (*wwire.ActionSyncResp, error)
}


type BaseResp struct {
	Code int    `json:"code" example:"0"`
	Msg  string `json:"msg" example:"ok"`
}

type ContractContentResp struct {
	BaseResp
	Contracts []string `json:"contracts"`
}

type DeployedContractResp struct {
	BaseResp
	ContractURLs []string `json:"url"`
}

type ContractStatusResp struct {
	BaseResp
	Status string `json:"status"`
}

type NodeClient struct {
	*RESTClient
}

func NewNodeClient(scheme, host, proxy string, http HttpClient) *NodeClient {
	client := NewRESTClient(scheme, host, proxy, http)
	return &NodeClient{client}
}

func (p *NodeClient) GetSupportedContractsReq() ([]string, error) {
	url := p.GetUrl(QUERY_INFO_SUPPORT_CONTRACTS)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	// Unmarshal the response.
	var result ContractContentResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.Contracts, nil
}

func (p *NodeClient) GetDeployedContractsReq() ([]string, error) {
	url := p.GetUrl(QUERY_INFO_DEPLOYED_CONTRACTS)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return nil, err
	}

	// Unmarshal the response.
	var result DeployedContractResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.ContractURLs, nil
}

func (p *NodeClient) GetContractStatusReq(contractUrl string) (string, error) {
	url := p.GetUrl(QUERY_INFO_CONTRACT + "/" + contractUrl)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Unmarshal the response.
	var result ContractStatusResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Status, nil
}

func (p *NodeClient) GetContractAnalyticsReq(contractUrl string) (string, error) {
	url := p.GetUrl(QUERY_INFO_CONTRACT_ANALYTICS + "/" + contractUrl)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Unmarshal the response.
	var result ContractStatusResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Status, nil
}


func (p *NodeClient) GetContractInvokeHistoryReq(contractUrl string, start, limit int) (string, error) {
	url := p.GetUrl(QUERY_INFO_CONTRACT_INVOKE_HISTORY + "/" + contractUrl)
	if start != 0 || limit != 0 {
		url.Query = make(map[string]string)  
		url.Query["start"] = fmt.Sprintf("%d", start)
		url.Query["limit"] = fmt.Sprintf("%d", limit)
	}
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Unmarshal the response.
	var result ContractStatusResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Status, nil
}


func (p *NodeClient) GetContractInvokeHistoryByAddressReq(contractUrl, address string, start, limit int) (string, error) {

	url := p.GetUrl(QUERY_INFO_CONTRACT_USERHISTORY + "/" + contractUrl + "/" + address)
	if start != 0 || limit != 0 {
		url.Query = make(map[string]string)  
		url.Query["start"] = fmt.Sprintf("%d", start)
		url.Query["limit"] = fmt.Sprintf("%d", limit)
	}
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Unmarshal the response.
	var result ContractStatusResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Status, nil
}


func (p *NodeClient) GetContractAllAddressesReq(contractUrl string, start, limit int) (string, error) {

	url := p.GetUrl(QUERY_INFO_CONTRACT_ALLUSER + "/" + contractUrl)
	if start != 0 || limit != 0 {
		url.Query = make(map[string]string)  
		url.Query["start"] = fmt.Sprintf("%d", start)
		url.Query["limit"] = fmt.Sprintf("%d", limit)
	}
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Unmarshal the response.
	var result ContractStatusResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Status, nil
}

func (p *NodeClient) GetContractStatusByAddressReq(contractUrl, address string) (string, error) {
	url := p.GetUrl(QUERY_INFO_CONTRACT_USER + "/" + contractUrl + "/" + address)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	// Unmarshal the response.
	var result ContractStatusResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}

	if result.Code != 0 {
		Log.Errorf("%v response message %s", url, result.Msg)
		return "", fmt.Errorf("%s", result.Msg)
	}

	return result.Status, nil
}

func (p *NodeClient) SendSigReq(req *wwire.SignRequest,
	sig []byte) ([][][]byte, error) {

	signedReq := wwire.SignReq{
		SignRequest: *req,
		Sig:         sig,
	}

	buff, err := json.Marshal(&signedReq)
	if err != nil {
		return nil, err
	}

	url := p.GetUrl(wwire.STP_ACTION_SIGN)
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return nil, err
	}

	var result wwire.SignResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	if result.Code != 0 {
		Log.Errorf("SendBootstrapSigReq failed, %s", result.Msg)
		return nil, fmt.Errorf("%s", result.Msg)
	}

	return result.TxSig, nil
}


func (p *NodeClient) SendActionResultNfty(msgId int64, action string, ret int, reason string) error {

	req := wwire.ActionResultNotify{
		Id:     msgId,
		Action: action,
		Result: ret,
		Reason: reason,
	}

	buff, err := json.Marshal(&req)
	if err != nil {
		return err
	}

	url := p.GetUrl(wwire.STP_ACTION_NFTY)
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return err
	}

	var result wwire.ActionResultResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return err
	}

	if result.Code != 0 {
		Log.Errorf("SendActionResultNfty failed, %s", result.Msg)
		return fmt.Errorf("%s", result.Msg)
	}

	return nil
}

func (p *NodeClient) SendPingReq(req *wwire.PingReq) (*wwire.PingResp, error) {
	buff, err := json.Marshal(&req)
	if err != nil {
		return nil, err
	}

	url := p.GetUrl(wwire.STP_PING)
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return nil, err
	}
	Log.Infof("Send PING to %s", url.String())

	var result wwire.PingResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	return &result, nil
}

func (p *NodeClient) SendActionSyncReq(req *wwire.ActionSyncReq) (*wwire.ActionSyncResp, error) {
	buff, err := json.Marshal(&req)
	if err != nil {
		return nil, err
	}

	url := p.GetUrl(wwire.STP_ACTION_SYNC)
	rsp, err := p.Http.SendPostRequest(url, buff)
	if err != nil {
		Log.Errorf("SendPostRequest %v failed. %v", url, err)
		return nil, err
	}

	var result wwire.ActionSyncResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return nil, err
	}

	return &result, nil
}
