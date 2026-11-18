//go:build guardian

package wallet

import (
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func TestAccountRecoveryRejectsOwnGuardian(t *testing.T) {
	manager := newAccountManagementAutoTestManager(t)
	if _, err := manager.ImportWallet(accountRootWrapperTestMnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	identity, err := manager.GetOrCreateAccountGuardianIdentity("password")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := manager.ExportAccountBackup("password", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clearAccountBackup(&backup)
	_, key, err := account.GenerateGuardianKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	options := account.CreateOptions{AccountID: identity.MailboxID, Backup: backup,
		RecoveryMode: account.RecoveryMode2Of3, Questions: coreQuestions(),
		GuardianMailboxID: identity.MailboxID, GuardianPublicKey: key}
	if _, err := manager.CreateAccountRecoveryPackage(options); err == nil {
		t.Fatal("own root guardian was accepted")
	}
	options.GuardianMailboxID = strings.ToUpper(identity.MailboxID)
	if _, err := manager.CreateAccountRecoveryPackage(options); err == nil {
		t.Fatal("case variant of own root guardian was accepted")
	}
}
