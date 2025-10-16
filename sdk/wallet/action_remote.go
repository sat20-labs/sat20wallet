package wallet

import "fmt"

// 在主网质押足够的资产，以便成为一个Miner
// 优先检查通道中已经存在的质押资产
func (p *Manager) StakeToBeMinner(bCoreNode bool, feeRate int64) (string, int64, error) {
	return "", 0, fmt.Errorf("not implemented")
}

