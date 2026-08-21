package wallet

import "strconv"

func remoteActionOperationLogCreate(action string, feeRate int64, sendTxInL1, toBootstrap bool) OperationLogCreate {
	category := "wallet"
	logAction := "remote_action"
	title := "Remote wallet action"
	summary := "Requesting wallet service action"

	switch action {
	case REMOTE_ACTION_DEPLOY_CONTRACT:
		category = "contract"
		logAction = "deploy_contract"
		title = "Deploy contract"
		summary = "Requesting contract deployment"
	case REMOTE_ACTION_DEPLOY_RUNES:
		category = "asset"
		logAction = "deploy_runes"
		title = "Deploy Runes asset"
		summary = "Requesting Runes deployment"
	case REMOTE_ACTION_ASCEND:
		category = "asset"
		logAction = "ascend_asset"
		title = "Ascend asset"
		summary = "Requesting asset ascend operation"
	}

	network := "SatoshiNet"
	if sendTxInL1 {
		network = "Bitcoin"
	}
	target := "service node"
	if toBootstrap {
		target = "bootstrap node"
	}
	return OperationLogCreate{
		Category: category,
		Action:   logAction,
		Title:    title,
		Summary:  summary,
		Parameters: map[string]string{
			"service_action": action,
			"fee_rate":       strconv.FormatInt(feeRate, 10),
			"network":        network,
			"target":         target,
		},
	}
}

func (p *Manager) beginRemoteActionOperationLog(action string, feeRate int64, sendTxInL1, toBootstrap bool) string {
	return p.beginOperationLogBestEffort(remoteActionOperationLogCreate(action, feeRate, sendTxInL1, toBootstrap))
}
