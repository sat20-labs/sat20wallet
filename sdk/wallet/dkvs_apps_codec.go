package wallet

import dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"

const (
	dkvsCodecMaxFieldSize   = dkvscore.CodecMaxFieldSize
	dkvsCodecMaxPayloadSize = dkvscore.CodecMaxPayloadSize
	dkvsCodecVersion        = dkvscore.CodecVersion
)

type DKVSWalletRecoveryBackup = dkvscore.DKVSWalletRecoveryBackup
type DKVSGuardianShare = dkvscore.DKVSGuardianShare
type DKVSOfflineMessage = dkvscore.DKVSOfflineMessage
type DKVSServiceAuthenticity = dkvscore.DKVSServiceAuthenticity

func encodeDKVSWalletRecoveryBackup(value DKVSWalletRecoveryBackup) ([]byte, error) {
	return dkvscore.EncodeWalletRecoveryBackup(value)
}

func decodeDKVSWalletRecoveryBackup(value []byte) (*DKVSWalletRecoveryBackup, error) {
	return dkvscore.DecodeWalletRecoveryBackup(value)
}

// Historical package-local names retained for wallet package tests and callers.
func encodeDKVSRecoveryBackup(value DKVSWalletRecoveryBackup) ([]byte, error) {
	return encodeDKVSWalletRecoveryBackup(value)
}

func decodeDKVSRecoveryBackup(value []byte) (*DKVSWalletRecoveryBackup, error) {
	return decodeDKVSWalletRecoveryBackup(value)
}

func encodeDKVSGuardianShare(value DKVSGuardianShare) ([]byte, error) {
	return dkvscore.EncodeGuardianShare(value)
}

func decodeDKVSGuardianShare(value []byte) (*DKVSGuardianShare, error) {
	return dkvscore.DecodeGuardianShare(value)
}

func encodeDKVSOfflineMessage(value DKVSOfflineMessage) ([]byte, error) {
	return dkvscore.EncodeOfflineMessage(value)
}

func decodeDKVSOfflineMessage(value []byte) (*DKVSOfflineMessage, error) {
	return dkvscore.DecodeOfflineMessage(value)
}

func encodeDKVSServiceAuthenticity(value DKVSServiceAuthenticity) ([]byte, error) {
	return dkvscore.EncodeServiceAuthenticity(value)
}

func decodeDKVSServiceAuthenticity(value []byte) (*DKVSServiceAuthenticity, error) {
	return dkvscore.DecodeServiceAuthenticity(value)
}
