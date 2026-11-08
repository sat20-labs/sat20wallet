package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11HistRecord struct {
	class string
	cat   string
	key   string
	value []byte
}

type rgb11HistProof struct {
	KeyHash       string          `json:"key_hash"`
	BeforeDecoded bool            `json:"before_decoded"`
	AfterDecoded  bool            `json:"after_decoded"`
	FieldsEqual   map[string]bool `json:"fields_equal"`
}

type rgb11HistGroup struct {
	Group       string           `json:"group"`
	BeforeCount int              `json:"before_count"`
	AfterCount  int              `json:"after_count"`
	BeforeHash  string           `json:"before_hash"`
	AfterHash   string           `json:"after_hash"`
	HashEqual   bool             `json:"hash_equal"`
	Fields      []string         `json:"changed_field_names"`
	Added       int              `json:"added_records"`
	Removed     int              `json:"removed_records"`
	Changed     int              `json:"changed_records"`
	Proofs      []rgb11HistProof `json:"changed_proofs,omitempty"`
}

type rgb11HistAudit struct {
	BeforeProjection int              `json:"before_projection_records"`
	AfterProjection  int              `json:"after_projection_records"`
	BeforeFrozen     int              `json:"before_frozen_records"`
	AfterFrozen      int              `json:"after_frozen_records"`
	BeforeEngine     int              `json:"before_engine_records"`
	AfterEngine      int              `json:"after_engine_records"`
	BeforeSplitError bool             `json:"before_split_error"`
	AfterSplitError  bool             `json:"after_split_error"`
	Groups           []rgb11HistGroup `json:"groups"`
}

type rgb11HistLock struct {
	OutpointHash    string          `json:"outpoint_hash"`
	Target          bool            `json:"target"`
	BeforePresent   bool            `json:"before_present"`
	AfterPresent    bool            `json:"after_present"`
	BeforeReason    string          `json:"before_reason,omitempty"`
	AfterReason     string          `json:"after_reason,omitempty"`
	BeforeOwner     bool            `json:"before_owner"`
	AfterOwner      bool            `json:"after_owner"`
	FieldsEqual     map[string]bool `json:"fields_equal"`
	OwnerEqual      map[string]bool `json:"owner_fields_equal"`
	DependencyValid bool            `json:"dependency_decoded"`
	InClosure       bool            `json:"in_target_closure"`
	OwnedCarrier    bool            `json:"owned_carrier"`
	Relations       []string        `json:"relations"`
}

func rgb11HistCategory(key string, engine bool) string {
	if engine {
		parts := strings.SplitN(key, "/", 3)
		if len(parts) > 1 {
			return "engine/" + parts[0] + "/" + parts[1]
		}
		return "engine/other"
	}
	for _, prefix := range []string{"prepared-receive", "receive-reservation", "receive-key", "ticker-ref"} {
		if key == prefix || strings.HasPrefix(key, prefix+"-") {
			return "projection/" + prefix
		}
	}
	if index := strings.IndexByte(key, '-'); index > 0 {
		return "projection/" + key[:index]
	}
	return "projection/other"
}

func rgb11HistRecords(snapshot *RGB11WalletSnapshot) (map[string]map[string][]byte, int, error) {
	if snapshot == nil {
		return nil, 0, rgb11wallet.ErrValidationReceipt
	}
	classes := make(map[string]string, len(snapshot.ProjectionRecords))
	frozenCount := 0
	view, err := rgb11wallet.SplitLocalProjection(snapshot.ProjectionRecords)
	if err == nil && view != nil {
		for _, record := range view.Frozen {
			classes[record.Key] = "frozen"
			frozenCount++
		}
	}
	groups := make(map[string]map[string][]byte)
	add := func(record rgb11wallet.SnapshotRecord, class string, engine bool) {
		group := class + "/" + rgb11HistCategory(record.Key, engine)
		if groups[group] == nil {
			groups[group] = make(map[string][]byte)
		}
		groups[group][record.Key] = append([]byte(nil), record.Value...)
	}
	for _, record := range snapshot.ProjectionRecords {
		class := classes[record.Key]
		if class == "" {
			class = "local"
		}
		add(record, class, false)
	}
	for _, record := range snapshot.EngineRecords {
		add(record, "engine", true)
	}
	return groups, frozenCount, err
}

func rgb11HistHash(records map[string][]byte) string {
	keys := make([]string, 0, len(records))
	for key := range records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	hash := sha256.New()
	for _, key := range keys {
		_, _ = hash.Write([]byte(key))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(records[key])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Decode the exact accepted R11R record with the public codec, never JSON.
// Only field names/equality and a key hash leave this diagnostic.
func rgb11HistProofDiff(key string, before, after []byte) rgb11HistProof {
	hash := sha256.Sum256([]byte(key))
	diff := rgb11HistProof{KeyHash: hex.EncodeToString(hash[:]), FieldsEqual: make(map[string]bool)}
	left, leftErr := rgb11wallet.ProofsFromProjectionSnapshot([]rgb11wallet.SnapshotRecord{{Key: key, Value: before}})
	right, rightErr := rgb11wallet.ProofsFromProjectionSnapshot([]rgb11wallet.SnapshotRecord{{Key: key, Value: after}})
	diff.BeforeDecoded = leftErr == nil && len(left) == 1
	diff.AfterDecoded = rightErr == nil && len(right) == 1
	if !diff.BeforeDecoded || !diff.AfterDecoded {
		return diff
	}
	oldProof, newProof := reflect.ValueOf(*left[0]), reflect.ValueOf(*right[0])
	for index := 0; index < oldProof.NumField(); index++ {
		name := oldProof.Type().Field(index).Name
		diff.FieldsEqual[name] = reflect.DeepEqual(oldProof.Field(index).Interface(), newProof.Field(index).Interface())
	}
	return diff
}

func rgb11HistChangedFields(before, after []byte) []string {
	var left, right map[string]json.RawMessage
	if json.Unmarshal(before, &left) != nil || json.Unmarshal(after, &right) != nil {
		return []string{"<opaque>"}
	}
	fields := make(map[string]bool)
	for name, value := range left {
		if next, ok := right[name]; !ok || !bytes.Equal(value, next) {
			fields[name] = true
		}
	}
	for name, value := range right {
		if previous, ok := left[name]; !ok || !bytes.Equal(value, previous) {
			fields[name] = true
		}
	}
	result := make([]string, 0, len(fields))
	for name := range fields {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func rgb11HistSnapshotDiff(before, after *RGB11WalletSnapshot) rgb11HistAudit {
	left, beforeFrozen, beforeErr := rgb11HistRecords(before)
	right, afterFrozen, afterErr := rgb11HistRecords(after)
	audit := rgb11HistAudit{BeforeFrozen: beforeFrozen, AfterFrozen: afterFrozen,
		BeforeSplitError: beforeErr != nil, AfterSplitError: afterErr != nil}
	if before != nil {
		audit.BeforeProjection, audit.BeforeEngine = len(before.ProjectionRecords), len(before.EngineRecords)
	}
	if after != nil {
		audit.AfterProjection, audit.AfterEngine = len(after.ProjectionRecords), len(after.EngineRecords)
	}
	if beforeErr != nil || afterErr != nil {
		return audit
	}
	groups := make(map[string]bool, len(left)+len(right))
	for name := range left {
		groups[name] = true
	}
	for name := range right {
		groups[name] = true
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		oldRows, newRows := left[name], right[name]
		if oldRows == nil {
			oldRows = map[string][]byte{}
		}
		if newRows == nil {
			newRows = map[string][]byte{}
		}
		diff := rgb11HistGroup{Group: name, BeforeCount: len(oldRows), AfterCount: len(newRows),
			BeforeHash: rgb11HistHash(oldRows), AfterHash: rgb11HistHash(newRows)}
		diff.HashEqual = diff.BeforeHash == diff.AfterHash
		fields := make(map[string]bool)
		for key, oldValue := range oldRows {
			newValue, ok := newRows[key]
			if !ok {
				diff.Removed++
				fields["<record_removed>"] = true
				continue
			}
			if !bytes.Equal(oldValue, newValue) {
				diff.Changed++
				if strings.HasPrefix(key, "proof-") {
					proof := rgb11HistProofDiff(key, oldValue, newValue)
					diff.Proofs = append(diff.Proofs, proof)
					if !proof.BeforeDecoded || !proof.AfterDecoded {
						fields["<proof_decode_error>"] = true
					}
					for field, equal := range proof.FieldsEqual {
						if !equal {
							fields[field] = true
						}
					}
				} else {
					for _, field := range rgb11HistChangedFields(oldValue, newValue) {
						fields[field] = true
					}
				}
			}
		}
		for key := range newRows {
			if _, ok := oldRows[key]; !ok {
				diff.Added++
				fields["<record_added>"] = true
			}
		}
		for field := range fields {
			diff.Fields = append(diff.Fields, field)
		}
		sort.Strings(diff.Fields)
		sort.Slice(diff.Proofs, func(i, j int) bool { return diff.Proofs[i].KeyHash < diff.Proofs[j].KeyHash })
		audit.Groups = append(audit.Groups, diff)
	}
	return audit
}

// Compute diagnostic membership from the actual immutable journal graph. This
// does not authorize any lock change and performs no live DB/evidence reads.
func rgb11HistDependency(snapshot *RGB11WalletSnapshot, target string) (map[string]bool, map[string]bool, bool) {
	points, owned := map[string]bool{target: true}, make(map[string]bool)
	if snapshot == nil {
		return points, owned, false
	}
	states, stateErr := rgb11wallet.TransfersFromProjectionSnapshot(snapshot.ProjectionRecords)
	outputs, outputErr := rgb11wallet.OutputsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if stateErr != nil || outputErr != nil {
		return points, owned, false
	}
	for point := range outputs {
		owned[point] = true
	}
	for changed := true; changed; {
		changed = false
		for _, state := range states {
			members := append(append([]string(nil), state.InputOutPoints...), state.OutputOutPoints...)
			affected := false
			for _, point := range members {
				affected = affected || points[point]
			}
			if affected {
				for _, point := range members {
					if !points[point] {
						points[point], changed = true, true
					}
				}
			}
		}
	}
	return points, owned, true
}

func rgb11HistLockDiff(before, after map[string]*LockedUtxo, target string,
	snapshot *RGB11WalletSnapshot, history ...*rgb11wallet.PendingTransfer) []rgb11HistLock {
	closure, owned, valid := rgb11HistDependency(snapshot, target)
	keys := make(map[string]bool, len(before)+len(after))
	for point := range before {
		keys[point] = true
	}
	for point := range after {
		keys[point] = true
	}
	points := make([]string, 0, len(keys))
	for point := range keys {
		points = append(points, point)
	}
	sort.Strings(points)
	changes := make([]rgb11HistLock, 0)
	for _, point := range points {
		oldLock, newLock := before[point], after[point]
		if reflect.DeepEqual(oldLock, newLock) {
			continue
		}
		hash := sha256.Sum256([]byte(point))
		diff := rgb11HistLock{OutpointHash: hex.EncodeToString(hash[:]), Target: point == target,
			BeforePresent: oldLock != nil, AfterPresent: newLock != nil,
			FieldsEqual: make(map[string]bool), OwnerEqual: make(map[string]bool),
			DependencyValid: valid, InClosure: closure[point], OwnedCarrier: owned[point]}
		var oldValue, newValue LockedUtxo
		if oldLock != nil {
			oldValue, diff.BeforeReason = *oldLock, oldLock.Reason
		}
		if newLock != nil {
			newValue, diff.AfterReason = *newLock, newLock.Reason
		}
		left, right := reflect.ValueOf(oldValue), reflect.ValueOf(newValue)
		for index := 0; index < left.NumField(); index++ {
			name := left.Type().Field(index).Name
			diff.FieldsEqual[name] = oldLock != nil && newLock != nil && reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface())
		}
		diff.BeforeOwner, diff.AfterOwner = oldValue.Owner != nil, newValue.Owner != nil
		var oldOwner, newOwner UtxoLockOwner
		if oldValue.Owner != nil {
			oldOwner = *oldValue.Owner
		}
		if newValue.Owner != nil {
			newOwner = *newValue.Owner
		}
		left, right = reflect.ValueOf(oldOwner), reflect.ValueOf(newOwner)
		for index := 0; index < left.NumField(); index++ {
			name := left.Type().Field(index).Name
			diff.OwnerEqual[name] = diff.BeforeOwner == diff.AfterOwner && reflect.DeepEqual(left.Field(index).Interface(), right.Field(index).Interface())
		}
		for index, pending := range history {
			if pending == nil {
				continue
			}
			name := "first"
			if index > 0 {
				name = "second"
			}
			for _, input := range pending.State.InputOutPoints {
				if input == point {
					diff.Relations = append(diff.Relations, name+".input")
				}
			}
			for _, output := range pending.State.OutputOutPoints {
				if output == point {
					diff.Relations = append(diff.Relations, name+".output")
				}
			}
		}
		changes = append(changes, diff)
	}
	return changes
}

func rgb11HistJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func rgb11HistReadSnapshot(manager *rgb11Manager) (*RGB11WalletSnapshot, error) {
	if manager == nil {
		return nil, rgb11wallet.ErrValidationReceipt
	}
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		return nil, err
	}
	return manager.readRGB11Local(walletID)
}
