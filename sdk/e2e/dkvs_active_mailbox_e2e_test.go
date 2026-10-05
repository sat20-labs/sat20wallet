package e2e

import (
	"net"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Real CoreNode, production BindAccount and paid MessageManager delivery. A
// receiver removes current mailbox state without retaining deletion history.
func TestSDKDKVSActiveMailbox(t *testing.T) {
	f, _, _, _ := sdkDKVSReviewPaidFixture(t)
	// P2P/DKVS readiness precedes STP startup after the indexer catches up.
	// Wait for the real service listener before exercising mailbox RPCs.
	require.Eventually(t, func() bool {
		connection, err := net.DialTimeout("tcp", f.Network.Core.stpAddr, time.Second)
		if err != nil {
			return false
		}
		_ = connection.Close()
		return true
	}, 15*time.Second, 100*time.Millisecond, "CoreNode MessageService listener was not ready")
	manager, _ := newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
	require.NoError(t, manager.InitializeAccountManagement("123456"))
	require.NoError(t, manager.BindAccountToCurrentCoreNode())
	root := manager.GetWallet()
	account := dkvs.AccountID(root.GetPubKey().SerializeCompressed())
	message, err := manager.SendAccountDirectMessage("active-delete-self", wallet.AccountMessageKindGeneric, account, []byte("private self message"))
	require.NoError(t, err)
	key, err := dkvs.MailMsgKey(account, account, message.MessageID)
	require.NoError(t, err)
	client := dkvsClientForNode(t, f.Network.Core)
	before, err := client.GetRecord(key)
	require.NoError(t, err)
	require.NotEmpty(t, before.Value)

	t.Run("DeleteAndRepeatWithoutTombstone", func(t *testing.T) {
		require.NoError(t, manager.DeleteMailboxMessage(root, key))
		_, err := client.GetRecord(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		state, err := client.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		require.NoError(t, manager.DeleteMailboxMessage(root, key), "already absent is idempotent without a deleted-key floor")
	})
}
