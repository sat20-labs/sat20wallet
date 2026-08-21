package wallet

import (
	"fmt"
)

type PunishBroadcastResult struct {
	ChannelId   string   `json:"channel_id"`
	CommitTxId  string   `json:"commit_txid"`
	PunishTxIds []string `json:"punish_txids"`
	Broadcasted bool     `json:"broadcasted"`
}

type ForceClosePlan struct {
	ChannelId          string   `json:"channel_id"`
	ChannelStatus      int      `json:"channel_status"`
	CommitHeight       int      `json:"commit_height"`
	ChanPoint          string   `json:"chan_point"`
	CsvDelay           int      `json:"csv_delay"`
	CommitTxId         string   `json:"commit_txid"`
	CommitTxHex        string   `json:"commit_tx_hex"`
	DeAnchorTxId       string   `json:"deanchor_txid,omitempty"`
	DeAnchorTxHex      string   `json:"deanchor_tx_hex,omitempty"`
	PrevTxIds          []string `json:"prev_txids,omitempty"`
	PrevTxHex          []string `json:"prev_tx_hex,omitempty"`
	NextTxIds          []string `json:"next_txids,omitempty"`
	NextTxHex          []string `json:"next_tx_hex,omitempty"`
	SweepCondition     string   `json:"sweep_condition"`
	UserActionRequired string   `json:"user_action_required"`
}

type SweepBuildResult struct {
	ChannelId     string   `json:"channel_id"`
	ChannelStatus int      `json:"channel_status"`
	CommitTxId    string   `json:"commit_txid"`
	SweepTxId     string   `json:"sweep_txid"`
	SweepTxHex    string   `json:"sweep_tx_hex"`
	TxIds         []string `json:"txids"`
	TxHex         []string `json:"tx_hex"`
	Fee           int64    `json:"fee"`
	Signed        bool     `json:"signed"`
	Verified      bool     `json:"verified"`
	Broadcastable bool     `json:"broadcastable"`
	Height        int      `json:"height"`
	CsvDelay      int      `json:"csv_delay"`
	Broadcasted   bool     `json:"broadcasted,omitempty"`
	UserAction    string   `json:"user_action"`
}

type PunishCoverageSummary struct {
	Status               string          `json:"status"`
	CoveredRevokedStates int             `json:"covered_revoked_states"`
	RevokedCommitments   []*PunishTxInfo `json:"revoked_commitments,omitempty"`
	Missing              []string        `json:"missing,omitempty"`
}

type CommitmentExport struct {
	ChannelId               string          `json:"channel_id"`
	ChannelAddress          string          `json:"channel_address"`
	ChanPoint               string          `json:"chan_point"`
	ChannelStatus           int             `json:"channel_status"`
	CommitHeight            int             `json:"commit_height"`
	CsvDelay                int             `json:"csv_delay"`
	LocalCommitmentTxId     string          `json:"local_commitment_txid,omitempty"`
	LocalCommitmentTxHex    string          `json:"local_commitment_tx_hex,omitempty"`
	RemoteCommitmentTxId    string          `json:"remote_commitment_txid,omitempty"`
	RemoteCommitmentTxHex   string          `json:"remote_commitment_tx_hex,omitempty"`
	LocalDeAnchorTxId       string          `json:"local_deanchor_txid,omitempty"`
	LocalDeAnchorTxHex      string          `json:"local_deanchor_tx_hex,omitempty"`
	RemoteDeAnchorTxId      string          `json:"remote_deanchor_txid,omitempty"`
	RemoteDeAnchorTxHex     string          `json:"remote_deanchor_tx_hex,omitempty"`
	LocalCommitmentPresent  bool            `json:"local_commitment_present"`
	RemoteCommitmentPresent bool            `json:"remote_commitment_present"`
	LocalBalance            []*DisplayAsset `json:"local_balance,omitempty"`
	RemoteBalance           []*DisplayAsset `json:"remote_balance,omitempty"`
	L2SpendableOutputs      interface{}     `json:"l2_spendable_outputs,omitempty"`
	L2PendingOutputs        interface{}     `json:"l2_pending_outputs,omitempty"`
	MissingEvidence         []string        `json:"missing_evidence,omitempty"`
}

type SafetySnapshot struct {
	ChannelId               string                 `json:"channel_id"`
	ChannelAddress          string                 `json:"channel_address"`
	ChanPoint               string                 `json:"chan_point"`
	RawStatus               int                    `json:"raw_status"`
	Status                  string                 `json:"status"`
	CommitHeight            int                    `json:"commit_height"`
	CsvDelay                int                    `json:"csv_delay"`
	LocalCommitmentPresent  bool                   `json:"local_commitment_present"`
	RemoteCommitmentPresent bool                   `json:"remote_commitment_present"`
	LocalCommitmentTxId     string                 `json:"local_commitment_txid,omitempty"`
	RemoteCommitmentTxId    string                 `json:"remote_commitment_txid,omitempty"`
	LocalDeAnchorTxId       string                 `json:"local_deanchor_txid,omitempty"`
	RemoteDeAnchorTxId      string                 `json:"remote_deanchor_txid,omitempty"`
	LocalBalance            []*DisplayAsset        `json:"local_balance,omitempty"`
	RemoteBalance           []*DisplayAsset        `json:"remote_balance,omitempty"`
	L2SpendableBalance      interface{}            `json:"l2_spendable_balance,omitempty"`
	L2PendingBalance        interface{}            `json:"l2_pending_balance,omitempty"`
	PunishCoverage          *PunishCoverageSummary `json:"punish_coverage"`
	MissingEvidence         []string               `json:"missing_evidence,omitempty"`
	NextCheck               string                 `json:"next_check,omitempty"`
}

func (p *Manager) PunishStatus(channelId string) ([]*PunishTxInfo, error) {
	tower := p.GetWatchTower()
	if tower == nil {
		return nil, fmt.Errorf("watchtower is not initialized")
	}
	items, err := tower.ListPunishTx(channelId, false)
	if err != nil || channelId == "" {
		return items, err
	}
	channel := p.FindChannel(channelId)
	currentRemoteTxID := ""
	if channel != nil {
		channel.Mutex.RLock()
		if channel.RemoteCommitment != nil && channel.RemoteCommitment.CommitTx != nil {
			currentRemoteTxID = channel.RemoteCommitment.CommitTx.TxID()
		}
		channel.Mutex.RUnlock()
	}
	filtered := make([]*PunishTxInfo, 0, len(items))
	for _, item := range items {
		if item != nil && item.CommitTxId != currentRemoteTxID {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (p *Manager) BuildPunishTx(channelId, commitTxId string) (*PunishTxInfo, error) {
	if channelId == "" || commitTxId == "" {
		return nil, fmt.Errorf("channel id and commit txid are required")
	}
	tower := p.GetWatchTower()
	if tower == nil {
		return nil, fmt.Errorf("watchtower is not initialized")
	}
	info, err := tower.GetPunishTxInfo(channelId, commitTxId, IsTestNet())
	if err != nil {
		return nil, err
	}
	if info.ChannelId != channelId || info.CommitTxId != commitTxId {
		return nil, fmt.Errorf("punish evidence does not match channel and commitment")
	}
	channel := p.FindChannel(channelId)
	if channel != nil {
		channel.Mutex.RLock()
		isCurrentRemote := channel.RemoteCommitment != nil && channel.RemoteCommitment.CommitTx != nil &&
			channel.RemoteCommitment.CommitTx.TxID() == commitTxId
		channel.Mutex.RUnlock()
		if isCurrentRemote {
			return nil, fmt.Errorf("current remote commitment is not punishable")
		}
	}
	return info, nil
}

// TestBroadcastPunishTx exposes a testnet-only drill. Runtime protection never
// depends on this method; the SDK monitor calls HandleUnexpectedChannelClose.
func (p *Manager) TestBroadcastPunishTx(channelId, commitTxId string) (*PunishBroadcastResult, error) {
	if !IsTestNet() {
		return nil, fmt.Errorf("punish drill is only available on testnet")
	}
	info, err := p.BuildPunishTx(channelId, commitTxId)
	if err != nil {
		return nil, err
	}
	tower := p.GetWatchTower()
	storedChannelId, txs, err := tower.GetPunishTx(commitTxId)
	if err != nil {
		return nil, err
	}
	if storedChannelId != channelId || info.ChannelId != channelId {
		return nil, fmt.Errorf("commit tx %s does not belong to channel %s", commitTxId, channelId)
	}
	if _, err := p.GetIndexerRPCClient().GetRawTx(commitTxId); err != nil {
		return nil, fmt.Errorf("revoked commitment %s is not visible on L1", commitTxId)
	}
	channel := p.FindChannel(channelId)
	if channel == nil {
		return nil, fmt.Errorf("channel %s not found", channelId)
	}
	err = p.HandleUnexpectedChannelClose(channel, commitTxId)
	result := &PunishBroadcastResult{
		ChannelId:   channelId,
		CommitTxId:  commitTxId,
		PunishTxIds: append([]string(nil), info.PunishTxIds...),
		Broadcasted: p.areL1TxsVisible(txs),
	}
	return result, err
}

func (p *Manager) SafetySnapshot(channelId string) (*SafetySnapshot, error) {
	exported, err := p.CommitmentExport(channelId)
	if err != nil {
		return nil, err
	}
	missing := append([]string(nil), exported.MissingEvidence...)
	coverage := &PunishCoverageSummary{Status: "PUNISH_COVERAGE_UNKNOWN"}
	items, err := p.PunishStatus(exported.ChannelId)
	if err != nil {
		missing = appendSafetyMissing(missing, "MISSING_PUNISH_COVERAGE")
		coverage.Missing = appendSafetyMissing(coverage.Missing, "MISSING_PUNISH_COVERAGE")
	} else if len(items) == 0 {
		coverage.Status = "NO_REVOKED_REMOTE_STATE"
	} else {
		coverage.Status = "COVERED"
		coverage.RevokedCommitments = items
		for _, item := range items {
			if item == nil || !item.Verified || !item.Broadcastable {
				coverage.Status = "PUNISH_COVERAGE_MISSING"
				coverage.Missing = appendSafetyMissing(coverage.Missing, "MISSING_PUNISH_COVERAGE")
				missing = appendSafetyMissing(missing, "MISSING_PUNISH_COVERAGE")
				break
			}
			coverage.CoveredRevokedStates++
		}
	}
	verified, verifyErr := p.CommitmentExport(channelId)
	if verifyErr != nil || !sameCommitmentGeneration(exported, verified) {
		missing = appendSafetyMissing(missing, "CHANNEL_STATE_CHANGED_DURING_SNAPSHOT")
	}

	status := "UNSAFE"
	nextCheck := "wait for channel recovery or inspect channel state before value-moving STP operations"
	if exported.ChannelStatus == int(CS_READY) {
		if len(missing) == 0 {
			status, nextCheck = "READY_SAFE", ""
		} else {
			status = "READY_DEGRADED"
			nextCheck = "resolve missing safety evidence before value-moving STP operations"
		}
	}
	return &SafetySnapshot{
		ChannelId: exported.ChannelId, ChannelAddress: exported.ChannelAddress,
		ChanPoint: exported.ChanPoint, RawStatus: exported.ChannelStatus, Status: status,
		CommitHeight: exported.CommitHeight, CsvDelay: exported.CsvDelay,
		LocalCommitmentPresent:  exported.LocalCommitmentPresent,
		RemoteCommitmentPresent: exported.RemoteCommitmentPresent,
		LocalCommitmentTxId:     exported.LocalCommitmentTxId,
		RemoteCommitmentTxId:    exported.RemoteCommitmentTxId,
		LocalDeAnchorTxId:       exported.LocalDeAnchorTxId, RemoteDeAnchorTxId: exported.RemoteDeAnchorTxId,
		LocalBalance: exported.LocalBalance, RemoteBalance: exported.RemoteBalance,
		L2SpendableBalance: exported.L2SpendableOutputs, L2PendingBalance: exported.L2PendingOutputs,
		PunishCoverage: coverage, MissingEvidence: missing, NextCheck: nextCheck,
	}, nil
}

func (p *Manager) CommitmentExport(channelId string) (*CommitmentExport, error) {
	channel := p.FindChannel(channelId)
	if channel == nil {
		return nil, fmt.Errorf("channel %s not found", channelId)
	}
	channel.Mutex.RLock()
	defer channel.Mutex.RUnlock()

	exported := &CommitmentExport{
		ChannelId: channel.ChannelId, ChannelAddress: channel.Address,
		ChannelStatus: int(channel.Status), CommitHeight: channel.CommitHeight, CsvDelay: int(channel.CsvDelay),
	}
	if channel.ChanPoint != nil {
		exported.ChanPoint = channel.ChanPoint.OutPointStr
	} else {
		exported.MissingEvidence = appendSafetyMissing(exported.MissingEvidence, "MISSING_CHANNEL_POINT")
	}
	if channel.LocalCommitment == nil || channel.LocalCommitment.CommitTx == nil {
		exported.MissingEvidence = appendSafetyMissing(exported.MissingEvidence, "MISSING_LOCAL_COMMITMENT")
	} else {
		exported.LocalCommitmentPresent = true
		exported.LocalCommitmentTxId = channel.LocalCommitment.CommitTx.TxID()
		var err error
		if IsTestNet() {
			exported.LocalCommitmentTxHex, err = EncodeMsgTx(channel.LocalCommitment.CommitTx)
			if err != nil {
				return nil, err
			}
		}
		exported.LocalBalance = convertBalance(channel.LocalCommitment.LocalBalance)
		exported.RemoteBalance = convertBalance(channel.LocalCommitment.RemoteBalance)
		if channel.LocalCommitment.DeAnchorTx != nil {
			exported.LocalDeAnchorTxId = channel.LocalCommitment.DeAnchorTx.TxID()
			if IsTestNet() {
				exported.LocalDeAnchorTxHex, err = EncodeMsgTx_SatsNet(channel.LocalCommitment.DeAnchorTx)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if channel.RemoteCommitment == nil || channel.RemoteCommitment.CommitTx == nil {
		exported.MissingEvidence = appendSafetyMissing(exported.MissingEvidence, "MISSING_REMOTE_COMMITMENT")
	} else {
		exported.RemoteCommitmentPresent = true
		exported.RemoteCommitmentTxId = channel.RemoteCommitment.CommitTx.TxID()
		var err error
		if IsTestNet() {
			exported.RemoteCommitmentTxHex, err = EncodeMsgTx(channel.RemoteCommitment.CommitTx)
			if err != nil {
				return nil, err
			}
		}
		if channel.RemoteCommitment.DeAnchorTx != nil {
			exported.RemoteDeAnchorTxId = channel.RemoteCommitment.DeAnchorTx.TxID()
			if IsTestNet() {
				exported.RemoteDeAnchorTxHex, err = EncodeMsgTx_SatsNet(channel.RemoteCommitment.DeAnchorTx)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	exported.L2SpendableOutputs = channel.GetValidOutput_SatsNet()
	exported.L2PendingOutputs = channel.GetPendingOutput_SatsNet()
	return exported, nil
}

func (p *Manager) ForceClosePlan(channelId string) (*ForceClosePlan, error) {
	channel := p.FindChannel(channelId)
	if channel == nil {
		return nil, fmt.Errorf("channel %s not found", channelId)
	}
	channel.Mutex.RLock()
	defer channel.Mutex.RUnlock()
	if channel.LocalCommitment == nil || channel.LocalCommitment.CommitTx == nil {
		return nil, fmt.Errorf("channel %s has no local commitment tx", channelId)
	}
	commitHex := ""
	var err error
	if IsTestNet() {
		commitHex, err = EncodeMsgTx(channel.LocalCommitment.CommitTx)
		if err != nil {
			return nil, err
		}
	}
	plan := &ForceClosePlan{
		ChannelId: channel.ChannelId, ChannelStatus: int(channel.Status), CommitHeight: channel.CommitHeight,
		CsvDelay: int(channel.CsvDelay), CommitTxId: channel.LocalCommitment.CommitTx.TxID(), CommitTxHex: commitHex,
		SweepCondition:     fmt.Sprintf("after local CSV delay of %d blocks", channel.CsvDelay),
		UserActionRequired: "broadcast local commitment only if cooperative close is unavailable or safety requires unilateral exit",
	}
	if channel.ChanPoint != nil {
		plan.ChanPoint = channel.ChanPoint.OutPointStr
	}
	if channel.LocalCommitment.DeAnchorTx != nil {
		plan.DeAnchorTxId = channel.LocalCommitment.DeAnchorTx.TxID()
		if IsTestNet() {
			plan.DeAnchorTxHex, err = EncodeMsgTx_SatsNet(channel.LocalCommitment.DeAnchorTx)
			if err != nil {
				return nil, err
			}
		}
	}
	for _, tx := range channel.LocalCommitment.PrevTxs {
		if tx == nil {
			continue
		}
		plan.PrevTxIds = append(plan.PrevTxIds, tx.TxID())
		if IsTestNet() {
			raw, err := EncodeMsgTx(tx)
			if err != nil {
				return nil, err
			}
			plan.PrevTxHex = append(plan.PrevTxHex, raw)
		}
	}
	for _, tx := range channel.LocalCommitment.NextTxs {
		if tx == nil {
			continue
		}
		plan.NextTxIds = append(plan.NextTxIds, tx.TxID())
		if IsTestNet() {
			raw, err := EncodeMsgTx(tx)
			if err != nil {
				return nil, err
			}
			plan.NextTxHex = append(plan.NextTxHex, raw)
		}
	}
	return plan, nil
}

func (p *Manager) BuildSweepTx(channelId string, height int) (*SweepBuildResult, error) {
	channel := p.FindChannel(channelId)
	if channel == nil {
		return nil, fmt.Errorf("channel %s not found", channelId)
	}
	if height <= 0 && p.GetIndexerRPCClient() != nil {
		height = p.GetIndexerRPCClient().GetSyncHeight()
	}
	if height <= 0 {
		return nil, fmt.Errorf("invalid sweep height")
	}
	channel.Mutex.RLock()
	defer channel.Mutex.RUnlock()
	if !channel.IsInitiator {
		return nil, fmt.Errorf("sweep build is only supported for the channel initiator wallet")
	}
	if channel.LocalCommitment == nil || channel.LocalCommitment.CommitTx == nil {
		return nil, fmt.Errorf("channel %s has no local commitment tx", channelId)
	}
	pkg, err := p.BuildSignedSweepTxForClient(channel, height, p.GetFeeRate())
	if err != nil {
		return nil, err
	}
	if pkg == nil || pkg.SweepTx == nil {
		return nil, fmt.Errorf("no sweepable output")
	}
	result := &SweepBuildResult{
		ChannelId: channel.ChannelId, ChannelStatus: int(channel.Status), CommitTxId: pkg.CommitTxId,
		SweepTxId: pkg.SweepTxId, Fee: pkg.Fee, Signed: pkg.Signed, Verified: pkg.Verified,
		Broadcastable: pkg.Broadcastable, Height: height, CsvDelay: int(channel.CsvDelay),
		UserAction: "SDK force-close monitor broadcasts automatically after the CSV delay",
	}
	for _, tx := range pkg.Txs {
		if tx == nil {
			continue
		}
		result.TxIds = append(result.TxIds, tx.TxID())
		if IsTestNet() {
			raw, err := EncodeMsgTx(tx)
			if err != nil {
				return nil, err
			}
			result.TxHex = append(result.TxHex, raw)
			if tx.TxID() == pkg.SweepTxId {
				result.SweepTxHex = raw
			}
		}
	}
	return result, nil
}

func appendSafetyMissing(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func sameCommitmentGeneration(a, b *CommitmentExport) bool {
	return a != nil && b != nil &&
		a.ChannelStatus == b.ChannelStatus && a.CommitHeight == b.CommitHeight &&
		a.LocalCommitmentTxId == b.LocalCommitmentTxId &&
		a.RemoteCommitmentTxId == b.RemoteCommitmentTxId
}
