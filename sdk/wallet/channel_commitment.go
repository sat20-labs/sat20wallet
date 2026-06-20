package wallet

import (
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type AssetToOutput struct {
	AssetName *AssetName
	Outputs   []*TxOutput
}

type ChannelCommitmentV1 struct {

	// FundingUtxos 按总量切分为两部分
	LocalBalance  map[AssetName]*indexer.Decimal
	RemoteBalance map[AssetName]*indexer.Decimal
	// LocalBalance  int64
	// RemoteBalance int64

	// CommitTx is the latest version of the commitment state, broadcast
	// able by us.
	CommitTx *wire.MsgTx

	Revocation []byte // previous commitment tx revocation

	// CommitSig is one half of the signature required to fully complete
	// the script for the commitment transaction above. This is the
	// signature signed by the remote party for our version of the
	// commitment transactions.
	CommitSig [][]byte

	DeAnchorTx  *swire.MsgTx
	DeAnchorSig [][]byte

	PrevTxs []*wire.MsgTx // 在CommitTx之前需要广播的tx，已经签名
	NextTxs []*wire.MsgTx // 在CommitTx之后需要广播的tx，已经签名
}

type CommitmentTx struct {
	// CommitTx is the latest version of the commitment state, broadcast
	// able by us.
	CommitTx *wire.MsgTx

	// CommitSig is one half of the signature required to fully complete
	// the script for the commitment transaction above. This is the
	// signature signed by the remote party for our version of the
	// commitment transactions.
	CommitSig [][]byte

	DeAnchorTx  *swire.MsgTx
	DeAnchorSig [][]byte

	PrevTxs []*wire.MsgTx // 在CommitTx之前需要广播的tx，已经签名
	NextTxs []*wire.MsgTx // 在CommitTx之后需要广播的tx，已经签名
}

type ChannelCommitment struct {
	ChannelCommitmentV1

	// 其他可能的版本，用于不同的用途
	Others []*CommitmentTx
}

func NewChannelCommitment() *ChannelCommitment {
	return &ChannelCommitment{
		ChannelCommitmentV1: ChannelCommitmentV1{
			LocalBalance:  make(map[AssetName]*indexer.Decimal),
			RemoteBalance: make(map[AssetName]*indexer.Decimal),
		},
	}
}
