package wallet

import (
	"errors"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/stretchr/testify/require"
)

type channelSigningPeer struct {
	NodeRPCClient
	calls   int
	request *wwire.SignRequest
}

func (c *channelSigningPeer) SendSigReq(request *wwire.SignRequest, _ []byte) ([][][]byte, error) {
	c.calls++
	c.request = request
	return nil, errors.New("selected signing peer")
}

func TestChannelSigningUsesConfiguredChannelPeerWithoutSwitchingServer(t *testing.T) {
	manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
	_, coreKey := btcec.PrivKeyFromBytes([]byte{2})
	_, bootKey := btcec.PrivKeyFromBytes([]byte{3})
	core, boot := &channelSigningPeer{}, &channelSigningPeer{}
	manager.serverNode = NewNode(core, "core", SERVER_NODE, coreKey, coreKey)
	manager.bootstrapNode = []*Node{NewNode(boot, "bootstrap", BOOTSTRAP_NODE, bootKey, bootKey)}
	current := manager.serverNode
	for _, spec := range []struct {
		name   string
		key    *btcec.PublicKey
		client *channelSigningPeer
	}{{"core", coreKey, core}, {"bootstrap", bootKey, boot}} {
		t.Run(spec.name, func(t *testing.T) {
			address, err := GetP2WSHaddress(manager.wallet.GetPaymentPubKey().SerializeCompressed(), spec.key.SerializeCompressed())
			require.NoError(t, err)
			witness, peer, err := manager.channelWitness(manager.wallet, address)
			require.NoError(t, err)
			require.Equal(t, spec.key.SerializeCompressed(), peer)
			before := spec.client.calls
			_, _, err = manager.reqRemoteSignAndBroadcast(manager.wallet, witness, peer, "unstake", address, nil, nil, nil, nil, nil)
			require.EqualError(t, err, "selected signing peer")
			require.Equal(t, before+1, spec.client.calls)
			require.Equal(t, address, spec.client.request.ChannelId)
			require.Equal(t, "unstake", spec.client.request.Reason)
			require.Same(t, current, manager.serverNode)
		})
	}
	_, unknown := btcec.PrivKeyFromBytes([]byte{4})
	address, err := GetP2WSHaddress(manager.wallet.GetPaymentPubKey().SerializeCompressed(), unknown.SerializeCompressed())
	require.NoError(t, err)
	_, _, err = manager.channelWitness(manager.wallet, address)
	require.Error(t, err)
	before := core.calls + boot.calls
	_, _, err = manager.reqRemoteSignAndBroadcast(manager.wallet, nil, unknown.SerializeCompressed(), "unstake", address, nil, nil, nil, nil, nil)
	require.Error(t, err)
	require.Equal(t, before, core.calls+boot.calls, "unknown peers must not fall back to another signer")
	require.Same(t, current, manager.serverNode)
}
