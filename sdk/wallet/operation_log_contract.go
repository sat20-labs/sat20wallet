package wallet

import (
	"encoding/json"
	"strings"
)

func contractInvokeAction(jsonInvokeParam string) string {
	var param InvokeParam
	if err := json.Unmarshal([]byte(jsonInvokeParam), &param); err != nil {
		return "invoke"
	}
	action := strings.TrimSpace(param.Action)
	if action == "" {
		return "invoke"
	}
	return action
}

func contractInvokeTitle(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case INVOKE_API_DEPOSIT:
		return "Deposit through contract"
	case INVOKE_API_WITHDRAW:
		return "Withdraw through contract"
	case INVOKE_API_ENABLE:
		return "Enable contract"
	default:
		return "Invoke contract"
	}
}

func (p *Manager) beginContractInvokeOperationLog(contractURL, jsonInvokeParam, network, assetName, amount string) string {
	action := contractInvokeAction(jsonInvokeParam)
	parameters := map[string]string{
		"contract": contractURL,
		"action":   action,
		"network":  network,
	}
	if strings.TrimSpace(assetName) != "" {
		parameters["asset"] = assetName
	}
	if strings.TrimSpace(amount) != "" && strings.TrimSpace(amount) != "0" {
		parameters["amount"] = amount
	}
	return p.beginOperationLogBestEffort(OperationLogCreate{
		Category:   "contract",
		Action:     "invoke_contract",
		Title:      contractInvokeTitle(action),
		Summary:    "Preparing contract invocation",
		Parameters: parameters,
	})
}

func (p *Manager) completeContractInvokeOperationLog(logID, txID string) {
	p.updateOperationLogBestEffort(logID, OperationLogUpdate{
		Status:  OperationLogSucceeded,
		Message: "Contract invocation submitted",
		TxID:    txID,
		Details: map[string]string{"txid": txID},
		Result:  map[string]string{"txid": txID},
	})
}

func (p *Manager) failContractInvokeOperationLog(logID string, err error) {
	if err == nil {
		return
	}
	p.updateOperationLogBestEffort(logID, OperationLogUpdate{
		Status:  OperationLogFailed,
		Message: err.Error(),
		Details: map[string]string{"error": err.Error()},
	})
}
