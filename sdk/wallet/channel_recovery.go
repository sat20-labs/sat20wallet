package wallet

import (
	"strings"

	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

func reopenFeeConfig(channel *Channel) *ChannelFeeConfig {
	if channel == nil || channel.FeeCfg == nil {
		return NewOldFeeConfig()
	}
	return channel.FeeCfg.Clone()
}

func ReopenFeeConfig(channel *Channel) *ChannelFeeConfig {
	return reopenFeeConfig(channel)
}

func reopenFeeConfigWithoutOpeningFee(feeCfg *ChannelFeeConfig) *ChannelFeeConfig {
	if feeCfg == nil {
		feeCfg = NewFeeConfig()
	}
	clone := feeCfg.Clone()
	clone.ManageFee = 0
	clone.MortgageFee = 0
	return clone
}

func ReopenFeeConfigWithoutOpeningFee(feeCfg *ChannelFeeConfig) *ChannelFeeConfig {
	return reopenFeeConfigWithoutOpeningFee(feeCfg)
}

func channelLedgerHasHistory(channel string, ledger []*sindexer.ChannelLedgerEntry) bool {
	for _, entry := range ledger {
		if entry == nil {
			continue
		}
		if entry.ChannelId == "" || entry.ChannelId == channel {
			return true
		}
	}
	return false
}

func ChannelLedgerHasHistoryEntries(channel string, ledger []*sindexer.ChannelLedgerEntry) bool {
	return channelLedgerHasHistory(channel, ledger)
}

func selectReopenFundingOutput(utxos []*indexerwire.TxOutputInfo, minFundingValue int64, isAscended func(string) bool) *indexerwire.TxOutputInfo {
	for _, out := range utxos {
		if out.Value < minFundingValue {
			continue
		}
		if isAscended != nil && isAscended(out.OutPoint) {
			continue
		}
		return out
	}
	return nil
}

func SelectReopenFundingOutput(utxos []*indexerwire.TxOutputInfo, minFundingValue int64, isAscended func(string) bool) *indexerwire.TxOutputInfo {
	return selectReopenFundingOutput(utxos, minFundingValue, isAscended)
}

func classifyRebuildFundingUtxo(ledger []*sindexer.ChannelLedgerEntry, utxo string) (string, bool) {
	parts := strings.Split(utxo, ":")
	if len(parts) != 2 {
		return "invalid outpoint", false
	}
	txid := parts[0]

	for _, entry := range ledger {
		if entry == nil {
			continue
		}
		switch entry.Direction {
		case "ascending":
			for _, outpoint := range entry.L1Outpoints {
				if outpoint == utxo {
					return "exact ascending outpoint", true
				}
			}
			if entry.L1TxId == txid {
				return "same L1 tx has ascending record", true
			}
		case "descending":
			for _, outpoint := range entry.ReturnedChannelOutputs {
				if outpoint == utxo {
					return "descending v2 returned channel output", true
				}
			}
			if entry.Legacy && entry.L1TxId == txid {
				return "legacy descending L1 tx returned output to channel address", true
			}
		}
	}

	return "no channel ledger evidence", false
}
