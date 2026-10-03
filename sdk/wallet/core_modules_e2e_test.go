package wallet

import (
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func TestSDKCoreModulesConnectedE2E(t *testing.T) {
	cfg := coreLoadE2EConfig(t)
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	chain := newCoreE2EChain()

	t.Run("dkvs_real_paid_cross_instance_prefix_and_CAS", func(t *testing.T) {
		coreDKVSE2E(t, cfg, chain)
	})
	sender := coreNewE2EManager(t, cfg, chain, coreE2ESenderMnemonic)
	receiver := coreNewE2EManager(t, cfg, chain, coreE2EReceiverMnemonic)
	senderMaterial := coreActivateE2E(t, sender, account.RecoveryMode2Of2)
	receiverMaterial := coreActivateE2E(t, receiver, account.RecoveryMode2Of2)
	rootID := sender.GetAccountManagementStatus().RootWalletID
	chain.fund(t, sender.GetWallet().GetAddress(), 16)
	chain.fund(t, receiver.GetWallet().GetAddress(), 6)

	var childID int64
	t.Run("backup_multiple_wallets_and_subaccounts_control", func(t *testing.T) {
		coreRequire(t, "create root subaccount", sender.EnsureAccount(rootID, 1, "Root savings", "did:root:1"))
		var err error
		childID, err = sender.ImportWallet(coreE2EChildMnemonic, coreE2EPassword)
		coreRequire(t, "add child wallet", err)
		coreRequire(t, "create child subaccounts", sender.EnsureAccount(childID, 2, "Child vault", "did:child:2"))
		coreRequire(t, "name child wallet", sender.UpdateWalletName(childID, "E2E child wallet"))
		coreRequire(t, "sync complete catalog", sender.SyncAccountManagementState(context.Background()))
		coreAssert(t, len(sender.GetWalletCatalog()) == 2, "multi-wallet fixture missing wallet")
		coreCheckpoint(t, "catalog_before_RGB_control", sender, cfg, chain, senderMaterial)
	})
	coreRequire(t, "select sender root", sender.SwitchWallet(rootID, coreE2EPassword))
	sender.SwitchAccount(0)

	// Exercise both transport lifecycles before adding other issuance schemas.
	// A UDA/IFA recovery defect must not prevent the NIA transport tests from
	// reaching their own checkpoints. Failing recovery checkpoints remain red.
	t.Run("invoice_transfer_every_paid_recovery_boundary", func(t *testing.T) {
		coreInvoiceTransferE2E(t, cfg, chain, sender, receiver, senderMaterial, receiverMaterial)
	})
	t.Run("direct_transfer_every_paid_recovery_boundary", func(t *testing.T) {
		coreDirectTransferE2E(t, cfg, chain, sender, receiver, senderMaterial, receiverMaterial)
	})

	t.Run("RGB11_asset_name_and_transcend_registration_descriptor", func(t *testing.T) {
		coreRGB11NamingE2E(t, cfg, chain, sender, senderMaterial)
	})

	t.Run("RGB_issuance_in_multiple_subaccounts", func(t *testing.T) {
		coreAssert(t, childID != 0, "child wallet fixture did not initialize")
		coreRequire(t, "select child wallet", sender.SwitchWallet(childID, coreE2EPassword))
		defer func() { _ = sender.SwitchWallet(rootID, coreE2EPassword); sender.SwitchAccount(0) }()
		sender.SwitchAccount(1)
		chain.fund(t, sender.GetWallet().GetAddress(), 5)
		uda, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
			Schema: "UDA", Ticker: "E2EU", Name: "E2E unique asset",
		})
		coreRequire(t, "issue native UDA in child account 1", err)
		coreAssert(t, uda.Projected == 1, "UDA must project one actual allocation")
		coreCheckpoint(t, "UDA_child_account_DKVS_recovery", sender, cfg, chain, senderMaterial)
		sender.SwitchAccount(2)
		chain.fund(t, sender.GetWallet().GetAddress(), 5)
		ifa, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
			Schema: "IFA", Ticker: "E2EI", Name: "E2E inflatable asset",
			Amounts: []uint64{1000}, InflationAmounts: []uint64{9000},
		})
		coreRequire(t, "issue native IFA in child account 2", err)
		coreAssert(t, ifa.Projected == 2, "IFA ownership and inflation rights must both be projected")
		coreCheckpoint(t, "IFA_multiwallet_DKVS_recovery", sender, cfg, chain, senderMaterial)
	})

	t.Run("guardian_two_of_three_and_wrong_knowledge", func(t *testing.T) {
		material := coreActivateE2E(t, sender, account.RecoveryMode2Of3)
		coreCheckpoint(t, "guardian_and_knowledge_restore_catalog_and_RGB", sender, cfg, chain, material)
		blank := coreNewE2EManager(t, cfg, chain, "")
		pkg, err := blank.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
		coreRequire(t, "load package for negative test", err)
		_, err = account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle,
			[]account.AnswerAttempt{{QuestionID: "one", Answer: "incorrect"}, {QuestionID: "two", Answer: "incorrect"}})
		coreAssert(t, err != nil && len(blank.GetWalletCatalog()) == 0, "wrong knowledge changed empty wallet state")
	})
}
