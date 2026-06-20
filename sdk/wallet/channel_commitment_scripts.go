package wallet

import (
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

func GenerateChannelScript2(serverCommit bool, channel *Channel, keyRing *CommitmentKeyRing, bootstrapKey *secp256k1.PublicKey) (utils.ScriptDescriptor, utils.ScriptDescriptor, error) {
	var delayScript, directScript utils.ScriptDescriptor

	var err error
	if serverCommit {
		if keyRing.ToLocalKey.IsEqual(bootstrapKey) {
			delayScript, err = CommitDelayScriptForClient(
				keyRing.ToLocalKey, keyRing.RevocationKey, uint32(channel.CsvDelay))
		} else {
			delayScript, err = CommitDelayScriptForServer(
				keyRing.ToLocalKey, bootstrapKey, keyRing.RevocationKey, uint32(channel.CsvDelay))
		}
		if err != nil {
			return nil, nil, err
		}

		directScript, _, err = CommitDirectScriptForClient(keyRing.ToRemoteKey)
		if err != nil {
			return nil, nil, err
		}
	} else {
		delayScript, err = CommitDelayScriptForClient(
			keyRing.ToLocalKey, keyRing.RevocationKey, uint32(channel.CsvDelay))
		if err != nil {
			return nil, nil, err
		}

		if keyRing.ToRemoteKey.IsEqual(bootstrapKey) {
			directScript, _, err = CommitDirectScriptForClient(keyRing.ToRemoteKey)
		} else {
			directScript, _, err = CommitDirectScriptForServer(keyRing.ToRemoteKey, bootstrapKey)
		}
		if err != nil {
			return nil, nil, err
		}
	}

	return delayScript, directScript, nil
}

func GenerateChannelScript3(serverCommit bool, channel *Channel, keyRing *CommitmentKeyRing, bootstrapKey *secp256k1.PublicKey) (utils.ScriptDescriptor, utils.ScriptDescriptor, error) {
	var delayScript, directScript utils.ScriptDescriptor
	var err error
	if serverCommit {
		if keyRing.ToLocalKey.IsEqual(bootstrapKey) {
			delayScript, err = CommitDelayScriptForClient(
				keyRing.ToLocalKey, keyRing.RevocationKey, uint32(channel.CsvDelay))
		} else {
			delayScript, err = CommitDelayScriptForServer(
				keyRing.ToLocalKey, bootstrapKey, keyRing.RevocationKey, uint32(channel.CsvDelay))
		}
		if err != nil {
			return nil, nil, err
		}

		directScript = &WitnessScriptDesc{
			OutputScript:  channel.GetChannelPkScript(),
			WitnessScript: channel.RedeemScript,
		}
	} else {
		delayScript, err = CommitDelayScriptForClient(
			keyRing.ToLocalKey, keyRing.RevocationKey, uint32(channel.CsvDelay))
		if err != nil {
			return nil, nil, err
		}

		if keyRing.ToRemoteKey.IsEqual(bootstrapKey) {
			directScript, _, err = CommitDirectScriptForClient(keyRing.ToRemoteKey)
		} else {
			directScript, _, err = CommitDirectScriptForServer(keyRing.ToRemoteKey, bootstrapKey)
		}
		if err != nil {
			return nil, nil, err
		}
	}

	return delayScript, directScript, nil
}
