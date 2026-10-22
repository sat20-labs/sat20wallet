package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// collectRGB11PrivateSettlementDescends supplies public transaction locators,
// not authorization. The receiving core must independently fetch and validate
// these signed transactions, their canonical confirmations, and native amount
// conservation. No private package, shared entropy or revocable branch crosses
// this boundary. Completed reservations are intentionally included.
func (p *Manager) collectRGB11PrivateSettlementDescends(owner common.Wallet,
	privateID string, pkg *rgb11wallet.ChannelPackage) ([]string, error) {
	if p == nil || p.db == nil || owner == nil || privateID == "" || pkg == nil || len(pkg.Steps) == 0 {
		return nil, fmt.Errorf("missing native private settlement lookup context")
	}
	l1IDs := make(map[string]bool, len(pkg.Steps))
	for _, step := range pkg.Steps {
		if step.Context.ChannelID != privateID {
			return nil, fmt.Errorf("native settlement package changed private channel")
		}
		tx := wire.NewMsgTx(2)
		reader := bytes.NewReader(step.Tx)
		if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 {
			return nil, fmt.Errorf("invalid native settlement transaction locator")
		}
		l1IDs[tx.TxID()] = true
	}
	// Funding roots embed the previous private splice history. A close package
	// contains only its new commitment/recovery steps, so those steps alone
	// would omit a previously confirmed partial exit. Obtain ancestor witness
	// identities from core's fully validated history, never from decoded hints.
	evidence, err := stpRGB11Evidence(p)
	if err != nil {
		return nil, err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	var reveals []seals.GraphBlindSeal
	for _, raw := range last.Reveals {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, seal)
	}
	history, err := rgb11wallet.NewNativeConsensusValidatorWithReveals(reveals...).ValidateHistoricalConsignmentReport(context.Background(), last.Consignment, evidence)
	if err != nil || history == nil {
		return nil, fmt.Errorf("native settlement ancestor history: %w", errRGB11ProjectionEvidence(err))
	}
	for _, consumer := range history.Consumers {
		l1IDs[consumer.WitnessTxID] = true
	}
	for _, item := range history.Allocations {
		point, err := wire.NewOutPointFromString(item.Allocation.OutPoint)
		if err != nil {
			return nil, err
		}
		l1IDs[point.Hash.String()] = true
	}
	ids := make(map[string]bool)
	collect := func(tx *swire.MsgTx) error {
		if tx == nil || len(tx.TxIn) == 0 {
			return nil
		}
		for _, input := range tx.TxIn {
			if input == nil || len(input.Witness) == 0 {
				return nil // an unsigned candidate cannot locate an observed exit
			}
		}
		var matched bool
		for _, output := range tx.TxOut {
			if output == nil {
				return fmt.Errorf("invalid persisted native descending transaction")
			}
			kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
			if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
				continue
			}
			payload, err := sindexer.ParseDescendPayload(data)
			if err != nil {
				return err
			}
			if !l1IDs[payload.L1TxId] {
				continue
			}
			if matched || payload.Version != 2 || (payload.Operation != sindexer.DESCEND_OP_CLOSE &&
				payload.Operation != sindexer.DESCEND_OP_SPLICING_OUT && payload.Operation != sindexer.DESCEND_OP_FORCE_CLOSE) {
				return fmt.Errorf("invalid native descending locator for the settlement")
			}
			matched = true
		}
		if matched {
			ids[tx.TxID()] = true
		}
		return nil
	}
	for _, typ := range []string{RESV_TYPE_SPLICING, RESV_TYPE_CLOSE} {
		prefix := []byte(GetDBKeyPrefix() + DB_KEY_RESV + typ + "-")
		if err := p.db.BatchRead(prefix, false, func(key, raw []byte) error {
			var base *ReservationBase
			var channel string
			var tx *swire.MsgTx
			if typ == RESV_TYPE_SPLICING {
				var record SplicingDataInDB
				if err := DecodeFromBytes(raw, &record); err != nil {
					return fmt.Errorf("decode native settlement splice locator: %w", err)
				}
				base, channel, tx = &record.ReservationBase, record.ChannelId, record.AnchorTx
			} else {
				var record ClosingDataInDB
				if err := DecodeFromBytes(raw, &record); err != nil {
					return fmt.Errorf("decode native settlement close locator: %w", err)
				}
				base, channel, tx = &record.ReservationBase, record.ChannelId, record.DeAnchorTx
			}
			if base.WalletId != owner.GetWalletId() || channel != privateID {
				return nil
			}
			if string(key) != GetResvKey(typ, base.Id) {
				return fmt.Errorf("native settlement reservation key mismatch")
			}
			return collect(tx)
		}); err != nil {
			return nil, err
		}
	}
	// Closing records can also be retained on the terminal channel snapshot.
	raw, err := p.db.Read([]byte(GetChannelKey(privateID)))
	if err == nil {
		var stored ChannelInDB
		if err := DecodeFromBytes(raw, &stored); err != nil {
			return nil, err
		}
		if stored.ChannelId != privateID || stored.LocalWalletId != owner.GetWalletId().Id ||
			stored.LocalChanCfg.PaymentKey == nil || !stored.LocalChanCfg.PaymentKey.IsEqual(owner.GetPaymentPubKey()) {
			return nil, fmt.Errorf("native settlement channel locator belongs to another owner")
		}
		if err := collect(stored.DeAnchorTx); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Strings(result)
	return result, nil
}
