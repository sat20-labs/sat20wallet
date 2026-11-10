package wallet

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/indexer/share/btclucky"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type BTCLuckyMiningConfig struct {
	Jobs             string `json:"jobs"`
	LowPriority      bool   `json:"lowPriority"`
	LowPrioritySleep string `json:"lowPrioritySleep"`
}

func (p *Manager) StartBTCLuckyMining(req BTCLuckyMiningConfig) (*btclucky.MinerStatus, error) {
	if p == nil {
		return nil, fmt.Errorf("wallet is not initialized")
	}
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	if p.btcLuckyMiner != nil && p.btcLuckyMiner.IsMining() {
		return nil, fmt.Errorf("btc lucky mining is already running")
	}

	owner, err := p.luckyWallet()
	if err != nil {
		return nil, err
	}
	walletPubKey := owner.GetPubKey().SerializeCompressed()
	if hex.EncodeToString(walletPubKey) == indexer.GetBootstrapPubKey() {
		return nil, fmt.Errorf("bootstrap does not participate in BTC lucky mining")
	}
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
	if backup := p.cfg.SlaveIndexerL1; backup != nil && strings.TrimSpace(backup.Host) != "" {
		scheme := strings.TrimSpace(backup.Scheme)
		if scheme == "" {
			scheme = "https"
		}
		endpoint := scheme + "://" + strings.TrimRight(backup.Host, "/")
		if proxy := strings.Trim(backup.Proxy, "/"); proxy != "" {
			endpoint += "/" + proxy
		}
		backend.SetSubmitURLs([]string{endpoint + "/v3/bitcoin/block/submit"})
	}
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
	p.btcLuckyMiner = miner
	st := miner.Status()
	return &st, nil
}

func (p *Manager) StopBTCLuckyMining() *btclucky.MinerStatus {
	if p == nil {
		return &btclucky.MinerStatus{}
	}
	p.channelIdentityMu.Lock()
	defer p.channelIdentityMu.Unlock()
	if p.btcLuckyMiner != nil {
		p.btcLuckyMiner.Stop()
	}
	return p.luckyStatus()
}

func (p *Manager) clearLuckyMiner() {
	if p.btcLuckyMiner != nil {
		p.btcLuckyMiner.Stop()
		p.btcLuckyMiner = nil
	}
}

func (p *Manager) GetBTCLuckyMiningStatus() *btclucky.MinerStatus {
	if p == nil {
		return &btclucky.MinerStatus{}
	}
	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	return p.luckyStatus()
}

func (p *Manager) luckyStatus() *btclucky.MinerStatus {
	if p.btcLuckyMiner != nil {
		st := p.btcLuckyMiner.Status()
		return &st
	}
	st := btclucky.MinerStatus{}
	owner, err := p.luckyWallet()
	if err != nil {
		st.LastError = err.Error()
		return &st
	}
	addr, err := p.btcLuckyRewardAddress(owner.GetPubKey().SerializeCompressed())
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.RewardAddress = addr
	}
	return &st
}

// Mining and reward signing use the root wallet's first account.
func (p *Manager) luckyWallet() (common.Wallet, error) {
	if p == nil {
		return nil, fmt.Errorf("wallet is not initialized")
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not initialized")
	}
	owner, err := p.accountDiscoveryRootLocked()
	if err != nil {
		return nil, err
	}
	if owner == nil || owner.GetPubKey() == nil {
		return nil, fmt.Errorf("wallet has no mining key")
	}
	return owner, nil
}

func (p *Manager) btcLuckyRewardAddress(walletPubKey []byte) (string, error) {
	indexerKey, err := p.l1IndexerClient.primaryIndexer().GetIndexerPubKey()
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
	if _, err := p.configuredSigningPeer(signer); err != nil {
		return "", err
	}
	return GetP2WSHaddress(walletPubKey, signer)
}

func (p *Manager) handleBTCLuckyMonitorTick(sendTxInL1 bool) {
	if p == nil || !sendTxInL1 {
		return
	}
	p.channelIdentityMu.RLock()
	defer p.channelIdentityMu.RUnlock()
	wallet, err := p.luckyWallet()
	if err != nil {
		return
	}
	owner := hex.EncodeToString(wallet.GetPubKey().SerializeCompressed())
	if owner == indexer.GetBootstrapPubKey() {
		return
	}
	client := p.l1IndexerClient.getActiveIndexer()
	height := client.GetSyncHeight()
	if height < 0 {
		return
	}
	hash, err := client.GetBlockHash(height)
	if err != nil || hash == "" {
		return
	}
	if owner == p.luckyScanOwner && hash == p.luckyScanHash {
		return
	}
	if err = p.scanLuckyCoinbases(wallet); err != nil {
		Log.Warningf("BTC lucky reward scan failed: %v", err)
		return
	}
	p.luckyScanOwner, p.luckyScanHash = owner, hash
}

func (p *Manager) scanLuckyCoinbases(owner common.Wallet) error {
	witness, _, address, err := p.btcLuckyWitness(owner)
	if err != nil {
		return err
	}
	coins, err := p.GetIndexerClient().GetUtxosWithAddress(address)
	if err != nil {
		return err
	}
	var maturity uint64
	var scanErr error
	for utxo := range coins {
		reward, err := p.InspectBTCLuckyReward(utxo, witness)
		if errors.Is(err, errNotBTCLuckyReward) {
			continue
		}
		if err != nil {
			scanErr = err
			continue
		}
		if maturity == 0 {
			info, err := p.BTCLuckyInfo()
			if err != nil {
				return err
			}
			maturity = info.Service.CoinbaseMaturity
		}
		if reward.Confirmations < maturity {
			continue
		}
		anchor, err := p.btcLuckyAscendData(utxo)
		if err == nil && anchor == nil {
			_, err = p.ascendLuckyReward(owner, utxo)
		}
		if err != nil {
			scanErr = err
		}
	}
	return scanErr
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
