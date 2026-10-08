package wallet

import (
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/anchortx"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type anchorHeightClient struct {
	IndexerRPCClient
	height int64
}

func (c *anchorHeightClient) GetBestHeight() int64 { return c.height }

func TestAnchorInvoiceSigningAndBuilders(t *testing.T) {
	params := GetChainParam_SatsNet()
	previous := params.POSV2Height
	params.POSV2Height = 2
	t.Cleanup(func() { params.POSV2Height = previous })
	client := &anchorHeightClient{height: 1}
	manager := &Manager{l2IndexerClient: &IndexerRPCClientMgr{active: client}}
	core, _ := btcec.PrivKeyFromBytes([]byte{71})
	peer, _ := btcec.PrivKeyFromBytes([]byte{73})
	witness, channelScript, err := GetP2WSHscript(core.PubKey().SerializeCompressed(), peer.PubKey().SerializeCompressed())
	require.NoError(t, err)
	channel := &Channel{ChannelInDB: ChannelInDB{IsInitiator: true, RedeemScript: witness,
		LocalChanCfg:  ChannelConfigV2{PaymentKey: peer.PubKey()},
		RemoteChanCfg: ChannelConfigV2{PaymentKey: core.PubKey()}}}
	funding := indexer.NewTxOutput(21000)
	funding.OutPointStr = strings.Repeat("12", 32) + ":0"
	funding.OutValue.PkScript = channelScript
	feeKey, _ := btcec.PrivKeyFromBytes([]byte{75})
	daoScript, err := GetP2TRpkScript(feeKey.PubKey())
	require.NoError(t, err)
	ticker := &indexer.TickerInfo{AssetName: indexer.ASSET_PLAIN_SAT, MaxSupply: "21000000000000000", N: 1}
	reservation := &SplicingReservation{SplicingOutput: funding}
	reservation.Channel = channel
	reservation.AssetName = &PLAIN_ASSET
	reservation.SplicingAmt = indexer.NewDefaultDecimal(21000)
	sign := func(message []byte) ([]byte, error) {
		return ecdsa.Sign(core, chainhash.HashB(message)).Serialize(), nil
	}
	require.True(t, anchortx.StartAnchorManager(&anchortx.AnchorConfig{ChainParams: params, IndexerHost: "unused.invalid", IndexerProxy: params.Name}))
	t.Cleanup(anchortx.Stop)
	for _, kind := range []string{"opening-and-dao", "splicing", "single"} {
		t.Run(kind, func(t *testing.T) {
			var tx *wire.MsgTx
			switch kind {
			case "opening-and-dao":
				tx = CreateOpeningAnchorTx(channel, funding, 20000, 1000, nil, daoScript, true)
			case "splicing":
				tx = CreateSplicingInAnchorTx(reservation, daoScript, ticker, nil, true)
			case "single":
				tx, err = CreateAnchorTx(funding, &PLAIN_ASSET, ticker, witness, nil, nil, true)
				require.NoError(t, err)
			}
			require.NotNil(t, tx)
			signature, err := manager.SignAnchorTx(tx, sign)
			require.NoError(t, err)
			_, _, err = CheckAnchorPkScript(tx.TxIn[0].SignatureScript, tx.TxOut, true)
			require.NoError(t, err)
			// Verify the SDK producer against the node's independent verifier.
			_, err = anchortx.CheckAnchorPkScriptWithCoreCheck(tx.TxIn[0].SignatureScript, false, func(pub []byte) bool {
				return string(pub) == string(core.PubKey().SerializeCompressed())
			}, tx.TxOut, true)
			require.NoError(t, err)
			if kind == "opening-and-dao" {
				signed := CreateOpeningAnchorTx(channel, funding, 20000, 1000, signature, daoScript, true)
				require.NotNil(t, signed)
				require.Equal(t, tx.TxHash(), signed.TxHash())
				require.Nil(t, CreateOpeningAnchorTx(channel, funding, 20000, 1000, signature, channelScript, true), "changing the DAO payee must invalidate its signature")
			}

			trailing := append(append([]byte{}, tx.TxIn[0].SignatureScript...), byte(0))
			_, _, err = CheckAnchorPkScript(trailing, tx.TxOut, true)
			require.Error(t, err, "the SDK must reject a different script encoding of the same authorization")
			noncanonical := tx.Copy()
			noncanonical.LockTime = 1
			_, err = manager.SignAnchorTx(noncanonical, sign)
			require.Error(t, err, "the SDK must not sign a noncanonical envelope")
			changed := tx.Copy()
			changed.TxOut[0].PkScript = daoScript
			_, _, err = CheckAnchorPkScript(changed.TxIn[0].SignatureScript, changed.TxOut, true)
			require.Error(t, err)
		})
	}
	unsigned := CreateOpeningAnchorTx(channel, funding, 20000, 1000, nil, daoScript, true)
	originalScript := append([]byte(nil), unsigned.TxIn[0].SignatureScript...)
	_, err = manager.SignAnchorTx(unsigned, func(message []byte) ([]byte, error) {
		return ecdsa.Sign(feeKey, chainhash.HashB(message)).Serialize(), nil
	})
	require.Error(t, err, "the signer must belong to the invoice channel")
	require.Equal(t, originalScript, unsigned.TxIn[0].SignatureScript)
	client.height = 0
	active, err := manager.AnchorOutputsActive()
	require.NoError(t, err)
	require.False(t, active)
	client.height = 1
	active, err = manager.AnchorOutputsActive()
	require.NoError(t, err)
	require.True(t, active)
	client.height = -1
	_, err = manager.AnchorOutputsActive()
	require.Error(t, err, "an unavailable height must not fall back to a legacy invoice")
}
