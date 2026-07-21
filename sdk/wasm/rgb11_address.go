package main

import (
	"context"
	"encoding/json"
	"strings"
	"syscall/js"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

type rgb11AddressReceiveWASMRequest struct {
	TTL          uint64 `json:"ttl,omitempty"`
	ExpiryHeight uint64 `json:"expiry_height,omitempty"`
	Autopay      bool   `json:"autopay,omitempty"`
	Flags        uint8  `json:"flags,omitempty"`
}

type rgb11AddressSendWASMRequest struct {
	ReceiverAddress  string `json:"receiver_address"`
	AssetName        string `json:"asset_name"`
	AmountRaw        string `json:"amount_raw"`
	FeeRate          int64  `json:"fee_rate"`
	MinConfirmations uint8  `json:"min_confirmations"`
	Expiry           int64  `json:"expiry,omitempty"`
}

type rgb11AddressDeliveryWASMRequest struct {
	TransferID   string `json:"transfer_id"`
	TTL          uint64 `json:"ttl,omitempty"`
	ExpiryHeight uint64 `json:"expiry_height,omitempty"`
	Autopay      bool   `json:"autopay,omitempty"`
	InlineLimit  int    `json:"inline_limit,omitempty"`
}

type rgb11AddressMailboxWASMRequest struct {
	Height       uint64 `json:"height,omitempty"`
	Now          uint64 `json:"now,omitempty"`
	TTL          uint64 `json:"ttl,omitempty"`
	ExpiryHeight uint64 `json:"expiry_height,omitempty"`
	Autopay      bool   `json:"autopay,omitempty"`
}

func rgb11AddressAutopay(enabled bool) *wallet.DKVSAutopayOptions {
	if !enabled {
		return nil
	}
	return &wallet.DKVSAutopayOptions{AddressParams: wallet.GetChainParam_SatsNet()}
}

func parseRGB11AssetName(value string) (indexer.AssetName, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 3)
	if len(parts) != 3 || !strings.EqualFold(parts[0], "rgb11") ||
		strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return indexer.AssetName{}, wallet.ErrRGB11Inconsistent
	}
	return indexer.AssetName{
		Protocol: strings.ToLower(parts[0]),
		Type:     parts[1],
		Ticker:   parts[2],
	}, nil
}

func enableRGB11AddressReceive(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request rgb11AddressReceiveWASMRequest
	if len(p) > 0 && p[0].Type() == js.TypeString && strings.TrimSpace(p[0].String()) != "" {
		if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
			return createJsRet(nil, -1, err.Error())
		}
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		endpoint, err := _mgr.EnableConfiguredRGB11AddressReceive(wallet.RGB11ReceiveCapabilityOptions{
			RecordOptions: dkvsindexer.RecordOptions{
				TTL: request.TTL, ExpiryHeight: request.ExpiryHeight,
			},
			Autopay: rgb11AddressAutopay(request.Autopay),
			Flags:   request.Flags,
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(endpoint)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"endpoint":  string(encoded),
			"temporary": endpoint.Temporary,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func resolveRGB11AddressEndpoint(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 || p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "missing RGB11 receiver address")
	}
	address := strings.TrimSpace(p[0].String())
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		endpoint, err := _mgr.ResolveConfiguredRGB11AddressEndpoint(address,
			dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())})
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(endpoint)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"endpoint": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func prepareRGB11AddressTransfer(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 || p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "missing RGB11 address transfer request")
	}
	var request rgb11AddressSendWASMRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	assetName, err := parseRGB11AssetName(request.AssetName)
	if err != nil {
		return createJsRet(nil, -1, "asset_name must be protocol:type:ticker for rgb11")
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		prepared, endpoint, err := _mgr.PrepareConfiguredRGB11AddressTransfer(
			context.Background(), wallet.RGB11AddressSendRequest{
				ReceiverAddress:  request.ReceiverAddress,
				AssetName:        assetName,
				AmountRaw:        request.AmountRaw,
				FeeRate:          request.FeeRate,
				MinConfirmations: request.MinConfirmations,
				Expiry:           request.Expiry,
			}, dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())},
		)
		if err != nil {
			return nil, -1, err.Error()
		}
		encodedTransfer, err := json.Marshal(prepared)
		if err != nil {
			return nil, -1, err.Error()
		}
		encodedEndpoint, err := json.Marshal(endpoint)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"transfer": string(encodedTransfer),
			"endpoint": string(encodedEndpoint),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func deliverAndBroadcastRGB11AddressTransfer(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 || p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "missing RGB11 delivery request")
	}
	var request rgb11AddressDeliveryWASMRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.DeliverAndBroadcastConfiguredRGB11AddressTransfer(
			request.TransferID, wallet.RGB11AddressDeliveryOptions{
				RecordOptions: dkvsindexer.RecordOptions{
					TTL: request.TTL, ExpiryHeight: request.ExpiryHeight,
				},
				Autopay:     rgb11AddressAutopay(request.Autopay),
				InlineLimit: request.InlineLimit,
			},
		)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"result":    string(encoded),
			"txid":      result.TxID,
			"temporary": result.Temporary,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func syncRGB11AddressMailbox(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request rgb11AddressMailboxWASMRequest
	if len(p) > 0 && p[0].Type() == js.TypeString && strings.TrimSpace(p[0].String()) != "" {
		if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
			return createJsRet(nil, -1, err.Error())
		}
	}
	if request.Now == 0 {
		request.Now = uint64(time.Now().UnixMilli())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.SyncConfiguredRGB11AddressMailbox(
			context.Background(), dkvsindexer.RecordVerificationOptions{
				Height: request.Height, Now: request.Now,
			}, wallet.RGB11AddressDeliveryOptions{
				RecordOptions: dkvsindexer.RecordOptions{
					TTL: request.TTL, ExpiryHeight: request.ExpiryHeight,
				},
				Autopay: rgb11AddressAutopay(request.Autopay),
			},
		)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"result": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getRGB11AddressCarrierWarning(this js.Value, p []js.Value) any {
	return createJsRet(map[string]any{"warning": wallet.RGB11AddressCarrierWarning}, 0, "ok")
}

var rgb11AddressRegisterCallback js.Func

func init() {
	rgb11AddressRegisterCallback = js.FuncOf(func(this js.Value, args []js.Value) any {
		obj := js.Global().Get(module)
		if obj.Type() != js.TypeObject {
			js.Global().Call("setTimeout", rgb11AddressRegisterCallback, 0)
			return nil
		}
		obj.Set("enableRGB11AddressReceive", js.FuncOf(enableRGB11AddressReceive))
		obj.Set("resolveRGB11AddressEndpoint", js.FuncOf(resolveRGB11AddressEndpoint))
		obj.Set("prepareRGB11AddressTransfer", js.FuncOf(prepareRGB11AddressTransfer))
		obj.Set("deliverAndBroadcastRGB11AddressTransfer", js.FuncOf(deliverAndBroadcastRGB11AddressTransfer))
		obj.Set("syncRGB11AddressMailbox", js.FuncOf(syncRGB11AddressMailbox))
		obj.Set("getRGB11AddressCarrierWarning", js.FuncOf(getRGB11AddressCarrierWarning))
		rgb11AddressRegisterCallback.Release()
		return nil
	})
	js.Global().Call("setTimeout", rgb11AddressRegisterCallback, 0)
}
