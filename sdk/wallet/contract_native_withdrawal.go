package wallet

import (
	"bytes"
	"context"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *ContractRuntimeBase) verifyNativeContractWithdrawal(req *wwire.RemoteSignMoreData_Contract,
	descend *swire.MsgTx, deal *DealInfo) error {
	if !p.nativePublicContract() || deal.Reason != INVOKE_RESULT_WITHDRAW {
		return nil
	}
	if !req.NativeWithdrawal || descend == nil || req.RGB11Proof == nil {
		return fmt.Errorf("native withdrawal requires a paired descend and native proof")
	}
	if len(deal.SendInfo) == 0 || len(deal.SendInfo) > 9 {
		return fmt.Errorf("invalid native withdrawal recipient count")
	}
	var bitcoin *wire.MsgTx
	for _, info := range req.Tx {
		if info.L1Tx && info.Reason == "" {
			if bitcoin != nil {
				return fmt.Errorf("multiple native withdrawal Bitcoin transactions")
			}
			var err error
			bitcoin, err = DecodeMsgTx(info.Tx)
			if err != nil {
				return err
			}
		}
	}
	if bitcoin == nil {
		return fmt.Errorf("missing native withdrawal Bitcoin transaction")
	}
	commitments := 0
	for _, output := range descend.TxOut {
		kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err == nil && kind == sindexer.CONTENT_TYPE_DESCENDING {
			if string(data) != bitcoin.TxID() || output.Value != 0 || len(output.Assets) != 1 ||
				output.Assets[0].Name != *p.runtime.GetAssetName() || output.Assets[0].BindingSat != 0 ||
				output.Assets[0].Amount.Cmp(deal.TotalAmt) != 0 {
				return fmt.Errorf("native descend must burn the exact outbound asset quantity")
			}
			commitments++
		}
	}
	if commitments != 1 {
		return fmt.Errorf("native withdrawal requires exactly one descend output")
	}
	var poolValue, poolChange int64
	for _, output := range deal.PreOutputs {
		if !bytes.Equal(output.OutValue.PkScript, p.pkScript) {
			continue
		}
		poolValue += output.Value()
		if amount := output.GetAsset(p.runtime.GetAssetName()); amount != nil && amount.Sign() > 0 {
			source, err := p.stp.GetWalletMgr().loadNativeChannelSource(p.ChannelAddr, output.OutPointStr)
			if err != nil {
				return err
			}
			validated, err := p.stp.GetWalletMgr().validateNativeChannelSource(context.Background(), p.localWallet,
				p.ChannelAddr, source, nativeproof.Current)
			if err != nil {
				return err
			}
			if validated.Asset.Amount.Cmp(amount) != 0 {
				return fmt.Errorf("native source amount differs from managed inventory")
			}
		}
	}
	for _, output := range bitcoin.TxOut {
		if bytes.Equal(output.PkScript, p.pkScript) {
			poolChange += output.Value
		}
	}
	if poolValue <= 0 || poolChange != poolValue {
		return fmt.Errorf("native withdrawal must preserve the full Bitcoin pool carrier")
	}
	return nil
}
