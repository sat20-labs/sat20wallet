package wallet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/invoicing"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Exercise the real ACK handlers with complete, signed two-output batches.
// Late ACKs must not undo the rejection tombstones or reacquire their inputs.
func TestRGB11ReviewMixedACKBatchCancellation(t *testing.T) {
	for _, transport := range []string{RGB11AddressTransport, RGB11ProxyTransport} {
		for _, nackFirst := range []bool{false, true} {
			order := "ACK-NACK"
			if nackFirst {
				order = "NACK-ACK"
			}
			t.Run(transport+"/"+order, func(t *testing.T) {
				sender, recipient, imported, evidence, _ := newRGB11GenericSendFixtureWithCarrier(t, 330)
				rgbInput := rgb11GenericFixtureSource(t, sender, imported)
				request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "100", FeeRate: 2, MinConfirmations: 1}
				prepared, err := runRGB11ManagedOperation(sender, context.Background(), rgb11ManagedOperationNew, func(m *rgb11Manager) (*RGB11PreparedTransfer, error) {
					return m.prepareRGB11AddressBatch(context.Background(), []RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
				})
				if err != nil {
					t.Fatal(err)
				}
				if len(prepared.State.InputOutPoints) != 2 {
					t.Fatal("mixed batch must reserve both the RGB carrier and a separate fee input")
				}
				proxy := &rgb11ProxyTestServer{}
				server := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
				defer server.Close()
				for _, state := range prepared.States {
					pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
					if err != nil {
						t.Fatal(err)
					}
					pending.State.Status = "delivered"
					if transport == RGB11ProxyTransport {
						pending.State.AddressMode = false
						pending.State.TransportMode = transport
						pending.State.Status = "relayed"
						contract, err := consensus.ParseContractID(imported.ContractID)
						if err != nil {
							t.Fatal(err)
						}
						script, err := AddrToPkScript(recipient.wallet.GetAddress(), GetChainParam())
						if err != nil {
							t.Fatal(err)
						}
						beneficiary, err := invoicing.NewWitnessBeneficiary(invoicing.BitcoinTestnet4, script, nil)
						if err != nil {
							t.Fatal(err)
						}
						endpoint, err := invoicing.ParseTransport("rpc://" + strings.TrimPrefix(server.URL, "http://"))
						if err != nil {
							t.Fatal(err)
						}
						expiry := time.Now().Add(time.Hour).Unix()
						pending.State.Invoice = (invoicing.Invoice{Contract: &contract, Beneficiary: beneficiary, Transports: []invoicing.Transport{endpoint}, Expiry: &expiry}).String()
					}
					if err := sender.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
						t.Fatal(err)
					}
				}
				if err := sender.SyncAccountManagementState(context.Background()); err != nil {
					t.Fatal(err)
				}
				unrelated := strings.Repeat("ee", 32) + ":0"
				if err := sender.utxoLockerL1.TryReserve([]string{unrelated}, rgb11wallet.LockReasonPending, "unrelated-owner"); err != nil {
					t.Fatal(err)
				}
				apply := func(index int, accepted bool) error {
					_, err := runRGB11ManagedOperation(sender, context.Background(), rgb11ManagedOperationContinue, func(m *rgb11Manager) (struct{}, error) {
						pending, err := m.projectionStore.LoadPendingTransfer(prepared.States[index].TransferID)
						if err != nil {
							return struct{}{}, err
						}
						if transport == RGB11ProxyTransport {
							proxy.mu.Lock()
							proxy.ack = &accepted
							proxy.mu.Unlock()
							_, err = m.FetchRGB11ProxyAck(context.Background(), pending.State.TransferID)
						} else {
							status := RGB11AddressACKRejected
							if accepted {
								status = RGB11AddressACKAccepted
							}
							_, err = m.acceptRGB11AddressACKDecoded(pending.State.SenderAccountID, pending.State.ReceiverAccountID, pending.State.AddressMessageID, RGB11AddressACK{Status: status})
						}
						return struct{}{}, err
					})
					return err
				}
				if nackFirst {
					if err := apply(1, false); err != nil {
						t.Fatal(err)
					}
					_ = apply(0, true) // A late ACK may be rejected, but cannot resurrect the batch.
				} else {
					if err := apply(0, true); err != nil {
						t.Fatal(err)
					}
					if err := apply(1, false); err != nil {
						t.Fatalf("verified NACK cannot cancel mixed batch: %v", err)
					}
				}
				for _, state := range prepared.States {
					pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
					if err != nil {
						t.Fatal(err)
					}
					if pending.State.Status != "rejected" || pending.State.AckStatus != "rejected" || pending.State.RejectReason != "recipient-rejected" || pending.State.WitnessTxID != prepared.TxID {
						t.Fatalf("batch terminal identity changed: %+v", pending.State)
					}
					for _, point := range pending.State.InputOutPoints {
						lock := sender.utxoLockerL1.GetLockedUtxoList()[point]
						if point == rgbInput {
							if lock == nil || lock.Reason != rgb11wallet.LockReasonRGB || lock.ReservationID != "" {
								t.Fatal("rejection did not restore the original RGB carrier protection")
							}
						} else if lock != nil {
							t.Fatal("rejection left the plain fee input locked")
						}
					}
				}
				if lock := sender.utxoLockerL1.GetLockedUtxoList()[unrelated]; lock == nil || lock.ReservationID != "unrelated-owner" {
					t.Fatal("unrelated reservation changed")
				}
				evidence.mu.Lock()
				broadcasts := len(evidence.broadcasted)
				evidence.mu.Unlock()
				if broadcasts != 0 {
					t.Fatal("mixed batch broadcast")
				}
				// Recreate the module from the existing DB, as in the established
				// restart fixture, and compare the durable tombstones and owners.
				sender.rgbManager.scopeStates.stopReconciliations()
				restarted, err := newRGB11Manager(sender, sender.db, sender.utxoLockerL1, evidence)
				if err != nil {
					t.Fatal(err)
				}
				sender.rgbManager = restarted
				if err := restarted.selectRGB11Scope(); err != nil {
					t.Fatal(err)
				}
				for _, state := range prepared.States {
					pending, err := restarted.projectionStore.LoadPendingTransfer(state.TransferID)
					if err != nil || pending.State.Status != "rejected" || pending.State.AckStatus != "rejected" || pending.State.WitnessTxID != prepared.TxID {
						t.Fatalf("restart lost rejected batch: %v", err)
					}
				}
				if lock := sender.utxoLockerL1.GetLockedUtxoList()[unrelated]; lock == nil || lock.ReservationID != "unrelated-owner" {
					t.Fatal("restart removed unrelated owner")
				}
			})
		}
	}
}

func TestRGB11ReviewRecipientRejectionPreservesIrreversibleIntent(t *testing.T) {
	for _, phase := range []string{rgb11StatusBroadcastAttempted, "broadcast", "pending", "peer-signed"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newExpiredCancelFixture(t, 1)
			pending, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(fixture.transferIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			stale := cloneRGB11BroadcastIntent(pending)
			pending.State.AckStatus = "accepted"
			pending.State.DeliveryAcknowledged = true
			if phase == "peer-signed" {
				pending.ChannelSend = &rgb11wallet.ChannelSendData{Signed: true}
			} else {
				pending.State.Status = phase
			}
			if err := fixture.manager.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				t.Fatal(err)
			}
			if err := fixture.manager.rgbManager.cancelRGB11RejectedPendingBatch([]*rgb11wallet.PendingTransfer{stale}, "recipient-rejected", nil); err == nil {
				t.Fatal("rejection cancelled a newer irreversible intent")
			}
			stored, err := fixture.manager.rgbManager.projectionStore.LoadPendingTransfer(pending.State.TransferID)
			if err != nil || stored.State.Status != pending.State.Status || stored.State.AckStatus != "accepted" {
				t.Fatalf("failed cancel changed newer intent: %v", err)
			}
			if lock := fixture.manager.utxoLockerL1.GetLockedUtxoList()[fixture.input]; lock == nil || lock.ReservationID != pending.ReservationID {
				t.Fatal("failed cancel released input owner")
			}
		})
	}
}
