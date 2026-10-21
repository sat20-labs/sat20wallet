package wallet

import "testing"

func TestCoreManagedRemoteRecordsDetectEveryFingerprintField(t *testing.T) {
	before := map[string]coreManagedRecordFingerprint{"record": {seq: 1, hash: "record-hash", digest: "content-hash", bytes: 10}}
	for _, field := range []string{"unchanged", "seq", "hash", "digest", "bytes", "added", "removed"} {
		t.Run(field, func(t *testing.T) {
			after := map[string]coreManagedRecordFingerprint{"record": before["record"]}
			value := after["record"]
			switch field {
			case "seq":
				value.seq++
			case "hash":
				value.hash = "other-record-hash"
			case "digest":
				value.digest = "other-content-hash"
			case "bytes":
				value.bytes++
			case "added":
				after["other"] = value
			case "removed":
				delete(after, "record")
			}
			if field != "removed" {
				after["record"] = value
			}
			if equal := coreManagedRemoteRecordsEqual(before, after); equal != (field == "unchanged") {
				t.Fatalf("fingerprint field %s: equality=%v", field, equal)
			}
		})
	}
}
