package lightnode

import (
	"bytes"
	"errors"
	"reflect"
	"testing"

	"github.com/sat20-labs/indexer/common"
)

func testScanEntries() []scanEntry {
	return []scanEntry{
		{key: []byte("a-3"), value: []byte("value-a-3")},
		{key: []byte("b-1"), value: []byte("value-b-1")},
		{key: []byte("a-1"), value: []byte("value-a-1")},
		{key: []byte("a-2"), value: []byte("value-a-2")},
	}
}

func TestScanSnapshotContract(t *testing.T) {
	tests := []struct {
		name    string
		options common.ScanOptions
		want    []string
	}{
		{name: "forward prefix", options: common.ScanOptions{Prefix: []byte("a-")}, want: []string{"a-1", "a-2", "a-3"}},
		{name: "reverse prefix", options: common.ScanOptions{Prefix: []byte("a-"), Reverse: true}, want: []string{"a-3", "a-2", "a-1"}},
		{name: "forward inclusive", options: common.ScanOptions{Prefix: []byte("a-"), Start: []byte("a-2"), StartInclusive: true}, want: []string{"a-2", "a-3"}},
		{name: "forward exclusive", options: common.ScanOptions{Prefix: []byte("a-"), Start: []byte("a-2")}, want: []string{"a-3"}},
		{name: "reverse inclusive", options: common.ScanOptions{Prefix: []byte("a-"), Start: []byte("a-2"), StartInclusive: true, Reverse: true}, want: []string{"a-2", "a-1"}},
		{name: "reverse exclusive", options: common.ScanOptions{Prefix: []byte("a-"), Start: []byte("a-2"), Reverse: true}, want: []string{"a-1"}},
		{name: "limit", options: common.ScanOptions{Prefix: []byte("a-"), Limit: 2}, want: []string{"a-1", "a-2"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var got []string
			err := scanSnapshot(testScanEntries(), test.options, func(k, _ []byte) error {
				got = append(got, string(k))
				return nil
			})
			if err != nil {
				t.Fatalf("scanSnapshot: %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("keys=%v, want %v", got, test.want)
			}
		})
	}
}

func TestScanSnapshotKeysOnlyStopErrorAndOwnership(t *testing.T) {
	entries := testScanEntries()
	var firstKey, firstValue []byte
	err := scanSnapshot(entries, common.ScanOptions{
		Prefix: []byte("a-"), CopyKey: true, CopyValue: true,
	}, func(k, v []byte) error {
		if firstKey == nil {
			firstKey, firstValue = k, v
		}
		return nil
	})
	if err != nil {
		t.Fatalf("owned scanSnapshot: %v", err)
	}
	for i := range entries {
		clear(entries[i].key)
		clear(entries[i].value)
	}
	if !bytes.Equal(firstKey, []byte("a-1")) || !bytes.Equal(firstValue, []byte("value-a-1")) {
		t.Fatalf("owned bytes changed: key=%q value=%q", firstKey, firstValue)
	}

	count := 0
	err = scanSnapshot(testScanEntries(), common.ScanOptions{Prefix: []byte("a-"), KeysOnly: true}, func(_ []byte, v []byte) error {
		if v != nil {
			t.Fatalf("KeysOnly value=%q, want nil", v)
		}
		count++
		return common.ErrStopScan
	})
	if err != nil || count != 1 {
		t.Fatalf("stop result: count=%d err=%v", count, err)
	}

	wantErr := errors.New("callback failed")
	err = scanSnapshot(testScanEntries(), common.ScanOptions{}, func(_, _ []byte) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("callback error=%v, want %v", err, wantErr)
	}
}
