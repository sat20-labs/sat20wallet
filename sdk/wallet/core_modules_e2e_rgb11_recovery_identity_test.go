package wallet

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// coreRGB11RecoveryIdentityAndBatchE2E adds focused CoreNode cold-recovery
// coverage for zero-balance contract intent, contract-derived asset identity,
// a real multi-output batch, and same-root self-send. The aggregate runner owns
// invoking this helper; this file does not define another E2E entry point.
func coreRGB11RecoveryIdentityAndBatchE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, sender, receiver *Manager,
	senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	t.Run("R03_zero_balance_contract_alias_cold_restore_receive_and_spend", func(t *testing.T) {
		coreRGB11ZeroBalanceAliasColdRestoreE2E(t, cfg, chain,
			sender, receiver, senderMaterial)
	})
	t.Run("R17_same_ticker_distinct_contracts_and_forged_cache", func(t *testing.T) {
		coreRGB11ContractIdentityAndCacheE2E(t, cfg, chain, sender, senderMaterial)
	})
	t.Run("R19_multi_output_batch_ack_cold_restore_and_respend", func(t *testing.T) {
		coreRGB11BatchColdRestoreE2E(t, cfg, chain, senderMaterial, receiverMaterial)
	})
	t.Run("R19_self_send_ack_cold_restore_and_respend", func(t *testing.T) {
		coreRGB11SelfSendColdRestoreE2E(t, cfg, chain, senderMaterial, receiverMaterial)
	})
}

func coreRGB11ZeroBalanceAliasColdRestoreE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, sender, receiver *Manager,
	senderMaterial *coreRecoveryMaterial) {
	t.Helper()
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EZ", Name: "E2E zero-balance recovery asset", Amounts: []uint64{333},
	})
	coreRequire(t, "issue zero-balance recovery NIA", err)
	const alias = "e2e-zero-balance-alias"
	coreRequire(t, "set local alias before full transfer",
		sender.SetRGB11LocalAssetName(issued.ContractID, alias))
	_, err = sender.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish issuer receive capability", err)
	_, err = receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish counterparty receive capability", err)

	beforeFullSend := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, sender, receiver,
		issued.AssetName, "333", beforeFullSend)
	coreAssert(t, chain.broadcastCount() == beforeFullSend+1,
		"zero-balance setup did not broadcast its full transfer exactly once")

	// This manager has a new database and imports only the account's confirmed
	// recovery package. The contract and alias are still user intent even though
	// the issuer no longer owns an allocation.
	coldIssuer := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, coldIssuer)
	_, err = coldIssuer.RefreshRGB11State(context.Background())
	coreRequire(t, "refresh zero-balance cold issuer", err)
	zeroBalance, err := coldIssuer.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read zero-balance cold contract", err)
	coreAssert(t, coreRGB11BalanceIsZero(zeroBalance),
		"cold restore resurrected the issuer's consumed allocation")
	info := coreFindRGB11ContractInfo(t, coldIssuer, issued.ContractID)
	coreAssert(t, info.AssetKey == issued.AssetName.String() && info.Ticker == alias,
		"cold restore omitted the zero-balance contract identity or local alias")
	_, err = coldIssuer.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish cold issuer receive capability", err)

	beforeReturn := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, receiver, coldIssuer,
		issued.AssetName, "200", beforeReturn)
	coldBalance, err := coldIssuer.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read allocation received by cold issuer", err)
	receiverBalance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read counterparty after zero-balance return", err)
	coreAssert(t, coldBalance != nil && coldBalance.Value.Uint64() == 200 &&
		receiverBalance != nil && receiverBalance.Value.Uint64() == 133,
		"zero-balance cold issuer did not receive the real allocation")

	beforeRespend := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, coldIssuer, receiver,
		issued.AssetName, "100", beforeRespend)
	coldBalance, err = coldIssuer.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold issuer after recovered spend", err)
	receiverBalance, err = receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read receiver after recovered spend", err)
	coreAssert(t, coldBalance != nil && coldBalance.Value.Uint64() == 100 &&
		receiverBalance != nil && receiverBalance.Value.Uint64() == 233,
		"cold-restored contract could not spend a newly received allocation")
}

func coreRGB11ContractIdentityAndCacheE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, sender *Manager, senderMaterial *coreRecoveryMaterial) {
	t.Helper()
	first, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EDUP", Name: "E2E duplicate ticker first contract", Amounts: []uint64{111},
	})
	coreRequire(t, "issue first duplicate-ticker contract", err)
	second, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EDUP", Name: "E2E duplicate ticker second contract", Amounts: []uint64{222},
	})
	coreRequire(t, "issue second duplicate-ticker contract", err)
	coreAssert(t, first.ContractID != second.ContractID && first.AssetName != second.AssetName,
		"same ticker issuance reused a contract-derived asset identity")

	cold := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, cold)
	_, err = cold.RefreshRGB11State(context.Background())
	coreRequire(t, "refresh duplicate-ticker cold restore", err)
	coreAssertRGB11IdentityBalance(t, cold, first.ContractID, first.AssetName, 111)
	coreAssertRGB11IdentityBalance(t, cold, second.ContractID, second.AssetName, 222)

	// Export a structurally valid but false amount cache from an isolated copy,
	// then ask another cold database to import it against the real Core evidence.
	// The selected import must revalidate the original RGB consignment with the
	// local chain-evidence provider, fail closed on the forged cache, and retain
	// the prior real balances. CoreNode stores the account payload; it does not
	// attest to allocation contents.
	forger := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, forger)
	_, err = forger.RefreshRGB11State(context.Background())
	coreRequire(t, "refresh forged-cache fixture", err)
	coreRequire(t, "seed self-consistent forged allocation cache",
		coreForgeRGB11LocalAmountCache(t, forger, first.AssetName))
	forgerCatalog, err := forger.accountManagedDataCatalog()
	coreRequire(t, "read forged-cache account catalog", err)
	forgerProvider := &rgb11AccountManagedDataProvider{owner: forger}
	forgedPayloads, err := forgerProvider.Export(forgerCatalog)
	coreRequire(t, "export forged-cache recovery closure", err)
	coldCatalog, err := cold.accountManagedDataCatalog()
	coreRequire(t, "read cold target catalog for forged-cache import", err)
	coldProvider := &rgb11AccountManagedDataProvider{owner: cold}
	importErr := coldProvider.Import(coldCatalog, forgedPayloads)
	coreAssert(t, importErr != nil,
		"cold import accepted a cached amount that disagrees with the original consignment and local validation")
	coreAssertRGB11IdentityBalance(t, cold, first.ContractID, first.AssetName, 111)
	coreAssertRGB11IdentityBalance(t, cold, second.ContractID, second.AssetName, 222)
}

func coreForgeRGB11LocalAmountCache(t *testing.T, manager *Manager,
	assetName indexer.AssetName) error {
	t.Helper()
	proofs, err := manager.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return err
	}
	var proof *rgb11wallet.AllocationProof
	for _, candidate := range proofs {
		if candidate != nil && candidate.AssetName == assetName && rgb11ProofIsCurrent(candidate) {
			proof = candidate
			break
		}
	}
	if proof == nil {
		return rgb11wallet.ErrInvalidProof
	}
	output, err := manager.rgbManager.projectionStore.LoadOutput(proof.OutPoint)
	if err != nil {
		return err
	}
	asset := output.GetAsset(&assetName)
	if asset == nil || asset.Value == nil || !asset.Value.IsUint64() || asset.Value.Uint64() == ^uint64(0) {
		return rgb11wallet.ErrProjectionMismatch
	}
	assetInfo, err := output.Assets.Find(&assetName)
	if err != nil {
		return err
	}
	receipt, err := manager.rgbManager.projectionStore.LoadValidationReceipt(proof.ConsignmentHash)
	if err != nil {
		return err
	}
	receipt.Allocations = append([]rgb11wallet.ValidatedAllocation(nil), receipt.Allocations...)
	allocationIndex := -1
	for i := range receipt.Allocations {
		if receipt.Allocations[i].OutPoint == proof.OutPoint && receipt.Allocations[i].AssetName == assetName {
			allocationIndex = i
			break
		}
	}
	if allocationIndex < 0 {
		return rgb11wallet.ErrValidationReceipt
	}
	forgedAmount := indexer.NewDecimalWithScale(int64(asset.Value.Uint64()+1), asset.Precision)
	receipt.Allocations[allocationIndex].Amount = *forgedAmount.Clone()
	raw, err := manager.rgbManager.projectionStore.LoadObject(proof.ConsignmentHash)
	if err != nil {
		return err
	}
	if err := manager.rgbManager.projectionStore.DiscardValidatedObject(proof.ConsignmentHash); err != nil {
		return err
	}
	storedReceipt, err := manager.rgbManager.projectionStore.ValidateAndStoreConsignment(
		context.Background(), rgb11ProjectionReceiptValidator{receipt: receipt},
		manager.rgbManager.evidence, raw)
	if err != nil {
		return err
	}
	receiptHash, err := storedReceipt.Hash()
	if err != nil {
		return err
	}
	forgedOutput := output.Clone()
	forgedProof := *proof
	forgedProof.ValidationHash = receiptHash
	return manager.rgbManager.projectionStore.CommitProjection(forgedOutput, &indexer.AssetInfo{
		Name: assetName, Amount: *forgedAmount.Clone(), BindingSat: assetInfo.BindingSat,
	}, &forgedProof)
}

func coreRGB11BatchColdRestoreE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	// Keep this long-lived public API goroutine away from the aggregate's shared
	// managers. If ACK polling fails and the API continues until its own timeout,
	// only these child-scoped databases can be affected.
	sender := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, sender)
	receiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, receiver)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2EB", Name: "E2E multi-output batch asset", Amounts: []uint64{600},
	})
	coreRequire(t, "issue multi-output batch asset", err)
	_, err = sender.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish batch sender receive capability", err)
	_, err = receiver.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish batch receiver capability", err)
	tickerInfo := coreFindRGB11ContractInfo(t, sender, issued.ContractID)
	assetName := issued.AssetName
	destinations := []*SendAssetInfo{
		{Address: receiver.GetWallet().GetAddress(), AssetName: &assetName,
			AssetAmt: indexer.NewDecimalWithScale(100, int(tickerInfo.Divisibility))},
		{Address: receiver.GetWallet().GetAddress(), AssetName: &assetName,
			AssetAmt: indexer.NewDecimalWithScale(200, int(tickerInfo.Divisibility))},
	}
	beforeBroadcast := chain.broadcastCount()
	type sendResult struct {
		txid string
		fee  int64
		err  error
	}
	sendDone := make(chan sendResult, 1)
	workerDone := make(chan struct{})
	// BatchSendAssetsV3 owns a two-minute internal context. Join that bounded
	// worker on every fatal/early return before coreRestoreE2E's manager cleanup
	// closes the child DBs. The progress deadline below stays short; cleanup may
	// wait for the API's own cancellation bound.
	defer func() {
		timer := time.NewTimer(2*time.Minute + 15*time.Second)
		defer timer.Stop()
		select {
		case <-workerDone:
		case <-timer.C:
			t.Errorf("core-e2e: public batch worker exceeded its two-minute API context")
			<-workerDone
		}
	}()
	go func() {
		defer close(workerDone)
		txid, fee, sendErr := sender.BatchSendAssetsV3(destinations,
			issued.AssetName.String(), 2, nil, "", false)
		sendDone <- sendResult{txid: txid, fee: fee, err: sendErr}
	}()

	// The sender's existing public batch API waits for every persisted receive
	// ACK. Drive the real receiver mailbox until both outputs are accepted.
	var staged []*rgb11wallet.TransferState
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case early := <-sendDone:
			coreRequire(t, "continue public batch send while receiver ACKs arrive", early.err)
			t.Fatal("core-e2e: public batch send completed before every recipient ACK")
		default:
		}
		result, syncErr := receiver.SyncConfiguredRGB11AddressMailbox(context.Background(),
			dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{})
		coreRequire(t, "accept CoreNode batch consignments and persist ACKs", syncErr)
		senderState, err := sender.GetRGB11State()
		coreRequire(t, "read current batch output identities", err)
		current := make(map[string]*rgb11wallet.TransferState)
		currentIDs := make(map[string]bool)
		for _, transfer := range senderState.Transfers {
			if transfer != nil && transfer.Direction == "send" && transfer.Asset.Name == issued.AssetName {
				coreAssert(t, transfer.TransferID != "" && transfer.WitnessTxID != "" && transfer.ConsignmentHash != "",
					"current batch output omitted its transfer identity")
				coreAssert(t, !currentIDs[transfer.TransferID], "current batch has duplicate output identities")
				current[transfer.TransferID], currentIDs[transfer.TransferID] = transfer, true
			}
		}
		coreAssertRGB11MailboxErrors(t, receiver, currentIDs, result)
		state, err := receiver.GetRGB11State()
		coreRequire(t, "read exact receiver batch ACK states", err)
		staged = nil
		seen := make(map[string]bool)
		for _, transfer := range state.Transfers {
			if transfer == nil || transfer.Direction != "receive" {
				continue
			}
			expected := current[transfer.TransferID]
			if expected == nil {
				continue
			}
			coreAssert(t, !seen[transfer.TransferID], "batch output has duplicate receive lifecycles")
			seen[transfer.TransferID] = true
			coreAssert(t, transfer.Asset.Name == issued.AssetName && transfer.WitnessTxID == expected.WitnessTxID &&
				transfer.ConsignmentHash == expected.ConsignmentHash && len(expected.OutputOutPoints) > 0 &&
				len(transfer.OutputOutPoints) == 1 && transfer.OutputOutPoints[0] == expected.OutputOutPoints[0],
				"receiver batch ACK is not bound to its exact sender output")
			if transfer.DeliveryAcknowledged && transfer.AckStatus == "ack-sent" {
				staged = append(staged, transfer)
			}
		}
		coreAssert(t, len(current) <= len(destinations) && len(staged) <= len(destinations),
			"batch produced extra send or receive output lifecycles")
		if len(current) == len(destinations) && len(staged) == len(destinations) {
			break
		}
		coreAssert(t, chain.broadcastCount() == beforeBroadcast,
			"sender broadcast a multi-output transaction before every recipient ACK")
		time.Sleep(100 * time.Millisecond)
	}
	coreAssert(t, len(staged) == len(destinations),
		"receiver did not persist one exact ACK per batch output before the bounded wait")
	for _, transfer := range staged {
		coreAssertRGB11ACKRecoveryPublished(t, receiver, &RGB11PreparedTransfer{State: transfer, TxID: transfer.WitnessTxID})
	}

	var sent sendResult
	select {
	case sent = <-sendDone:
	case <-time.After(45 * time.Second):
		t.Fatal("core-e2e: public batch send did not finish after both ACKs")
	}
	coreRequire(t, "complete public two-output RGB11 batch send", sent.err)
	coreAssert(t, sent.txid != "" && sent.fee > 0,
		"public batch API returned no complete witness transaction identity")
	coreAssert(t, chain.broadcastCount() == beforeBroadcast+1,
		"two ACKed recipients did not produce exactly one batch transaction broadcast")
	for _, transfer := range staged {
		coreAssert(t, transfer.WitnessTxID == sent.txid,
			"receiver ACK lifecycle was not bound to the returned full batch transaction")
	}
	chain.confirm(sent.txid)
	_, err = sender.RefreshRGB11State(context.Background())
	coreRequire(t, "settle public RGB11 batch sender", err)
	_, err = receiver.RefreshRGB11State(context.Background())
	coreRequire(t, "settle public RGB11 batch receiver", err)
	senderBalance, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read sender after multi-output batch", err)
	receiverBalance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read receiver after multi-output batch", err)
	coreAssert(t, senderBalance != nil && senderBalance.Value.Uint64() == 300 &&
		receiverBalance != nil && receiverBalance.Value.Uint64() == 300,
		"confirmed batch did not conserve both recipient allocations and sender change")

	coldReceiver := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, coldReceiver)
	_, err = coldReceiver.RefreshRGB11State(context.Background())
	coreRequire(t, "refresh batch receiver after cold restore", err)
	recoveredBalance, err := coldReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold-restored batch allocation", err)
	coreAssert(t, recoveredBalance != nil && recoveredBalance.Value.Uint64() == 300,
		"cold restore lost or duplicated the two acknowledged batch outputs")

	beforeRespend := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, coldReceiver, sender,
		issued.AssetName, "50", beforeRespend)
	recoveredBalance, err = coldReceiver.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold batch receiver after respend", err)
	senderBalance, err = sender.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read sender after cold batch respend", err)
	coreAssert(t, recoveredBalance != nil && recoveredBalance.Value.Uint64() == 250 &&
		senderBalance != nil && senderBalance.Value.Uint64() == 350,
		"cold-restored batch allocations could not be spent through the real send path")
}

func coreRGB11SelfSendColdRestoreE2E(t *testing.T, cfg coreE2EConfig,
	chain *coreE2EChain, senderMaterial, receiverMaterial *coreRecoveryMaterial) {
	t.Helper()
	// Use isolated databases for both roots. All send/receive stages stay on one
	// public Manager so this exercises its real account gate and mailbox path.
	self := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, self)
	otherRoot := coreRestoreE2E(t, cfg, chain, receiverMaterial)
	coreSynchronizeRestoredAccount(t, otherRoot)

	issued, err := self.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "E2ESS", Name: "E2E self-send recovery asset", Amounts: []uint64{300},
	})
	coreRequire(t, "issue self-send recovery asset", err)
	_, err = self.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish self-send receive capability", err)
	contractExport, err := self.ExportRGB11Contract(issued.ContractID)
	coreRequire(t, "export self-send contract for second root", err)
	contract, err := base64.StdEncoding.DecodeString(contractExport.ContractConsignmentBase64)
	coreRequire(t, "decode self-send contract for second root", err)
	_, err = otherRoot.ImportRGB11ContractFile(context.Background(), contract)
	coreRequire(t, "import self-send contract into second root", err)
	_, err = otherRoot.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	coreRequire(t, "publish second-root receive capability", err)
	coreAssert(t, self.GetWallet().GetAddress() != otherRoot.GetWallet().GetAddress(),
		"self-send respend destination is not an independent root")

	beforeBroadcast := chain.broadcastCount()
	prepared, _, err := self.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: self.GetWallet().GetAddress(), AssetName: issued.AssetName,
		AmountRaw: "100", FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	coreRequire(t, "prepare public RGB11 self-send", err)
	coreAssert(t, prepared != nil && prepared.State != nil && prepared.State.TransferID != "",
		"public self-send omitted its durable operation identity")
	coreAssert(t, chain.broadcastCount() == beforeBroadcast,
		"public self-send broadcast before its own receive ACK")
	delivery, err := self.DeliverAndBroadcastConfiguredRGB11AddressTransfer(
		prepared.State.TransferID, RGB11AddressDeliveryOptions{})
	coreRequire(t, "deliver public RGB11 self-send", err)
	coreAssert(t, delivery.AwaitingACK && !delivery.Broadcast &&
		chain.broadcastCount() == beforeBroadcast,
		"public self-send crossed the ACK barrier during delivery")

	coreWaitRGB11MailboxACK(t, chain, self, prepared, beforeBroadcast)
	coreAssertRGB11ACKRecoveryPublished(t, self, prepared)
	coreAssert(t, chain.broadcastCount() == beforeBroadcast,
		"self-send ACK publication broadcast before sender consumed the ACK")
	coreWaitRGB11MailboxBroadcast(t, chain, self, prepared, beforeBroadcast)

	chain.confirm(prepared.TxID)
	_, err = self.RefreshRGB11State(context.Background())
	coreRequire(t, "settle both roles of RGB11 self-send", err)
	coreRequire(t, "publish settled self-send recovery package",
		self.SyncAccountManagementState(context.Background()))
	coreAssertSelfSendRecoveryRolesAndLocks(t, self, issued.AssetName, prepared.State.TransferID)
	selfBalance, err := self.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read self-send balance after settlement", err)
	coreAssert(t, selfBalance != nil && selfBalance.Value.Uint64() == 300,
		"self-send did not conserve the sender's total allocation")

	coldSelf := coreRestoreE2E(t, cfg, chain, senderMaterial)
	coreSynchronizeRestoredAccount(t, coldSelf)
	_, err = coldSelf.RefreshRGB11State(context.Background())
	coreRequire(t, "refresh cold-restored self-send root", err)
	coreAssertSelfSendRecoveryRolesAndLocks(t, coldSelf, issued.AssetName, prepared.State.TransferID)
	coldBalance, err := coldSelf.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold-restored self-send balance", err)
	coreAssert(t, coldBalance != nil && coldBalance.Value.Uint64() == 300,
		"cold self-send recovery lost or duplicated the two same-root roles")

	beforeRespend := chain.broadcastCount()
	coreSendRGB11AddressAmountAndSettle(t, chain, coldSelf, otherRoot,
		issued.AssetName, "50", beforeRespend)
	coldBalance, err = coldSelf.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read cold self-send root after real respend", err)
	otherBalance, err := otherRoot.GetRGB11AssetBalance(&issued.AssetName)
	coreRequire(t, "read second root after cold self-send respend", err)
	coreAssert(t, coldBalance != nil && coldBalance.Value.Uint64() == 250 &&
		otherBalance != nil && otherBalance.Value.Uint64() == 50,
		"cold-restored self-send allocation could not be spent through the public ACK path")
}

func coreAssertSelfSendRecoveryRolesAndLocks(t *testing.T, manager *Manager,
	assetName indexer.AssetName, transferID string) {
	t.Helper()
	state, err := manager.GetRGB11State()
	coreRequire(t, "read self-send role and proof state", err)
	foundSend, foundReceive := false, false
	for _, transfer := range state.Transfers {
		if transfer == nil || transfer.TransferID != transferID {
			continue
		}
		foundSend = foundSend || transfer.Direction == "send"
		foundReceive = foundReceive || transfer.Direction == "receive"
	}
	coreAssert(t, foundSend && foundReceive,
		"same-root recovery did not retain both send and receive lifecycle roles")
	locks := manager.GetUtxoLocker().GetLockedUtxoList()
	current := 0
	for _, proof := range state.Proofs {
		if proof == nil || proof.AssetName != assetName || !rgb11ProofIsCurrent(proof) {
			continue
		}
		current++
		lock := locks[proof.OutPoint]
		coreAssert(t, lock != nil && lock.Reason == rgb11wallet.LockReasonRGB,
			"self-send current carrier lost its RGB lock after restore")
	}
	coreAssert(t, current >= 2,
		"self-send did not recover both real recipient and change allocations")
}

func coreFindRGB11ContractInfo(t *testing.T, manager *Manager,
	contractID string) *RGB11TickerInfo {
	t.Helper()
	state, err := manager.GetRGB11State()
	coreRequire(t, "read RGB11 contract identity", err)
	if state != nil {
		for _, info := range state.TickerInfos {
			if info != nil && info.ContractID == contractID {
				return info
			}
		}
	}
	t.Fatalf("core-e2e: no RGB11 ticker information for selected contract")
	return nil
}

func coreAssertRGB11IdentityBalance(t *testing.T, manager *Manager,
	contractID string, assetName indexer.AssetName, expected uint64) {
	t.Helper()
	info := coreFindRGB11ContractInfo(t, manager, contractID)
	coreAssert(t, info.AssetKey == assetName.String(),
		"ticker metadata changed the immutable contract-derived asset identity")
	balance, err := manager.GetRGB11AssetBalance(&assetName)
	coreRequire(t, "read contract-derived RGB11 allocation balance", err)
	coreAssert(t, balance != nil && balance.Value.Uint64() == expected,
		"recovery used ticker metadata instead of the matching contract allocation")
}
