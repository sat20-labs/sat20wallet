package wallet

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// The existing account backup covers the wallet catalog. The UI submits IDs,
// not a second copy of names, account metadata or secret material.
func (p *Manager) checkRecoveryWallets(ids []int64) error {
	catalog := p.GetWalletCatalog()
	if len(ids) != len(catalog) {
		return fmt.Errorf("account recovery must include the complete wallet catalog")
	}
	selected := make(map[int64]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	for _, w := range catalog {
		if !selected[w.ID] {
			return fmt.Errorf("account recovery wallet selection changed")
		}
	}
	return nil
}
func (p *Manager) PreflightRecovery(password string, ids []int64) (*RecoverySummary, error) {
	if err := p.checkRecoveryWallets(ids); err != nil {
		return nil, err
	}
	result, err := p.AccountPreflight(password, nil)
	if err != nil {
		return nil, err
	}
	view := &RecoverySummary{AccountID: result.AccountID}
	for _, w := range result.Wallets {
		kind := "mnemonic"
		if w.Type != 0 {
			kind = "private_key"
		}
		view.Wallets = append(view.Wallets, WalletSummary{Name: w.Name, Kind: kind, AccountCount: w.AccountCount, DIDs: w.DIDs})
	}
	return view, nil
}
func (p *Manager) GetGuardianContact(password string) (*ExportFile, error) {
	return nil, ErrGuardianDisabled
}

func (p *Manager) GetAccountView(ctx context.Context) (*AccountView, error) {
	s := p.GetAccountManagementStatus()
	view := &AccountView{Active: s.Active, AccountID: s.AccountID, State: s.AccountState, RecoveryMode: s.RecoveryMode,
		DiscoveryStatus: s.DiscoveryStatus, Locator: s.PublicLocator, BackupState: s.BackupState,
		LastRehearsalAt: s.LastRehearsalAt, ErrorCode: s.LastDKVSSyncErrorCode}
	if !s.Active || !s.RecoveryConfigured {
		view.BackupRisk = "notConfigured"
	} else if s.ManagedDataDirty || s.PendingChanges > 0 {
		view.BackupRisk = "unconfirmed"
	}
	view.Modules = []ModuleView{p.rgbModuleView(s)}
	if s.ActivationPending {
		view.State = "activating"
	}
	if s.StorageMode != "" {
		view.Storage = &StorageView{Option: StorageOption{ID: s.StorageMode}, Status: "ready"}
	}

	auth, err := p.PendingAccountStorageAuthorization()
	if err == nil {
		view.Storage = p.storageView(auth)
	} else if !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
		return nil, err
	}
	// Funding status checks local payment intent before querying AUTOPAY.
	funding, fundingErr := p.GetAccountAutopayFundingStatus()
	if fundingErr == nil && funding.Required {
		view.Autopay = p.paymentView(funding)
	} else if fundingErr != nil && s.StorageMode == AccountStoragePaid {
		// Payment availability must not hide this device's backup risk.
		view.Autopay = &PaymentView{Status: "error", ErrorCode: fundingErr.Error()}
	}
	return view, nil
}
func (p *Manager) RefreshAccount(ctx context.Context) (*AccountView, error) {
	if !p.accountRecoveryConfigured() {
		if err := p.startAccountDiscovery(); err != nil {
			return nil, err
		}
		return p.GetAccountView(ctx)
	}
	if err := p.RetryDKVSSync(); err != nil {
		return nil, err
	}
	return p.GetAccountView(ctx)
}
func (p *Manager) paymentSource(q *PaymentQuote) {
	status := p.GetAccountManagementStatus()
	q.Network = p.GetChain()
	q.SourceWallet = quoteWalletLabel(status.RootWalletID, p.GetWalletCatalog())
	if root, err := p.accountManagementRootWallet(); err == nil {
		q.SourceAddress = PublicKeyToP2TRAddress_SatsNet(root.GetPubKey())
	}
}
func quoteWalletLabel(id int64, catalog []WalletCatalogEntry) string {
	label := strconv.FormatInt(id, 10)
	for _, w := range catalog {
		if w.ID == id {
			return w.Name + " (" + label + ")"
		}
	}
	return label
}
func storageOption(raw AccountStorageOption, source func(*PaymentQuote)) StorageOption {
	result := StorageOption{ID: raw.ID, Available: raw.Available, RecordCount: raw.RecordCount, TTLBlocks: raw.TTLBlocks, AnnualCost: raw.EstimatedAnnualCost}
	if raw.Mode == AccountStorageTemporary {
		result.WarningCodes = []string{"temporary_storage", "same_node_required"}
	}
	if !raw.Available {
		result.UnavailableCode = "storage_unavailable"
	}
	if raw.Mode == AccountStoragePaid {
		result.WarningCodes = []string{"continuous_payment_required"}
		result.Payment = &PaymentQuote{Contract: raw.ContractAddress, Amount: AssetAmount{Asset: raw.FeeAsset, Amount: raw.EstimatedCost, Precision: uint8(contractcommon.GasFeePrecision)}, RatePerBlock: raw.AmountPerBlock, FundingBlocks: accountPaidDefaultFundingBlocks, ModuleLimit: raw.BlobKeyLimit}
		source(result.Payment)
	}
	return result
}
func (p *Manager) storageView(auth *AccountStorageAuthorization) *StorageView {
	return &StorageView{Option: storageOption(auth.Summary, p.paymentSource), Status: "ready", TxID: auth.TransactionID, CanCancel: true}
}
func (p *Manager) GetStorageOptions(ctx context.Context, count uint64) (result []StorageOption, err error) {
	q, cancel, err := p.beginStorageQuote(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer func() {
		// The legacy paid-capacity RPC may finish after cancellation or a
		// source change. Never deliver even a partially built quote then.
		if check := q.current(); check != nil {
			result, err = nil, check
		}
	}()
	if count == 0 {
		count = accountDefaultRecordCount
	}
	options, err := p.buildStorageOptions(q.readPolicy, q.current)
	if err != nil {
		return nil, err
	}
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	result = make([]StorageOption, 0, len(options))
	for _, o := range options {
		if o.ID == AccountStoragePaid && o.Available {
			rate, err := accountAmountPerBlock(defaults, count)
			if err != nil {
				return nil, err
			}
			cost, err := multiplyDecimal(rate, accountPaidDefaultFundingBlocks)
			if err != nil {
				return nil, err
			}
			annual, err := multiplyDecimal(rate, 2_628_000)
			if err != nil {
				return nil, err
			}
			o.RecordCount, o.AmountPerBlock, o.EstimatedCost, o.EstimatedAnnualCost = count, rate, cost, annual
		}
		result = append(result, storageOption(o, q.paymentSource))
	}
	return result, nil
}
func (p *Manager) ConfirmStorage(ctx context.Context, confirmed StorageOption) (opValue0 *StorageView, opErr error) {
	opLogID := p.beginWalletLog("account", "account_storage_confirm", "Configure account storage")
	defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()

	if auth, err := p.PendingAccountStorageAuthorization(); err == nil {
		if auth.Summary.ID != confirmed.ID || (confirmed.ID == AccountStoragePaid && auth.Summary.RecordCount != confirmed.RecordCount) {
			return nil, fmt.Errorf("cancel existing storage setup before changing the plan")
		}
		if confirmed.ID == AccountStorageTemporary && auth.Summary.TTLBlocks != confirmed.TTLBlocks {
			return nil, ErrStorageTTLChanged
		}
		return p.storageView(auth), nil
	} else if !errors.Is(err, ErrAccountStorageAuthorizationMissing) {
		return nil, err
	}
	options, err := p.GetStorageOptions(ctx, confirmed.RecordCount)
	if err != nil {
		return nil, err
	}
	var current *StorageOption
	for i := range options {
		if options[i].ID == confirmed.ID {
			current = &options[i]
			break
		}
	}
	if current == nil || !current.Available {
		return nil, fmt.Errorf("storage option is unavailable")
	}
	if (current.Payment == nil) != (confirmed.Payment == nil) || (current.Payment != nil && *current.Payment != *confirmed.Payment) {
		return nil, ErrAccountAutopayFundingQuoteChanged
	}
	if confirmed.ID == AccountStoragePaid {
		auth, err := p.ReusePaidAccountStorage(confirmed.RecordCount)
		if err == nil {
			return p.storageView(auth), nil
		}
		if !errors.Is(err, ErrAccountPaidStorageNotReusable) {
			return nil, err
		}
	}
	auth, err := p.confirmAccountStorage(confirmed.ID, confirmed.RecordCount, &confirmed.TTLBlocks)
	if err != nil {
		var pending *AccountAutopayPendingError
		if errors.As(err, &pending) {
			return &StorageView{Option: *current, Status: "pending", TxID: pending.TransactionID}, nil
		}
		return nil, err
	}
	return p.storageView(auth), nil
}
func (p *Manager) CancelStorageSetup() error {
	p.CancelPendingAccountStorageAuthorization()
	return nil
}
func (p *Manager) paymentQuote(s *AccountAutopayFundingStatus) PaymentQuote {
	q := PaymentQuote{Contract: s.ContractAddress, SourceAddress: s.Payer, Amount: AssetAmount{Asset: s.FeeAsset, Amount: s.RecommendedFundingAmount, Precision: uint8(contractcommon.GasFeePrecision)}, RatePerBlock: s.EffectiveAmountPerBlock, FundingBlocks: s.RecommendedFundingBlocks, ModuleLimit: s.EffectiveBlobKeyLimit, TxID: s.FundingTransactionID}
	p.paymentSource(&q)
	return q
}
func (p *Manager) paymentView(s *AccountAutopayFundingStatus) *PaymentView {
	state := "ready"
	if s.NeedsFunding {
		state = "needs_funding"
	}
	if s.FundingPending {
		state = "pending"
	}
	quote := p.paymentQuote(s)
	return &PaymentView{Status: state, Balance: s.Balance, ExpiryHeight: s.EstimatedExpiryHeight, Quote: &quote, TxID: s.FundingTransactionID, CanFund: s.CanFund || s.FundingCanResume, ErrorCode: s.FundingError}
}
func (p *Manager) FundAutopay(ctx context.Context, confirmed PaymentQuote) (*PaymentView, error) {
	status, err := p.GetAccountAutopayFundingStatus()
	if err != nil {
		return nil, err
	}
	if p.paymentQuote(status) != confirmed {
		return nil, ErrAccountAutopayFundingQuoteChanged
	}
	result, err := p.FundAccountAutopay(*status)
	if err != nil {
		return nil, err
	}
	view := p.paymentView(status)
	view.TxID = result.TransactionID
	if result.Pending {
		view.Status = "pending"
	} else {
		view.Status = "ready"
	}
	return view, nil
}

func (p *Manager) rgbModuleView(status AccountManagementStatus) ModuleView {
	view := ModuleView{Name: "rgb11", CanWrite: status.WriteAllowed && (status.AccountState == "local" || status.AccountState == "ready")}
	catalog, err := p.GetWalletCatalogSnapshot()
	if err != nil {
		view.CanWrite = false
		view.ErrorCode = "WALLET_UNAVAILABLE"
		return view
	}
	scope := ""
	for _, w := range catalog.Wallets {
		if w.ID == catalog.CurrentWalletID {
			scope = fmt.Sprintf("%s/%s/%d", p.GetChain(), w.Fingerprint, catalog.CurrentAccountIndex)
			break
		}
	}
	if scope == "" {
		view.CanWrite = false
		return view
	}
	for _, m := range status.Modules {
		if m.Provider == "rgb11" && m.Scope == scope {
			view.CanWrite = view.CanWrite && m.WriteAllowed
			view.ErrorCode = m.LastErrorCode
			break
		}
	}
	return view
}
