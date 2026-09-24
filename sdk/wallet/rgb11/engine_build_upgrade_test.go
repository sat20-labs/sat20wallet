package rgb11wallet

import "testing"

func TestStableEngineReadsRC11RecoveryAndRewritesStableID(t *testing.T) {
	old := &RecoveryPackage{
		Version: RecoveryPackageVersion, WalletID: "upgrade-wallet",
		EngineBuildID: legacyRC11EngineBuildID,
	}
	encoded, err := EncodeRecoveryPackage(old)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeRecoveryPackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	oldSnapshot, err := decoded.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	current := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: oldSnapshot.WalletID,
		EngineBuildID: NativeEngineBuildID,
	}
	merged, err := MergeRecoverySnapshot(current, oldSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if merged.EngineBuildID != NativeEngineBuildID {
		t.Fatalf("merge rc.11 recovery into stable wallet: id=%q", merged.EngineBuildID)
	}

	unknown := *old
	unknown.EngineBuildID = "rgb11-go-unknown"
	if err := ValidateRecoveryPackage(&unknown); err == nil {
		t.Fatal("unknown engine build was accepted")
	}
}

func TestStableEngineReadsRC11ActiveRecovery(t *testing.T) {
	old := &ActiveRecoveryPackage{
		Version: ActiveRecoveryPackageVersion, WalletID: "upgrade-wallet",
		EngineBuildID: legacyRC11EngineBuildID,
	}
	encoded, err := EncodeActiveRecoveryPackage(old)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeActiveRecoveryPackage(encoded)
	if err != nil {
		t.Fatal(err)
	}
	oldSnapshot, err := decoded.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	current := &RGB11WalletSnapshot{
		Version: WalletSnapshotVersion, WalletID: oldSnapshot.WalletID,
		EngineBuildID: NativeEngineBuildID,
	}
	merged, err := MergeActiveRecoverySnapshot(current, oldSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if merged.EngineBuildID != NativeEngineBuildID {
		t.Fatalf("merge rc.11 active recovery into stable wallet: id=%q", merged.EngineBuildID)
	}

	unknown := *old
	unknown.EngineBuildID = "rgb11-go-unknown"
	if err := ValidateActiveRecoveryPackage(&unknown); err == nil {
		t.Fatal("unknown active engine build was accepted")
	}
}
