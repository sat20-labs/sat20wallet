package wallet

import "strconv"

func localActionOperationLogCreate(action string, actionParam any, feeRate int64) OperationLogCreate {
	input := OperationLogCreate{
		Category: "wallet",
		Action:   "local_action",
		Title:    "Wallet action",
		Summary:  "Starting wallet action",
		Parameters: map[string]string{
			"action":   action,
			"fee_rate": strconv.FormatInt(feeRate, 10),
		},
	}

	switch action {
	case LOCAL_ACTION_CONFIRM_TX:
		input.Category = "transaction"
		input.Action = "confirm_transaction"
		input.Title = "Confirm Bitcoin transaction"
		input.Summary = "Waiting for Bitcoin transaction confirmation"
		if txID, ok := actionParam.(string); ok {
			input.Parameters["txid"] = txID
		}
	case LOCAL_ACTION_CONFIRM_TX_L2:
		input.Category = "transaction"
		input.Action = "confirm_satoshinet_transaction"
		input.Title = "Confirm SatoshiNet transaction"
		input.Summary = "Waiting for SatoshiNet transaction confirmation"
		if txID, ok := actionParam.(string); ok {
			input.Parameters["txid"] = txID
		}
	case LOCAL_ACTION_UNSTAKE_MINER:
		input.Category = "staking"
		input.Action = "miner_unstake"
		input.Title = "Miner unstake"
		input.Summary = "Preparing miner unstake"
	case LOCAL_ACTION_LOCK_WITH_EXPAND:
		input.Category = "channel"
		input.Action = "lock_with_expand"
		input.Title = "Lock and expand channel"
		input.Summary = "Preparing channel asset expansion"
		if param, ok := actionParam.(*LocalActionParam_Expand); ok && param != nil {
			input.Parameters["channel_id"] = param.ChannelId
			input.Parameters["asset"] = param.AssetName.String()
			if param.Amt != nil {
				input.Parameters["amount"] = param.Amt.String()
			}
			input.Parameters["contract"] = param.ContractURL
		}
	}
	return input
}
