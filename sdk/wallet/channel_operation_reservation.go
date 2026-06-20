package wallet

import (
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
)

type FundingReservation struct {
	FundingDataInDB

	Req                *wwire.OpenChannelRequest
	ReqSig             []byte
	Accept             *wwire.AcceptChannel
	FundingCreated     *wwire.FundingCreated
	FundingSigned      *wwire.FundingSigned
	FundingBroadcasted *wwire.FundingBroadcasted

	Invoice                 []byte
	LocalDeAnchorPreFetcher stxscript.PrevOutputFetcher
	FundingUtxos            []*TxOutput
	Channel                 *Channel
}

func (p *FundingReservation) GenerateChannel() *Channel {
	return nil
}

type ClosingReservation struct {
	ClosingDataInDB

	Req                *wwire.CloseChannelRequest
	ReqSig             []byte
	LocalSigned        *wwire.ClosingSigned
	RemoteSigned       *wwire.ClosingSigned
	ClosingBroadcasted *wwire.ClosingBroadcasted
	Inscribes          []*InscribeResv
	RevealPrivKey      []byte

	DeAnchorPrefetcher stxscript.PrevOutputFetcher
	Channel            *Channel
}

type CommitmentKeyRing struct {
	// CommitPoint is the per-commitment point for one channel state. All
	// commitment-local tweaks are derived from this point, so it changes for
	// every new commitment height.
	CommitPoint *btcec.PublicKey
	// ToLocalKey belongs to the owner of this commitment transaction. The owner
	// can spend it only after the CSV delay unless the state is revoked.
	ToLocalKey *btcec.PublicKey
	// ToRemoteKey belongs to the counterparty/reservation side and is spendable
	// immediately. Local/remote here is always from this commitment tx's view.
	ToRemoteKey *btcec.PublicKey
	// RevocationKey lets the counterparty take the delayed local output if this
	// commitment has been revoked and later broadcast.
	RevocationKey *btcec.PublicKey
	BootstrapKey  *btcec.PublicKey
	RevealPrivKey *btcec.PrivateKey
}

func (p *CommitmentKeyRing) GetRevealKey() []byte {
	if p.RevealPrivKey == nil {
		return nil
	}
	return p.RevealPrivKey.Serialize()
}

type CommitInfo struct {
	wwire.CommitSigInfo
	CommitTx *wire.MsgTx
	// CommitPrevTxs are transactions that must be available before CommitTx can
	// be valid, such as inscription commit/reveal transactions used by outputs
	// embedded into the commitment.
	CommitPrevTxs []*wire.MsgTx
	// CommitNextTxs are transactions that spend outputs from CommitTx after it
	// is broadcast. BRC20 transfer reveal transactions are kept here so
	// punish/sweep code can spend the revealed outputs later.
	CommitNextTxs []*wire.MsgTx
	// CommitOthers holds extra commitment-adjacent transactions that are not in
	// the simple prev/next chain, but still need signatures and watchtower
	// coverage together with this commitment.
	CommitOthers []*CommitmentTx
}

type RevocationInfo struct {
	// LocalRevKey is the current local revocation public key known by the peer;
	// LocalNextRevKey is the next point sent with RevokeAndAck.
	LocalRevKey     []byte
	LocalNextRevKey []byte
	// LocalRev stores the secret we reveal after the peer's new commitment is
	// verified. Revealing it lets the peer punish our old local commitment.
	LocalRev        *wwire.RevokeAndAck
	LocalCommitInfo CommitInfo

	// RemoteRevKey is the current remote revocation public key. A received
	// revocation secret must derive back to this key before we advance state.
	RemoteRevKey     []byte
	RemoteNextRevKey []byte
	// RemoteRev stores the peer's revealed old secret; it is used to construct
	// the punish transaction for the peer's revoked remote commitment.
	RemoteRev        *wwire.RevokeAndAck
	RemoteCommitInfo CommitInfo
	// SignedPunishTx is cached after ReceiveRevocation proves the old remote
	// commitment can actually be punished.
	SignedPunishTx *wire.MsgTx

	RevealPrivKey []byte
	FeeRate       int64
	// Channel is the new channel state being negotiated; OldChannel is retained
	// until revocation is verified so its remote commitment can be punished if
	// the peer later broadcasts it.
	Channel    *Channel
	OldChannel *Channel
}

type PaymentReservation struct {
	PaymentDataInDB
	RevocationInfo

	TickerInfo *indexer.TickerInfo

	LocalCommitBalance  *Decimal
	RemoteCommitBalance *Decimal

	UnlockReq         *wwire.UnlockRequest
	LockReq           *wwire.LockRequest
	RecoverPaymentReq *wwire.RecoverPaymentRequest
	ReqSig            []byte

	PaymentPreFetcher stxscript.PrevOutputFetcher
	LocalPaymentSig   [][]byte
	RemotePaymentSig  [][]byte

	MatchedContracts []ContractRuntime
}

type SplicingReservation struct {
	SplicingDataInDB
	RevocationInfo

	TickerInfo *indexer.TickerInfo

	InReq  *wwire.SplicingInRequest
	OutReq *wwire.SplicingOutRequest
	ReqSig []byte

	SplicingInputs []*TxOutput
	Fees           []*TxOutput
	StubUtxo       *TxOutput

	Bonus int64

	NewCapacity           int64
	NewLocalBalance       *Decimal
	NewRemoteBalance      *Decimal
	NewLocalPlainBalance  int64
	NewRemotePlainBalance int64

	Invoice    []byte
	InvoiceSig []byte

	PreFetcherForSplicing txscript.PrevOutputFetcher
	PreFetcherForAnchorTx stxscript.PrevOutputFetcher
	Inscribe              *InscribeResv
	LocalSplicingSigInfo  wwire.SplicingSigInfo
	RemoteSplicingSigInfo wwire.SplicingSigInfo

	NewChanPoint         *indexer.TxOutput
	SplicingOutput       *indexer.TxOutput
	SplicingChangeOutput *indexer.TxOutput

	MatchedContracts []ContractRuntime
}

func (p *SplicingReservation) AnchorTxId() string {
	if p == nil {
		return ""
	}
	if p.RecoverAscended {
		return p.RecoveredAnchorTxId
	}
	if p.AnchorTx != nil {
		return p.AnchorTx.TxID()
	}
	return ""
}

func (p *SplicingReservation) InputStubOutpoint() string {
	if p == nil || !p.StubIsSet || p.StubUtxo == nil {
		return ""
	}
	return p.StubUtxo.OutPointStr
}
