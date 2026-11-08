package wallet

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func newRGB11AccountScopeRecoveryManager(t *testing.T, remote *rgb11MemoryDKVSHTTP,
	chain *coreE2EChain) *Manager {
	t.Helper()
	database := indexerdb.NewKVDB(t.TempDir())
	manager := coreNewE2EManagerWithDB(t, coreE2EConfig{
		Core: AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
	}, chain, database, "", remote)
	useRGB11TestCoreAuthority(t, manager)
	client := newRGB11MessageNodeClient(remote)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	t.Cleanup(func() {
		manager.Close()
		if manager.db != nil {
			_ = manager.db.Close()
			manager.db = nil
		}
	})
	return manager
}

func activateRGB11AccountScopeRecovery(t *testing.T, manager *Manager, mnemonic string) (
	*account.RecoveryPackage, *AccountStorageAuthorization, []byte) {
	t.Helper()
	if _, err := manager.ImportWallet(mnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	pkg, auth, secret := rereviewRecoveryPackage(t, manager)
	if err := manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, ""); err != nil {
		t.Fatal(err)
	}
	return pkg, auth, secret
}

// Recovery uses the authenticated account state/blob read through the public
// SDK after the original database is closed. Neither an original RGB snapshot
// nor a copied wallet/ticker cache is supplied to any restored manager.
func TestRGB11AccountRecoveryTwoAssetScopesAndScopeBinding(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	ctx := context.Background()
	remote := newRGB11MemoryDKVSHTTP()
	chain := newCoreE2EChain()
	source := newRGB11AccountScopeRecoveryManager(t, remote, chain)
	pkg, auth, secret := activateRGB11AccountScopeRecovery(t, source, coreE2ESenderMnemonic)
	rootID := source.GetAccountManagementStatus().RootWalletID
	if err := source.EnsureAccount(rootID, 1, "Second RGB scope", ""); err != nil {
		t.Fatal(err)
	}
	assets := make([]*RGB11IssueResult, 2)
	for index := uint32(0); index < 2; index++ {
		if err := source.SwitchAccount(index); err != nil {
			t.Fatal(err)
		}
		chain.fund(t, source.GetWallet().GetAddress(), 6)
		issued, err := source.IssueRGB11Asset(ctx, RGB11IssueRequest{
			Schema: "NIA", Ticker: fmt.Sprintf("SCOPE%d", index),
			Name: "Two-scope recovery fixture", Amounts: []uint64{100 * uint64(index+1)},
		})
		if err != nil {
			t.Fatalf("issue in scope %d through public SDK: %v", index, err)
		}
		installRGB11PublicContractFixture(t, source, remote, issued.ContractID)
		assets[index] = issued
	}
	if err := source.SyncAccountManagementState(ctx); err != nil {
		t.Fatal(err)
	}
	source.Close()
	if err := source.db.Close(); err != nil {
		t.Fatal(err)
	}
	source.db = nil

	// Negative cases run before the valid target spends either original seal.
	for _, mode := range []string{"swapped-payloads", "wrong-account-index", "valid"} {
		t.Run(mode, func(t *testing.T) {
			target := newRGB11AccountScopeRecoveryManager(t, remote, chain)
			value, err := target.LoadAccountManagementStateForRecovery(
				auth.Location, pkg.Envelope.Locator, secret, coreE2ESenderMnemonic)
			if err != nil {
				t.Fatalf("read backup without original database: %v", err)
			}
			if value.Index.Version != account.ManagedIndexVersion {
				t.Fatal("account recovery did not load the current module index")
			}
			var rgbItems []int
			seenScopes := make(map[uint32]bool)
			payloads := make(map[int][]byte)
			accountIndices := make(map[int]uint32)
			for i, item := range value.Copies {
				if item.Entry.Provider != rgb11AccountManagedProviderID {
					continue
				}
				payload, err := openAccountPayload(secret, pkg.Envelope.Locator.AccountID, _chain, item.Entry, item.Value)
				if err != nil {
					t.Fatal(err)
				}
				payloads[i] = payload
				material, err := rgb11wallet.DecodeRecoveryPackage(payload)
				if err != nil {
					t.Fatal(err)
				}
				if material.AccountIndex >= 2 || seenScopes[material.AccountIndex] {
					t.Fatal("backup contains duplicate or unexpected RGB account scope")
				}
				proofs, err := rgb11wallet.ProofsFromProjectionSnapshot(material.ProjectionRecords)
				if err != nil || len(proofs) == 0 {
					t.Fatalf("scope %d has no actual RGB allocation: %v", material.AccountIndex, err)
				}
				for _, proof := range proofs {
					if proof.AssetName != assets[material.AccountIndex].AssetName {
						t.Fatalf("backup mixed assets in scope %d", material.AccountIndex)
					}
				}
				seenScopes[material.AccountIndex] = true
				accountIndices[i] = material.AccountIndex
				rgbItems = append(rgbItems, i)
			}
			if len(rgbItems) != 2 || !seenScopes[0] || !seenScopes[1] {
				t.Fatal("one account backup must contain both asset-bearing scopes")
			}
			switch mode {
			case "swapped-payloads":
				payloads[rgbItems[0]], payloads[rgbItems[1]] = payloads[rgbItems[1]], payloads[rgbItems[0]]
			case "wrong-account-index":
				material, err := rgb11wallet.DecodeRecoveryPackage(payloads[rgbItems[0]])
				if err != nil {
					t.Fatal(err)
				}
				material.AccountIndex = 1 - material.AccountIndex
				payloads[rgbItems[0]], err = rgb11wallet.EncodeRecoveryPackage(material)
				if err != nil {
					t.Fatalf("build structurally valid wrong-scope material: %v", err)
				}
			}
			if mode != "valid" {
				for _, i := range rgbItems {
					copy := &value.Copies[i]
					copy.Value, err = sealAccountPayload(secret, pkg.Envelope.Locator.AccountID, _chain, copy.Entry, payloads[i])
					if err != nil {
						t.Fatal(err)
					}
					copy.Record = cloneDKVSRecord(copy.Record)
					copy.Record.Value = bytes.Clone(copy.Value)
				}
				if _, err := validateRecoveredAccountManagementState(*value, secret, pkg.Envelope.Locator); err != nil {
					t.Fatalf("negative fixture must pass envelope integrity before scope validation: %v", err)
				}
			}
			beforeBroadcasts := chain.broadcastCount()
			_, err = target.RestoreAccountManagementState(*value, secret, "password", pkg.Envelope.Locator,
				AccountManagementRestoreOptions{Location: auth.Location, StorageMode: AccountStorageTemporary,
					RecordTTL: auth.RecordOptions.TTL})
			if mode != "valid" {
				if err != nil {
					t.Fatalf("module-local restore failure must not discard the restored account: %v", err)
				}
				blocked := 0
				for _, module := range target.GetAccountManagementStatus().Modules {
					if module.Provider == rgb11AccountManagedProviderID && !module.WriteAllowed && module.LastError == ErrRGB11Inconsistent.Error() {
						blocked++
					}
				}
				wantBlocked := 1
				if mode == "swapped-payloads" {
					wantBlocked = 2
				}
				if blocked != wantBlocked {
					t.Fatalf("blocked RGB scopes=%d want=%d", blocked, wantBlocked)
				}
				if chain.broadcastCount() != beforeBroadcasts {
					t.Fatal("invalid account recovery broadcast a transaction")
				}
				if err := target.SwitchAccount(accountIndices[rgbItems[0]]); err != nil {
					t.Fatal(err)
				}
				_, writeErr := target.CreateRGB11Invoice(RGB11InvoiceRequest{
					Mode: "witness", ContractID: assets[0].ContractID, AmountRaw: "1",
				})
				if writeErr == nil {
					t.Fatalf("incomplete recovery allowed a new RGB operation: %v", writeErr)
				}
				provider := &rgb11AccountManagedDataProvider{owner: target}
				accounts, lookupErr := provider.accountsByScope()
				if lookupErr != nil {
					t.Fatal(lookupErr)
				}
				for n, i := range rgbItems {
					if mode == "wrong-account-index" && n != 0 {
						continue
					}
					scoped, scopeErr := target.newScopedRGB11Manager(accounts[value.Copies[i].Entry.Scope])
					if scopeErr != nil {
						t.Fatal(scopeErr)
					}
					proofs, proofErr := scoped.projectionStore.ListProofs()
					if proofErr != nil || len(proofs) != 0 {
						t.Fatalf("invalid scope installed assets: count=%d err=%v", len(proofs), proofErr)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("restore both nonempty scopes through public SDK: %v", err)
			}
			// Construct both same-network Managers before starting background
			// readers: NewManager selects process-global chain parameters.
			peer := newRGB11AccountScopeRecoveryManager(t, remote, chain)
			activateRGB11AccountScopeRecovery(t, peer, coreE2EReceiverMnemonic)
			if err := target.StartDKVSSync(); err != nil {
				t.Fatal(err)
			}
			defer target.StopDKVSSync()
			readyCtx, cancelReady := context.WithTimeout(ctx, 10*time.Second)
			defer cancelReady()
			if err := target.WaitAccountManagedDataReady(readyCtx); err != nil {
				t.Fatalf("establish verified online session after recovery: %v", err)
			}
			// Import is durable before the cold device obtains its own online
			// session. The existing sync/wait above must complete before new work.
			status := target.GetAccountManagementStatus()
			if !status.Active || !status.RecoveryConfigured || status.LocalOnly ||
				status.ActivationPending || status.StateSeq == 0 || !status.DKVSWriteAllowed {
				t.Fatalf("restored account not write-ready after real sync: active=%t configured=%t local=%t pending=%t seq=%d writable=%t",
					status.Active, status.RecoveryConfigured, status.LocalOnly, status.ActivationPending, status.StateSeq, status.DKVSWriteAllowed)
			}
			for index := uint32(0); index < 2; index++ {
				if !t.Run(fmt.Sprintf("spend-scope-%d", index), func(t *testing.T) {
					if err := target.SwitchAccount(index); err != nil {
						t.Fatal(err)
					}
					assertRGB11PublicScopeBalance(t, target, assets[index].AssetName, 100*uint64(index+1))
					assertRGB11PublicScopeBalance(t, target, assets[1-index].AssetName, 0)
					amount := uint64(10) * uint64(index+1)
					invoice, err := peer.CreateRGB11Invoice(RGB11InvoiceRequest{
						Mode: "witness", TransportMode: "out-of-band", ContractID: assets[index].ContractID,
						AmountRaw: fmt.Sprint(amount), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
					})
					if err != nil {
						t.Fatal(err)
					}
					// A committed spend deliberately waits for the background backup's
					// quiet interval before the next scope can be write-ready.
					readyCtx, cancelReady := context.WithTimeout(ctx, accountManagedBackupQuiet+15*time.Second)
					defer cancelReady()
					if err := target.WaitAccountManagedDataReady(readyCtx); err != nil {
						logRGB11LocalRecoveryGate(t, target)
						t.Fatalf("restored scope %d is not ready to write: %v", index, err)
					}
					prepared, err := target.PrepareRGB11Transfer(ctx, RGB11SendRequest{
						Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
					})
					if err != nil {
						t.Fatalf("prepare spend in restored scope %d: %v", index, err)
					}
					before := chain.broadcastCount()
					txid, err := target.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
					if err != nil || chain.broadcastCount() != before+1 {
						t.Fatalf("sign and broadcast in restored scope %d: %v", index, err)
					}
					raw, err := chain.GetRawTx(txid)
					var signed wire.MsgTx
					if err != nil || signed.Deserialize(bytes.NewReader(raw)) != nil || !signed.HasWitness() {
						t.Fatal("restored scope did not produce a signed Bitcoin witness")
					}
					chain.confirm(txid)
					if _, err := peer.AcceptRGB11Consignment(ctx, invoice.RequestID, []byte(prepared.RecipientConsignment)); err != nil {
						t.Fatalf("native validation of restored scope %d history: %v", index, err)
					}
					for _, manager := range []*Manager{target, peer} {
						if _, err := manager.RefreshRGB11State(ctx); err != nil {
							logRGB11LocalRecoveryGate(t, manager)
							t.Fatal(err)
						}
					}
					assertRGB11PublicScopeBalance(t, target, assets[index].AssetName, 90*uint64(index+1))
					assertRGB11PublicScopeBalance(t, target, assets[1-index].AssetName, 0)
					assertRGB11PublicScopeBalance(t, peer, assets[index].AssetName, amount)
				}) {
					return
				}
			}
			for index := uint32(0); index < 2; index++ {
				if err := target.SwitchAccount(index); err != nil {
					t.Fatal(err)
				}
				assertRGB11PublicScopeBalance(t, target, assets[index].AssetName, 90*uint64(index+1))
				assertRGB11PublicScopeBalance(t, target, assets[1-index].AssetName, 0)
			}
		})
	}
}

func assertRGB11RejectedScopeRecoveryHasNoAssets(t *testing.T, target *Manager) {
	t.Helper()
	provider := &rgb11AccountManagedDataProvider{owner: target}
	accounts, err := provider.accountsByScope()
	if err != nil {
		t.Fatal(err)
	}
	for _, accountValue := range accounts {
		manager, err := target.newScopedRGB11Manager(accountValue)
		if err != nil {
			t.Fatal(err)
		}
		proofs, err := manager.projectionStore.ListProofs()
		if err != nil || len(proofs) != 0 {
			t.Fatalf("rejected scope package installed RGB allocations: count=%d err=%v", len(proofs), err)
		}
	}
}

func assertRGB11PublicScopeBalance(t *testing.T, manager *Manager, name indexer.AssetName, want uint64) {
	t.Helper()
	balance, err := manager.GetRGB11AssetBalance(&name)
	// The public balance API returns nil when this scope has no matching
	// allocation. Only an expected zero may use that representation.
	if err == nil && want == 0 && balance == nil {
		return
	}
	if err != nil || balance == nil || balance.Value.Uint64() != want {
		t.Fatalf("scope balance=%v want=%d err=%v", balance, want, err)
	}
}
