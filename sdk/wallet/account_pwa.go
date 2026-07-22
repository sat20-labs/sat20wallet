package wallet

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	AccountStorageTemporary = "temporary"
	AccountStoragePaid      = "paid"

	accountPaidDefaultFundingBlocks = uint64(1000)
	accountPaidDefaultTTL           = uint64((365 * 24 * time.Hour) / time.Millisecond)
	accountRequiredRecords          = uint64(4)
)

type AccountIndexerLocation struct {
	Scheme string `json:"scheme"`
	Host   string `json:"host"`
	Proxy  string `json:"proxy,omitempty"`
}

type AccountStorageOption struct {
	ID                   string   `json:"id"`
	Mode                 string   `json:"mode"`
	Available            bool     `json:"available"`
	Title                string   `json:"title"`
	Description          string   `json:"description"`
	Warnings             []string `json:"warnings,omitempty"`
	ExpiryHeight         uint64   `json:"expiry_height,omitempty"`
	EstimatedExpiryTime  int64    `json:"estimated_expiry_time,omitempty"`
	FeeAsset             string   `json:"fee_asset,omitempty"`
	EstimatedCost        string   `json:"estimated_cost,omitempty"`
	EstimatedAnnualCost  string   `json:"estimated_annual_cost,omitempty"`
	MinimumRetention     string   `json:"minimum_retention,omitempty"`
	RecommendedRetention string   `json:"recommended_retention,omitempty"`
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

func (p *Manager) accountDKVSClient() (*SatsNetDKVSClient, error) {
	location, err := p.AccountIndexerLocation()
	if err != nil {
		return nil, err
	}
	return NewSatsNetDKVSClient(location.Scheme, location.Host, location.Proxy, p.http), nil
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

func accountAmountPerBlock(defaults dkvsindexer.NetworkDefaults) (string, error) {
	minimum, err := decimalRat(defaults.AutopayMinAmountPerBlock)
	if err != nil {
		return "", err
	}
	perRecord, err := decimalRat(defaults.FullRecordFeePerBlock)
	if err != nil {
		return "", err
	}
	required := new(big.Rat).Mul(perRecord, new(big.Rat).SetInt(new(big.Int).SetUint64(accountRequiredRecords)))
	if required.Cmp(minimum) > 0 {
		minimum = required
	}
	return decimalString(minimum), nil
}

func (p *Manager) GetAccountStorageOptions() ([]AccountStorageOption, error) {
	client, err := p.accountDKVSClient()
	if err != nil {
		return nil, err
	}
	options := make([]AccountStorageOption, 0, 2)
	policy, configErr := client.GetConfig()
	if configErr == nil && policy != nil && policy.Enabled {
		expires := time.Now().Add(time.Duration(policy.MaxTTL) * time.Millisecond).UnixMilli()
		options = append(options, AccountStorageOption{
			ID: "temporary", Mode: AccountStorageTemporary, Available: true,
			Title: "临时缓存", Description: "由当前连接节点临时保存；到期后数据可能被删除。",
			Warnings:            []string{"这不是长期账户备份。", "恢复时需要能够访问保存数据的同一节点。"},
			EstimatedExpiryTime: expires,
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
		amountPerBlock, amountErr := accountAmountPerBlock(defaults)
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
		paid.Description = "通过 AUTOPAY 支付后保存加密账户数据。"
		paid.FeeAsset = defaults.AutopayFeeAssetName
		paid.EstimatedCost = cost
		paid.EstimatedAnnualCost = annual
		paid.MinimumRetention = fmt.Sprintf("%d blocks", accountPaidDefaultFundingBlocks)
		paid.RecommendedRetention = "由当前网络配置决定"
	}
	options = append(options, paid)
	return options, nil
}

func (p *Manager) ConfirmAccountStorage(optionID string) (*AccountStorageAuthorization, error) {
	if p == nil || p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}
	location, err := p.AccountIndexerLocation()
	if err != nil {
		return nil, err
	}
	client, err := p.accountDKVSClient()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(optionID)) {
	case AccountStorageTemporary:
		policy, err := client.GetConfig()
		if err != nil {
			return nil, err
		}
		if policy == nil || !policy.Enabled || policy.MaxTTL == 0 {
			return nil, fmt.Errorf("current node does not provide temporary DKVS cache")
		}
		return &AccountStorageAuthorization{
			ID: AccountStorageTemporary, Mode: AccountStorageTemporary,
			RecordOptions: dkvsindexer.RecordOptions{Seq: 1, TTL: policy.MaxTTL},
			Summary: AccountStorageOption{ID: AccountStorageTemporary, Mode: AccountStorageTemporary, Available: true,
				Title: "临时缓存", Description: "由当前连接节点临时保存；到期后数据可能被删除。",
				EstimatedExpiryTime: time.Now().Add(time.Duration(policy.MaxTTL) * time.Millisecond).UnixMilli()},
			Location: location, Policy: policy,
		}, nil
	case AccountStoragePaid:
		return p.confirmPaidAccountStorage(location)
	default:
		return nil, fmt.Errorf("unsupported account storage option %q", optionID)
	}
}

func (p *Manager) confirmPaidAccountStorage(location AccountIndexerLocation) (*AccountStorageAuthorization, error) {
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	if !defaults.Enabled || defaults.AutopayContract == "" {
		return nil, fmt.Errorf("paid DKVS storage is not configured for the current network")
	}
	amountPerBlock, err := accountAmountPerBlock(defaults)
	if err != nil {
		return nil, err
	}
	fundingAmount, err := multiplyDecimal(amountPerBlock, accountPaidDefaultFundingBlocks)
	if err != nil {
		return nil, err
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
	currentHeight := uint64(0)
	if p.l2IndexerClient != nil {
		if height := p.l2IndexerClient.GetSyncHeight(); height > 0 {
			currentHeight = uint64(height)
		}
	}
	expiryHeight := currentHeight + accountPaidDefaultFundingBlocks
	return &AccountStorageAuthorization{
		ID: AccountStoragePaid, Mode: AccountStoragePaid,
		RecordOptions: dkvsindexer.RecordOptions{Seq: 1, TTL: accountPaidDefaultTTL, ExpiryHeight: expiryHeight},
		Autopay:       &DKVSAutopayOptions{AddressParams: GetChainParam_SatsNet(), PoolContract: defaults.AutopayContract},
		Summary: AccountStorageOption{ID: AccountStoragePaid, Mode: AccountStoragePaid, Available: true,
			Title: "付费保存", Description: "AUTOPAY 交易已经广播。", ExpiryHeight: expiryHeight,
			FeeAsset: defaults.AutopayFeeAssetName, EstimatedCost: fundingAmount},
		TransactionID: result.TxID, Location: location,
	}, nil
}

func (p *Manager) NewAccountRepositoryForStorage(auth AccountStorageAuthorization) (account.Repository, error) {
	client, err := p.accountDKVSClient()
	if err != nil {
		return nil, err
	}
	switch auth.Mode {
	case AccountStorageTemporary:
		return NewFreeLocalAccountDKVSRepository(client, p.wallet, auth.RecordOptions)
	case AccountStoragePaid:
		if auth.Autopay == nil {
			return nil, fmt.Errorf("missing account AUTOPAY authorization")
		}
		return NewAccountDKVSRepository(client, p.wallet, *auth.Autopay, auth.RecordOptions)
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

func (p *Manager) AccountPreflight(password string, metadata []AccountWalletMetadataInput) (*AccountPreflightResult, error) {
	backup, err := p.ExportAccountBackup(password, metadataMap(metadata))
	if err != nil {
		return nil, err
	}
	defer clearAccountBackup(&backup)
	accountID, err := dkvsAccountID(p.wallet)
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
	client, err := p.accountDKVSClient()
	if err != nil {
		return err
	}
	switch auth.Mode {
	case AccountStorageTemporary:
		return client.PutAccountGuardianCapsuleWithFreeLocal(p.wallet, mailboxID, capsule, auth.RecordOptions)
	case AccountStoragePaid:
		if auth.Autopay == nil {
			return fmt.Errorf("missing account AUTOPAY authorization")
		}
		return client.PutAccountGuardianCapsuleWithAutopay(p.wallet, mailboxID, capsule, auth.RecordOptions, *auth.Autopay)
	default:
		return fmt.Errorf("unsupported account storage mode %q", auth.Mode)
	}
}

func (p *Manager) RestoreAccountBackupWithResult(value account.Backup, password string) ([]RestoredWalletResult, error) {
	backup, err := account.NormalizeBackup(value)
	if err != nil {
		return nil, err
	}
	if p.IsWalletExist() {
		return nil, fmt.Errorf("account restore requires an empty wallet database")
	}
	result := make([]RestoredWalletResult, 0, len(backup.Wallets))
	for _, item := range backup.Wallets {
		id, err := p.ImportWallet(item.Mnemonic, password)
		if err != nil {
			return nil, err
		}
		p.mutex.Lock()
		info := p.walletInfoMap[id]
		if info == nil {
			p.mutex.Unlock()
			return nil, fmt.Errorf("restored wallet %d was not persisted", id)
		}
		info.Accounts = int(item.AccountCount)
		err = saveWallet(p.db, &info.WalletInDB)
		walletValue := info.Wallet
		p.mutex.Unlock()
		if err != nil {
			return nil, err
		}
		accounts := make([]RestoredSubAccountResult, 0, len(item.SubAccounts))
		for _, sub := range item.SubAccounts {
			pubKey := walletValue.GetPubKeyByIndex(sub.Index)
			pubKeyHex := ""
			if pubKey != nil {
				pubKeyHex = fmt.Sprintf("%x", pubKey.SerializeCompressed())
			}
			accounts = append(accounts, RestoredSubAccountResult{
				Index: sub.Index, DID: sub.DID, Address: walletValue.GetAddressByIndex(sub.Index), PubKey: pubKeyHex,
			})
		}
		result = append(result, RestoredWalletResult{ID: id, Name: item.Name, Accounts: accounts})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}
