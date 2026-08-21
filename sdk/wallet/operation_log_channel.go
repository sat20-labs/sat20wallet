package wallet

import "strconv"

func operationLogDecimal(value *Decimal) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func operationLogAssetName(value *AssetName) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func splicingInOperationLogCreate(resv *SplicingReservation) OperationLogCreate {
	parameters := map[string]string{}
	if resv != nil {
		parameters["channel_id"] = resv.ChannelId
		parameters["asset"] = operationLogAssetName(resv.AssetName)
		parameters["amount"] = operationLogDecimal(resv.Amt)
		parameters["fee_rate"] = strconv.FormatInt(resv.FeeRate, 10)
		if resv.InReq != nil {
			parameters["reason"] = resv.InReq.Reason
		}
	}

	input := OperationLogCreate{
		Category:   "channel",
		Action:     "splice_in",
		Title:      "Splice into channel",
		Summary:    "Preparing channel splice-in",
		Parameters: parameters,
	}
	if resv != nil && !resv.IsInitiator {
		input.Action = "respond_splice_in"
		input.Title = "Respond to channel splice-in"
		input.Summary = "Executing a peer-requested channel splice-in"
		input.Parameters["trigger"] = "peer_request"
	}
	return input
}

func splicingOutOperationLogCreate(resv *SplicingReservation) OperationLogCreate {
	parameters := map[string]string{}
	if resv != nil {
		parameters["channel_id"] = resv.ChannelId
		parameters["asset"] = operationLogAssetName(resv.AssetName)
		parameters["amount"] = operationLogDecimal(resv.Amt)
		parameters["destination"] = resv.DestAddr
		parameters["fee_rate"] = strconv.FormatInt(resv.FeeRate, 10)
		if resv.OutReq != nil {
			parameters["reason"] = resv.OutReq.Reason
		}
	}

	input := OperationLogCreate{
		Category:   "channel",
		Action:     "splice_out",
		Title:      "Splice out of channel",
		Summary:    "Preparing channel splice-out",
		Parameters: parameters,
	}
	if resv != nil && !resv.IsInitiator {
		input.Action = "respond_splice_out"
		input.Title = "Respond to channel splice-out"
		input.Summary = "Executing a peer-requested channel splice-out"
		input.Parameters["trigger"] = "peer_request"
	}
	return input
}

func remoteExpandOperationLogCreate(resv *SplicingReservation, utxo string) OperationLogCreate {
	parameters := map[string]string{
		"utxo":    utxo,
		"trigger": "peer_request",
	}
	if resv != nil {
		parameters["channel_id"] = resv.ChannelId
		parameters["asset"] = operationLogAssetName(resv.AssetName)
		parameters["amount"] = operationLogDecimal(resv.Amt)
		parameters["fee_rate"] = strconv.FormatInt(resv.FeeRate, 10)
		if resv.InReq != nil {
			parameters["reason"] = resv.InReq.Reason
		}
	}
	return OperationLogCreate{
		Category:   "channel",
		Action:     "respond_expand_channel",
		Title:      "Respond to channel expansion",
		Summary:    "Executing a peer-requested channel expansion",
		Parameters: parameters,
	}
}

func peerAcceptedSplicingMessage(resv *SplicingReservation, operation string) string {
	if resv != nil && !resv.IsInitiator {
		return "Peer-requested " + operation + " accepted for execution"
	}
	return "Peer accepted channel " + operation + " request"
}
