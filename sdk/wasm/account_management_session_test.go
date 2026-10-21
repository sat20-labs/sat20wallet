//go:build js && wasm

package main

import (
	"bytes"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	walletsdk "github.com/sat20-labs/sat20wallet/sdk/wallet"
)

func TestAccountCommitRecoveryDistinguishesExpiredPreview(t *testing.T) {
	previousManager := _mgr
	_mgr = &walletsdk.Manager{}
	defer func() { clearAccountSessions(); _mgr = previousManager }()
	cases := []struct {
		name          string
		seedSession   bool
		abortFirst    bool
		commitRetries int
		want          string
	}{
		{name: "missing", want: "recovery session expired", commitRetries: 1},
		{name: "aborted", seedSession: true, abortFirst: true, want: "recovery session expired", commitRetries: 1},
		{name: "aborted-reuse", seedSession: true, abortFirst: true, want: "recovery session expired", commitRetries: 2},
		{name: "not-previewed", seedSession: true, want: "account recovery preview is required", commitRetries: 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			clearAccountSessions()
			var secret []byte
			var backup *account.Backup
			if testCase.seedSession {
				session := &accountRecoverySession{Package: &account.RecoveryPackage{}}
				if testCase.abortFirst {
					secret = bytes.Repeat([]byte{7}, 32)
					backup = &account.Backup{Wallets: []account.WalletBackup{{Mnemonic: "test-only recovery material"}}}
					session.Secret = secret
					session.Backup = backup
				}
				accountSessions.Lock()
				accountSessions.recovery["test-session"] = session
				accountSessions.Unlock()
			}
			if testCase.abortFirst {
				accountAbortSession(js.Undefined(), []js.Value{js.ValueOf(`{"session_id":"test-session"}`)})
				if !bytes.Equal(secret, make([]byte, len(secret))) || backup.Wallets[0].Mnemonic != "" {
					t.Fatal("aborting a recovery session retained its secret or backup mnemonic")
				}
			}
			for attempt := 0; attempt < testCase.commitRetries; attempt++ {
				response := awaitAccountResult(t, accountCommitRecovery(js.Undefined(), []js.Value{
					js.ValueOf(`{"session_id":"test-session","password":"test-password"}`),
				}).(js.Value))
				if response.Get("code").Int() != -1 || response.Get("msg").String() != testCase.want {
					t.Errorf("commit attempt %d: code=%d msg=%q, want refusal %q", attempt+1,
						response.Get("code").Int(), response.Get("msg").String(), testCase.want)
				}
			}
		})
	}
}

func TestAccountSessionCleanupClearsRecoveryAndRGBMaterial(t *testing.T) {
	for _, action := range []string{"abort", "release"} {
		t.Run(action, func(t *testing.T) {
			clearAccountSessions()
			secret, recoveryPrivate, activationPrivate, rgb := []byte{1, 2, 3}, []byte{4, 5, 6}, []byte{10, 11, 12}, []byte{7, 8, 9}
			dkvsShare := &account.RecoveryShare{Data: "private-dkvs-share"}
			userShare := &account.RecoveryShare{Data: "private-user-share"}
			recoveryGuardianShare := &account.RecoveryShare{Data: "private-recovery-guardian-share"}
			activationGuardianShare := &account.RecoveryShare{Data: "private-activation-guardian-share"}
			backup := &account.Backup{Wallets: []account.WalletBackup{{Mnemonic: "private-mnemonic"}}}
			managed := &walletsdk.RecoveredAccountManagementState{
				State:       account.ManagedState{Wallets: []account.ManagedWallet{{Mnemonic: "private-mnemonic"}}},
				ManagedData: account.ManagedDataBundle{Items: []account.ManagedDataItem{{Provider: "rgb11", Payload: rgb}}},
			}
			session := &accountRecoverySession{Secret: secret, RequestPrivate: recoveryPrivate,
				DKVSShare: dkvsShare, UserShare: userShare, GuardianShare: recoveryGuardianShare,
				Backup: backup, ManagedState: managed}
			activation := &accountActivationSession{Package: &account.RecoveryPackage{UserShare: account.RecoveryShare{Data: "private-activation-user-share"}},
				GuardianShare: activationGuardianShare, RequestPrivate: activationPrivate}
			accountSessions.recovery["shared-session"], accountSessions.activation["shared-session"] = session, activation
			if action == "abort" {
				accountAbortSession(js.Undefined(), []js.Value{js.ValueOf(`{"session_id":"shared-session"}`)})
				// Releasing a missing session is idempotent and cannot restore its material.
				accountAbortSession(js.Undefined(), []js.Value{js.ValueOf(`{"session_id":"shared-session"}`)})
			} else {
				clearAccountSessions()
			}
			if len(accountSessions.recovery) != 0 || len(accountSessions.activation) != 0 ||
				!bytes.Equal(secret, make([]byte, 3)) || !bytes.Equal(recoveryPrivate, make([]byte, 3)) ||
				!bytes.Equal(activationPrivate, make([]byte, 3)) || !bytes.Equal(rgb, make([]byte, 3)) ||
				backup.Wallets[0].Mnemonic != "" || managed.State.Wallets[0].Mnemonic != "" ||
				activation.Package.UserShare.Data != "" || activationGuardianShare.Data != "" ||
				dkvsShare.Data != "" || userShare.Data != "" || recoveryGuardianShare.Data != "" || session.ManagedState != nil {
				t.Fatal("aborted/released session retained account or RGB11 recovery material")
			}
		})
	}
}

func TestAccountActivationSuccessCleanupClearsGuardianShare(t *testing.T) {
	clearAccountSessions()
	defer clearAccountSessions()
	requestPrivate := []byte{1, 2, 3}
	guardianShare := &account.RecoveryShare{Data: "private-guardian-share"}
	session := &accountActivationSession{
		Package:        &account.RecoveryPackage{UserShare: account.RecoveryShare{Data: "private-user-share"}},
		RequestPrivate: requestPrivate,
		GuardianShare:  guardianShare,
	}
	accountSessions.Lock()
	accountSessions.activation["successful-rehearsal"] = session
	// This is the cleanup used after accountRehearse successfully activates the
	// account. Keep guardianShare separately to catch a pointer dropped too early.
	clearAccountActivationSession(session)
	delete(accountSessions.activation, "successful-rehearsal")
	accountSessions.Unlock()

	if guardianShare.Data != "" || len(requestPrivate) != 3 || !bytes.Equal(requestPrivate, make([]byte, 3)) ||
		session.GuardianShare != nil || session.RequestPrivate != nil || session.Package.UserShare.Data != "" {
		t.Fatal("successful activation cleanup retained guardian or recovery material")
	}
}

func TestAccountActivationSessionRemainsUsableAfterLongIdle(t *testing.T) {
	clearAccountSessions()
	defer clearAccountSessions()
	const sessionID = "long-idle-activation"
	session := &accountActivationSession{Package: &account.RecoveryPackage{}}
	accountSessions.Lock()
	accountSessions.activation[sessionID] = session
	accountSessions.Unlock()
	advanceWASMWallClock(t, 25*time.Minute)

	response := awaitAccountResult(t, accountRehearse(js.Undefined(), []js.Value{
		js.ValueOf(`{"session_id":"long-idle-activation","password":""}`),
	}).(js.Value))
	if response.Get("code").Int() != -1 || response.Get("msg").String() != "wallet password is required" {
		t.Fatalf("long-idle activation session response code=%d msg=%q, want password refusal", response.Get("code").Int(), response.Get("msg").String())
	}
	accountSessions.Lock()
	remaining := accountSessions.activation[sessionID]
	accountSessions.Unlock()
	if remaining != session {
		t.Fatal("long-idle activation session was removed before rehearsal validation")
	}
}

func TestAccountRecoverySessionRemainsUsableAfterLongIdle(t *testing.T) {
	clearAccountSessions()
	defer clearAccountSessions()
	const sessionID = "long-idle-recovery"
	session := &accountRecoverySession{Package: &account.RecoveryPackage{}}
	accountSessions.Lock()
	accountSessions.recovery[sessionID] = session
	accountSessions.Unlock()
	advanceWASMWallClock(t, 25*time.Minute)

	response := awaitAccountResult(t, accountPreviewRecovery(js.Undefined(), []js.Value{
		js.ValueOf(`{"session_id":"long-idle-recovery"}`),
	}).(js.Value))
	if response.Get("code").Int() != -1 || response.Get("msg").String() != account.ErrInsufficientShares.Error() {
		t.Fatalf("long-idle recovery session response code=%d msg=%q, want insufficient-shares refusal", response.Get("code").Int(), response.Get("msg").String())
	}
	accountSessions.Lock()
	remaining := accountSessions.recovery[sessionID]
	accountSessions.Unlock()
	if remaining != session {
		t.Fatal("long-idle recovery session was removed before preview validation")
	}
}

func awaitAccountResult(t *testing.T, promise js.Value) js.Value {
	t.Helper()
	completed := make(chan js.Value, 1)
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		completed <- args[0]
		return nil
	})
	defer callback.Release()
	promise.Call("then", callback)
	select {
	case result := <-completed:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("account operation did not resolve")
		return js.Undefined()
	}
}

func advanceWASMWallClock(t *testing.T, duration time.Duration) {
	t.Helper()
	datePrototype := js.Global().Get("Date").Get("prototype")
	originalGetTime := datePrototype.Get("getTime")
	advancedMillis := time.Now().UnixMilli() + duration.Milliseconds()
	fakeGetTime := js.FuncOf(func(js.Value, []js.Value) any { return float64(advancedMillis) })
	datePrototype.Set("getTime", fakeGetTime)
	t.Cleanup(func() {
		datePrototype.Set("getTime", originalGetTime)
		fakeGetTime.Release()
	})
	if observed := time.Now().UnixMilli(); observed < advancedMillis {
		t.Fatalf("isolated WASM wall clock did not advance by %s", duration)
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
