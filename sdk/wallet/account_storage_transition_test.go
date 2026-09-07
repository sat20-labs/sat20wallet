package wallet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func TestPaidAccountStorageDoesNotOfferOrAcceptTemporaryDowngrade(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()

	manager := newAccountStorageHeightTestManager(t, 7200, 3467, nil, 39420)
	manager.wallet = NewInternalWalletWithMnemonic(accountRootWrapperTestMnemonic, "", GetChainParam())
	manager.accountProfile = &accountManagementProfile{StorageMode: AccountStoragePaid}
	options, err := manager.GetAccountStorageOptions()
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range options {
		if option.Mode == AccountStorageTemporary {
			t.Fatalf("paid account still offers temporary storage: %+v", option)
		}
	}
	if _, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0); !errors.Is(err, ErrAccountStorageModeDowngrade) {
		t.Fatalf("paid account temporary confirmation error=%v", err)
	}

	activationManager := newAccountManagementAutoTestManager(t)
	if _, _, err := activationManager.CreateWallet("password"); err != nil {
		t.Fatal(err)
	}
	activationManager.mutex.Lock()
	activationManager.accountProfile.StorageMode = AccountStoragePaid
	activationManager.mutex.Unlock()
	if err := activationManager.ActivateAccountManagement(
		bytes.Repeat([]byte{0x42}, 32), "password",
		AccountStorageAuthorization{Mode: AccountStorageTemporary},
		account.Locator{}, ""); !errors.Is(err, ErrAccountStorageModeDowngrade) {
		t.Fatalf("paid account temporary activation error=%v", err)
	}
}
