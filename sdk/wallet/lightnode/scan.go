package lightnode

import (
	"bytes"
	"errors"
	"sort"

	"github.com/sat20-labs/indexer/common"
)

type scanEntry struct {
	key   []byte
	value []byte
}

func scanSnapshot(entries []scanEntry, options common.ScanOptions, r func(k, v []byte) error) error {
	sort.SliceStable(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].key, entries[j].key) < 0
	})

	count := 0
	visit := func(entry scanEntry) (bool, error) {
		if len(options.Prefix) > 0 && !bytes.HasPrefix(entry.key, options.Prefix) {
			return false, nil
		}
		if len(options.Start) > 0 {
			cmp := bytes.Compare(entry.key, options.Start)
			if (!options.Reverse && (cmp < 0 || (cmp == 0 && !options.StartInclusive))) ||
				(options.Reverse && (cmp > 0 || (cmp == 0 && !options.StartInclusive))) {
				return false, nil
			}
		}

		key := entry.key
		if options.CopyKey {
			key = append([]byte(nil), key...)
		}
		var value []byte
		if !options.KeysOnly {
			value = entry.value
			if options.CopyValue {
				value = append([]byte(nil), value...)
			}
		}

		err := r(key, value)
		if err != nil {
			if errors.Is(err, common.ErrStopScan) {
				return true, nil
			}
			return false, err
		}
		count++
		return options.Limit > 0 && count >= options.Limit, nil
	}

	if options.Reverse {
		for i := len(entries) - 1; i >= 0; i-- {
			stop, err := visit(entries[i])
			if err != nil || stop {
				return err
			}
		}
		return nil
	}

	for i := range entries {
		stop, err := visit(entries[i])
		if err != nil || stop {
			return err
		}
	}
	return nil
}
