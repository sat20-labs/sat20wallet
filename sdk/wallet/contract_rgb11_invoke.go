package wallet

import (
	"context"
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

// ErrRGB11ContractRetry prevents a temporary proof or persistence failure
// from advancing the public contract's block cursor past an authenticated input.
var ErrRGB11ContractRetry = errors.New("RGB11 contract input is awaiting validation")

// Materialization precedes the ledger cursor update and runtime mutex. RPC or
// proof failures remain retryable and cannot silently skip a deposit block.
func (p *ContractRuntimeBase) materializeRGB11ContractInvokes(data *InvokeDataInBlock) error {
	if data == nil || !p.rgb11PublicContract() || p.stp == nil || p.stp.GetWalletMgr() == nil {
		return nil
	}
	p.mutex.RLock()
	process := p.EnableBlockL1 != INIT_ENABLE_BLOCK && data.Height >= p.EnableBlockL1 && data.Height > p.CurrBlockL1
	p.mutex.RUnlock()
	if !process {
		return nil
	}
	for _, invoke := range data.InvokeTxVect {
		if invoke == nil || invoke.Tx == nil || invoke.Address != p.ChannelAddr || invoke.Handled {
			continue
		}
		outpoint := fmt.Sprintf("%s:%d", invoke.Tx.TxID(), invoke.InvokeVout)
		receipt, err := p.stp.GetWalletMgr().MaterializeRGB11ContractInput(context.Background(), p.runtime, outpoint)
		if errors.Is(err, ErrRGB11ContractInputNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrRGB11ContractRetry, outpoint, err)
		}
		receipt.Output.UtxoId = indexer.ToUtxoId(data.Height, invoke.TxIndex, invoke.InvokeVout)
		invoke.rgb11ContractInput, invoke.TxOutput, invoke.Invoker = receipt, receipt.Output, receipt.Recipient
		encoded, err := (&InvokeParam{Action: INVOKE_API_DEPOSIT}).Encode()
		if err != nil {
			return err
		}
		invoke.InvokeParam = &sindexer.ContractInvokeData{ContractPath: p.URL(), InvokeParam: encoded}
	}
	return nil
}
