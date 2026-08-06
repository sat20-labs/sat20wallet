package wallet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	AccountStorageTemporary = "temporary"
	AccountStoragePaid      = "paid"

	accountPaidDefaultFundingBlocks = uint64(1000)
	accountRequiredRecords          = uint64(5)
	accountMinimumRecordCount       = uint64(100)
	accountDefaultRecordCount       = uint64(100)
)

type AccountIndexerLocation struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Proxy  string `json:"proxy,omitempty"`
}

type AccountStorageOption struct {
	ID                    string   `json:"id"`
	Mode                  string   `json:"mode"`
	Available             bool     `json:"available"`
	Title                 string   `json:"title"`
	Description           string   `json:"description"`
	Warnings              []string `json:"warnings,omitempty"`
	TTLBlocks             uint64   `json:"ttl_blocks,omitempty"`
	EstimatedExpiryHeight uint64   `json:"estimated_expiry_height,omitempty"`
	FeeAsset              string   `json:"fee_asset,omitempty"`
	EstimatedCost         string   `json:"estimated_cost,omitempty"`
	EstimatedAnnualCost   string   `json:"estimated_annual_cost,omitempty"`
	MinimumRetention      string   `json:"minimum_retention,omitempty"`
	RecommendedRetention  string   `json:"recommended_retention,omitempty"`
	RecordCount           uint64   `json:"record_count,omitempty"`
	DefaultRecordCount    uint64   `json:"default_record_count,omitempty"`
	FullRecordFee         string   `json:"full_record_fee_per_block,omitempty"`
	MinimumAmountPerBlock string   `json:"minimum_amount_per_block,omitempty"`
	AmountPerBlock        string   `json:"amount_per_block,omitempty"`
	ContractAddress       string   `json:"contract_address,omitempty"`
}

type AccountStorageAuthorization struct {
	ID            string                    `json:"id"`
	Mode          string                    `json:"mode"`
	RecordOptions dkvsindexer.RecordOptions `json:"record_options"`
	Autopay       *DKVSAutopayOptions       `json:"-"`
	Summary       AccountStorageOption      `json:"summary"`
	TransactionID string                    `json:"transaction_id,omitempty"`
	Location      AccountIndexerLocation    `json:"location"`
	Policy        *AccountFreeLocalPolicy   `json:"-"`
}

type AccountWalletMetadataInput struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	SubAccounts map[uint32]string `json:"sub_accounts"`
}

type AccountPreflightResult struct {
	AccountID string                  `json:"account_id"`
	Wallets   []account.WalletSummary `json:"wallets"`
	Location  AccountIndexerLocation  `json:"location"`
}

type RestoredSubAccountResult struct {
	Index   uint32 `json:"index"`
	DID     string `json:"did"`
	Address string `json:"address"`
	PubKey  string `json:"pub_key"`
}

type RestoredWalletResult struct {
	ID       int64                      `json:"id"`
	Name     string                     `json:"name"`
	Accounts []RestoredSubAccountResult `json:"accounts"`
}

func (p *Manager) AccountIndexerLocation() (AccountIndexerLocation, error) {
	if p == nil || p.cfg == nil || p.cfg.IndexerL2 == nil {
		return AccountIndexerLocation{}, fmt.Errorf("SatoshiNet indexer is not configured")
	}
	return AccountIndexerLocation{
		Scheme: p.cfg.IndexerL2.Scheme,
		Host:   p.cfg.IndexerL2.Host,
		Proxy:  p.cfg.IndexerL2.Proxy,
	}, nil
}

func (p *Manager) accountDKVSStore() (*dkvsStore, error) {
	if p == nil {
		return nil, fmt.Errorf("DKVS manager is unavailable")
	}
	return p.ensureDKVSManager().primaryStore()
}

func (p *Manager) accountDKVSStoreForLocation(location AccountIndexerLocation) (*dkvsStore, error) {
	if p == nil {
		return nil, fmt.Errorf("DKVS manager is unavailable")
	}
	return p.ensureDKVSManager().storeFor(
		location.Scheme, location.Host, location.Proxy, p.http,
	)
}

func (p *Manager) LoadAccountGuardianCapsule(location AccountIndexerLocation,
	mailboxID, packageID, shareID string) ([]byte, error) {

	store, err := p.accountDKVSStoreForLocation(location)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.MailShareKey(mailboxID, packageID, shareID)
	if err != nil {
		return nil, err
	}
	record, err := store.Get(key)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, fmt.Errorf("guardian capsule is unavailable")
	}
	capsule, err := account.DecodeGuardianCapsuleStorage(record.Value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(capsule)
}

func (p *Manager) LoadAccountRecoveryPackage(location AccountIndexerLocation,
	locator account.Locator) (*account.RecoveryPackage, error) {

	store, err := p.accountDKVSStoreForLocation(location)
	if err != nil {
		return nil, err
	}
	repository, err := newReadOnlyAccountDKVSRepository(store, locator.AccountID)
	if err != nil {
		return nil, err
	}
	return account.NewManager(repository).Load(context.Background(), locator)
}

func decimalRat(value string) (*big.Rat, error) {
	result, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || result.Sign() < 0 {
		return nil, fmt.Errorf("invalid decimal amount %q", value)
	}
	return result, nil
}

func decimalString(value *big.Rat) string {
	if value == nil {
		return "0"
	}
	if value.IsInt() {
		return value.Num().String()
	}
	return strings.TrimRight(strings.TrimRight(value.FloatString(8), "0"), ".")
}

func multiplyDecimal(value string, multiplier uint64) (string, error) {
	amount, err := decimalRat(value)
	if err != nil {
		return "", err
	}
	amount.Mul(amount, new(big.Rat).SetInt(new(big.Int).SetUint64(multiplier)))
	return decimalString(amount), nil
}

func normalizeAccountRecordCount(recordCount uint64) (uint64, error) {
	if recordCount == 0 {
		recordCount = accountDefaultRecordCount
	}
	if recordCount < accountMinimumRecordCount {
		return 0, fmt.Errorf("account storage requires at least %d records", accountMinimumRecordCount)
	}
	return recordCount, nil
}

func accountAmountPerBlock(defaults dkvsindexer.NetworkDefaults, recordCount uint64) (string, error) {
	recordCount, err := normalizeAccountRecordCount(recordCount)
	if err != nil {
		return "", err
	}
	minimum, err := decimalRat(defaults.AutopayMinAmountPerBlock)
	if err != nil {
		return "", err
	}
	perRecord, err := decimalRat(defaults.FullRecordFeePerBlock)
	if err != nil {
		return "", err
	}
	required := new(big.Rat).Mul(perRecord, new(big.Rat).SetInt(new(big.Int).SetUint64(recordCount)))
	if required.Cmp(minimum) > 0 {
		minimum = required
	}
	return decimalString(minimum), nil
}

func estimatedDKVSExpiryHeight(issueHeight, ttl uint64) uint64 {
	if ttl == 0 || issueHeight > ^uint64(0)-ttl {
		return 0
	}
	return issueHeight + ttl
}

func (p *Manager) GetAccountStorageOptions() ([]AccountStorageOption, error) {
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, err
	}
	options := make([]AccountStorageOption, 0, 2)
	policy, configErr := store.Config()
	if configErr == nil && policy != nil && policy.Enabled {
		currentHeight, _ := p.ensureDKVSManager().verificationHeight()
		options = append(options, AccountStorageOption{
			ID: "temporary", Mode: AccountStorageTemporary, Available: true,
			Title: "临时缓存", Description: "由当前连接节点临时保存；到期后数据可能被删除。",
			Warnings:              []string{"这不是长期账户备份。", "恢复时需要能够访问保存数据的同一节点。"},
			TTLBlocks:             policy.MaxTTL,
			EstimatedExpiryHeight: estimatedDKVSExpiryHeight(currentHeight, policy.MaxTTL),
		})
	} else {
		warning := "当前连接节点不提供临时 DKVS 缓存。"
		if configErr != nil {
			warning = "无法读取当前节点的 DKVS 配置。"
		}
		options = append(options, AccountStorageOption{
			ID: "temporary", Mode: AccountStorageTemporary, Available: false,
			Title: "临时缓存", Description: warning, Warnings: []string{warning},
		})
	}

	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	paid := AccountStorageOption{ID: "paid", Mode: AccountStoragePaid, Title: "付费保存"}
	if !defaults.Enabled || defaults.AutopayContract == "" {
		paid.Description = "当前网络尚未提供可用的 DKVS 付费保存配置。"
		paid.Warnings = []string{paid.Description}
	} else {
		amountPerBlock, amountErr := accountAmountPerBlock(defaults, accountDefaultRecordCount)
		if amountErr != nil {
			return nil, amountErr
		}
		cost, costErr := multiplyDecimal(amountPerBlock, accountPaidDefaultFundingBlocks)
		if costErr != nil {
			return nil, costErr
		}
		annual, annualErr := multiplyDecimal(amountPerBlock, 2_628_000)
		if annualErr != nil {
			return nil, annualErr
		}
		paid.Available = true
		paid.Description = "通过 AUTOPAY 按区块持续支付后，全网保存加密账户数据。"
		paid.Warnings = []string{"余额不足、未继续支付时，数据会停止全网同步并转为节点临时缓存。"}
		paid.FeeAsset = defaults.AutopayFeeAssetName
		paid.ContractAddress = defaults.AutopayContract
		paid.EstimatedCost = cost
		paid.EstimatedAnnualCost = annual
		paid.RecordCount = accountDefaultRecordCount
		paid.DefaultRecordCount = accountDefaultRecordCount
		paid.FullRecordFee = defaults.FullRecordFeePerBlock
		paid.MinimumAmountPerBlock = defaults.AutopayMinAmountPerBlock
		paid.AmountPerBlock = amountPerBlock
		paid.MinimumRetention = fmt.Sprintf("initial funding for %d blocks", accountPaidDefaultFundingBlocks)
		paid.RecommendedRetention = "持续支付期间全网保存"
	}
	options = append(options, paid)
	return options, nil
}

func (p *Manager) ConfirmAccountStorage(optionID string, recordCount uint64) (*AccountStorageAuthorization, error) {
	if p == nil || p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	location, err := p.AccountIndexerLocation()
	if err != nil {
		return nil, err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(optionID)) {
	case AccountStorageTemporary:
		policy, err := store.Config()
		if err != nil {
			return nil, err
		}
		if policy == nil || !policy.Enabled || policy.MaxTTL == 0 {
			return nil, fmt.Errorf("current node does not provide temporary DKVS cache")
		}
		currentHeight, _ := p.ensureDKVSManager().verificationHeight()
		return &AccountStorageAuthorization{
			ID: AccountStorageTemporary, Mode: AccountStorageTemporary,
			RecordOptions: dkvsindexer.RecordOptions{Seq: 1, TTL: policy.MaxTTL},
			Summary: AccountStorageOption{ID: AccountStorageTemporary, Mode: AccountStorageTemporary, Available: true,
				Title: "临时缓存", Description: "由当前连接节点临时保存；到期后数据可能被删除。",
				TTLBlocks:             policy.MaxTTL,
				EstimatedExpiryHeight: estimatedDKVSExpiryHeight(currentHeight, policy.MaxTTL)},
			Location: location, Policy: policy,
		}, nil
	case AccountStoragePaid:
		return p.confirmPaidAccountStorage(location, recordCount)
	default:
		return nil, fmt.Errorf("unsupported account storage option %q", optionID)
	}
}

func (p *Manager) confirmPaidAccountStorage(location AccountIndexerLocation, recordCount uint64) (*AccountStorageAuthorization, error) {
	releaseRGB11Scope := p.beginRGB11ScopeChange()
	defer releaseRGB11Scope()
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, err
	}
	p.mutex.Lock()
	previous := p.wallet
	p.wallet = root
	p.mutex.Unlock()
	defer func() {
		p.mutex.Lock()
		p.wallet = previous
		p.mutex.Unlock()
	}()
	return p.confirmPaidAccountStorageWithCurrentWallet(location, recordCount)
}

func (p *Manager) confirmPaidAccountStorageWithCurrentWallet(location AccountIndexerLocation,
	recordCount uint64) (*AccountStorageAuthorization, error) {
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	if !defaults.Enabled || defaults.AutopayContract == "" {
		return nil, fmt.Errorf("paid DKVS storage is not configured for the current network")
	}
	recordCount, err := normalizeAccountRecordCount(recordCount)
	if err != nil {
		return nil, err
	}
	amountPerBlock, err := accountAmountPerBlock(defaults, recordCount)
	if err != nil {
		return nil, err
	}
	fundingAmount, err := multiplyDecimal(amountPerBlock, accountPaidDefaultFundingBlocks)
	if err != nil {
		return nil, err
	}
	if p.wallet.GetPubKey() == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	payer := PublicKeyToP2TRAddress_SatsNet(p.wallet.GetPubKey())
	if strings.TrimSpace(payer) == "" {
		return nil, fmt.Errorf("unable to derive AUTOPAY payer")
	}
	ready, err := p.accountAutopayReady(defaults, payer, amountPerBlock)
	if err != nil {
		return nil, fmt.Errorf("check existing AUTOPAY storage: %w", err)
	}
	if ready {
		return accountPaidStorageAuthorization(location, defaults, recordCount,
			amountPerBlock, fundingAmount, "", true), nil
	}
	param := contractcommon.TemplateAutopayConfigInvokeParam{AmountPerBlock: amountPerBlock}
	encodedParam, err := param.Encode()
	if err != nil {
		return nil, err
	}
	result, err := p.InvokeUnifiedContract(&ContractInvokeRequest{
		ContractType: ContractTypeTemplate, SubType: contractcommon.TemplateAutopay,
		ContractAddress: defaults.AutopayContract, Action: contractcommon.TemplateInvokeAPIConfig,
		Param: base64.StdEncoding.EncodeToString(encodedParam), ParamEncoding: "base64",
		Assets: []ContractFundingAsset{{AssetName: defaults.AutopayFeeAssetName, Amount: fundingAmount}},
	})
	if err != nil {
		return nil, err
	}
	if err := p.waitForAccountAutopayReady(defaults, amountPerBlock); err != nil {
		return nil, err
	}
	return accountPaidStorageAuthorization(location, defaults, recordCount,
		amountPerBlock, fundingAmount, result.TxID, false), nil
}

func accountPaidStorageAuthorization(location AccountIndexerLocation,
	defaults dkvsindexer.NetworkDefaults, recordCount uint64, amountPerBlock,
	fundingAmount, transactionID string, reused bool) *AccountStorageAuthorization {

	description := "AUTOPAY 首次区块支付已确认。"
	if reused {
		description = "已复用当前钱包有效的 AUTOPAY 付费存储。"
	}
	return &AccountStorageAuthorization{
		ID: AccountStoragePaid, Mode: AccountStoragePaid,
		RecordOptions: dkvsindexer.RecordOptions{Seq: 1},
		Autopay:       &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: defaults.AutopayContract},
		Summary: AccountStorageOption{ID: AccountStoragePaid, Mode: AccountStoragePaid, Available: true,
			Title: "付费保存", Description: description,
			FeeAsset: defaults.AutopayFeeAssetName, EstimatedCost: fundingAmount,
			RecordCount: recordCount, DefaultRecordCount: accountDefaultRecordCount,
			FullRecordFee:         defaults.FullRecordFeePerBlock,
			MinimumAmountPerBlock: defaults.AutopayMinAmountPerBlock, AmountPerBlock: amountPerBlock,
			RecommendedRetention: "持续支付期间全网保存", ContractAddress: defaults.AutopayContract},
		TransactionID: transactionID, Location: location,
	}
}

func (p *Manager) NewAccountRepositoryForStorage(auth AccountStorageAuthorization) (account.Repository, error) {
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, err
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, err
	}
	switch auth.Mode {
	case AccountStorageTemporary:
		return NewFreeLocalAccountDKVSRepository(store, root, auth.RecordOptions)
	case AccountStoragePaid:
		if auth.Autopay == nil {
			return nil, fmt.Errorf("missing account AUTOPAY authorization")
		}
		return newAccountDKVSRepository(store, root, *auth.Autopay, auth.RecordOptions)
	default:
		return nil, fmt.Errorf("unsupported account storage mode %q", auth.Mode)
	}
}

func metadataMap(values []AccountWalletMetadataInput) map[int64]AccountWalletMetadata {
	result := make(map[int64]AccountWalletMetadata, len(values))
	for _, value := range values {
		result[value.ID] = AccountWalletMetadata{Name: strings.TrimSpace(value.Name), SubAccountDIDs: value.SubAccounts}
	}
	return result
}

func clearAccountBackup(value *account.Backup) {
	if value == nil {
		return
	}
	for index := range value.Wallets {
		value.Wallets[index].Mnemonic = ""
	}
}

type preparedAccountRestore struct {
	wallets map[int64]*WalletInfo
	status  *Status
	results []RestoredWalletResult
}

func cloneStatusForAccountRestore(value *Status) *Status {
	if value == nil {
		return newDefaultStatus()
	}
	value.RLock()
	defer value.RUnlock()
	return &Status{
		SoftwareVer: value.SoftwareVer, DBver: value.DBver,
		TotalWallet: value.TotalWallet, CurrentWallet: value.CurrentWallet,
		CurrentAccount: value.CurrentAccount, CurrentChain: value.CurrentChain,
		SyncHeight: value.SyncHeight, SyncHeightL1: value.SyncHeightL1,
		SyncHeightL2:   value.SyncHeightL2,
		BlockHashMapL1: cloneIntStringMap(value.BlockHashMapL1),
		BlockHashMapL2: cloneIntStringMap(value.BlockHashMapL2),
		MaxFeeRateL1:   value.MaxFeeRateL1, HasStaked: value.HasStaked,
		ContractSubAccountIndex: value.ContractSubAccountIndex,
	}
}

// prepareAccountRestoreLocked performs every fallible wallet operation before
// touching persistent or live manager state. The caller must hold p.mutex.
func (p *Manager) prepareAccountRestoreLocked(value account.Backup, password string) (*preparedAccountRestore, error) {
	backup, err := account.NormalizeBackup(value)
	if err != nil {
		return nil, err
	}
	if len(p.walletInfoMap) != 0 || p.wallet != nil {
		return nil, fmt.Errorf("account restore requires an empty wallet database")
	}
	prepared := &preparedAccountRestore{
		wallets: make(map[int64]*WalletInfo, len(backup.Wallets)),
		results: make([]RestoredWalletResult, 0, len(backup.Wallets)),
		status:  cloneStatusForAccountRestore(p.status),
	}
	fingerprints := make(map[string]struct{}, len(backup.Wallets))
	for _, item := range backup.Wallets {
		walletValue := NewInternalWalletWithMnemonic(item.Mnemonic, "", GetChainParam())
		if walletValue == nil {
			return nil, fmt.Errorf("restore wallet %q: invalid mnemonic", item.Name)
		}
		fingerprint := walletFingerprint(walletValue)
		if _, exists := fingerprints[fingerprint]; exists {
			return nil, fmt.Errorf("restore wallet %q: duplicate wallet identity", item.Name)
		}
		fingerprints[fingerprint] = struct{}{}
		id := walletValue.GetId()
		if _, exists := prepared.wallets[id]; exists {
			return nil, fmt.Errorf("restore wallet %q: duplicate wallet id", item.Name)
		}
		info := &WalletInfo{WalletInDB: WalletInDB{
			Id: id, Accounts: int(item.AccountCount), Type: WALLET_TYPE_MNEMONIC, Name: item.Name,
			AccountNames: make(map[uint32]string, len(item.SubAccounts)),
			AccountDIDs:  make(map[uint32]string, len(item.SubAccounts)),
		}, Wallet: walletValue}
		for _, sub := range item.SubAccounts {
			info.AccountNames[sub.Index] = sub.Name
			info.AccountDIDs[sub.Index] = sub.DID
		}
		key, err := p.newSnaclKey(password)
		if err != nil {
			return nil, fmt.Errorf("derive restored wallet key %q: %w", item.Name, err)
		}
		ciphertext, err := key.Encrypt([]byte(item.Mnemonic))
		if err != nil {
			return nil, fmt.Errorf("encrypt restored wallet %q: %w", item.Name, err)
		}
		info.Mnemonic = ciphertext
		info.Salt = key.Marshal()
		accounts := make([]RestoredSubAccountResult, 0, len(item.SubAccounts))
		for _, sub := range item.SubAccounts {
			pubKey := walletValue.GetPubKeyByIndex(sub.Index)
			pubKeyHex := ""
			if pubKey != nil {
				pubKeyHex = fmt.Sprintf("%x", pubKey.SerializeCompressed())
			}
			accounts = append(accounts, RestoredSubAccountResult{
				Index: sub.Index, DID: sub.DID,
				Address: walletValue.GetAddressByIndex(sub.Index), PubKey: pubKeyHex,
			})
		}
		prepared.wallets[id] = info
		prepared.results = append(prepared.results, RestoredWalletResult{ID: id, Name: item.Name, Accounts: accounts})
	}
	sort.Slice(prepared.results, func(i, j int) bool { return prepared.results[i].ID < prepared.results[j].ID })
	if len(prepared.results) == 0 {
		return nil, fmt.Errorf("account restore produced no wallets")
	}
	prepared.status.TotalWallet = len(prepared.wallets)
	prepared.status.CurrentWallet = prepared.results[0].ID
	prepared.status.CurrentAccount = 0
	if prepared.status.SoftwareVer == "" {
		prepared.status.SoftwareVer = SOFTWARE_VERSION
	}
	if prepared.status.DBver == "" {
		prepared.status.DBver = DB_VERSION
	}
	if prepared.status.CurrentChain == "" {
		prepared.status.CurrentChain = _chain
	}
	normalizeStatus(prepared.status)
	return prepared, nil
}

// persistPreparedAccountRestoreLocked is the only commit point for account
// recovery. Wallet records, status and optional management profile become
// visible together; manager memory is updated only after a successful Flush.
func (p *Manager) persistPreparedAccountRestoreLocked(prepared *preparedAccountRestore,
	profile *accountManagementProfile) error {
	if prepared == nil || len(prepared.wallets) == 0 || prepared.status == nil {
		return fmt.Errorf("invalid prepared account restore")
	}
	batch := p.db.NewWriteBatch()
	if batch == nil {
		return fmt.Errorf("create account restore batch")
	}
	defer batch.Close()
	for id, info := range prepared.wallets {
		encoded, err := EncodeToBytes(&info.WalletInDB)
		if err != nil {
			return err
		}
		if err := batch.Put([]byte(getWalletDBKey(id)), encoded); err != nil {
			return err
		}
	}
	statusBytes, err := encodeStatusToBytes(prepared.status)
	if err != nil {
		return err
	}
	if err := batch.Put([]byte(DB_KEY_STATUS), statusBytes); err != nil {
		return err
	}
	if profile != nil {
		profileBytes, err := EncodeToBytes(profile)
		if err != nil {
			return err
		}
		if err := batch.Put(accountManagementProfileKey(), profileBytes); err != nil {
			return err
		}
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	p.walletInfoMap = prepared.wallets
	p.status = prepared.status
	p.wallet = prepared.wallets[prepared.status.CurrentWallet].Wallet
	p.wallet.SetSubAccount(0)
	p.accountProfile = profile
	return nil
}

func (p *Manager) AccountPreflight(password string, metadata []AccountWalletMetadataInput) (*AccountPreflightResult, error) {
	backup, err := p.ExportAccountBackup(password, metadataMap(metadata))
	if err != nil {
		return nil, err
	}
	defer clearAccountBackup(&backup)
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, err
	}
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, err
	}
	location, err := p.AccountIndexerLocation()
	if err != nil {
		return nil, err
	}
	locator := account.Locator{Version: account.Version, AccountID: accountID,
		PackageID: "00000000000000000000000000000000", RecoveryMode: account.RecoveryMode2Of3}
	return &AccountPreflightResult{AccountID: accountID, Wallets: account.SummarizeBackup(locator, backup).Wallets, Location: location}, nil
}

func (p *Manager) ExportAccountBackupForPWA(password string, metadata []AccountWalletMetadataInput) (account.Backup, error) {
	return p.ExportAccountBackup(password, metadataMap(metadata))
}

func (p *Manager) PutGuardianCapsuleForStorage(auth AccountStorageAuthorization, mailboxID string,
	capsule account.GuardianShareCapsule) error {
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	encoded, err := account.EncodeGuardianCapsuleStorage(capsule)
	if err != nil {
		return err
	}
	if len(encoded) > account.MaxRecoveryObjectSize {
		return fmt.Errorf("guardian capsule exceeds DKVS value limit")
	}
	key, err := dkvsindexer.MailShareKey(mailboxID, capsule.PackageID, capsule.ShareID)
	if err != nil {
		return err
	}
	mutation := dkvsValueMutation{
		Key: key, Value: encoded, Owner: p.wallet, Signature: dkvsSignatureAccount,
		Policy: dkvsStoragePolicy{
			TTL: auth.RecordOptions.TTL,
		},
	}
	switch auth.Mode {
	case AccountStorageTemporary:
		mutation.Policy.FreeLocal = true
	case AccountStoragePaid:
		if auth.Autopay == nil {
			return fmt.Errorf("missing account AUTOPAY authorization")
		}
		mutation.Policy.Autopay = auth.Autopay
	default:
		return fmt.Errorf("unsupported account storage mode %q", auth.Mode)
	}
	_, err = store.Put(mutation)
	return err
}

func (p *Manager) RestoreAccountBackupWithResult(value account.Backup, password string) ([]RestoredWalletResult, error) {
	releaseRGB11Scope := p.beginRGB11ScopeChange()
	defer releaseRGB11Scope()
	p.mutex.Lock()
	prepared, err := p.prepareAccountRestoreLocked(value, password)
	if err != nil {
		p.mutex.Unlock()
		return nil, err
	}
	if err := p.persistPreparedAccountRestoreLocked(prepared, nil); err != nil {
		p.mutex.Unlock()
		return nil, err
	}
	_ = p.rgbManager.selectRGB11Scope()
	_ = p.rgbManager.rebuildRGB11Locks()
	results := append([]RestoredWalletResult(nil), prepared.results...)
	p.mutex.Unlock()
	if err := p.refreshDKVSRegistrations(); err != nil {
		Log.Warningf("refresh DKVS registrations after account restore failed: %v", err)
	}
	return results, nil
}
