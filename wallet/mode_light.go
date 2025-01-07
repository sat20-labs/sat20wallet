//go:build wasm


package wallet

import (
	"net/http"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/common"
	"github.com/sat20-labs/sat20wallet/wallet/lightnode"
)


func NewKVDB(_ string) common.KVDB {
	return lightnode.NewKVDB()
}


func NewHTTPClient() common.HttpClient {
	var httpClient *http.Client
	
	// 在WASM环境中，使用默认的Transport
	httpClient = &http.Client{
	}
	
	return &netClient{client: httpClient}
}


// 注册回调函数
func (p *Manager) RegisterCallback(callback js.Value) {
    p.msgCallback = callback
}

// 发送消息
func (p *Manager) SendMessageToUpper(eventName string, data interface{}) {
	Log.Infof("message notified: %s", eventName)
    if p.msgCallback != nil{
        p.msgCallback.(js.Value).Invoke(eventName, js.ValueOf(data))
    }
}
