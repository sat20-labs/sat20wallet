package wallet

import (
	"github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
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

func (p *AssetLockContract) AllowDeploy(stp *Manager, resv Reservation) error {
	return nil
}

func (p *AssetLockContract) AllowInvoke(stp *Manager, resv Reservation) error {
	return nil
}

func (p *AssetLockContract) Invoke(*Manager, Reservation, *swire.MsgTx) error {
	return nil
}

func (p *AssetLockContract) Content() string {
	return ""
}

func (p *AssetLockContract) RuntimeContent() []byte {
	return nil
}
