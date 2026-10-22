package wallet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

func preparedRecoveryFixture(t *testing.T, arc bool) (*otherJournalFixture, *RevocationInfo, *wwire.CommitSigInfo) {
	t.Helper()
	f := newOtherJournalFixture(t, false)
	if arc {
		f.channel.AssetFunding = nil
		f.channel.LocalCommitment.AssetProofs, f.channel.RemoteCommitment.AssetProofs = nil, nil
		name := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_FT, Ticker: "atom"}, N: 1}
		f.channel.FundingUtxos[name] = []*TxOutput{indexer.NewTxOutput(1000)}
	}
	old, err := cloneSTPRecoveryChannel(f.channel)
	if err != nil {
		t.Fatal(err)
	}
	info := &RevocationInfo{Channel: f.channel, OldChannel: old}
	info.RemoteCommitInfo.CommitAssetProofs = cloneSTPAssetProofs(f.channel.RemoteCommitment.AssetProofs)
	response := &wwire.CommitSigInfo{RecoveryOnly: true, RecoveryCommitTxID: f.channel.RemoteCommitment.CommitTx.TxID(), NativeOtherTxs: f.packages}
	return f, info, response
}

func TestSTPNativeRecoveryPreparationNeverReleasesParentAuthorization(t *testing.T) {
	f, info, _ := preparedRecoveryFixture(t, false)
	info.RemoteCommitInfo.CommitSig = [][]byte{{1}}
	info.RemoteCommitInfo.CommitDeAnchorSig = [][]byte{{2}}
	info.RemoteCommitInfo.CommitPrevTxSig = [][][]byte{{{3}}}
	info.RemoteCommitInfo.CommitNextTxSig = [][][]byte{{{4}}}
	info.RemoteCommitInfo.CommitOtherTxSig = [][][]byte{{{5}}}
	request, err := f.manager.NativeCommitmentRecoveryRequest(info)
	if err != nil || request == nil || len(request.CommitOtherTxSig) != 1 || len(request.CommitSig) != 0 ||
		len(request.CommitDeAnchorSig) != 0 || len(request.CommitPrevTxSig) != 0 || len(request.CommitNextTxSig) != 0 {
		t.Fatalf("preparation leaked parent authorization: request=%+v err=%v", request, err)
	}
	for _, field := range []string{"parent", "deanchor", "predecessor", "successor", "response"} {
		copy := *request
		switch field {
		case "parent":
			copy.CommitSig = [][]byte{{1}}
		case "deanchor":
			copy.CommitDeAnchorSig = [][]byte{{1}}
		case "predecessor":
			copy.CommitPrevTxSig = [][][]byte{{{1}}}
		case "successor":
			copy.CommitNextTxSig = [][][]byte{{{1}}}
		case "response":
			copy.NativeOtherTxs = f.packages
		}
		if err := ValidateNativeRecoveryOnlyRequest(&copy); err == nil {
			t.Fatalf("preparation accepted forbidden %s authorization", field)
		}
	}
}

func TestSTPNativeRecoveryPreparationAtomicForProofAndARCProtocols(t *testing.T) {
	for _, arc := range []bool{false, true} {
		for _, fault := range []string{"put", "flush", "success"} {
			name := "proof-" + fault
			if arc {
				name = "arc-" + fault
			}
			t.Run(name, func(t *testing.T) {
				f, info, response := preparedRecoveryFixture(t, arc)
				before, _ := EncodeMsgTx(f.channel.RemoteCommitment.Others[0].CommitTx)
				f.db.operations = 0
				if fault == "put" {
					f.db.failOperation = 2 // Ordinary remote journal, then preparation.
					if arc {
						f.db.failOperation = 1 // ARC has only the preparation journal.
					}
				}
				f.db.failFlush = fault == "flush"
				err := f.manager.AcceptNativeCommitmentRecovery(info, response)
				if fault != "success" {
					if !errors.Is(err, errNativeSettlementWrite) {
						t.Fatalf("missing injected failure: %v", err)
					}
					after, _ := EncodeMsgTx(f.channel.RemoteCommitment.Others[0].CommitTx)
					if before != after {
						t.Fatal("failed preparation published uncommitted authorization")
					}
					stored := f.reopen(t)
					if _, err := f.db.Read(stpNativePreparedRecoveryKey(f.channel)); !errors.Is(err, indexer.ErrKeyNotFound) {
						t.Fatalf("partial preparation survived restart: %v", err)
					}
					if recovered, err := f.manager.LoadSTPNativePreparedRecovery(stored); err != nil || recovered != nil {
						t.Fatalf("failed preparation created a recovery obligation: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				stored := f.reopen(t)
				recovered, err := f.manager.LoadSTPNativePreparedRecovery(stored)
				if err != nil || recovered == nil || recovered.Status != CS_NATIVE_RECOVERY || recovered.CommitHeight != info.OldChannel.CommitHeight {
					t.Fatalf("READY was not held after successful preparation: %+v %v", recovered, err)
				}
				record, err := f.manager.readSTPNativePreparedRecovery(&Channel{ChannelInDB: *recovered})
				if err != nil || record == nil {
					t.Fatalf("prepared record cannot be independently reloaded: %v", err)
				}
				actual, _ := EncodeMsgTx(record.Candidate.RemoteCommitment.Others[0].CommitTx)
				if actual != f.packages[0][0] || !equalSTPCommitmentPackage(recovered.LocalCommitment, info.OldChannel.LocalCommitment) {
					t.Fatal("restart lost the signed peer sweep or changed the safe old local obligation")
				}
			})
		}
	}
}

func TestSTPNativeRecoveryPreparationRejectsCandidateIdentityChanges(t *testing.T) {
	for _, field := range []string{"local-key", "remote-key", "redeem", "entropy", "peer", "wallet", "csv"} {
		t.Run(field, func(t *testing.T) {
			f, info, response := preparedRecoveryFixture(t, false)
			if err := f.manager.AcceptNativeCommitmentRecovery(info, response); err != nil {
				t.Fatal(err)
			}
			record, err := f.manager.readSTPNativePreparedRecovery(f.channel)
			if err != nil {
				t.Fatal(err)
			}
			key, _ := btcec.PrivKeyFromBytes([]byte{99})
			switch field {
			case "local-key":
				record.Candidate.LocalChanCfg.PaymentKey = key.PubKey()
			case "remote-key":
				record.Candidate.RemoteChanCfg.PaymentKey = key.PubKey()
			case "redeem":
				record.Candidate.RedeemScript[0] ^= 1
			case "entropy":
				record.Candidate.AssetEntropy[0]++
			case "peer":
				record.Candidate.PeerNodeId = []byte{99}
			case "wallet":
				record.Candidate.LocalWalletId++
			case "csv":
				record.Candidate.CsvDelay++
			}
			raw, err := EncodeToBytes(record)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.db.Write(stpNativePreparedRecoveryKey(f.channel), raw); err != nil {
				t.Fatal(err)
			}
			stored := f.reopen(t)
			if recovered, err := f.manager.LoadSTPNativePreparedRecovery(stored); err == nil || recovered != nil {
				t.Fatalf("changed %s accepted after database reopen", field)
			}
		})
	}
}

func TestSTPNativeRecoveryIgnoresReplacedChannelObservation(t *testing.T) {
	f, info, response := preparedRecoveryFixture(t, false)
	if err := f.manager.AcceptNativeCommitmentRecovery(info, response); err != nil {
		t.Fatal(err)
	}
	f.manager.channelMap = map[string]*Channel{f.channel.ChannelId: info.OldChannel}
	before, _ := f.db.Read([]byte(GetChannelKey(f.channel.ChannelId)))
	if handled, err := f.manager.RetryNativePreparedRecovery(f.channel); !handled || err != nil {
		t.Fatalf("stale observation was not discarded: %v %v", handled, err)
	}
	if err := f.manager.HandleUnexpectedChannelClose(f.channel, f.channel.RemoteCommitment.CommitTx.TxID()); err != nil {
		t.Fatal(err)
	}
	after, _ := f.db.Read([]byte(GetChannelKey(f.channel.ChannelId)))
	if !bytes.Equal(before, after) || f.manager.GetChannel(f.channel.ChannelId) != info.OldChannel {
		t.Fatal("stale channel observation changed the authoritative channel")
	}
}

func TestSTPNativeRecoveryKeepsOldSafetyWatchUntilExactPeerParentConfirms(t *testing.T) {
	f, info, response := preparedRecoveryFixture(t, false)
	if err := f.manager.AcceptNativeCommitmentRecovery(info, response); err != nil {
		t.Fatal(err)
	}
	old := info.OldChannel
	f.manager.channelMap = map[string]*Channel{old.ChannelId: old}
	f.manager.l1IndexerClient = &IndexerRPCClientMgr{active: &nativeRecoveryRPC{confirmed: map[string]bool{}}}
	if handled, err := f.manager.RetryNativePreparedRecovery(old); handled || err != nil {
		t.Fatalf("unconfirmed candidate suppressed the old commitment watch: %v %v", handled, err)
	}
	if old.Status != CS_NATIVE_RECOVERY || old.ChanPoint.OutPointStr != info.OldChannel.ChanPoint.OutPointStr ||
		!equalSTPCommitmentPackage(old.LocalCommitment, info.OldChannel.LocalCommitment) {
		t.Fatal("unconfirmed candidate replaced the old funding/exit context or remained READY")
	}
}

func TestSTPNativeRecoveryPreparationCleanupSharesTerminalTransaction(t *testing.T) {
	f := newNativeRecoveryFixture(t)
	preparedKey := stpNativePreparedRecoveryKey(f.channel)
	prepared := []byte("prepared recovery must survive a failed terminal commit")
	if err := f.db.Write(preparedKey, prepared); err != nil {
		t.Fatal(err)
	}
	f.db.failFlush = true
	if err := f.manager.persistNativeForceCloseLocked(f.resv, f.resv.ClosingDataInDB, CS_CLOSED_FORCELY); !errors.Is(err, errNativeSettlementWrite) {
		t.Fatalf("terminal failure was not reported: %v", err)
	}
	actual, err := f.db.Read(preparedKey)
	if err != nil || !bytes.Equal(actual, prepared) || f.channel.Status != CS_READY {
		t.Fatal("failed terminal commit removed preparation or published terminal state")
	}
	f.db.failFlush = false
	if err := f.manager.persistNativeForceCloseLocked(f.resv, f.resv.ClosingDataInDB, CS_CLOSED_FORCELY); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Read(preparedKey); !errors.Is(err, indexer.ErrKeyNotFound) || f.channel.Status != CS_CLOSED_FORCELY {
		t.Fatal("terminal state and preparation cleanup did not commit together")
	}
}
