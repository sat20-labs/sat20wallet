package wallet

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/stretchr/testify/require"
)

func TestRGBMetaDuringBackup(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	for _, mode := range []string{"local", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			remote, chain := newRGB11MemoryDKVSHTTP(), newCoreE2EChain()
			directory := t.TempDir()
			database := indexerdb.NewKVDB(directory)
			cfg := coreE2EConfig{Core: AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
			var armed atomic.Bool
			var count atomic.Int32
			var moduleKey string
			firstAccepted, secondEntered := make(chan []byte, 1), make(chan []byte, 1)
			releaseACK, releaseNext := make(chan struct{}), make(chan struct{})
			var ackOnce, nextOnce sync.Once
			release := func() {
				ackOnce.Do(func() { close(releaseACK) })
				nextOnce.Do(func() { close(releaseNext) })
			}
			defer release()
			http := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			http.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if armed.Load() && path == "/v3/dkvs/records/batch-cas" {
					var request DKVSBatchCASRequest
					if err := json.Unmarshal(body, &request); err != nil {
						return nil, err
					}
					for _, mutation := range request.Mutations {
						if mutation.Record == nil || mutation.Record.Key != moduleKey {
							continue
						}
						switch count.Add(1) {
						case 1:
							response, err := remote.SendDKVSPostContext(ctx, path, body)
							if err != nil {
								return nil, err
							}
							firstAccepted <- bytes.Clone(mutation.Record.Value)
							select {
							case <-releaseACK:
								return response, nil
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						case 2:
							secondEntered <- bytes.Clone(mutation.Record.Value)
							select {
							case <-releaseNext:
							case <-ctx.Done():
								return nil, ctx.Err()
							}
						}
					}
				}
				return remote.SendDKVSPostContext(ctx, path, body)
			}
			manager := coreNewE2EManagerWithDB(t, cfg, chain, database, "", http)
			useRGB11TestCoreAuthority(t, manager)
			client := newRGB11MessageNodeClient(remote)
			manager.SetServerNode(NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey()))
			var closed bool
			t.Cleanup(func() {
				release()
				if !closed {
					manager.Close()
					require.NoError(t, database.Close())
				}
			})
			_, _, secret := activateRGB11AccountScopeRecovery(t, manager, coreE2ESenderMnemonic)
			defer zeroBytes(secret)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
			row := rgb11LocalModuleForTest(t, manager)
			entry, err := accountEntry(row.AccountID, row.Provider, row.Scope)
			require.NoError(t, err)
			moduleKey = entry.Key
			walletID, err := manager.RGB11WalletID()
			require.NoError(t, err)
			require.NoError(t, manager.UpdateWalletName(manager.GetCurrentWalletId(), "Export in progress"))
			armed.Store(true)
			// A real RGB mutation, not a wallet rename, now owns its independent
			// module publication. Hold the accepted N receipt while local work runs.
			initial, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
				Mode: "witness", TransportMode: "out-of-band", AmountRaw: "1",
				WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
			})
			require.NoError(t, err)
			require.NotNil(t, initial)
			var first []byte
			select {
			case first = <-firstAccepted:
			case <-ctx.Done():
				t.Fatal("RGB module N did not reach its real ACK boundary")
			}
			before := rgb11LocalModuleForTest(t, manager)
			prior, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			require.NoError(t, err)
			var invoice *corewallet.ReceiveRequest
			done := make(chan error, 1)
			go func() {
				if mode == "local" {
					done <- manager.rgbManager.projectionStore.SaveLocalMetadata("test-tip", []byte("tip"))
					return
				}
				var err error
				invoice, err = manager.CreateRGB11Invoice(RGB11InvoiceRequest{
					Mode: "witness", TransportMode: "out-of-band", AmountRaw: "2",
					WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
				})
				done <- err
			}()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(2 * time.Second):
				release()
				<-done
				t.Fatal("local operation waited for the network backup")
			}
			afterSnapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
			require.NoError(t, err)
			after := rgb11LocalModuleForTest(t, manager)
			if mode == "local" {
				require.Equal(t, before.Generation, after.Generation)
				require.Equal(t, before.Confirmed, after.Confirmed)
				require.Equal(t, prior, afterSnapshot, "checkpoint must not change the protected RGB recovery")
				value, err := manager.rgbManager.projectionStore.LoadLocalMetadata("test-tip")
				require.NoError(t, err)
				require.Equal(t, []byte("tip"), value)
			} else {
				require.NotNil(t, invoice)
				require.Greater(t, after.Generation, before.Generation)
				require.Greater(t, after.Generation, after.Confirmed)
				require.False(t, reflect.DeepEqual(prior, afterSnapshot))
			}
			select {
			case <-releaseACK:
				t.Fatal("ACK was released before the local operation committed")
			default:
			}
			ackOnce.Do(func() { close(releaseACK) })
			if mode == "recovery" {
				select {
				case <-secondEntered:
				case <-ctx.Done():
					t.Fatal("N+1 was not retained for publication")
				}
				pending := rgb11LocalModuleForTest(t, manager)
				require.Equal(t, after.Generation, pending.Generation)
				require.Greater(t, pending.Generation, pending.Confirmed, "ACK N cleared N+1")
			}
			nextOnce.Do(func() { close(releaseNext) })
			require.NoError(t, manager.WaitAccountManagedDataReady(ctx))
			manager.StopDKVSSync()
			manager.accountBackgroundWG.Wait()
			checkPayload := func(encoded []byte, snapshot *RGB11WalletSnapshot) {
				t.Helper()
				payload, err := openAccountPayload(secret, row.AccountID, _chain, entry, encoded)
				require.NoError(t, err)
				want, err := rgb11wallet.RecoveryPackageFromSnapshot(snapshot, 0)
				require.NoError(t, err)
				expected, err := rgb11wallet.EncodeRecoveryPackage(want)
				require.NoError(t, err)
				require.Equal(t, expected, payload, "module must contain the exact captured recovery closure")
			}
			// N was already captured before the second mutation. Its immutable
			// signed payload stays N; only the later publication may contain N+1.
			checkPayload(first, prior)
			remote.mu.Lock()
			finalRecord := cloneRGB11DKVSRecord(remote.records[moduleKey])
			remote.mu.Unlock()
			require.NotNil(t, finalRecord)
			checkPayload(finalRecord.Value, afterSnapshot)
			final := rgb11LocalModuleForTest(t, manager)
			require.Equal(t, after.Generation, final.Generation)
			require.Equal(t, final.Generation, final.Confirmed)
			require.Equal(t, finalRecord.Value, final.Value)
			state, err := account.OpenManagedState(secret, row.AccountID, manager.accountProfile.StateEnvelope)
			require.NoError(t, err)
			require.Len(t, state.Wallets, 1)
			require.Equal(t, "Export in progress", state.Wallets[0].Name)
			localKey := accountLocalKey(final.AccountID, final.Provider, final.Scope)
			armed.Store(false)
			manager.Close()
			require.NoError(t, database.Close())
			closed = true
			reopened := indexerdb.NewKVDB(directory)
			raw, err := reopened.Read(localKey)
			require.NoError(t, err)
			var persisted accountLocalKV
			require.NoError(t, DecodeFromBytes(raw, &persisted))
			require.Equal(t, final.Generation, persisted.Generation)
			require.Equal(t, final.Confirmed, persisted.Confirmed)
			require.Equal(t, final.Value, persisted.Value)
			restarted := coreNewE2EManagerWithDB(t, cfg, chain, reopened, "", http)
			useRGB11TestCoreAuthority(t, restarted)
			restarted.SetServerNode(NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey()))
			t.Cleanup(func() { restarted.Close(); require.NoError(t, reopened.Close()) })
			_, err = restarted.UnlockWallet("password")
			require.NoError(t, err)
			retained := rgb11LocalModuleForTest(t, restarted)
			require.Equal(t, final.Generation, retained.Generation)
			require.Equal(t, final.Confirmed, retained.Confirmed)
			restartedID, err := restarted.RGB11WalletID()
			require.NoError(t, err)
			require.Equal(t, walletID, restartedID)
			for _, receive := range []*corewallet.ReceiveRequest{initial, invoice} {
				if receive == nil {
					continue
				}
				record, err := reopened.Read([]byte("rgb11-engine-" + restarted.rgbManager.rgb11ScopeKey() + "-wallet/receive/" + receive.RequestID))
				require.NoError(t, err)
				durable, err := corewallet.DecodeReceiveRequest(record)
				require.NoError(t, err)
				require.Equal(t, receive, durable, "all receive secrets must survive real disk reopen")
				loaded, err := restarted.rgbManager.engine.LoadReceive(receive.RequestID)
				require.NoError(t, err)
				require.Equal(t, durable, loaded, "real Manager unlock must load the same durable receive")
			}
		})
	}
}
