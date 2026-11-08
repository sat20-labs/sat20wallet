package wallet

import (
	"context"
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestPaidAccountStorageDoesNotOfferOrAcceptTemporaryDowngrade(t *testing.T) {
	manager, endpoint, _ := newInactiveAccountGroup(t, accountRootWrapperTestMnemonic, "password")
	manager.http = endpoint
	manager.SetIndexerHttpClient_SatsNet(NewIndexerClient(manager.cfg.IndexerL2.Scheme,
		manager.cfg.IndexerL2.Host, manager.cfg.IndexerL2.Proxy, endpoint))
	endpoint.mu.Lock()
	endpoint.bestHeight = 3467
	endpoint.freeLocal.MaxTTL = 7200
	endpoint.mu.Unlock()
	// Real recovery material is prepared while temporary storage is eligible.
	pkg, temporary, secret := rereviewRecoveryPackage(t, manager)
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	rate, err := accountAmountPerBlock(defaults, 100)
	if err != nil {
		t.Fatal(err)
	}
	balance, err := multiplyDecimal(rate, accountPaidDefaultFundingBlocks)
	if err != nil {
		t.Fatal(err)
	}
	payer := PublicKeyToP2TRAddress_SatsNet(manager.GetWallet().GetPubKey())
	// This existing HTTP boundary represents an already-funded real payer;
	// the SDK still validates its policy and obtains the authorization itself.
	endpoint.mu.Lock()
	endpoint.autopayState = &dkvsindexer.AutopayContractState{
		Contract: defaults.AutopayContract, TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		CurrentBlock: 3467, ServiceName: defaults.AutopayServiceName,
		Recipient: defaults.AutopayRecipient, FeeAssetName: defaults.AutopayFeeAssetName,
		MinAmountPerBlock: defaults.AutopayMinAmountPerBlock, Status: "active",
		Delegates: map[string]dkvsindexer.AutopayDelegateState{payer: {
			AmountPerBlock: rate, Balance: balance, LastPayHeight: 3467, Status: "active", BlobKeyLimit: 100,
		}},
	}
	endpoint.mu.Unlock()
	paid, err := manager.ConfirmAccountStorage(AccountStoragePaid, 100)
	if err != nil {
		t.Fatal(err)
	}
	if paid.Mode != AccountStoragePaid || paid.TransactionID != "" {
		t.Fatal("paid fixture did not reuse the funded delegate")
	}
	if err := manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
		func(auth *AccountStorageAuthorization) error {
			repository, err := manager.NewAccountRepositoryForStorage(*auth)
			if err != nil {
				return err
			}
			if err := account.NewManager(repository).Publish(context.Background(), *pkg); err != nil {
				return err
			}
			return manager.ActivateAccountManagement(secret, "password", *auth,
				pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID)
		}); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	status := manager.GetAccountManagementStatus()
	if !status.RecoveryConfigured || status.ActivationPending || status.StorageMode != AccountStoragePaid {
		t.Fatal("paid fixture did not establish its real configured baseline")
	}

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
	if err := manager.ActivateAccountManagement(secret, "password", *temporary,
		pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); !errors.Is(err, ErrAccountStorageModeDowngrade) {
		t.Fatalf("paid account temporary activation error=%v", err)
	}
}
