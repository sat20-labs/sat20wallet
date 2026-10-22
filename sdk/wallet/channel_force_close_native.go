package wallet

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func nativeForceClose(resv *ClosingReservation) bool {
	if resv == nil || resv.Channel == nil {
		return false
	}
	if len(resv.AssetProofs) != 0 || len(resv.Channel.AssetFunding) != 0 {
		return true
	}
	if needsSTPNativeOtherExchange(resv.Channel, resv.Channel.RemoteCommitment) {
		return true
	}
	return resv.Channel.LocalCommitment != nil && len(resv.Channel.LocalCommitment.AssetProofs) != 0 ||
		resv.Channel.RemoteCommitment != nil && len(resv.Channel.RemoteCommitment.AssetProofs) != 0
}

// persistNativeForceCloseLocked freezes the signed Bitcoin package, proof and
// monitor state in one transaction. The caller holds the channel mutex. Native
// reservations use PreTxs for the complete ordered package (including CloseTx),
// because a commitment can also have signed BRC20 successors. Replaying only a
// transaction ID or rebuilding at a later height would lose that exact package.
func (p *Manager) persistNativeForceCloseLocked(resv *ClosingReservation, next ClosingDataInDB, status ChannelStatus) error {
	if p == nil || p.db == nil || resv == nil || resv.Channel == nil || next.CloseTx == nil ||
		next.Id == 0 || next.Id != resv.Id || next.ChannelId != resv.Channel.ChannelId {
		return fmt.Errorf("invalid native force-close persistence context")
	}
	if status != CS_CLOSED_FORCELY && (status < CS_CLOSE_FORCELY_BROADCASTED || status > CS_CLOSE_FORCELY_SWEEP_CONFIRMED) {
		return fmt.Errorf("invalid native force-close phase %d", status)
	}
	if resv.Channel.Status >= CS_CLOSE_FORCELY_BROADCASTED && resv.Channel.Status <= CS_CLOSE_FORCELY_SWEEP_CONFIRMED &&
		resv.Channel.UpdateTime != resv.Id {
		return fmt.Errorf("stale native force-close reservation")
	}
	next.CloseTx = next.CloseTx.Copy()
	next.PreTxs = cloneSTPBitcoinChain(next.PreTxs)
	next.AssetProofs = cloneSTPAssetProofs(next.AssetProofs)
	if next.DeAnchorTx != nil {
		next.DeAnchorTx = next.DeAnchorTx.Copy()
	}
	if len(next.PreTxs) == 0 {
		next.PreTxs = []*wire.MsgTx{next.CloseTx.Copy()}
	}
	if _, err := nativeForceClosePackage(&next); err != nil {
		return err
	}
	next.Status = ResvStatus(status)
	if status == CS_CLOSED_FORCELY {
		next.Status = RS_CLOSED
	}
	key := []byte(GetResvKey(RESV_TYPE_CLOSE, next.Id))
	if raw, err := p.db.Read(key); err == nil {
		var previous ClosingDataInDB
		if err := DecodeFromBytes(raw, &previous); err != nil {
			return err
		}
		if previous.ChannelId != next.ChannelId || previous.Id != next.Id {
			return fmt.Errorf("native force-close reservation identity changed")
		}
		if previous.Status == next.Status && (!equalSTPBitcoinChain(previous.PreTxs, next.PreTxs) ||
			!equalSTPBitcoinChain([]*wire.MsgTx{previous.CloseTx}, []*wire.MsgTx{next.CloseTx}) ||
			!equalSTPAssetProofs(previous.AssetProofs, next.AssetProofs)) {
			return fmt.Errorf("native force-close signed package is immutable")
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	raw, err := EncodeToBytes(&resv.Channel.ChannelInDB)
	if err != nil {
		return err
	}
	var candidate ChannelInDB
	if err := DecodeFromBytes(raw, &candidate); err != nil {
		return err
	}
	candidate.Status, candidate.UpdateTime = status, next.Id
	candidate.ClosingTx = next.CloseTx.Copy()
	if err := candidate.PrepareForSave(); err != nil {
		return err
	}
	channelRaw, err := EncodeToBytes(&candidate)
	if err != nil {
		return err
	}
	reservationRaw, err := EncodeToBytes(&next)
	if err != nil {
		return err
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return err
	}
	defer batch.Close()
	if err := batch.Put([]byte(GetChannelKey(candidate.ChannelId)), channelRaw); err != nil {
		return err
	}
	if err := batch.Put(key, reservationRaw); err != nil {
		return err
	}
	if status == CS_CLOSED_FORCELY {
		preparedKey := stpNativePreparedRecoveryKey(resv.Channel)
		if _, err := p.db.Read(preparedKey); err == nil {
			if err := batch.Delete(preparedKey); err != nil {
				return err
			}
		} else if !errors.Is(err, indexer.ErrKeyNotFound) {
			return err
		}
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	// In-memory state is published only after both durable values committed.
	resv.Channel.ChannelInDB = candidate
	resv.Channel.ResvId = next.Id
	if status == CS_CLOSED_FORCELY {
		resv.Channel.ResvId = 0
	}
	resv.ClosingDataInDB = next
	p.AddResv(resv)
	p.DisableChannel(resv.Channel)
	p.backupChannel(resv.Channel)
	return nil
}

func nativeForceClosePackage(record *ClosingDataInDB) ([]*wire.MsgTx, error) {
	if record == nil || record.CloseTx == nil {
		return nil, fmt.Errorf("native force-close transaction is missing")
	}
	seen := make(map[string]*wire.MsgTx)
	var result []*wire.MsgTx
	for _, tx := range append(append([]*wire.MsgTx{}, record.PreTxs...), record.CloseTx) {
		if tx == nil {
			return nil, fmt.Errorf("nil native force-close package transaction")
		}
		if previous := seen[tx.TxID()]; previous != nil {
			if !equalSTPBitcoinChain([]*wire.MsgTx{previous}, []*wire.MsgTx{tx}) {
				return nil, fmt.Errorf("conflicting native force-close transaction witness")
			}
			continue
		}
		seen[tx.TxID()] = tx
		result = append(result, tx)
	}
	return result, nil
}

func (p *Manager) broadcastNativeForceCloseLocked(resv *ClosingReservation) error {
	var deanchorErr error
	if resv.IsInitiator && resv.DeAnchorTx != nil {
		if p.GetIndexerRPCClient_SatsNet() == nil {
			deanchorErr = fmt.Errorf("native force-close SatoshiNet client is unavailable")
		} else if !p.GetIndexerRPCClient_SatsNet().IsTxConfirmed(resv.DeAnchorTx.TxID()) {
			_, deanchorErr = p.BroadcastTx_SatsNet(resv.DeAnchorTx)
		}
	}
	txs, err := nativeForceClosePackage(&resv.ClosingDataInDB)
	if err != nil {
		return errors.Join(deanchorErr, err)
	}
	_, err = p.broadcastPunishPackage(txs, "native force close recovery")
	return errors.Join(deanchorErr, err)
}

func (p *Manager) startNativeForceCloseLocked(resv *ClosingReservation) error {
	if err := p.persistNativeForceCloseLocked(resv, resv.ClosingDataInDB, CS_CLOSE_FORCELY_BROADCASTED); err != nil {
		return err
	}
	return p.broadcastNativeForceCloseLocked(resv)
}

func (p *Manager) confirmedNativeCloseTransaction(resv *ClosingReservation) (*wire.MsgTx, error) {
	if p.GetIndexerRPCClient() == nil || resv.CloseTx == nil || !p.GetIndexerRPCClient().IsTxConfirmed(resv.CloseTx.TxID()) {
		return nil, fmt.Errorf("native force-close transaction is awaiting confirmation")
	}
	raw, err := p.GetIndexerRPCClient().GetRawTx(resv.CloseTx.TxID())
	if err != nil {
		return nil, err
	}
	tx, err := DecodeMsgTx(raw)
	if err != nil || tx.TxID() != resv.CloseTx.TxID() {
		return nil, fmt.Errorf("native force-close canonical transaction is unavailable")
	}
	return tx, nil
}

func (p *Manager) nativeForceCloseConfirmedLocked(resv *ClosingReservation) error {
	tx, err := p.confirmedNativeCloseTransaction(resv)
	if err != nil {
		return err
	}
	if err := p.ImportSTPChannelSettlement(tx, resv.AssetProofs); err != nil {
		return err
	}
	height, err := p.GetIndexerRPCClient().GetTxHeight(tx.TxID())
	if err != nil || height <= 0 {
		return fmt.Errorf("native force-close confirmation height is unavailable: %w", err)
	}
	next := resv.ClosingDataInDB
	next.CloseHeight = height
	if err := p.persistNativeForceCloseLocked(resv, next, CS_CLOSE_FORCELY_CONFIRMED); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_CHANNEL_FORCELY_CLOSED, tx.TxID())
	p.updateOperationLogByReservationBestEffort(RESV_TYPE_CLOSE, resv.Id, OperationLogUpdate{
		Status: OperationLogRunning, Message: "Commitment confirmed; waiting for CSV delay before sweep",
		Details: map[string]string{"commit_txid": tx.TxID(), "close_height": strconv.Itoa(height), "csv_delay": strconv.Itoa(int(resv.Channel.CsvDelay))},
	})
	return nil
}

func (p *Manager) nativeForceCloseCompletedLocked(resv *ClosingReservation) error {
	if err := p.importSTPNativePreparedFunding(resv.Channel); err != nil {
		return err
	}
	tx, err := p.confirmedNativeCloseTransaction(resv)
	if err != nil {
		return err
	}
	if !resv.IsInitiator {
		if err := p.importPeerNativeForceCloseLocked(resv, tx); err != nil {
			return err
		}
	}
	if err := p.ImportSTPChannelSettlement(tx, resv.AssetProofs); err != nil {
		return err
	}
	if err := p.persistNativeForceCloseLocked(resv, resv.ClosingDataInDB, CS_CLOSED_FORCELY); err != nil {
		return err
	}
	p.SendMessageToUpper(MSG_CHANNEL_SWEPT, tx.TxID())
	p.updateOperationLogByReservationBestEffort(RESV_TYPE_CLOSE, resv.Id, OperationLogUpdate{
		Status: OperationLogSucceeded, Message: "Force close completed; funds are spendable", TxID: tx.TxID(),
		Result: map[string]string{"channel_id": resv.ChannelId, "txid": tx.TxID()},
	})
	p.DelResvWithId(resv.Id)
	if tower := p.GetWatchTower(); tower != nil {
		return tower.CleanAllCommitTx(resv.ChannelId)
	}
	return nil
}

// The peer's stored deanchor can contain only our signature. Recover the
// canonical signed transaction before exporting its locator to the public
// pool. Never broadcast that partial transaction or treat mempool presence as
// proof that the mapped supply was actually burnt.
func (p *Manager) importPeerNativeForceCloseLocked(resv *ClosingReservation, actual *wire.MsgTx) error {
	remote := resv.Channel.RemoteCommitment
	if remote == nil || remote.CommitTx == nil || remote.CommitTx.TxID() != actual.TxID() {
		return fmt.Errorf("peer native force-close commitment changed")
	}
	if remote.DeAnchorTx != nil {
		rpc := p.GetIndexerRPCClient_SatsNet()
		if rpc == nil || !rpc.IsTxConfirmed(remote.DeAnchorTx.TxID()) {
			return fmt.Errorf("peer native force-close descending transaction is awaiting confirmation")
		}
		raw, err := rpc.GetRawTx(remote.DeAnchorTx.TxID())
		if err != nil {
			return err
		}
		confirmed, err := DecodeMsgTx_SatsNet(raw)
		if err != nil || confirmed.TxID() != remote.DeAnchorTx.TxID() {
			return fmt.Errorf("peer native force-close descending transaction is unavailable")
		}
		next := resv.ClosingDataInDB
		next.DeAnchorTx = confirmed
		if err := p.persistNativeForceCloseLocked(resv, next, resv.Channel.Status); err != nil {
			return err
		}
	}
	if err := p.ImportSTPChannelSettlement(actual, remote.AssetProofs); err != nil {
		return err
	}
	// Server commitments can return client assets through a distinct signed
	// other-sweep. Its actual confirmed witness and proof are a separate receipt;
	// importing the commitment alone must not complete the client's recovery.
	for _, other := range remote.Others {
		if other == nil || other.CommitTx == nil || len(other.AssetProofs) == 0 {
			continue
		}
		if !p.GetIndexerRPCClient().IsTxConfirmed(other.CommitTx.TxID()) {
			return fmt.Errorf("peer native other-sweep is awaiting confirmation")
		}
		raw, err := p.GetIndexerRPCClient().GetRawTx(other.CommitTx.TxID())
		if err != nil {
			return err
		}
		confirmed, err := DecodeMsgTx(raw)
		if err != nil || confirmed.TxID() != other.CommitTx.TxID() {
			return fmt.Errorf("peer native other-sweep canonical transaction is unavailable")
		}
		if err := p.ImportSTPChannelSettlement(confirmed, other.AssetProofs); err != nil {
			return err
		}
	}
	return nil
}

// RetryNativeForceClose resumes only the exact durable package. It never
// recreates a broadcast sweep with a new locktime. Server CSV signing remains
// with the server/bootstrap signer; its completed package must then be handed
// to HandleNativeForceCloseSweep before the first broadcast.
func (p *Manager) RetryNativeForceClose(resv *ClosingReservation) (bool, error) {
	if !nativeForceClose(resv) {
		return false, nil
	}
	resv.Lock()
	defer resv.Unlock()
	resv.Channel.Mutex.Lock()
	defer resv.Channel.Mutex.Unlock()
	switch resv.Channel.Status {
	case CS_CLOSE_FORCELY_BROADCASTED:
		err := p.broadcastNativeForceCloseLocked(resv)
		if p.GetIndexerRPCClient() != nil && p.GetIndexerRPCClient().IsTxConfirmed(resv.CloseTx.TxID()) {
			return true, p.nativeForceCloseConfirmedLocked(resv)
		}
		return true, err
	case CS_CLOSE_FORCELY_CONFIRMED:
		if !resv.Channel.IsInitiator {
			return false, nil
		}
		if p.GetIndexerRPCClient() == nil {
			return true, fmt.Errorf("native force-close Bitcoin client is unavailable")
		}
		height := p.GetIndexerRPCClient().GetSyncHeight()
		if height-resv.CloseHeight <= int(resv.Channel.CsvDelay) {
			return true, nil
		}
		pkg, err := p.BuildSignedSweepTxForClient(resv.Channel, height, resv.FeeRate)
		if err != nil {
			return true, err
		}
		return true, p.handleNativeForceCloseSweepLocked(resv, pkg)
	case CS_CLOSE_FORCELY_SWEEP_BROADCASTED, CS_CLOSE_FORCELY_SWEEP_CONFIRMED:
		err := p.broadcastNativeForceCloseLocked(resv)
		if p.GetIndexerRPCClient() != nil && p.GetIndexerRPCClient().IsTxConfirmed(resv.CloseTx.TxID()) {
			return true, p.nativeForceCloseCompletedLocked(resv)
		}
		return true, err
	case CS_CLOSED_FORCELY:
		return true, nil
	default:
		return false, nil
	}
}

// HandleNativeForceCloseSweep commits a client or co-signed server recovery
// package before broadcasting. A true handled result excludes the legacy path,
// including when persistence, confirmation or native receipt import fails.
func (p *Manager) HandleNativeForceCloseSweep(resv *ClosingReservation, pkg *SignedSweepTxPackage) (bool, error) {
	if !nativeForceClose(resv) {
		return false, nil
	}
	resv.Lock()
	defer resv.Unlock()
	resv.Channel.Mutex.Lock()
	defer resv.Channel.Mutex.Unlock()
	return true, p.handleNativeForceCloseSweepLocked(resv, pkg)
}

func (p *Manager) handleNativeForceCloseSweepLocked(resv *ClosingReservation, pkg *SignedSweepTxPackage) error {
	if pkg == nil || pkg.SweepTx == nil {
		if resv.Channel.Status != CS_CLOSE_FORCELY_CONFIRMED {
			return fmt.Errorf("native force-close has no confirmed sweep source")
		}
		return p.nativeForceCloseCompletedLocked(resv)
	}
	if resv.Channel.Status != CS_CLOSE_FORCELY_CONFIRMED && resv.Channel.Status != CS_CLOSE_FORCELY_SWEEP_BROADCASTED {
		return fmt.Errorf("native force-close is not ready for a sweep")
	}
	if !pkg.Signed || !pkg.Verified || !pkg.Broadcastable || pkg.PrevFetcher == nil {
		return fmt.Errorf("native force-close sweep is not fully signed and verified")
	}
	if err := VerifySignedTx(pkg.SweepTx, pkg.PrevFetcher); err != nil {
		return err
	}
	for name, raw := range pkg.NativeProofs {
		protocol, ok := stpAssetProtocols.byName[name].(STPAssetChannelProtocol)
		if !ok || len(raw) == 0 {
			return fmt.Errorf("unknown native force-close recovery proof %s", name)
		}
		if _, err := protocol.VerifyChannelProof(p, nil, pkg.SweepTx, raw, STPOpForceClose); err != nil {
			return err
		}
	}
	next := resv.ClosingDataInDB
	next.CloseTx, next.PreTxs = pkg.SweepTx, pkg.Txs
	next.AssetProofs = pkg.NativeProofs
	if err := p.persistNativeForceCloseLocked(resv, next, CS_CLOSE_FORCELY_SWEEP_BROADCASTED); err != nil {
		return err
	}
	return p.broadcastNativeForceCloseLocked(resv)
}
