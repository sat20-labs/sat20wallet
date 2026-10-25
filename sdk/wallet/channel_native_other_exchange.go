package wallet

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func needsSTPNativeOtherExchange(channel *Channel, commitment *ChannelCommitment) bool {
	if needsSTPNativeCommitmentJournal(channel, commitment) {
		return true
	}
	if channel != nil {
		for asset, outputs := range channel.FundingUtxos {
			if len(outputs) != 0 && STPPolicyFor(&asset).ExchangeRecoveryPackages {
				return true
			}
		}
	}
	return false
}

func stpOtherTransactions(other *CommitmentTx) ([]*wire.MsgTx, error) {
	if other == nil || other.CommitTx == nil {
		return nil, fmt.Errorf("missing native other-sweep transaction")
	}
	txs := append(append([]*wire.MsgTx{}, other.PrevTxs...), other.CommitTx)
	return append(txs, other.NextTxs...), nil
}

// The counterparty must receive the already authorized sweep before accepting
// a revocation. Otherwise a server could publish its commitment while leaving
// client assets locked behind a second signature that it can withhold.
func encodeSTPNativeOtherTransactions(channel *Channel) ([][]string, error) {
	commitment := channel.LocalCommitment
	// Every constructed Other needs its complete peer signatures. The native
	// journal and close classification remain separately protocol-gated.
	if !needsSTPNativeOtherExchange(channel, commitment) &&
		(commitment == nil || len(commitment.Others) == 0) {
		return nil, nil
	}
	if commitment == nil || commitment.CommitTx == nil {
		return nil, fmt.Errorf("missing native local commitment")
	}
	result := make([][]string, len(commitment.Others))
	for i, other := range commitment.Others {
		txs, err := stpOtherTransactions(other)
		if err != nil {
			return nil, err
		}
		fetcher := txscript.NewMultiPrevOutFetcher(nil)
		AddTxOutputsToPrevFetcher(fetcher, commitment.CommitTx)
		for _, tx := range txs {
			if tx == nil {
				return nil, fmt.Errorf("nil native other-sweep package transaction")
			}
			if err := VerifySignedTx(tx, fetcher); err != nil {
				return nil, fmt.Errorf("native other-sweep is not fully authorized: %w", err)
			}
			raw, err := EncodeMsgTx(tx)
			if err != nil {
				return nil, err
			}
			result[i] = append(result[i], raw)
			AddTxOutputsToPrevFetcher(fetcher, tx)
		}
	}
	return result, nil
}

func (p *Manager) acceptSTPNativeOtherTransactions(channel *Channel, packages [][]string) error {
	return p.acceptSTPNativeOtherTransactionsWithJournal(channel, packages, nil)
}

func (p *Manager) acceptSTPNativeOtherTransactionsWithJournal(channel *Channel, packages [][]string,
	prepare func(*Channel) ([]byte, []byte, error)) error {
	return p.acceptSTPNativeOtherTransactionsWithJournalAndRGBView(channel, packages, prepare, nil)
}

func (p *Manager) acceptSTPNativeOtherTransactionsWithJournalAndRGBView(channel *Channel, packages [][]string,
	prepare func(*Channel) ([]byte, []byte, error), view *stpRGBEvidenceView) error {
	commitment := channel.RemoteCommitment
	// Share exact package/signature validation with legacy Others without
	// enabling native journal storage or native force-close for those assets.
	if !needsSTPNativeOtherExchange(channel, commitment) &&
		(commitment == nil || len(commitment.Others) == 0) {
		if len(packages) != 0 {
			return fmt.Errorf("unexpected native other-sweep package")
		}
		return nil
	}
	if commitment == nil || commitment.CommitTx == nil || len(packages) != len(commitment.Others) {
		return fmt.Errorf("native other-sweep package count differs from the verified commitment")
	}
	updated := make([]*CommitmentTx, len(packages))
	for i, encoded := range packages {
		other := commitment.Others[i]
		expected, err := stpOtherTransactions(other)
		if err != nil {
			return err
		}
		if len(encoded) != len(expected) {
			return fmt.Errorf("native other-sweep chain length mismatch")
		}
		fetcher := txscript.NewMultiPrevOutFetcher(nil)
		AddTxOutputsToPrevFetcher(fetcher, commitment.CommitTx)
		verified := make([]*wire.MsgTx, len(encoded))
		for j, raw := range encoded {
			tx, err := DecodeMsgTx(raw)
			if err != nil || expected[j] == nil || tx.TxHash() != expected[j].TxHash() {
				return fmt.Errorf("native other-sweep changed its independently built transaction")
			}
			if err := VerifySignedTx(tx, fetcher); err != nil {
				return fmt.Errorf("native other-sweep signature validation: %w", err)
			}
			verified[j] = tx
			AddTxOutputsToPrevFetcher(fetcher, tx)
		}
		copy := *other
		copy.PrevTxs = verified[:len(other.PrevTxs)]
		copy.CommitTx = verified[len(other.PrevTxs)]
		copy.NextTxs = verified[len(other.PrevTxs)+1:]
		updated[i] = &copy
	}
	candidate, _ := stpFundingValidationContext(channel, nil)
	remote := *commitment
	remote.Others = updated
	candidate.RemoteCommitment = &remote
	remoteKey, remoteValue, err := p.prepareSTPNativeCommitmentWriteWithRGBView(candidate, &remote, "remote", view)
	if err != nil {
		return err
	}
	var extraKey, extraValue []byte
	if prepare != nil {
		extraKey, extraValue, err = prepare(candidate)
		if err != nil {
			return err
		}
	}
	var localKey, localValue []byte
	// A local revocation can precede the peer's reply. Refresh its recovery
	// snapshot only if it already describes this exact local candidate; the
	// signed local obligation and the released-secret marker remain unchanged.
	if len(remoteKey) != 0 && needsSTPNativeCommitmentJournal(candidate, candidate.LocalCommitment) {
		raw, err := p.db.Read(stpNativeCommitmentKey(candidate, "local"))
		if err == nil {
			var stored stpNativeCommitmentRecord
			if err := DecodeFromBytes(raw, &stored); err != nil {
				return err
			}
			if equalSTPCommitmentPackage(stored.Commitment, candidate.LocalCommitment) {
				// Matching transaction bytes alone are insufficient: the role,
				// funding inventory, entropy and signed deanchor must also be
				// the exact obligation whose snapshot is being refreshed.
				if err := p.verifySTPNativeCommitmentPersisted(candidate, candidate.LocalCommitment); err != nil {
					return err
				}
				localKey, localValue, err = p.prepareSTPNativeCommitmentWriteWithRGBView(candidate, candidate.LocalCommitment, "local", view)
				if err != nil {
					return err
				}
			}
		} else if !errors.Is(err, indexer.ErrKeyNotFound) {
			return err
		}
	}
	if err := completeSTPRGBView(view); err != nil {
		return err
	}
	if len(remoteKey) != 0 || len(extraKey) != 0 {
		// Recovery loads the local journal's channel snapshot. Publishing the
		// remote journal alone would strand its complete sweep signatures if
		// a crash occurred before that local snapshot was refreshed.
		batch, err := NewAtomicWriteBatch(p.db)
		if err != nil {
			return err
		}
		defer batch.Close()
		if len(remoteKey) != 0 {
			if err := batch.Put(remoteKey, remoteValue); err != nil {
				return err
			}
		}
		if len(localKey) != 0 {
			if err := batch.Put(localKey, localValue); err != nil {
				return err
			}
		}
		if len(extraKey) != 0 {
			if err := batch.Put(extraKey, extraValue); err != nil {
				return err
			}
		}
		if err := batch.Flush(); err != nil {
			return err
		}
	}
	channel.RemoteCommitment.Others = updated
	return nil
}
