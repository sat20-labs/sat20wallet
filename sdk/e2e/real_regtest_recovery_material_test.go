package e2e

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// References existing private acceptance material; it never reconfigures a
// live account merely to fit an older 2-of-2 test prerequisite.
type sdkRegtestRecoveryMaterial struct {
	Role              string                     `json:"role"`
	Locator           account.Locator            `json:"locator"`
	UserSharePath     string                     `json:"user_share_path"`
	PublicLocatorPath string                     `json:"public_locator_path"`
	Answers           []sdkRegtestRecoveryAnswer `json:"answers"`
}

type sdkRegtestRecoveryAnswer struct {
	QuestionID string `json:"question_id"`
	Prompt     string `json:"prompt"`
	Answer     string `json:"answer"`
}

func loadSDKRegtestRecoveryMaterial(path string, locator account.Locator) (*sdkRegtestRecoveryMaterial, error) {
	if path == "" {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(path) || info.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("existing recovery fixture requires an absolute private 0600 file")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []sdkRegtestRecoveryMaterial
	if err := json.Unmarshal(encoded, &entries); err != nil {
		return nil, fmt.Errorf("decode existing recovery fixture: %w", err)
	}
	var found *sdkRegtestRecoveryMaterial
	for _, entry := range entries {
		if entry.Locator.AccountID != locator.AccountID {
			continue
		}
		if found != nil || entry.Locator != locator ||
			(locator.RecoveryMode != account.RecoveryMode2Of2 && locator.RecoveryMode != account.RecoveryMode2Of3) ||
			!filepath.IsAbs(entry.UserSharePath) || !filepath.IsAbs(entry.PublicLocatorPath) || len(entry.Answers) < 2 {
			return nil, fmt.Errorf("existing recovery fixture identity, package, mode or material mismatch")
		}
		copy := entry
		found = &copy
	}
	if found == nil {
		return nil, fmt.Errorf("existing recovery fixture does not contain this account")
	}
	return found, nil
}

func sdkRegtestRecoveryMaterialFor(t *testing.T, locator account.Locator) *sdkRegtestRecoveryMaterial {
	t.Helper()
	material, err := loadSDKRegtestRecoveryMaterial(os.Getenv("SAT20_REAL_RECOVERY_FIXTURES"), locator)
	require.NoError(t, err)
	return material
}

func bindSDKRegtestRecoveryAnswers(pkg *account.RecoveryPackage, retained []sdkRegtestRecoveryAnswer) ([]account.AnswerAttempt, error) {
	byPrompt := make(map[string]string)
	for _, answer := range retained {
		if answer.Prompt == "" || answer.Answer == "" {
			return nil, fmt.Errorf("retained knowledge material requires its original prompt and answer")
		}
		if _, duplicate := byPrompt[answer.Prompt]; duplicate {
			return nil, fmt.Errorf("retained knowledge prompts must be unambiguous")
		}
		byPrompt[answer.Prompt] = answer.Answer
	}
	var answers []account.AnswerAttempt
	seen := make(map[string]bool)
	for _, question := range pkg.KnowledgeBundle.QuestionShares {
		if answer, found := byPrompt[question.Question.Prompt]; found {
			if seen[question.Question.Prompt] {
				return nil, fmt.Errorf("package knowledge prompts must match distinct retained questions")
			}
			seen[question.Question.Prompt] = true
			answers = append(answers, account.AnswerAttempt{QuestionID: question.Question.ID, Answer: answer})
		}
	}
	if len(answers) < 2 || len(answers) < int(pkg.KnowledgeBundle.Threshold) {
		return nil, fmt.Errorf("retained knowledge prompts do not match this package's required threshold")
	}
	return answers, nil
}

func sdkRegtestRecoveryAnswers(t *testing.T, pkg *account.RecoveryPackage) []account.AnswerAttempt {
	t.Helper()
	if material := sdkRegtestRecoveryMaterialFor(t, pkg.Envelope.Locator); material != nil {
		answers, err := bindSDKRegtestRecoveryAnswers(pkg, material.Answers)
		require.NoError(t, err)
		return answers
	}
	return e2eKnowledgeAnswers()
}

func sdkRegtestSignedRecoveryLocator(t *testing.T, manager *wallet.Manager,
	authorization *wallet.AccountStorageAuthorization, locator account.Locator) string {
	t.Helper()
	if material := sdkRegtestRecoveryMaterialFor(t, locator); material != nil {
		info, err := os.Stat(material.PublicLocatorPath)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		encoded, err := os.ReadFile(material.PublicLocatorPath)
		require.NoError(t, err)
		value := strings.TrimSpace(string(encoded))
		verified, err := wallet.DecodeAccountPublicLocator(value, "regtest")
		require.NoError(t, err)
		require.Equal(t, locator, verified.Locator)
		return value
	}
	value := wallet.AccountPublicLocator{Version: account.Version, Network: "regtest",
		StorageLocation: authorization.Location, StorageMode: authorization.Mode,
		RecordTTL: authorization.RecordOptions.TTL, Locator: locator}
	if authorization.Autopay != nil {
		value.AutopayContract = authorization.Autopay.PoolContract
	}
	require.NoError(t, manager.SignAccountPublicLocator(&value))
	encoded, err := wallet.EncodeAccountPublicLocator(value, "regtest")
	require.NoError(t, err)
	return encoded
}

func TestSDKPWARegtestExistingRecoveryFixtureIdentity(t *testing.T) {
	for _, mode := range []account.RecoveryMode{account.RecoveryMode2Of2, account.RecoveryMode2Of3} {
		t.Run(string(mode), func(t *testing.T) {
			locator := account.Locator{Version: account.Version, AccountID: "current-root", PackageID: "current-package", RecoveryMode: mode}
			material := sdkRegtestRecoveryMaterial{Locator: locator, UserSharePath: filepath.Join(t.TempDir(), "share"),
				PublicLocatorPath: filepath.Join(t.TempDir(), "locator"), Answers: []sdkRegtestRecoveryAnswer{{QuestionID: "a"}, {QuestionID: "b"}}}
			encoded, err := json.Marshal([]sdkRegtestRecoveryMaterial{material})
			require.NoError(t, err)
			path := filepath.Join(t.TempDir(), "material.json")
			require.NoError(t, os.WriteFile(path, encoded, 0o600))
			actual, err := loadSDKRegtestRecoveryMaterial(path, locator)
			require.NoError(t, err)
			require.Equal(t, locator, actual.Locator)
			for _, field := range []string{"root", "package", "mode"} {
				wrong := locator
				switch field {
				case "root":
					wrong.AccountID = "other-root"
				case "package":
					wrong.PackageID = "old-package"
				case "mode":
					wrong.RecoveryMode = "unknown"
				}
				_, err := loadSDKRegtestRecoveryMaterial(path, wrong)
				require.Error(t, err)
			}
			require.NoError(t, os.Chmod(path, 0o644))
			_, err = loadSDKRegtestRecoveryMaterial(path, locator)
			require.Error(t, err)
		})
	}
}

func TestSDKPWARegtestRecoveryAnswersUseExactPackagePrompts(t *testing.T) {
	pkg := &account.RecoveryPackage{KnowledgeBundle: account.KnowledgeRecoveryBundle{Threshold: 2,
		QuestionShares: []account.EncryptedQuestionShare{
			{Question: account.KnowledgeQuestion{ID: "book-page", Prompt: "first original prompt"}},
			{Question: account.KnowledgeQuestion{ID: "private-phrase", Prompt: "second original prompt"}},
		}}}
	retained := []sdkRegtestRecoveryAnswer{
		{QuestionID: "book", Prompt: "first original prompt", Answer: "first retained answer"},
		{QuestionID: "note", Prompt: "second original prompt", Answer: "second retained answer"},
	}
	bound, err := bindSDKRegtestRecoveryAnswers(pkg, retained)
	require.NoError(t, err)
	require.Equal(t, []account.AnswerAttempt{{QuestionID: "book-page", Answer: retained[0].Answer},
		{QuestionID: "private-phrase", Answer: retained[1].Answer}}, bound)
	wrong := append([]sdkRegtestRecoveryAnswer(nil), retained...)
	wrong[0].Prompt = "another package's prompt"
	_, err = bindSDKRegtestRecoveryAnswers(pkg, wrong)
	require.Error(t, err)
	_, err = bindSDKRegtestRecoveryAnswers(pkg, []sdkRegtestRecoveryAnswer{retained[0], retained[0]})
	require.Error(t, err)
}

// Read only: prove that the retained private material can actually decrypt
// the current remote package before running asset preparation or transfers.
func TestSDKPWARegtestExistingRecoveryMaterialReadback(t *testing.T) {
	if os.Getenv("SAT20_REAL_L1_INDEXER") == "" {
		t.Skip("requires the existing Bitcoin Core 31.0 and real regtest indexer")
	}
	path := os.Getenv("SAT20_REAL_RECOVERY_FIXTURES")
	if path == "" {
		t.Skip("set SAT20_REAL_RECOVERY_FIXTURES to enable private recovery material readback")
	}
	encoded, err := os.ReadFile(path)
	require.NoError(t, err)
	var entries []sdkRegtestRecoveryMaterial
	require.NoError(t, json.Unmarshal(encoded, &entries))
	require.Len(t, entries, 2)
	seeds := readSDKPWARegtestMnemonics(t)
	for _, entry := range entries {
		t.Run(entry.Role, func(t *testing.T) {
			material := sdkRegtestRecoveryMaterialFor(t, entry.Locator)
			encoded, err := os.ReadFile(material.PublicLocatorPath)
			require.NoError(t, err)
			public, err := wallet.DecodeAccountPublicLocator(strings.TrimSpace(string(encoded)), "regtest")
			require.NoError(t, err)
			require.Equal(t, material.Locator, public.Locator)
			share := loadRGB11RegtestUserShare(t, material.UserSharePath)
			require.Equal(t, material.Locator.PackageID, share.PackageID)
			l1, err := urlForRGB11RegtestIndexer(os.Getenv("SAT20_REAL_L1_INDEXER"))
			require.NoError(t, err)
			location := public.StorageLocation
			config := &sdkcommon.Config{Env: "dev", Chain: "regtest",
				IndexerL1: &sdkcommon.Indexer{Scheme: l1.Scheme, Host: l1.Host, Proxy: strings.Trim(l1.Path, "/")},
				IndexerL2: &sdkcommon.Indexer{Scheme: location.Scheme, Host: location.Host, Proxy: location.Proxy}}
			db := indexerdb.NewKVDB(t.TempDir())
			require.NotNil(t, db)
			manager := wallet.NewManager(config, db)
			t.Cleanup(func() { manager.Close(); db.Close() })
			pkg, err := manager.LoadAccountRecoveryPackage(location, material.Locator)
			require.NoError(t, err)
			var provided, available []string
			for _, answer := range material.Answers {
				provided = append(provided, answer.QuestionID)
			}
			for _, question := range pkg.KnowledgeBundle.QuestionShares {
				available = append(available, question.Question.ID)
				t.Logf("package question ID=%s; prompt SHA256=%x", question.Question.ID, sha256.Sum256([]byte(question.Question.Prompt)))
			}
			t.Logf("mode=%s; provided question IDs=%v; package question IDs=%v", material.Locator.RecoveryMode, provided, available)
			dkvsShare, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, sdkRegtestRecoveryAnswers(t, pkg))
			require.NoError(t, err, "decrypt the existing DKVS capsule using its retained knowledge material")
			backup, secret, err := account.RecoverAccount(pkg.Envelope, share, dkvsShare)
			require.NoError(t, err, "combine existing user/DKVS shares and decrypt the original backup")
			defer clearBytes(secret)
			root, err := account.RootBootstrapBackup(backup)
			require.NoError(t, err)
			require.NotEmpty(t, root.Wallets)
			require.NotEmpty(t, seeds[entry.Role])
			require.True(t, root.Wallets[0].Mnemonic == seeds[entry.Role], "existing package must recover the same fixed root")
		})
	}
}
