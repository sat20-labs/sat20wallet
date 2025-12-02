package wallet

import (
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) GetTickerInfo(name *swire.AssetName) *indexer.TickerInfo {
	return p.getTickerInfo(name)
}

func (p *Manager) GetWalletMgr() *Manager {
	return p
}
func (p *Manager) GetIndexerClient() IndexerRPCClient {
	return p.l1IndexerClient
}
func (p *Manager) GetIndexerClient_SatsNet() IndexerRPCClient {
	return p.l2IndexerClient
}

func (p *Manager) GetMode() string {
	return p.cfg.Mode
}

// 只观察的合约管理器不需要实现这些接口
func (p *Manager) GetContract(url string) ContractRuntime {
	return nil
}
func (p *Manager) GetServerNodePubKey() *secp256k1.PublicKey {
	return nil
}
func (p *Manager) GetSpecialContractResv(assetName, templateName string) ContractDeployResvIF {
	return nil
}
func (p *Manager) GetDeployReservation(id int64) ContractDeployResvIF {
	return nil
}
func (p *Manager) SaveReservation(ContractDeployResvIF) error {
	return fmt.Errorf("not implemented")
}
func (p *Manager) SaveReservationWithLock(ContractDeployResvIF) error {
	return fmt.Errorf("not implemented")
}
func (p *Manager) GetDB() indexer.KVDB {
	return p.db
}

func (p *Manager) NeedRebuildTraderHistory() bool {
	return false
}

func (p *Manager) CoGenerateStubUtxos(n int, feeRate int64, contractURL string, invokeCount int64,
	excludeRecentBlock bool) (string, int64, error) {
	return "", 0, fmt.Errorf("not implemented")
}
func (p *Manager) CoBatchSendV3(dest []*SendAssetInfo, assetNameStr string, feeRate int64,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool) (string, int64, error) {
	return "", 0, fmt.Errorf("not implemented")
}
func (p *Manager) CoSendOrdxWithStub(dest string, assetNameStr string, amt int64, feeRate int64, stub string,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte,
	sendDeAnchorTx, excludeRecentBlock bool) (string, int64, error) {
	return "", 0, fmt.Errorf("not implemented")
}
func (p *Manager) CoBatchSendV2_SatsNet(dest []*SendAssetInfo, assetName string,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (p *Manager) CoBatchSend_SatsNet(destAddr []string, assetName string, amtVect []string,
	reason, contractURL string, invokeCount int64, memo, static, runtime []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (p *Manager) SendSigReq(req *wwire.SignRequest, sig []byte) ([][][]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *Manager) CreateContractDepositAnchorTx(contract ContractRuntime, destAddr string,
	splicingOutput *indexer.TxOutput, assetName *AssetName, memo []byte) (*swire.MsgTx, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *Manager) AscendAssetInCoreChannel(assetNameStr string, utxo string, memo []byte) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (p *Manager) DeployContract(templateName, contractContent string,
	fees []string, feeRate int64, deployer string) (string, int64, error) {
	return "", 0, fmt.Errorf("not implemented")
}
