package e2e

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

const messageTopicMemberMnemonic = "comfort very add tuition senior run eight snap burst appear exile dutch"

func TestRealSatoshiNetMessageTopicSDKToCore(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	f := newTemplateFixtureWithArgs(t, map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)

	gas := contractcommon.GetGasAssetName()
	ownerActor := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	memberActor := newDKVSKeyPathActor(t, keyFromMnemonic(t, messageTopicMemberMnemonic, 0))
	require.Equal(t, defaults.AutopayDeployer, ownerActor.Address)

	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{ownerActor, ownerActor, memberActor, memberActor})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName], defaults.AutopayFeeAssetName,
		[]int64{5000, 5000}, []int64{10000, 10000}, []*dkvsKeyPathActor{ownerActor, memberActor})

	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	deployAssets := txAsset(gas, 290000)
	deployAssets = append(deployAssets, txAsset(defaults.AutopayFeeAssetName, 5000)...)
	deploy, contractAddress := buildDKVSKeyPathTemplateDeploy(t, ownerActor,
		contractcommon.TemplateAutopay, content, ownerActor.Address, defaults.AutopayDeployNonce,
		[]dkvsPrevOut{gasOuts[0], feeOuts[0]}, wire.TxOut{Value: 10000, Assets: deployAssets})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)

	fundMember := buildDKVSKeyPathTemplateDefaultInvoke(t, memberActor, contractAddress,
		[]dkvsPrevOut{feeOuts[1]}, wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 5000)})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{fundMember}, 0)
	heartbeatOwner := buildDKVSKeyPathAssetTransfer(t, ownerActor, gasOuts[1], gas, 290000, 9000, ownerActor)
	heartbeatMember := buildDKVSKeyPathAssetTransfer(t, memberActor, gasOuts[3], gas, 290000, 9000, memberActor)
	f.Network.sendManyAndMine(t, []*wire.MsgTx{heartbeatOwner, heartbeatMember}, 0)

	state := fetchTemplateAutopayView(t, f.Network.Core, contractAddress.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
	require.Equal(t, templateruntime.AutopayStatusActive, state.Delegates[ownerActor.Address].Status)
	require.Equal(t, templateruntime.AutopayStatusActive, state.Delegates[memberActor.Address].Status)

	owner, _ := newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
	member, _ := newWalletManagerForNode(t, f.Network.Core, messageTopicMemberMnemonic)
	require.NoError(t, owner.InitializeAccountManagement("123456"))
	require.NoError(t, member.InitializeAccountManagement("123456"))
	require.NoError(t, owner.BindAccountToCurrentCoreNode())
	require.NoError(t, member.BindAccountToCurrentCoreNode())

	ownerID := dkvsindexer.AccountID(owner.GetWallet().GetPubKey().SerializeCompressed())
	memberID := dkvsindexer.AccountID(member.GetWallet().GetPubKey().SerializeCompressed())
	snapshot, err := owner.CreateMessageTopic("developers", "Developers", 16)
	require.NoError(t, err)
	require.Equal(t, ownerID, snapshot.Meta.OwnerAccount)
	require.Equal(t, uint64(1), snapshot.State.KeySeq)
	require.Equal(t, uint32(1), snapshot.State.MemberCount)

	require.NoError(t, member.RequestMessageTopicJoin("developers", snapshot.Meta.ServiceCoreNode))
	pending, err := owner.GetMessageTopicState("developers")
	require.NoError(t, err)
	require.Equal(t, "PENDING_JOIN", topicMemberStatusE2E(t, pending, memberID))

	commit, err := owner.ApproveMessageTopicJoin("developers", memberID)
	require.NoError(t, err)
	require.Equal(t, uint64(2), commit.NewKeySeq)
	joined, err := owner.GetMessageTopicState("developers")
	require.NoError(t, err)
	require.Equal(t, uint32(2), joined.State.MemberCount)
	require.Equal(t, "ACTIVE", topicMemberStatusE2E(t, joined, memberID))
	// Explicit topic-key sync checks the mailbox generation and downloads a new
	// snapshot only when the CoreNode reports that mailbox changed.
	acceptedKeys, err := member.SyncMessageTopicKeys("developers")
	require.NoError(t, err)
	require.GreaterOrEqual(t, acceptedKeys, 1)

	ownerPublish, err := owner.PublishMessageTopic("owner-topic-1", "developers", snapshot.Meta.ServiceCoreNode, []byte("hello from owner"))
	require.NoError(t, err)
	require.Equal(t, uint64(0), ownerPublish.SenderMsgID)
	memberMessages, _, err := member.ReadMessageTopicMessages("developers", 0, 10)
	require.NoError(t, err)
	require.Len(t, memberMessages, 1)
	require.Equal(t, "hello from owner", string(memberMessages[0].Plaintext))

	memberPublish, err := member.PublishMessageTopic("member-topic-1", "developers", snapshot.Meta.ServiceCoreNode, []byte("hello from member"))
	require.NoError(t, err)
	require.Equal(t, uint64(0), memberPublish.SenderMsgID)
	ownerMessages, _, err := owner.ReadMessageTopicMessages("developers", 0, 10)
	require.NoError(t, err)
	require.Len(t, ownerMessages, 1)
	require.Equal(t, "hello from member", string(ownerMessages[0].Plaintext))

	require.NoError(t, member.RequestMessageTopicLeave("developers", snapshot.Meta.ServiceCoreNode))
	leaving, err := owner.GetMessageTopicState("developers")
	require.NoError(t, err)
	require.Equal(t, "PENDING_LEAVE", topicMemberStatusE2E(t, leaving, memberID))
	leaveCommit, err := owner.FinalizeMessageTopicLeave("developers", memberID)
	require.NoError(t, err)
	require.Equal(t, uint64(3), leaveCommit.NewKeySeq)
	left, err := owner.GetMessageTopicState("developers")
	require.NoError(t, err)
	require.Equal(t, uint32(1), left.State.MemberCount)
	require.Equal(t, "LEFT", topicMemberStatusE2E(t, left, memberID))

	memberCrypto, err := wallet.NewTopicCryptoManager(member, member.GetWallet().(*wallet.InternalWallet))
	require.NoError(t, err)
	_, err = memberCrypto.LoadTopicKey("developers", 3)
	require.Error(t, err)
	key3, err := dkvsindexer.MailTopicKeyKey(memberID, "developers", "3")
	require.NoError(t, err)
	requireDKVSAbsent(t, f.Network.Core, key3)
}

func topicMemberStatusE2E(t *testing.T, snapshot *wire.TopicServiceSnapshot, accountID string) string {
	t.Helper()
	require.NotNil(t, snapshot)
	for _, member := range snapshot.Members {
		if member.AccountID == accountID {
			return member.Status
		}
	}
	t.Fatalf("topic member %s not found", accountID)
	return ""
}
