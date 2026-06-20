package wallet

import "fmt"

func IsServerCommitment(whoseCommit int, channel *Channel) bool {
	if channel == nil {
		return false
	}
	if whoseCommit == 0 {
		return !channel.IsInitiator
	}
	return channel.IsInitiator
}

func SelectCommitBalances(channel *Channel, serverCommit bool) (map[AssetName]*Decimal, map[AssetName]*Decimal) {
	if channel == nil {
		return nil, nil
	}
	if serverCommit != channel.IsInitiator {
		return channel.GetCommitLocalBalance(), channel.GetCommitRemoteBalance()
	}
	return channel.GetCommitRemoteBalance(), channel.GetCommitLocalBalance()
}

func ClampCommitmentFeeRate(channel *Channel, feeRate int64) int64 {
	if channel == nil || channel.FeeCfg == nil {
		return feeRate
	}
	if feeRate > channel.FeeCfg.CommitmentFeeRate {
		return channel.FeeCfg.CommitmentFeeRate
	}
	return feeRate
}

func InitCommitmentPlainValues(localBalance, remoteBalance map[AssetName]*Decimal, plainSats int64) (int64, int64) {
	localPlainValue := localBalance[PLAIN_ASSET].Int64()
	localPlainValue += plainSats
	remotePlainValue := remoteBalance[PLAIN_ASSET].Int64()
	return localPlainValue, remotePlainValue
}

func RebalanceCommitmentPlainOutputs(channel *Channel, serverCommit bool, localPlainValue, remotePlainValue int64, localPkScript, remotePkScript []byte) (int64, int64, []byte, []byte, error) {
	if channel == nil || channel.FeeCfg == nil {
		return localPlainValue, remotePlainValue, localPkScript, remotePkScript, nil
	}

	if serverCommit {
		localPlainValue, remotePlainValue = remotePlainValue, localPlainValue
		localPkScript, remotePkScript = remotePkScript, localPkScript

		remotePlainValue -= channel.FeeCfg.MinReserveSats
		localPlainValue += channel.FeeCfg.MinReserveSats
		if remotePlainValue < 0 {
			return 0, 0, nil, nil, fmt.Errorf("invalid remote plain sats %d", remotePlainValue)
		}

		remotePlainValue += channel.FeeCfg.CommitmentFee / 2
		localPlainValue -= channel.FeeCfg.CommitmentFee / 2
		if localPlainValue < 0 {
			return 0, 0, nil, nil, fmt.Errorf("invalid local plain sats %d", localPlainValue)
		}
	}

	return localPlainValue, remotePlainValue, localPkScript, remotePkScript, nil
}
