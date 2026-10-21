package wallet

import (
	"encoding/base64"
	"testing"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
)

func TestUnifiedAutopayFundingActionRoundTrip(t *testing.T) {
	t.Run("autopay_encodes_the_existing_config_codec", func(t *testing.T) {
		request := &ContractInvokeRequest{
			SubType:   contractcommon.TemplateAutopay,
			Action:    "funding",
			Param:     `{"gasFundingAmount":"50.25"}`,
			GasLimit:  1234,
			CallNonce: 9,
		}
		converted, err := convertUnifiedInvokeRequestParam(ContractTypeTemplate, request)
		if err != nil {
			t.Fatalf("convert AUTOPAY funding request: %v", err)
		}
		if converted.Action != "funding" {
			t.Fatalf("action changed: got %q", converted.Action)
		}

		configBytes, err := base64.StdEncoding.DecodeString(converted.Param)
		if err != nil {
			t.Fatalf("decode converted funding param: %v", err)
		}
		var config contractcommon.TemplateAutopayConfigInvokeParam
		if err := config.Decode(configBytes); err != nil {
			t.Fatalf("decode AUTOPAY config codec: %v", err)
		}
		if config.AmountPerBlock != "" || config.BlobKeyLimit != 0 || config.GasFundingAmount != "50.25" {
			t.Fatalf("unexpected funding config: %+v", config)
		}

		payload := contractcommon.InvokePayload{
			GasLimit:  request.GasLimit,
			CallNonce: request.CallNonce,
			Action:    converted.Action,
			Param:     configBytes,
		}
		decoded, err := contractcommon.DecodeInvokePayload(contractcommon.EncodeInvokePayload(payload))
		if err != nil {
			t.Fatalf("round-trip invoke payload: %v", err)
		}
		if decoded.Action != "funding" || decoded.GasLimit != payload.GasLimit || decoded.CallNonce != payload.CallNonce {
			t.Fatalf("invoke payload header changed: got %+v want %+v", decoded, payload)
		}
		var decodedConfig contractcommon.TemplateAutopayConfigInvokeParam
		if err := decodedConfig.Decode(decoded.Param); err != nil {
			t.Fatalf("decode round-tripped funding config: %v", err)
		}
		if decodedConfig != config {
			t.Fatalf("round-tripped config changed: got %+v want %+v", decodedConfig, config)
		}
	})

	t.Run("other_templates_do_not_accept_autopay_funding", func(t *testing.T) {
		_, err := convertUnifiedInvokeRequestParam(ContractTypeTemplate, &ContractInvokeRequest{
			SubType: contractcommon.TemplateExchange,
			Action:  "funding",
			Param:   `{"gasFundingAmount":"50.25"}`,
		})
		if err == nil {
			t.Fatal("AUTOPAY funding action was accepted for an Exchange template")
		}
	})
}
