package wallet

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

var ErrGuardianDisabled = errors.New("guardian recovery is disabled")

type accountActivationSession struct {
	Package       *account.RecoveryPackage
	Summary       account.RecoverySummary
	Authorization AccountStorageAuthorization
	Locator       accountLocatorPayload
}

type accountRecoverySession struct {
	Rehearsal bool
	Package   *account.RecoveryPackage
	Locator   accountLocatorPayload
	UserShare account.RecoveryShare
	Knowledge account.KnowledgeRecoveryBundle

	Backup       *account.Backup
	Secret       []byte
	ManagedState *RecoveredAccountManagementState
}

type recoverySessions struct {
	sync.Mutex
	activation map[string]*accountActivationSession
	recovery   map[string]*accountRecoverySession
}
type accountLocatorPayload = AccountPublicLocator

func zeroAccountBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func wipeRecoveryBackup(value *account.Backup) {
	if value == nil {
		return
	}
	for index := range value.Wallets {
		zeroAccountBytes(value.Wallets[index].Secret)
		value.Wallets[index].Secret = nil
	}
}

func accountRandomID(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func clearAccountActivationSession(session *accountActivationSession) {
	if session == nil {
		return
	}
	if session.Package != nil {
		session.Package.UserShare = account.RecoveryShare{}
	}
}

func clearAccountRecoverySession(session *accountRecoverySession) {
	if session == nil {
		return
	}
	zeroAccountBytes(session.Secret)
	wipeRecoveryBackup(session.Backup)
	session.UserShare = account.RecoveryShare{}
	session.Knowledge = account.KnowledgeRecoveryBundle{}
	if session.ManagedState != nil {
		for index := range session.ManagedState.State.Wallets {
			zeroAccountBytes(session.ManagedState.State.Wallets[index].Secret)
			session.ManagedState.State.Wallets[index].Secret = nil
		}
		for index := range session.ManagedState.Copies {
			item := &session.ManagedState.Copies[index]
			zeroAccountBytes(item.Value)
			item.Value = nil
			if item.Record != nil {
				zeroAccountBytes(item.Record.Value)
				item.Record = nil
			}
		}
		zeroAccountBytes(session.ManagedState.Envelope)
		session.ManagedState.Envelope = nil
	}
	session.Secret, session.Backup, session.ManagedState = nil, nil, nil
}

func snapshotRecoveryForCommit(session *accountRecoverySession) *accountRecoverySession {
	managed := *session.ManagedState
	managed.State.Wallets = append([]account.ManagedWallet(nil), managed.State.Wallets...)
	for index := range managed.State.Wallets {
		managed.State.Wallets[index].SubAccounts = append([]account.SubAccount(nil), managed.State.Wallets[index].SubAccounts...)
		managed.State.Wallets[index].Secret = bytes.Clone(managed.State.Wallets[index].Secret)
	}
	managed.Index.Entries = append([]account.ManagedEntry(nil), managed.Index.Entries...)
	managed.Envelope = bytes.Clone(managed.Envelope)
	managed.Copies = append([]AccountKVCopy(nil), managed.Copies...)
	for index := range managed.Copies {
		item := &managed.Copies[index]
		item.Value = bytes.Clone(item.Value)
		if item.Record != nil {
			record := *item.Record
			record.Value = bytes.Clone(record.Value)
			record.PubKey = bytes.Clone(record.PubKey)
			record.Signature = bytes.Clone(record.Signature)
			record.FeeProof = bytes.Clone(record.FeeProof)
			item.Record = &record
		}
	}
	return &accountRecoverySession{Locator: session.Locator,
		Secret: bytes.Clone(session.Secret), ManagedState: &managed}
}

func (p *Manager) clearRecoverySessions() {
	p.recoverySessions.Lock()
	defer p.recoverySessions.Unlock()
	for id, session := range p.recoverySessions.activation {
		clearAccountActivationSession(session)
		delete(p.recoverySessions.activation, id)
	}
	for id, session := range p.recoverySessions.recovery {
		clearAccountRecoverySession(session)
		delete(p.recoverySessions.recovery, id)
	}
}

func (p *Manager) encodeAccountLocator(value accountLocatorPayload) (string, error) {
	return EncodeAccountPublicLocator(value, p.GetChain())
}
func (p *Manager) decodeAccountLocator(value string) (accountLocatorPayload, error) {
	return DecodeAccountPublicLocator(value, p.GetChain())
}

func (p *Manager) decodeRecoveryFile(value string) (accountLocatorPayload, account.RecoveryShare, error) {
	locatorText, shareText, ok := strings.Cut(strings.TrimSpace(value), "\n")
	if !ok || strings.Contains(shareText, "\n") {
		return accountLocatorPayload{}, account.RecoveryShare{}, account.ErrInvalidRecoveryPackage
	}
	locator, err := p.decodeAccountLocator(locatorText)
	if err != nil {
		return accountLocatorPayload{}, account.RecoveryShare{}, err
	}
	share, err := account.DecodeRecoveryShare(strings.TrimSpace(shareText))
	if err != nil {
		return accountLocatorPayload{}, account.RecoveryShare{}, err
	}
	if share.Role != account.ShareRoleUser || share.PackageID != locator.Locator.PackageID {
		return accountLocatorPayload{}, account.RecoveryShare{}, fmt.Errorf("user share does not match recovery package")
	}
	return locator, share, nil
}

func recoveryFile(name, text string) *ExportFile {
	return &ExportFile{Name: name, MIME: "text/plain", Data: []byte(text)}
}
func recoverySummary(value account.RecoverySummary) RecoverySummary {
	result := RecoverySummary{AccountID: value.AccountID}
	for _, w := range value.Wallets {
		result.Wallets = append(result.Wallets, WalletSummary{Name: w.Name, Kind: recoveryWalletKind(w.Type), AccountCount: w.AccountCount, DIDs: w.DIDs})
	}
	return result
}
func recoveryQuestions(knowledge account.KnowledgeRecoveryBundle) []Question {
	result := make([]Question, 0, len(knowledge.QuestionShares))
	for _, entry := range knowledge.QuestionShares {
		q := entry.Question
		result = append(result, Question{ID: q.ID, Prompt: q.Prompt, CaseSensitive: q.CaseSensitive, IgnorePunctuation: q.IgnorePunctuation})
	}
	return result
}
func recoveryAnswers(values []Answer) []account.AnswerAttempt {
	result := make([]account.AnswerAttempt, len(values))
	for i, v := range values {
		result[i] = account.AnswerAttempt{QuestionID: v.QuestionID, Answer: v.Text}
	}
	return result
}

func (p *Manager) CreateRecovery(ctx context.Context, request RecoverySetup) (opValue0 *RecoverySetupView, opErr error) {
	if request.Mode == string(account.RecoveryMode2Of3) || request.GuardianContact != "" {
		return nil, ErrGuardianDisabled
	}
	opLogID := p.beginWalletLog("account", "account_recovery_create", "Create account recovery")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	manager := p
	if manager == nil {
		return nil, fmt.Errorf("Manager not initialized")
	}
	if err := p.checkRecoveryWallets(request.WalletIDs); err != nil {
		return nil, err
	}
	var data *RecoverySetupView
	var sessionID string
	var session *accountActivationSession
	err := manager.UseAccountStorageAuthorization(AccountStoragePurposeRecovery,
		func(storage *AccountStorageAuthorization) error {
			backup, err := manager.ExportAccountBackupForPWA(request.Password, nil)
			if err != nil {
				return err
			}
			defer wipeRecoveryBackup(&backup)
			// An imported root may belong to an existing account. Explicit
			// setup initializes only a genuinely new account; existing profiles
			// retain their AccountSecret.
			if err := manager.InitializeAccountManagement(request.Password); err != nil {
				return err
			}
			repository, err := manager.NewAccountRepositoryForStorage(*storage)
			if err != nil {
				return err
			}
			accountIDProvider, ok := repository.(interface{ AccountID() string })
			if !ok {
				return fmt.Errorf("account repository does not expose account id")
			}
			questions := make([]account.QuestionAnswer, len(request.Questions))
			for index, input := range request.Questions {
				questions[index] = account.QuestionAnswer{
					Question: account.KnowledgeQuestion{ID: input.Question.ID, Prompt: input.Question.Prompt, CaseSensitive: input.Question.CaseSensitive, IgnorePunctuation: input.Question.IgnorePunctuation},
					Answer:   input.Answer, Confirmation: input.Confirmation,
				}
			}
			bootstrap, err := account.RootBootstrapBackup(backup)
			if err != nil {
				return err
			}
			options := account.CreateOptions{AccountID: accountIDProvider.AccountID(), Backup: bootstrap, RecoveryMode: account.RecoveryMode(request.Mode), Questions: questions}
			recovery := account.NewManager(repository)
			pkg, err := manager.CreateAccountRecoveryPackage(options)
			if err != nil {
				return err
			}
			if err := recovery.Publish(ctx, *pkg); err != nil {
				return err
			}
			stored, err := recovery.Load(ctx, pkg.Envelope.Locator)
			if err != nil {
				return fmt.Errorf("verify published recovery package: %w", err)
			}
			knowledge, err := account.OpenRecoveryKnowledge(*stored, pkg.UserShare)
			if err != nil {
				return fmt.Errorf("verify published recovery questions: %w", err)
			}
			locator := accountLocatorPayload{
				Version: account.Version, Network: manager.GetChain(),
				StorageLocation: storage.Location, StorageMode: storage.Mode,
				RecordTTL: storage.RecordOptions.TTL, Locator: pkg.Envelope.Locator,
			}
			if storage.Autopay != nil {
				locator.AutopayContract = storage.Autopay.PoolContract
			}
			if err := manager.SignAccountPublicLocator(&locator); err != nil {
				return err
			}
			locatorText, err := EncodeAccountPublicLocator(locator, manager.GetChain())
			if err != nil {
				return err
			}
			userShare, err := account.EncodeRecoveryShare(pkg.UserShare)
			if err != nil {
				return err
			}
			sessionID, err = accountRandomID(nil)
			if err != nil {
				return err
			}
			summary := account.SummarizeBackup(pkg.Envelope.Locator, backup)
			stored.UserShare = pkg.UserShare
			session = &accountActivationSession{Package: stored, Summary: summary, Authorization: *storage,
				Locator: locator}
			data = &RecoverySetupView{SessionID: sessionID, Locator: locatorText, Summary: recoverySummary(summary),
				Questions: recoveryQuestions(knowledge),
				Files:     []ExportFile{*recoveryFile("user-share.txt", locatorText+"\n"+userShare)}, CanActivate: true}

			return nil
		})
	if err != nil {
		if session != nil && session.Package != nil {
			session.Package.UserShare = account.RecoveryShare{}
		}
		return nil, err
	}
	// Do not expose a session from a cancelled or superseded SDK operation.
	p.recoverySessions.Lock()
	if p.recoverySessions.activation == nil {
		p.recoverySessions.activation = make(map[string]*accountActivationSession)
	}
	p.recoverySessions.activation[sessionID] = session
	p.recoverySessions.Unlock()
	return data, nil
}

func (p *Manager) AcceptGuardianSetup(ctx context.Context, password, setupText string) (opValue0 *ExportFile, opErr error) {
	return nil, ErrGuardianDisabled
}

func (p *Manager) ConfirmGuardianSetup(ctx context.Context, sessionID, receiptText string) (opValue0 *RecoverySetupView, opErr error) {
	return nil, ErrGuardianDisabled
}

func (p *Manager) ActivateRecovery(ctx context.Context, sessionID string, request ActivationInput) (opValue0 *AccountView, opErr error) {
	opLogID := p.beginWalletLog("account", "account_recovery_activate", "Activate account recovery")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	if request.Factors.GuardianResponse != "" {
		return nil, ErrGuardianDisabled
	}

	p.recoverySessions.Lock()
	session := p.recoverySessions.activation[sessionID]
	if session == nil || session.Package == nil {
		p.recoverySessions.Unlock()
		return nil, fmt.Errorf("activation session expired")
	}
	pkg := *session.Package
	p.recoverySessions.Unlock()
	locator, companion, err := p.decodeRecoveryFile(request.RecoveryFile)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(locator, session.Locator) {
		return nil, fmt.Errorf("saved recovery code does not match the final recovery code")
	}

	if strings.TrimSpace(request.Password) == "" {
		return nil, fmt.Errorf("wallet password is required")
	}
	if pkg.Envelope.Locator.RecoveryMode == account.RecoveryMode2Of3 {
		return nil, ErrGuardianDisabled
	}
	if companion != pkg.UserShare {
		return nil, fmt.Errorf("user recovery share does not match the activation session")
	}
	knowledge, err := account.OpenRecoveryKnowledge(pkg, companion)
	if err != nil {
		return nil, err
	}
	dkvsShare, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, knowledge, recoveryAnswers(request.Factors.Answers))
	if err != nil {
		return nil, err
	}
	backup, secret, err := account.RecoverAccount(pkg.Envelope, dkvsShare, companion)
	if err != nil {
		return nil, fmt.Errorf("knowledge recovery share does not match the activation package")
	}
	defer zeroAccountBytes(secret)
	defer wipeRecoveryBackup(&backup)
	publicLocator, err := p.encodeAccountLocator(session.Locator)
	if err != nil {
		return nil, err
	}
	if err := p.ActivateAccountManagement(secret, request.Password, session.Authorization,
		pkg.Envelope.Locator, publicLocator); err != nil {
		return nil, err
	}
	p.recoverySessions.Lock()
	clearAccountActivationSession(session)
	delete(p.recoverySessions.activation, sessionID)
	p.recoverySessions.Unlock()
	return p.GetAccountView(ctx)

}

func (p *Manager) LoadRecovery(ctx context.Context, recoveryFile string, rehearsal bool) (*RecoveryView, error) {
	locator, userShare, err := p.decodeRecoveryFile(recoveryFile)
	if err != nil {
		return nil, err
	}
	if locator.Locator.RecoveryMode == account.RecoveryMode2Of3 {
		return nil, ErrGuardianDisabled
	}
	if rehearsal && locator.Locator.AccountID != p.GetAccountManagementStatus().AccountID {
		return nil, fmt.Errorf("rehearsal recovery code belongs to another account")
	}

	pkg, err := p.LoadAccountRecoveryPackage(locator.StorageLocation, locator.Locator)
	if err != nil {
		return nil, err
	}
	knowledge, err := account.OpenRecoveryKnowledge(*pkg, userShare)
	if err != nil {
		return nil, err
	}
	sessionID, err := accountRandomID(nil)
	if err != nil {
		return nil, err
	}
	p.recoverySessions.Lock()
	if p.recoverySessions.recovery == nil {
		p.recoverySessions.recovery = make(map[string]*accountRecoverySession)
	}
	p.recoverySessions.recovery[sessionID] = &accountRecoverySession{
		Package: pkg, Locator: locator, Rehearsal: rehearsal, UserShare: userShare, Knowledge: knowledge,
	}
	p.recoverySessions.Unlock()
	return &RecoveryView{SessionID: sessionID, Mode: string(pkg.Envelope.Locator.RecoveryMode), Questions: recoveryQuestions(knowledge)}, nil

}

func (p *Manager) CreateGuardianRequest(ctx context.Context, sessionID string) (opValue0 *ExportFile, opErr error) {
	return nil, ErrGuardianDisabled
}

func (p *Manager) CreateGuardianResponse(ctx context.Context, password, requestText string) (opValue0 *ExportFile, opErr error) {
	return nil, ErrGuardianDisabled
}

func (p *Manager) PreviewRecovery(ctx context.Context, sessionID string, factors RecoveryFactors) (opValue0 *RecoveryView, opErr error) {
	if factors.GuardianResponse != "" {
		return nil, ErrGuardianDisabled
	}
	opLogID := p.beginWalletLog("account", "account_recovery_preview", "Preview account recovery")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	p.recoverySessions.Lock()
	session := p.recoverySessions.recovery[sessionID]
	if session == nil || session.Package == nil {
		p.recoverySessions.Unlock()
		return nil, fmt.Errorf("recovery session expired")
	}
	pkg, userShare, knowledge := session.Package, session.UserShare, session.Knowledge
	p.recoverySessions.Unlock()
	if len(factors.Answers) == 0 {
		return nil, account.ErrInsufficientShares
	}
	dkvsShare, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, knowledge, recoveryAnswers(factors.Answers))
	if err != nil {
		return nil, err
	}
	backup, secret, err := account.RecoverAccount(pkg.Envelope, dkvsShare, userShare)
	if err != nil {
		return nil, err
	}
	if len(backup.Wallets) == 0 || backup.Wallets[0].Type != account.WalletMnemonic {
		zeroAccountBytes(secret)
		return nil, fmt.Errorf("recovery package does not contain a root wallet")
	}
	managed, err := p.LoadAccountManagementStateForRecovery(
		session.Locator.StorageLocation, session.Locator.Locator, secret, string(backup.Wallets[0].Secret))
	if err != nil {
		zeroAccountBytes(secret)
		wipeRecoveryBackup(&backup)
		return nil, fmt.Errorf("load latest account state: %w", err)
	}
	latestBackup, err := account.BackupFromManagedState(managed.State)
	if err != nil {
		zeroAccountBytes(secret)
		wipeRecoveryBackup(&backup)
		return nil, err
	}
	wipeRecoveryBackup(&backup)
	p.recoverySessions.Lock()
	if p.recoverySessions.recovery[sessionID] != session {
		p.recoverySessions.Unlock()
		zeroAccountBytes(secret)
		wipeRecoveryBackup(&latestBackup)
		clearAccountRecoverySession(&accountRecoverySession{ManagedState: managed})
		return nil, fmt.Errorf("recovery session expired")
	}
	clearAccountRecoverySession(&accountRecoverySession{
		Secret: session.Secret, Backup: session.Backup, ManagedState: session.ManagedState,
	})
	session.Secret = secret
	session.Backup = &latestBackup
	session.ManagedState = managed
	p.recoverySessions.Unlock()
	summary := recoverySummary(account.SummarizeBackup(session.Package.Envelope.Locator, latestBackup))
	return &RecoveryView{SessionID: sessionID, Mode: string(pkg.Envelope.Locator.RecoveryMode), Questions: recoveryQuestions(knowledge), Summary: &summary, CanCommit: !session.Rehearsal}, nil

}

func (p *Manager) CommitRecovery(ctx context.Context, sessionID, newPassword string) (opValue0 *RecoveryResult, opErr error) {
	opLogID := p.beginWalletLog("account", "account_recovery_commit", "Restore managed account")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	if p == nil {
		return nil, fmt.Errorf("Manager not initialized")
	}
	p.recoverySessions.Lock()
	session := p.recoverySessions.recovery[sessionID]
	if session == nil {
		p.recoverySessions.Unlock()
		return nil, fmt.Errorf("recovery session expired")
	}
	if session.Rehearsal {
		p.recoverySessions.Unlock()
		return nil, fmt.Errorf("rehearsal cannot import wallets")
	}
	if session.Backup == nil {
		p.recoverySessions.Unlock()
		return nil, fmt.Errorf("account recovery preview is required")
	}
	if session.ManagedState == nil || len(session.Secret) != 32 {
		p.recoverySessions.Unlock()
		return nil, fmt.Errorf("latest managed account state is unavailable")
	}
	// Once the user confirms restore, it owns an independent snapshot.
	// Aborting/expiring the preview must not zero buffers used by its commit.
	snapshot := snapshotRecoveryForCommit(session)
	p.recoverySessions.Unlock()
	defer clearAccountRecoverySession(snapshot)
	locator := snapshot.Locator
	publicLocator, err := p.encodeAccountLocator(locator)
	if err != nil {
		return nil, err
	}
	_, err = p.RestoreAccountManagementState(
		*snapshot.ManagedState, snapshot.Secret, newPassword, locator.Locator,
		AccountManagementRestoreOptions{
			Location: locator.StorageLocation, StorageMode: locator.StorageMode,
			RecordTTL: locator.RecordTTL, AutopayContract: locator.AutopayContract,
			PublicLocator: publicLocator,
		})
	if err != nil {
		return nil, err
	}
	status := p.GetAccountManagementStatus()
	if !status.Active || status.RootWalletID == 0 || status.AccountID == "" {
		return nil, fmt.Errorf("restored account has no explicit root wallet")
	}
	p.recoverySessions.Lock()
	clearAccountRecoverySession(session)
	delete(p.recoverySessions.recovery, sessionID)
	p.recoverySessions.Unlock()
	result := &RecoveryResult{AccountID: status.AccountID, RootWalletID: status.RootWalletID,
		Complete: len(status.Modules) > 0, FailedModules: []ModuleView{}}
	failed := make(map[string]bool)
	for _, module := range status.Modules {
		if module.BackupState == "restoring" && !failed[module.Provider] {
			result.Complete = false
			failed[module.Provider] = true
			result.FailedModules = append(result.FailedModules, ModuleView{
				Name: module.Provider, ErrorCode: module.LastErrorCode})
		}
	}

	return result, nil

}

func (p *Manager) AbortRecovery(sessionID string) (opErr error) {
	opLogID := p.beginWalletLog("account", "account_recovery_abort", "Cancel account recovery")
	defer func() { p.finishWalletLog(opLogID, opErr, nil) }()

	p.recoverySessions.Lock()
	defer p.recoverySessions.Unlock()
	clearAccountRecoverySession(p.recoverySessions.recovery[sessionID])
	clearAccountActivationSession(p.recoverySessions.activation[sessionID])
	delete(p.recoverySessions.recovery, sessionID)
	delete(p.recoverySessions.activation, sessionID)
	return nil

}

func recoveryWalletKind(kind account.WalletType) string {
	if kind == account.WalletMnemonic {
		return "mnemonic"
	}
	return "private_key"
}

var _ AccountAPI = (*Manager)(nil)
