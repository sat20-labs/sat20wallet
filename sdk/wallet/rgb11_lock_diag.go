//go:build rgb11discard

package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
)

type RGB11LockDiagnostic struct {
	ManagerIdentity   string      `json:"manager_identity"`
	DBIdentity        string      `json:"db_identity"`
	DBType            string      `json:"db_type"`
	ConfigDB          string      `json:"config_db"`
	Env               string      `json:"env"`
	Chain             string      `json:"chain"`
	Mode              string      `json:"mode"`
	WalletID          int64       `json:"wallet_id"`
	SubAccount        uint32      `json:"sub_account"`
	RawKey            string      `json:"raw_key"`
	RawState          string      `json:"raw_state"`
	RawLength         int         `json:"raw_length"`
	RawHash           string      `json:"raw_hash,omitempty"`
	RawError          string      `json:"raw_error,omitempty"`
	RawLock           *LockedUtxo `json:"raw_lock,omitempty"`
	RawOwnerPresent   bool        `json:"raw_owner_present"`
	MarkerKey         string      `json:"marker_key"`
	MarkerState       string      `json:"marker_state"`
	MarkerLength      int         `json:"marker_length"`
	MarkerHash        string      `json:"marker_hash,omitempty"`
	MarkerError       string      `json:"marker_error,omitempty"`
	MarkerValue       int64       `json:"marker_value"`
	LockerNetwork     string      `json:"locker_network"`
	LockerIdentity    string      `json:"locker_identity"`
	LockerRefreshTime int64       `json:"locker_refresh_time"`
	LockerCount       int         `json:"locker_count"`
	LockerLock        *LockedUtxo `json:"locker_lock,omitempty"`
	LockerOwnerSet    bool        `json:"locker_owner_present"`
}

func (p *Manager) DiagnoseRGB11Lock(outpoint string) (*RGB11LockDiagnostic, error) {
	if p == nil || p.db == nil || p.utxoLockerL1 == nil {
		return nil, ErrRGB11Inconsistent
	}
	if strings.TrimSpace(outpoint) == "" || outpoint != rgb11StaleLockOnlyTarget.OutPoint {
		return nil, errors.New("diagnostic is restricted to the approved C/69fa lock")
	}
	diagnostic := &RGB11LockDiagnostic{
		ManagerIdentity: fmt.Sprintf("%p", p), DBIdentity: fmt.Sprintf("%p", p.db),
		DBType: fmt.Sprintf("%T", p.db), Env: _env, Chain: _chain, Mode: _mode,
		RawKey:    GetLockedUtxoKey(L1_NETWORK_BITCOIN, outpoint),
		MarkerKey: GeLastLockTimeKey(L1_NETWORK_BITCOIN),
	}
	if p.cfg != nil {
		diagnostic.ConfigDB = p.cfg.DB
	}
	if p.status != nil {
		diagnostic.WalletID = p.status.CurrentWallet
	}
	if p.wallet != nil {
		diagnostic.SubAccount = p.wallet.GetSubAccount()
	}
	readRGB11DiagValue(p.db, diagnostic.RawKey, func(raw []byte) error {
		diagnostic.RawState = "present"
		diagnostic.RawLength = len(raw)
		diagnostic.RawHash = rgb11DiagHash(raw)
		var lock LockedUtxo
		if err := DecodeFromBytes(raw, &lock); err != nil {
			return err
		}
		diagnostic.RawLock = &lock
		diagnostic.RawOwnerPresent = lock.Owner != nil
		return nil
	}, &diagnostic.RawState, &diagnostic.RawError)
	readRGB11DiagValue(p.db, diagnostic.MarkerKey, func(raw []byte) error {
		diagnostic.MarkerState = "present"
		diagnostic.MarkerLength = len(raw)
		diagnostic.MarkerHash = rgb11DiagHash(raw)
		return DecodeFromBytes(raw, &diagnostic.MarkerValue)
	}, &diagnostic.MarkerState, &diagnostic.MarkerError)

	locker := p.utxoLockerL1
	locker.mutex.RLock()
	diagnostic.LockerNetwork = locker.network
	diagnostic.LockerIdentity = fmt.Sprintf("%p", locker)
	diagnostic.LockerRefreshTime = locker.refreshTime
	diagnostic.LockerCount = len(locker.lockmap)
	diagnostic.LockerLock = cloneLockedUtxo(locker.lockmap[outpoint])
	diagnostic.LockerOwnerSet = diagnostic.LockerLock != nil && diagnostic.LockerLock.Owner != nil
	locker.mutex.RUnlock()
	return diagnostic, nil
}

func readRGB11DiagValue(database indexer.KVDB, key string, decode func([]byte) error,
	state, errorText *string) {
	raw, err := database.Read([]byte(key))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		*state = "absent"
		return
	}
	if err != nil {
		*state = "error"
		*errorText = err.Error()
		return
	}
	if err := decode(raw); err != nil {
		*state = "decode_error"
		*errorText = err.Error()
	}
}

func rgb11DiagHash(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
