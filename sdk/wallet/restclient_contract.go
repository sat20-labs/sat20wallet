package wallet

import (
	"encoding/json"
	"fmt"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

type contractListResp struct {
	indexerwire.BaseResp
	Total        int             `json:"total"`
	Contracts    []string        `json:"contracts,omitempty"`
	ContractURLs []string        `json:"url,omitempty"`
	Data         json.RawMessage `json:"data,omitempty"`
}

type contractResp struct {
	indexerwire.BaseResp
	Status string          `json:"status,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type contractHistoryResp struct {
	indexerwire.BaseResp
	Total  int             `json:"total"`
	Status string          `json:"status,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

func (p *IndexerClient) GetContractsJSON(start, limit int) (string, error) {
	url := p.GetUrl("/v3/contracts")
	addPagingQuery(url, start, limit)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	var result contractListResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("%s", result.Msg)
	}
	return string(rsp), nil
}

func (p *IndexerClient) GetContractJSON(contract string) (string, error) {
	return p.getContractJSON("/v3/contracts/" + contract)
}

func (p *IndexerClient) GetContractStateJSON(contract string) (string, error) {
	return p.getContractJSON("/v3/contracts/" + contract + "/state")
}

func (p *IndexerClient) GetContractHistoryJSON(contract string, start, limit int) (string, error) {
	url := p.GetUrl("/v3/contracts/" + contract + "/history")
	addPagingQuery(url, start, limit)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	var result contractHistoryResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("%s", result.Msg)
	}
	return string(rsp), nil
}

func (p *IndexerClient) getContractJSON(path string) (string, error) {
	url := p.GetUrl(path)
	rsp, err := p.Http.SendGetRequest(url)
	if err != nil {
		Log.Errorf("SendGetRequest %v failed. %v", url, err)
		return "", err
	}

	var result contractResp
	if err := json.Unmarshal(rsp, &result); err != nil {
		Log.Errorf("Unmarshal failed. %v\n%s", err, string(rsp))
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("%s", result.Msg)
	}
	if len(result.Data) != 0 {
		return string(result.Data), nil
	}
	return result.Status, nil
}

func addPagingQuery(url *URL, start, limit int) {
	if start == 0 && limit == 0 {
		return
	}
	url.Query = make(map[string]string)
	url.Query["start"] = fmt.Sprintf("%d", start)
	url.Query["limit"] = fmt.Sprintf("%d", limit)
}

func (p *IndexerRPCClientMgr) GetContractsJSON(start, limit int) (string, error) {
	client, err := p.contractIndexer()
	if err != nil {
		return "", err
	}
	return client.GetContractsJSON(start, limit)
}

func (p *IndexerRPCClientMgr) GetContractJSON(contract string) (string, error) {
	client, err := p.contractIndexer()
	if err != nil {
		return "", err
	}
	return client.GetContractJSON(contract)
}

func (p *IndexerRPCClientMgr) GetContractStateJSON(contract string) (string, error) {
	client, err := p.contractIndexer()
	if err != nil {
		return "", err
	}
	return client.GetContractStateJSON(contract)
}

func (p *IndexerRPCClientMgr) GetContractHistoryJSON(contract string, start, limit int) (string, error) {
	client, err := p.contractIndexer()
	if err != nil {
		return "", err
	}
	return client.GetContractHistoryJSON(contract, start, limit)
}

func (p *IndexerRPCClientMgr) contractIndexer() (*IndexerClient, error) {
	client, ok := p.getActiveIndexer().(*IndexerClient)
	if !ok {
		return nil, fmt.Errorf("active indexer does not support contract queries")
	}
	return client, nil
}
