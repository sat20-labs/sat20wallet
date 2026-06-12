package wallet

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"
)

func TestSTPActionGobNamesStayBackwardCompatible(t *testing.T) {
	tests := []struct {
		name    string
		param   any
		oldName []byte
	}{
		{
			name:    "expand",
			param:   &LocalActionParam_Expand{ContractURL: "channel/asset/contract"},
			oldName: []byte("*stp.LocalActionParam_Expand"),
		},
		{
			name:    "unstake miner",
			param:   &LocalActionParam_UnstakeMiner{Value: 1000},
			oldName: []byte("*stp.LocalActionParam_UnstakeMiner"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			value := struct {
				Param any
			}{Param: tt.param}
			if err := gob.NewEncoder(&buf).Encode(value); err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			if !bytes.Contains(buf.Bytes(), tt.oldName) {
				t.Fatalf("encoded gob does not contain old type name %q", tt.oldName)
			}

			var decoded struct {
				Param any
			}
			if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&decoded); err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if decoded.Param == nil {
				t.Fatalf("decoded param is nil")
			}
		})
	}
}

func TestSTPActionReservationGobShapeStaysBackwardCompatible(t *testing.T) {
	old := struct {
		ReservationBase
		Action      string
		ActionParam any
		FeeRate     int64
		ReqTime     int64
		ReqPubKey   []byte
		ReqSig      []byte
		TxId        string
		IsL1Tx      bool
		ActionResvs []*SubActionInfo
	}{
		ReservationBase: ReservationBase{
			Id:          time.Now().UnixMicro(),
			IsInitiator: true,
			Status:      RS_PERFORM_ACTION_TX_BROADCASTED,
		},
		Action:      LOCAL_ACTION_UNSTAKE_MINER,
		ActionParam: &LocalActionParam_UnstakeMiner{Value: 1200},
		FeeRate:     11,
		ReqTime:     time.Now().Unix(),
		ReqPubKey:   []byte{1, 2, 3},
		TxId:        "txid",
		IsL1Tx:      true,
		ActionResvs: []*SubActionInfo{{ActionType: "unstake", TxId: "txid"}},
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(old); err != nil {
		t.Fatalf("encode old local action failed: %v", err)
	}

	var decoded LocalActionPerformData
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&decoded); err != nil {
		t.Fatalf("decode sdk local action failed: %v", err)
	}
	if decoded.Id != old.Id || decoded.Status != old.Status || decoded.Action != old.Action || decoded.TxId != old.TxId {
		t.Fatalf("decoded local action mismatch: got %+v want %+v", decoded, old)
	}
	if _, ok := decoded.ActionParam.(*LocalActionParam_UnstakeMiner); !ok {
		t.Fatalf("decoded action param has type %T", decoded.ActionParam)
	}
}

func TestRemoteDeployRunesParamRoundTrip(t *testing.T) {
	in := &RemoteDeployRunesParam{
		AssetName: "runes:f:TEST",
		Symbol:    42,
		MaxSupply: 21000000,
		Limit:     1000,
		SelfMint:  false,
		DestAddr:  "tb1ptest",
	}
	script, err := EncodeRemoteDeployRunesParam(in)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	out, err := DecodeRemoteDeployRunesParam(script)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if *out != *in {
		t.Fatalf("round trip mismatch: got %+v want %+v", out, in)
	}
}

func TestNormalizeRemoteDeployRunesAssetName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "SATOSHINET•GAS", want: "runes:f:SATOSHINET•GAS"},
		{name: "runes:f:SATOSHINET•GAS", want: "runes:f:SATOSHINET•GAS"},
		{name: "brc20:f:SATOSHINET", want: "brc20:f:SATOSHINET"},
	}

	for _, tt := range tests {
		got := normalizeRemoteDeployRunesAssetName(tt.name)
		if got != tt.want {
			t.Fatalf("normalizeRemoteDeployRunesAssetName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
