package wallet

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/share/btclucky"
)

type BTCLuckyMiningConfig struct {
	Jobs             string `json:"jobs"`
	LowPriority      bool   `json:"lowPriority"`
	LowPrioritySleep string `json:"lowPrioritySleep"`
}

func (p *Manager) StartBTCLuckyMining(req BTCLuckyMiningConfig) (*btclucky.MinerStatus, error) {
	if p == nil || p.wallet == nil {
		return nil, fmt.Errorf("wallet is not initialized")
	}
	if hex.EncodeToString(p.wallet.GetPubKey().SerializeCompressed()) == indexer.GetBootstrapPubKey() {
		return nil, fmt.Errorf("bootstrap does not participate in BTC lucky mining")
	}
	if p.btcLuckyMiner != nil && p.btcLuckyMiner.IsMining() {
		return nil, fmt.Errorf("btc lucky mining is already running")
	}

	walletPubKey := p.wallet.GetPubKey().SerializeCompressed()
	rewardAddr, err := p.btcLuckyRewardAddress(walletPubKey)
	if err != nil {
		return nil, err
	}

	lowPrioritySleep := time.Second
	if strings.TrimSpace(req.LowPrioritySleep) != "" {
		lowPrioritySleep, err = time.ParseDuration(req.LowPrioritySleep)
		if err != nil {
			return nil, err
		}
	}
	jobs := strings.TrimSpace(req.Jobs)
	if jobs == "" {
		jobs = "1"
	}

	backend := btclucky.NewHTTPTemplateBackend(p.l1IndexerBaseURL(), 10*time.Second)
	miner, err := btclucky.NewMiner(btclucky.BTCLuckyMinerConfig{
		Enabled:          true,
		Backend:          btclucky.BTCLuckyBackendHTTPTemplate,
		RewardAddr:       rewardAddr,
		MinerID:          hex.EncodeToString(walletPubKey),
		Jobs:             jobs,
		LowPriority:      req.LowPriority,
		LowPrioritySleep: lowPrioritySleep,
		Network:          p.cfg.Chain,
	}, backend)
	if err != nil {
		return nil, err
	}
	if err := miner.Start(); err != nil {
		return nil, err
	}
	p.mutex.Lock()
	p.btcLuckyMiner = miner
	p.mutex.Unlock()
	st := miner.Status()
	return &st, nil
}

func (p *Manager) StopBTCLuckyMining() *btclucky.MinerStatus {
	if p == nil || p.btcLuckyMiner == nil {
		st := btclucky.MinerStatus{Enabled: false}
		if rewardAddr, err := p.btcLuckyRewardAddress(nil); err == nil {
			st.RewardAddress = rewardAddr
		} else if err != nil {
			st.LastError = err.Error()
		}
		return &st
	}
	p.btcLuckyMiner.Stop()
	st := p.btcLuckyMiner.Status()
	return &st
}

func (p *Manager) GetBTCLuckyMiningStatus() *btclucky.MinerStatus {
	if p == nil || p.btcLuckyMiner == nil {
		st := btclucky.MinerStatus{Enabled: false}
		if rewardAddr, err := p.btcLuckyRewardAddress(nil); err == nil {
			st.RewardAddress = rewardAddr
		} else if err != nil {
			st.LastError = err.Error()
		}
		return &st
	}
	st := p.btcLuckyMiner.Status()
	return &st
}

func (p *Manager) btcLuckyRewardAddress(walletPubKey []byte) (string, error) {
	if p == nil || p.wallet == nil {
		return "", fmt.Errorf("wallet is not initialized")
	}
	if len(walletPubKey) == 0 {
		walletPubKey = p.wallet.GetPubKey().SerializeCompressed()
	}
	indexerKey, err := p.GetIndexerRPCClient().GetIndexerPubKey()
	if err != nil {
		return "", err
	}
	core, err := p.GetIndexerClient_SatsNet().IsCoreNode(walletPubKey)
	if err != nil {
		return "", err
	}
	bootstrap, err := hex.DecodeString(indexer.GetBootstrapPubKey())
	if err != nil {
		return "", err
	}
	signer, err := btclucky.RewardSigner(walletPubKey, indexerKey, bootstrap, core)
	if err != nil {
		return "", err
	}
	return GetP2WSHaddress(walletPubKey, signer)
}

func (p *Manager) handleBTCLuckyMonitorTick(sendTxInL1 bool) {
	if p == nil || !sendTxInL1 {
		return
	}
	if p.wallet != nil && hex.EncodeToString(p.wallet.GetPubKey().SerializeCompressed()) != indexer.GetBootstrapPubKey() {
		rewards, err := p.GetBTCLuckyRewards()
		if err == nil {
			for _, reward := range rewards {
				if reward.Status != "unascended" {
					continue
				}
				if _, err := p.AscendBTCLuckyReward(reward.FundingUtxo); err != nil {
					Log.Warningf("BTC lucky reward ascend %s failed: %v", reward.FundingUtxo, err)
				}
			}
		}
	}
}

func (p *Manager) l1IndexerBaseURL() string {
	if p == nil || p.cfg == nil || p.cfg.IndexerL1 == nil {
		return ""
	}
	scheme := strings.TrimSpace(p.cfg.IndexerL1.Scheme)
	if scheme == "" {
		scheme = "https"
	}
	host := strings.TrimRight(strings.TrimSpace(p.cfg.IndexerL1.Host), "/")
	proxy := strings.Trim(strings.TrimSpace(p.cfg.IndexerL1.Proxy), "/")
	if proxy == "" {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s/%s", scheme, host, proxy)
}
