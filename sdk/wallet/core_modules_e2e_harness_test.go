package wallet

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
)

const coreE2ESenderMnemonic = "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire"
const coreE2EReceiverMnemonic = "comfort very add tuition senior run eight snap burst appear exile dutch"
const coreE2EChildMnemonic = "legal winner thank year wave sausage worth useful legal winner thank yellow"
const coreE2EPassword = "core-e2e-only-password"

type coreE2EConfig struct {
	Core AccountIndexerLocation `json:"core"`
	Bootstrap AccountIndexerLocation `json:"bootstrap"`
	CorePeer string `json:"core_peer"`
	BootstrapPeer string `json:"bootstrap_peer"`
	Contract string `json:"contract"`
}

func coreRequire(t *testing.T, phase string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("core-e2e: %s: %v", phase, err)
	}
}

func coreAssert(t *testing.T, ok bool, message string) {
	t.Helper()
	if !ok {
		t.Fatalf("core-e2e: %s", message)
	}
}

func coreE2ELoopback(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback() && u.Port() != ""
}

func coreLoadE2EConfig(t *testing.T) coreE2EConfig {
	t.Helper()
	raw := os.Getenv("SAT20WALLET_CORE_E2E_CONFIG")
	if raw == "" {
		t.Skip("launched by sdk/e2e/TestSDKCoreModulesE2E against its temporary local nodes")
	}
	var cfg coreE2EConfig
	coreRequire(t, "decode isolated harness", json.Unmarshal([]byte(raw), &cfg))
	for _, location := range []AccountIndexerLocation{cfg.Core, cfg.Bootstrap} {
		coreAssert(t, coreE2ELoopback(location.Scheme+"://"+location.Host), "refuse non-loopback DKVS endpoint")
	}
	for _, peer := range []string{cfg.CorePeer, cfg.BootstrapPeer} {
		parts := strings.Split(peer, "@")
		coreAssert(t, len(parts) == 3 && coreE2ELoopback(parts[2]), "refuse non-loopback CoreNode endpoint")
	}
	coreAssert(t, cfg.Contract != "", "AUTOPAY contract missing from isolated harness")
	return cfg
}

func coreNewE2EManager(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain, mnemonic string) *Manager {
	t.Helper()
	db := indexerdb.NewKVDB(t.TempDir())
	config := &sdkcommon.Config{Env: "test", Chain: "testnet",
		IndexerL1: &sdkcommon.Indexer{Scheme: cfg.Core.Scheme, Host: cfg.Core.Host, Proxy: cfg.Core.Proxy},
		IndexerL2: &sdkcommon.Indexer{Scheme: cfg.Core.Scheme, Host: cfg.Core.Host, Proxy: cfg.Core.Proxy},
		Peers: []string{cfg.BootstrapPeer, cfg.CorePeer},
	}
	manager := NewManager(config, db)
	coreAssert(t, manager != nil, "construct SDK manager")
	// No Start: one business writer per root. Explicit SDK operations establish
	// checkpoint timing. Keep the scope registry needed by explicit Refresh,
	// but stop only its automatic workers so observation cannot race settlement.
	t.Cleanup(manager.Close)
	manager.SetIndexerHttpClient(&coreE2EL1Indexer{chain: chain})
	if manager.rgbManager.scopeStates != nil {
		manager.rgbManager.scopeStates.stopReconciliations()
	}
	rgb, err := newRGB11Manager(manager, db, manager.utxoLockerL1, chain)
	coreRequire(t, "construct native RGB module", err)
	rgb.scopeStates.stopReconciliations()
	manager.rgbManager = rgb
	if mnemonic != "" {
		_, err := manager.ImportWallet(mnemonic, coreE2EPassword)
		coreRequire(t, "import isolated mnemonic", err)
	}
	return manager
}

func coreQuestions() []account.QuestionAnswer {
	return []account.QuestionAnswer{
		{Question: account.KnowledgeQuestion{ID: "one", Prompt: "synthetic question one"}, Answer: "test answer one", Confirmation: "test answer one"},
		{Question: account.KnowledgeQuestion{ID: "two", Prompt: "synthetic question two"}, Answer: "test answer two", Confirmation: "test answer two"},
		{Question: account.KnowledgeQuestion{ID: "three", Prompt: "synthetic question three"}, Answer: "test answer three", Confirmation: "test answer three"},
	}
}

func coreAnswers() []account.AnswerAttempt {
	return []account.AnswerAttempt{{QuestionID: "one", Answer: "test answer one"}, {QuestionID: "two", Answer: "test answer two"}}
}

type coreRecoveryMaterial struct {
	locator account.Locator
	userShare account.RecoveryShare
	guardianPrivate []byte
	auth AccountStorageAuthorization
}

func coreActivateE2E(t *testing.T, manager *Manager, mode account.RecoveryMode) *coreRecoveryMaterial {
	t.Helper()
	coreRequire(t, "initialize managed account", manager.InitializeAccountManagement(coreE2EPassword))
	auth, err := manager.ReusePaidAccountStorage(100)
	coreRequire(t, "reuse real AUTOPAY delegate", err)
	coreAssert(t, auth.Mode == AccountStoragePaid && auth.TransactionID == "", "AUTOPAY reuse must never fund")
	backup, err := manager.ExportAccountBackupForPWA(coreE2EPassword, nil)
	coreRequire(t, "export account catalog", err)
	defer clearAccountBackup(&backup)
	bootstrap, err := account.RootBootstrapBackup(backup)
	coreRequire(t, "bootstrap backup", err)
	id, err := manager.RootAccountID()
	coreRequire(t, "root identity", err)
	options := account.CreateOptions{AccountID: id, Backup: bootstrap, RecoveryMode: mode, Questions: coreQuestions()}
	material := &coreRecoveryMaterial{auth: *auth}
	if mode == account.RecoveryMode2Of3 {
		private, public, err := account.GenerateGuardianKey(nil)
		coreRequire(t, "generate synthetic guardian", err)
		material.guardianPrivate = private
		options.GuardianPublicKey = public
		options.GuardianMailboxID = id
		t.Cleanup(func() { zeroBytes(private) })
	}
	err = manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery, func(storage *AccountStorageAuthorization) error {
		pkg, err := manager.CreateAccountRecoveryPackage(options)
		if err != nil { return err }
		repo, err := manager.NewAccountRepositoryForStorage(*storage)
		if err != nil { return err }
		if err := account.NewManager(repo).Publish(context.Background(), *pkg); err != nil { return err }
		if pkg.GuardianCapsule != nil {
			if err := manager.PutGuardianCapsuleForStorage(*storage, id, *pkg.GuardianCapsule); err != nil { return err }
		}
		share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, coreAnswers())
		if err != nil { return err }
		recovered, secret, err := account.RecoverAccount(pkg.Envelope, pkg.UserShare, share)
		if err != nil { return err }
		defer zeroBytes(secret)
		defer clearAccountBackup(&recovered)
		material.locator, material.userShare = pkg.Envelope.Locator, pkg.UserShare
		return manager.ActivateAccountManagement(secret, coreE2EPassword, *storage, pkg.Envelope.Locator, "")
	})
	coreRequire(t, "publish/rehearse/activate paid account", err)
	coreAssert(t, manager.GetAccountManagementStatus().RecoveryConfigured, "recovery activation did not commit")
	return material
}

func coreRestoreE2E(t *testing.T, cfg coreE2EConfig, chain *coreE2EChain, material *coreRecoveryMaterial) *Manager {
	t.Helper()
	target := coreNewE2EManager(t, cfg, chain, "")
	pkg, err := target.LoadAccountRecoveryPackage(material.auth.Location, material.locator)
	coreRequire(t, "load recovery package from real DKVS", err)
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, coreAnswers())
	coreRequire(t, "recover knowledge share", err)
	companion := material.userShare
	if len(material.guardianPrivate) > 0 {
		coreAssert(t, pkg.Manifest.Guardian != nil, "guardian reference missing")
		reference := pkg.Manifest.Guardian
		data, err := target.LoadAccountGuardianCapsule(material.auth.Location, reference.MailboxID, material.locator.PackageID, reference.ShareID)
		coreRequire(t, "read guardian capsule", err)
		var capsule account.GuardianShareCapsule
		coreRequire(t, "decode guardian capsule", json.Unmarshal(data, &capsule))
		companion, err = account.DecryptGuardianShare(capsule, material.guardianPrivate)
		coreRequire(t, "recover guardian share", err)
	}
	backup, secret, err := account.RecoverAccount(pkg.Envelope, share, companion)
	coreRequire(t, "recover account secret", err)
	defer zeroBytes(secret)
	defer clearAccountBackup(&backup)
	coreAssert(t, len(backup.Wallets) > 0, "recovery package omitted root")
	state, err := target.LoadAccountManagementStateForRecovery(material.auth.Location, material.locator, secret, backup.Wallets[0].Mnemonic)
	coreRequire(t, "load latest managed state and RGB bundle", err)
	_, err = target.RestoreAccountManagementState(*state, secret, coreE2EPassword, material.locator,
		AccountManagementRestoreOptions{Location: material.auth.Location, StorageMode: AccountStoragePaid, AutopayContract: material.auth.Autopay.PoolContract})
	coreRequire(t, "restore independent SDK instance", err)
	return target
}
