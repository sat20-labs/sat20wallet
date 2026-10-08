package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	indexercommon "github.com/sat20-labs/indexer/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templatecontract "github.com/sat20-labs/satoshinet/contract/template"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"strings"
)

func accountAutopaySignedTransaction(record *OperationLogRecord, defaults dkvsindexer.NetworkDefaults, payer string) (*wire.MsgTx, error) {
	if record == nil || record.Action != "account_autopay_fund" || len(record.PreparedTransaction) == 0 ||
		record.Parameters["network"] != _chain || record.Parameters["payer"] != payer || payer == "" ||
		record.Parameters["contract"] != defaults.AutopayContract || record.Parameters["asset"] != defaults.AutopayFeeAssetName {
		return nil, fmt.Errorf("original AUTOPAY transaction or funding binding is unavailable")
	}
	reader := bytes.NewReader(record.PreparedTransaction)
	tx := wire.NewMsgTx(2)
	if err := tx.Deserialize(reader); err != nil {
		return nil, fmt.Errorf("decode original AUTOPAY transaction: %w", err)
	}
	if reader.Len() != 0 || tx.TxID() != record.TxID || len(tx.TxIn) == 0 {
		return nil, fmt.Errorf("original AUTOPAY transaction ID or encoding does not match")
	}
	for _, input := range tx.TxIn {
		if len(input.Witness) != 1 || (len(input.Witness[0]) != 64 && len(input.Witness[0]) != 65) {
			return nil, fmt.Errorf("original AUTOPAY transaction is not signed")
		}
	}
	parsed, err := templatecontract.ParseTx(tx, templatecontract.StandardContractScriptResolver(templatecontract.ContractPrefixForNet(GetChainParam_SatsNet().Net)))
	if err != nil {
		return nil, fmt.Errorf("validate original AUTOPAY intent: %w", err)
	}
	expected := contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: record.Parameters["amount_per_block"]}
	param, err := expected.Encode()
	if err != nil {
		return nil, err
	}
	if parsed.Invoke == nil || parsed.Invoke.Action != contractcommon.TemplateInvokeAPIConfig || !bytes.Equal(parsed.Invoke.Param, param) || len(parsed.ContractOutputs) != 1 {
		return nil, fmt.Errorf("original AUTOPAY configuration does not match funding intent")
	}
	output := parsed.ContractOutputs[0]
	contract, err := contractcommon.DecodeContractAddress(defaults.AutopayContract)
	if err != nil || !output.Contract.Equal(contract) {
		return nil, fmt.Errorf("original AUTOPAY contract does not match")
	}
	amount, err := output.AssetAmount(defaults.AutopayFeeAssetName)
	if err != nil {
		return nil, err
	}
	if amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("original AUTOPAY funding is empty")
	}
	principal, err := indexercommon.NewDecimalFromString(record.Parameters["amount"], amount.Precision)
	if err != nil || principal.Sign() <= 0 || amount.Cmp(principal) < 0 {
		return nil, fmt.Errorf("original AUTOPAY funding amount does not match intent")
	}
	return tx, nil
}

// Explicit user continuation only. Status reads never call this function.
func (p *Manager) resumeAccountAutopayTransaction(record *OperationLogRecord, defaults dkvsindexer.NetworkDefaults, payer string) (string, error) {
	tx, err := accountAutopaySignedTransaction(record, defaults, payer)
	if err != nil {
		return "", err
	}
	_, err = p.BroadcastTx_SatsNet(tx)
	// Unknown acknowledgement keeps the same signed transaction pending. A
	// subsequent contract/receipt query decides completion, never a new payment.
	return record.TxID, &AccountAutopayPendingError{TransactionID: record.TxID, Cause: err}
}

// A known signed transaction survives a delayed/unknown submission result.
// This error carries its receipt; it never authorizes paid storage.
type AccountAutopayPendingError struct {
	TransactionID string
	Cause         error
}

func (e *AccountAutopayPendingError) Error() string {
	return fmt.Sprintf("AUTOPAY 原充值交易等待提交或确认，请查询或继续提交原交易 %s", e.TransactionID)
}
func (e *AccountAutopayPendingError) Unwrap() error { return e.Cause }

func accountAutopayLogPending(record *OperationLogRecord) bool {
	return record != nil && record.Action == "account_autopay_fund" && record.TxID != "" &&
		(record.Status == OperationLogPending || record.Status == OperationLogRunning)
}

// Logs stay under the UI identity which initiated the operation. The actual
// payer is the root, and is explicitly matched across UI wallet switches.
func (p *Manager) accountAutopayFundingRecord(defaults dkvsindexer.NetworkDefaults, payer string) (*OperationLogRecord, error) {
	records, err := p.operationLogManager().List()
	if err != nil {
		return nil, err
	}
	var latest *OperationLogRecord
	for _, record := range records {
		if record.Action != "account_autopay_fund" || record.Parameters["network"] != _chain ||
			record.Parameters["payer"] != payer || record.Parameters["contract"] != defaults.AutopayContract {
			continue
		}
		if accountAutopayLogPending(record) {
			return record, nil
		}
		if latest == nil && record.TxID != "" {
			latest = record
		}
	}
	return latest, nil
}

// Query the original receipt and the existing contract history. An absent
// transaction or an unavailable query is unknown, never proof of failure.
func (p *Manager) accountAutopayReceiptOutcome(record *OperationLogRecord) (confirmed bool, executed bool, failure string, err error) {
	info, err := p.l2IndexerClient.GetTxInfo(record.TxID)
	if err != nil {
		return false, false, "", err
	}
	if info == nil || info.BlockHeight <= 0 {
		return false, false, "", nil
	}
	for start := 0; ; {
		raw, err := p.l2IndexerClient.GetContractHistoryJSON(record.Parameters["contract"], start, 100)
		if err != nil {
			return false, false, "", err
		}
		var history struct {
			Total int                                    `json:"total"`
			Data  []contractcommon.ContractHistoryRecord `json:"data"`
		}
		if err := json.Unmarshal([]byte(raw), &history); err != nil {
			return false, false, "", err
		}
		for _, item := range history.Data {
			related, _ := item.Details["result_for_txid"].(string)
			if item.Contract != record.Parameters["contract"] || item.Height <= 0 ||
				(item.TxID != record.TxID && related != record.TxID) {
				continue
			}
			if item.Kind != "result" {
				continue
			}
			switch strings.ToLower(item.Status) {
			case "success":
				return true, true, "", nil
			case "revert", "out_of_gas", "invalid":
				return true, false, item.Status, nil
			}
		}
		start += len(history.Data)
		if len(history.Data) == 0 || start >= history.Total {
			break
		}
	}
	return true, false, "", nil
}

func (p *Manager) reconcileAccountAutopayReceipt(record *OperationLogRecord, ready bool) (bool, error) {
	confirmed, executed, failure, queryErr := p.accountAutopayReceiptOutcome(record)
	if queryErr != nil || !confirmed {
		return true, nil
	}
	if failure != "" {
		_, err := p.UpdateOperationLog(record.ID, OperationLogUpdate{Status: OperationLogFailed,
			Message: "合约确认 AUTOPAY 充值失败：" + failure})
		if err != nil {
			return true, err
		}
		return false, fmt.Errorf("合约确认 AUTOPAY 充值失败：%s", failure)
	}
	if !executed && !ready {
		return true, nil
	}
	_, err := p.UpdateOperationLog(record.ID, OperationLogUpdate{Status: OperationLogSucceeded,
		Message: "合约查询确认 AUTOPAY 充值已成功"})
	return err != nil, err
}
