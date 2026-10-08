//go:build js && wasm

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	walletsdk "github.com/sat20-labs/sat20wallet/sdk/wallet"
)

func TestAccountSessionCleanupClearsRecoveryAndRGBMaterial(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "release", true: "expired"}[expired], func(t *testing.T) {
			clearAccountSessions()
			secret, private, rgb := []byte{1, 2, 3}, []byte{4, 5, 6}, []byte{7, 8, 9}
			share := &account.RecoveryShare{Data: "private-share"}
			backup := &account.Backup{Wallets: []account.WalletBackup{{Mnemonic: "private-mnemonic"}}}
			managed := &walletsdk.RecoveredAccountManagementState{
				State:       account.ManagedState{Wallets: []account.ManagedWallet{{Mnemonic: "private-mnemonic"}}},
				ManagedData: account.ManagedDataBundle{Items: []account.ManagedDataItem{{Provider: "rgb11", Payload: rgb}}},
			}
			expires := time.Now().Add(time.Hour)
			if expired {
				expires = time.Now().Add(-time.Second)
			}
			session := &accountRecoverySession{Secret: secret, RequestPrivate: private,
				DKVSShare: share, Backup: backup, ManagedState: managed, ExpiresAt: expires}
			activation := &accountActivationSession{Package: &account.RecoveryPackage{UserShare: account.RecoveryShare{Data: "private-share"}},
				GuardianShare: share, RequestPrivate: private, ExpiresAt: expires}
			accountSessions.recovery["recovery"], accountSessions.activation["activation"] = session, activation
			if expired {
				accountSessions.Lock()
				accountCleanupSessions()
				accountSessions.Unlock()
			} else {
				clearAccountSessions()
			}
			if len(accountSessions.recovery) != 0 || len(accountSessions.activation) != 0 ||
				!bytes.Equal(secret, make([]byte, 3)) || !bytes.Equal(private, make([]byte, 3)) || !bytes.Equal(rgb, make([]byte, 3)) ||
				share.Data != "" || backup.Wallets[0].Mnemonic != "" || managed.State.Wallets[0].Mnemonic != "" ||
				activation.Package.UserShare.Data != "" || session.ManagedState != nil {
				t.Fatal("expired/released session retained account or RGB11 recovery material")
			}
		})
	}
}

func TestAbortingPreviewDoesNotCorruptConfirmedRecoverySnapshot(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	id := strings.Repeat("a", 64)
	state := account.ManagedState{Version: account.ManagedStateVersion, Revision: 1,
		RootFingerprint: strings.Repeat("1", 64), Wallets: []account.ManagedWallet{{
			Fingerprint: strings.Repeat("1", 64), Revision: 1, Name: "Root",
			Mnemonic:     "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
			AccountCount: 1, SubAccounts: []account.SubAccount{{Index: 0, Name: "Account 1"}},
		}}}
	envelope, err := account.SealManagedState(secret, id, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	preview := &accountRecoverySession{Secret: secret, ManagedState: &walletsdk.RecoveredAccountManagementState{
		State: state, Envelope: envelope, ManagedData: account.ManagedDataBundle{
			Version: account.ManagedDataBundleVersion, Revision: 1,
			Items: []account.ManagedDataItem{{Provider: "rgb11", Scope: "root/0", Payload: []byte("private proof")}},
		},
	}}
	snapshot, err := snapshotAccountRecoveryForCommit(preview)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountRecoverySession(snapshot)
	clearAccountRecoverySession(preview)
	decoded, err := account.OpenManagedState(snapshot.Secret, id, snapshot.ManagedState.Envelope)
	if err != nil || decoded.Wallets[0].Mnemonic != snapshot.ManagedState.State.Wallets[0].Mnemonic ||
		string(snapshot.ManagedState.ManagedData.Items[0].Payload) != "private proof" {
		t.Fatal("canceling a preview corrupted the confirmed account/RGB11 recovery snapshot")
	}
}
