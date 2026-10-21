package e2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

type sdkPWARegtestChannelPrepState string

const (
	sdkPWARegtestChannelNeedsOpening sdkPWARegtestChannelPrepState = "opening-required"
	sdkPWARegtestChannelReadyReused  sdkPWARegtestChannelPrepState = "ready-reused"
)

// This pure decision boundary deliberately has no cache or wallet mutation.
// The real preparation helper below owns only prerequisites; the browser case
// remains responsible for opening the channel through the actual UI.
func decideSDKPWARegtestChannelPreparation(feature, channelID string, found bool,
	status wallet.ChannelStatus) (string, sdkPWARegtestChannelPrepState, error) {
	if feature == "" || channelID == "" {
		return "", "", fmt.Errorf("feature and derived channel ID are required")
	}
	if !found {
		if feature == "l2-channel-open" {
			return channelID, sdkPWARegtestChannelNeedsOpening, nil
		}
		return "", "", fmt.Errorf("feature %s requires an existing READY channel", feature)
	}
	if status != wallet.CS_READY {
		return "", "", fmt.Errorf("channel %s has status %d; READY required", channelID, status)
	}
	return channelID, sdkPWARegtestChannelReadyReused, nil
}

// prepareSDKPWARegtestChannelPrerequisites observes B0's existing channel and
// prepares only the inputs needed by the already-defined browser case. It
// never opens, closes, deletes, or caches channel state; Opening stays a real
// UI action. A missing channel is accepted only for that first-opening case.
func prepareSDKPWARegtestChannelPrerequisites(t *testing.T, feature string,
	ordinary1, ordinary2 *wallet.Manager, mine func(string, int)) (channelID string, state sdkPWARegtestChannelPrepState) {
	t.Helper()
	require.NotNil(t, ordinary1)
	require.NotNil(t, ordinary2)

	derivedID, err := ordinary2.GetChannelAddress()
	require.NoError(t, err, "derive the fixed B0 channel ID")
	require.NotEmpty(t, derivedID)
	channel := ordinary2.GetCurrentChannel()
	if channel == nil {
		channel = ordinary2.GetChannel(derivedID)
	}
	server := ordinary2.GetServerNode()
	require.NotNil(t, server)
	client, ok := server.RPCClient().(*wallet.NodeClient)
	require.True(t, ok, "real channel preparation requires the actual SDK node client")
	var inventory struct {
		Code     int      `json:"code"`
		Msg      string   `json:"msg"`
		Channels []string `json:"channels"`
	}
	require.NoError(t, posPWAReadJSON(client.GetUrl("/info/channels").String(), &inventory))
	require.Zero(t, inventory.Code, inventory.Msg)
	remoteFound := false
	for _, id := range inventory.Channels {
		remoteFound = remoteFound || id == derivedID
	}
	if remoteFound {
		var peer struct {
			Code    int    `json:"code"`
			Msg     string `json:"msg"`
			Channel *struct {
				ChannelID string               `json:"channelId"`
				Status    wallet.ChannelStatus `json:"status"`
			} `json:"channel"`
		}
		require.NoError(t, posPWAReadJSON(client.GetUrl("/info/channel/"+derivedID).String(), &peer))
		require.Zero(t, peer.Code, peer.Msg)
		require.NotNil(t, peer.Channel)
		require.Equal(t, derivedID, peer.Channel.ChannelID)
		require.Equal(t, wallet.CS_READY, peer.Channel.Status, "the actual peer must be READY before the UI operation")
	}
	// Restore is for a missing local channel. A cached READY prerequisite must
	// not call the production rebuild API again at the same commit height;
	// the fresh PWA context independently restores its authoritative peer state.
	if remoteFound && channel == nil {
		require.NoError(t, ordinary2.SyncChannel("restore", server.RPCClient()),
			"reuse the SDK's authenticated peer synchronization before comparing the old local channel")
		channel = ordinary2.GetChannel(derivedID)
	}
	if !remoteFound {
		require.True(t, channel == nil || channel.Status == wallet.CS_CLOSED,
			"a locally active channel missing at its peer must not be replaced")
		// Historical ledger entries and archived local state are retained;
		// neither proves that the peer currently has an active channel.
		channel = nil
	}
	found, status := channel != nil, wallet.CS_UNKNOWN
	if found {
		require.Equal(t, derivedID, channel.ChannelId, "reuse only B0's existing channel")
		status = channel.Status
	}

	channelID, state, err = decideSDKPWARegtestChannelPreparation(feature, derivedID, found, status)
	require.NoError(t, err)
	if state == sdkPWARegtestChannelNeedsOpening {
		require.NotNil(t, mine, "missing opening prerequisites may need real L1 confirmation")
		prepareSDKPWARegtestCoreChannelCoins(t, ordinary1, mine)
		prepareSDKPWARegtestChannelOpeningCoins(t, ordinary1, ordinary2, 1_000_000, mine)
	}

	if strings.Contains(feature, "v2-asset-splice") {
		require.Equal(t, "32", os.Getenv("SATOSHINET_RPCTEST_POS_V2_HEIGHT"),
			"attach the already-activated POS32 environment; this helper does not change node configuration")
		require.Equal(t, sdkPWARegtestPOSV2ActivationHeight, wallet.GetChainParam_SatsNet().POSV2Height,
			"SDK and attached POS32 network must agree")
		active, activeErr := ordinary1.AnchorOutputsActive()
		require.NoError(t, activeErr)
		require.True(t, active, "the attached network must already have activated POS v2")
		prepareSDKPWARegtestChannelV2Asset(t, ordinary1, ordinary2, mine)
	}

	return channelID, state
}

func prepareSDKPWARegtestChannelV2Asset(t *testing.T, source, receiver *wallet.Manager,
	mine func(string, int)) {
	t.Helper()
	asset := wallet.ParseAssetString("ordx:f:pwapos")
	require.NotNil(t, asset)
	const required = int64(1200) // original send and both split outputs need this real balance

	receiverBalance := receiver.GetAssetBalance("", asset)
	receiverAmount := int64(0)
	if receiverBalance != nil {
		receiverAmount = receiverBalance.Int64()
	}
	if receiverAmount >= required {
		return
	}

	need := required - receiverAmount
	// This fixture uses the existing single-transaction N=1 ORDX sender.
	// A smaller prefix cannot form a dust-valid output; retain the accepted
	// small-batch boundary instead of adding a multi-transaction constructor.
	topUp := need
	if topUp < 330 {
		topUp = 330
	}
	ticker := source.GetTickerInfoV2(asset.String())
	if ticker == nil {
		// Reuse the existing SDK deploy/mint preparation; it deploys only when
		// absent and confirms the real inscription through the supplied miner.
		require.NotNil(t, mine, "real ORDX deployment/mint needs L1 confirmation")
		prepareSDKPWARegtestORDXSendAsset(t, source, mine)
		ticker = source.GetTickerInfoV2(asset.String())
		require.NotNil(t, ticker, "the real indexer must expose the prepared PWAPOS ticker")
	}
	require.Equal(t, 1, ticker.N, "PWAPOS preparation relies on its existing one-sat binding")

	sourceBalance := source.GetAssetBalance("", asset)
	sourceAmount := int64(0)
	if sourceBalance != nil {
		sourceAmount = sourceBalance.Int64()
	}
	if sourceAmount < topUp {
		if ticker.TotalMinted == "0" {
			require.NotNil(t, mine, "real ORDX mint needs L1 confirmation")
			prepareSDKPWARegtestORDXSendAsset(t, source, mine)
		} else {
			maxSupply, parseErr := strconv.ParseInt(ticker.MaxSupply, 10, 64)
			require.NoError(t, parseErr, "read the existing PWAPOS supply cap")
			minted, parseErr := strconv.ParseInt(ticker.TotalMinted, 10, 64)
			require.NoError(t, parseErr, "read existing confirmed PWAPOS issuance")
			limit, parseErr := strconv.ParseInt(ticker.Limit, 10, 64)
			require.NoError(t, parseErr, "read the existing SDK mint limit")
			require.GreaterOrEqual(t, maxSupply-minted, topUp-sourceAmount,
				"existing PWAPOS supply cap cannot fund the required B0 balance")
			require.GreaterOrEqual(t, limit, topUp-sourceAmount,
				"the existing SDK mint limit cannot satisfy this single required top-up")
			require.NotNil(t, mine, "real ORDX mint needs L1 confirmation")
			mint, mintErr := source.MintAsset_ordx(source.GetWallet().GetAddress(), ticker,
				topUp-sourceAmount, nil, 2)
			require.NoError(t, mintErr, "use the existing SDK mint path within the published cap")
			require.NotNil(t, mint)
			require.NotNil(t, mint.RevealTx)
			mine(source.GetWallet().GetAddressByIndex(1), 1)
			confirmed, confirmErr := source.GetIndexerClient().GetTxInfo(mint.RevealTx.TxID())
			require.NoError(t, confirmErr)
			require.NotNil(t, confirmed)
			require.GreaterOrEqual(t, confirmed.Confirmations, uint64(1),
				"the SDK mint must be indexed after a real L1 confirmation")
		}
		sourceBalance = source.GetAssetBalance("", asset)
		require.NotNil(t, sourceBalance)
		sourceAmount = sourceBalance.Int64()
	}
	require.GreaterOrEqual(t, sourceAmount, topUp,
		"A0's existing or SDK-minted PWAPOS must cover the dust-valid B0 top-up")

	require.NotNil(t, mine, "real PWAPOS transfer needs L1 confirmation")
	tx, sendErr := source.SendAssets(receiver.GetWallet().GetAddress(), asset.String(),
		strconv.FormatInt(topUp, 10), 2, nil)
	require.NoError(t, sendErr, "send the bounded dust-valid PWAPOS top-up from A0 to B0")
	require.NotNil(t, tx)
	mine(source.GetWallet().GetAddressByIndex(1), 1)
	confirmed, confirmErr := source.GetIndexerClient().GetTxInfo(tx.TxID())
	require.NoError(t, confirmErr)
	require.NotNil(t, confirmed)
	require.GreaterOrEqual(t, confirmed.Confirmations, uint64(1),
		"the A0-to-B0 PWAPOS transfer must be indexed after a real L1 confirmation")
	finalBalance := receiver.GetAssetBalance("", asset)
	require.NotNil(t, finalBalance)
	require.GreaterOrEqual(t, finalBalance.Int64(), required,
		"B0 must hold the original 1200-unit safety balance before asset splicing")
	require.Equal(t, receiverAmount+topUp, finalBalance.Int64(), "exact confirmed receiver delta")
	remaining := source.GetAssetBalance("", asset)
	remainingAmount := int64(0)
	if remaining != nil {
		remainingAmount = remaining.Int64()
	}
	require.Equal(t, sourceAmount-topUp, remainingAmount, "top-up must conserve both wallets' real PWAPOS")
	t.Logf("real PWAPOS preparation: deficit=%d top_up=%d recipient_balance=%d txid=%s",
		need, topUp, finalBalance.Int64(), tx.TxID())
}

func TestSDKPWARegtestChannelPreparationDecision(t *testing.T) {
	t.Run("existing READY channel is reused unchanged", func(t *testing.T) {
		const channelID = "regtest-channel-id"
		gotID, state, err := decideSDKPWARegtestChannelPreparation("l2-channel-splice", channelID, true, wallet.CS_READY)
		require.NoError(t, err)
		require.Equal(t, channelID, gotID)
		require.Equal(t, sdkPWARegtestChannelReadyReused, state)
	})

	t.Run("missing opening channel is left for the UI", func(t *testing.T) {
		const channelID = "derived-channel-id"
		gotID, state, err := decideSDKPWARegtestChannelPreparation("l2-channel-open", channelID, false, wallet.CS_UNKNOWN)
		require.NoError(t, err)
		require.Equal(t, channelID, gotID)
		require.Equal(t, sdkPWARegtestChannelNeedsOpening, state)
	})

	t.Run("missing non-opening channel is rejected", func(t *testing.T) {
		_, _, err := decideSDKPWARegtestChannelPreparation("l2-channel-v2-asset-splice", "derived-channel-id", false, wallet.CS_UNKNOWN)
		require.ErrorContains(t, err, "requires an existing READY channel")
	})

	t.Run("existing non-READY channel is rejected without replacement", func(t *testing.T) {
		const channelID = "existing-channel-id"
		gotID, state, err := decideSDKPWARegtestChannelPreparation("l2-channel-open", channelID, true, wallet.CS_ANCHOR_CONFIRMED)
		require.ErrorContains(t, err, "READY required")
		require.Empty(t, gotID)
		require.Empty(t, state)
	})

	t.Run("decision has no cached channel state", func(t *testing.T) {
		const channelID = "stable-derived-id"
		for range 2 {
			gotID, state, err := decideSDKPWARegtestChannelPreparation("l2-channel-open", channelID, false, wallet.CS_UNKNOWN)
			require.NoError(t, err)
			require.Equal(t, channelID, gotID)
			require.Equal(t, sdkPWARegtestChannelNeedsOpening, state)
		}
	})
}
