package rgb11wallet

import "testing"

func TestRecoveryRequiresCurrentEngineBuild(t *testing.T) {
	for _, build := range []string{NativeEngineBuildID, "rgb11-go-0.11.1-rc.11+sat20.1", "rgb11-go-unknown"} {
		t.Run(build, func(t *testing.T) {
			stable := &RecoveryPackage{Version: RecoveryPackageVersion, WalletID: "current-engine-wallet", EngineBuildID: build}
			active := &ActiveRecoveryPackage{Version: ActiveRecoveryPackageVersion, WalletID: stable.WalletID, EngineBuildID: build}
			encoded, stableErr := EncodeRecoveryPackage(stable)
			activeEncoded, activeErr := EncodeActiveRecoveryPackage(active)
			if build != NativeEngineBuildID {
				if stableErr == nil || activeErr == nil {
					t.Fatal("an obsolete or unknown engine build was accepted")
				}
				return
			}
			if stableErr != nil || activeErr != nil {
				t.Fatalf("current engine rejected: stable=%v active=%v", stableErr, activeErr)
			}
			if _, err := DecodeRecoveryPackage(encoded); err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeActiveRecoveryPackage(activeEncoded); err != nil {
				t.Fatal(err)
			}
		})
	}
}
