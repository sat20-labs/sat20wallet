package wallet

import (
	"github.com/sat20-labs/indexer/common"
)

type AssetLockContract struct {
	ContractBase
	AssetName common.AssetName

	// 释放计划
}

func NewAssetLockContract() *AssetLockContract {
	return &AssetLockContract{
		ContractBase: ContractBase{
			TemplateName: TEMPLATE_CONTRACT_AMM,
		},
	}
}

func (p *AssetLockContract) GetContractName() string {
	return p.AssetName.String() + URL_SEPARATOR + p.TemplateName
}

func (p *AssetLockContract) CalcDeployFee() int64 {
	return 0
}

func (p *AssetLockContract) Content() string {
	return ""
}

func (p *AssetLockContract) RuntimeContent() []byte {
	return nil
}
