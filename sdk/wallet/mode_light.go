//go:build wasm

package wallet

import (
	"net/http"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/lightnode"
)

func NewKVDB(_ string) common.KVDB {
	return lightnode.NewKVDB()
}


func NewHTTPClient() HttpClient {
	var httpClient *http.Client

	// 在WASM环境中，使用默认的Transport
	httpClient = &http.Client{}

	return &NetClient{Client: httpClient}
}

