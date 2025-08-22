//go:build !wasm

package wallet

import (
	"net"
	"net/http"
	"time"

	db "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/supernode"
)

func NewKVDB(dbPath string) db.KVDB {
	return supernode.NewKVDB(dbPath)
}


func NewHTTPClient() HttpClient {
	var httpClient *http.Client

	netTransport := &http.Transport{
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second, // keepalive超时时间
		}).Dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxConnsPerHost:       10,
		MaxIdleConnsPerHost:   10,
	}
	httpClient = &http.Client{
		Timeout:   60 * time.Second,
		Transport: netTransport,
	}

	return &NetClient{Client: httpClient}
}
