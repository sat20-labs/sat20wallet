package wallet

import dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"

const (
	dkvsCodecMaxFieldSize   = dkvscore.CodecMaxFieldSize
	dkvsCodecMaxPayloadSize = dkvscore.CodecMaxPayloadSize
	dkvsCodecVersion        = dkvscore.CodecVersion
)

type DKVSOfflineMessage = dkvscore.DKVSOfflineMessage
type DKVSServiceAuthenticity = dkvscore.DKVSServiceAuthenticity

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
