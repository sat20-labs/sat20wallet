package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Production selection, signing, broadcast decoding and receipt reconciliation
// run unchanged. Only the HTTP node is controlled, including delayed execution.
type autopayRenewalHTTP struct {
	*autopayReceiptTestHTTP
	output      *indexerwire.TxOutputInfo
	txid        string
	submissions [][]byte
}

func (h *autopayRenewalHTTP) SendGetRequest(url *URL) ([]byte, error) {
	if strings.Contains(url.Path, "/v3/address/asset/") {
		return json.Marshal(map[string]any{"code": 0, "data": []*indexerwire.TxOutputInfo{h.output}})
	}
	return h.autopayReceiptTestHTTP.SendGetRequest(url)
}
func (h *autopayRenewalHTTP) SendPostRequest(_ *URL, body []byte) ([]byte, error) {
	var request indexerwire.SendRawTxReq
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(request.SignedTxHex)
	if err != nil {
		return nil, err
	}
	tx := swire.NewMsgTx(2)
	if err := tx.Deserialize(bytes.NewReader(raw)); err != nil {
		return nil, err
	}
	h.txid = tx.TxID()
	h.submissions = append(h.submissions, append([]byte(nil), raw...))
	return json.Marshal(map[string]any{"code": 0, "data": h.txid})
}

func TestAccountAutopayRenewalWaitsForNewTransactionExecution(t *testing.T) {
	for _, outcome := range []string{"success", "revert"} {
		t.Run(outcome, func(t *testing.T) {
			manager, defaults, _, receipt := accountAutopayExpiryManagerAt(t, "active", "1000000000", "10", 100, 100, 100, nil)
			script, err := GetP2TRpkScript(manager.GetWallet().GetPaymentPubKey())
			require.NoError(t, err)
			assets := []*indexer.DisplayAsset{{AssetName: *indexer.NewAssetNameFromString(GetGasAssetName()), Amount: "1000000000"}}
			if defaults.AutopayFeeAssetName != GetGasAssetName() {
				assets = append(assets, &indexer.DisplayAsset{AssetName: *indexer.NewAssetNameFromString(defaults.AutopayFeeAssetName), Amount: "1000000000"})
			}
			http := &autopayRenewalHTTP{autopayReceiptTestHTTP: receipt, output: &indexerwire.TxOutputInfo{
				OutPoint: strings.Repeat("ab", 32) + ":0", Value: 1000000, PkScript: script, Assets: assets,
			}}
			manager.l2IndexerClient.SetMaster(NewIndexerClient("http", "renewal-test", "testnet", http))
			previousNoSend := NOT_SEND_TX
			NOT_SEND_TX = false
			t.Cleanup(func() { NOT_SEND_TX = previousNoSend })
			quote, err := manager.GetAccountAutopayFundingStatus()
			require.NoError(t, err)
			require.True(t, quote.Ready)
			result, err := manager.FundAccountAutopay(*quote)
			require.NoError(t, err)
			require.True(t, result.Pending, "old service readiness confirmed an unmined renewal")
			require.Equal(t, http.txid, result.TransactionID)
			require.Len(t, http.submissions, 1)
			receipt.height = 101
			status, err := manager.GetAccountAutopayFundingStatus()
			require.NoError(t, err)
			require.True(t, status.Ready)
			require.True(t, status.FundingPending, "inclusion without execution confirmed a renewal")
			require.Len(t, http.submissions, 1, "observing status broadcast a transaction")
			continued, err := manager.FundAccountAutopay(*status)
			require.NoError(t, err)
			require.True(t, continued.Pending)
			require.Equal(t, result.TransactionID, continued.TransactionID)
			require.Len(t, http.submissions, 2)
			require.Equal(t, http.submissions[0], http.submissions[1])
			receipt.history = []contractcommon.ContractHistoryRecord{{Contract: defaults.AutopayContract,
				Height: 102, TxID: strings.Repeat("cd", 32), Kind: "result", Status: outcome,
				Details: map[string]interface{}{"result_for_txid": http.txid}}}
			status, err = manager.GetAccountAutopayFundingStatus()
			require.NoError(t, err)
			require.False(t, status.FundingPending)
			logs, err := manager.operationLogManager().List()
			require.NoError(t, err)
			require.Len(t, logs, 1)
			if outcome == "success" {
				require.Equal(t, OperationLogSucceeded, logs[0].Status)
			} else {
				require.Equal(t, OperationLogFailed, logs[0].Status)
				require.Contains(t, status.FundingError, "revert")
			}
		})
	}
}
