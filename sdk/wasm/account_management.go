//go:build js && wasm
// +build js,wasm

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	walletsdk "github.com/sat20-labs/sat20wallet/sdk/wallet"
)

const accountSessionTTL = 20 * time.Minute

type accountLocatorPayload = walletsdk.AccountPublicLocator

type accountActivationSession struct {
	Package          *account.RecoveryPackage
	Summary          account.RecoverySummary
	Authorization    walletsdk.AccountStorageAuthorization
	Locator          accountLocatorPayload
	GuardianVerified bool
	GuardianShare    *account.RecoveryShare
	RequestPrivate   []byte
	ExpiresAt        time.Time
}

type accountRecoverySession struct {
	Package        *account.RecoveryPackage
	Locator        accountLocatorPayload
	DKVSShare      *account.RecoveryShare
	UserShare      *account.RecoveryShare
	GuardianShare  *account.RecoveryShare
	RequestPrivate []byte
	Backup         *account.Backup
	Secret         []byte
	ManagedState   *walletsdk.RecoveredAccountManagementState
	ExpiresAt      time.Time
}

var accountSessions = struct {
	sync.Mutex
	activation map[string]*accountActivationSession
	recovery   map[string]*accountRecoverySession
}{
	activation: make(map[string]*accountActivationSession),
	recovery:   make(map[string]*accountRecoverySession),
}

type accountQuestionInput struct {
	ID                string `json:"id"`
	Prompt            string `json:"prompt"`
	Answer            string `json:"answer"`
	Confirmation      string `json:"confirmation"`
	CaseSensitive     bool   `json:"case_sensitive"`
	IgnorePunctuation bool   `json:"ignore_punctuation"`
}

type accountGuardianContact struct {
	Version   uint32 `json:"version"`
	Network   string `json:"network"`
	MailboxID string `json:"mailbox_id"`
	PublicKey string `json:"recovery_public_key"`
	Name      string `json:"display_name,omitempty"`
}

type accountGuardianSetupPayload struct {
	Version   uint32                       `json:"version"`
	Locator   accountLocatorPayload        `json:"locator"`
	MailboxID string                       `json:"mailbox_id"`
	Capsule   account.GuardianShareCapsule `json:"capsule"`
}

type accountGuardianReceipt struct {
	Version   uint32                           `json:"version"`
	Location  walletsdk.AccountIndexerLocation `json:"location"`
	MailboxID string                           `json:"mailbox_id"`
	PackageID string                           `json:"package_id"`
	ShareID   string                           `json:"share_id"`
	Storage   walletsdk.AccountStorageOption   `json:"storage"`
}

type accountGuardianRecoveryRequest struct {
	Version           uint32                           `json:"version"`
	Locator           accountLocatorPayload            `json:"locator"`
	GuardianLocation  walletsdk.AccountIndexerLocation `json:"guardian_location"`
	MailboxID         string                           `json:"mailbox_id"`
	PackageID         string                           `json:"package_id"`
	ShareID           string                           `json:"share_id"`
	RecoveryPublicKey string                           `json:"recovery_public_key"`
}

type accountPreflightRequest struct {
	Password string                                 `json:"password"`
	Wallets  []walletsdk.AccountWalletMetadataInput `json:"wallets"`
}

type accountCreateRequest struct {
	Password     string                                 `json:"password"`
	Wallets      []walletsdk.AccountWalletMetadataInput `json:"wallets"`
	RecoveryMode account.RecoveryMode                   `json:"recovery_mode"`
	Questions    []accountQuestionInput                 `json:"questions"`
	Guardian     *accountGuardianContact                `json:"guardian,omitempty"`
}

type accountAnswersRequest struct {
	SessionID string                  `json:"session_id"`
	Answers   []account.AnswerAttempt `json:"answers"`
	UserShare string                  `json:"user_share,omitempty"`
	Password  string                  `json:"password,omitempty"`
}

func zeroAccountBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func clearWASMBackup(value *account.Backup) {
	if value == nil {
		return
	}
	for index := range value.Wallets {
		value.Wallets[index].Mnemonic = ""
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

func accountStructData(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func accountParseJSON(args []js.Value, target any) error {
	if len(args) < 1 || args[0].Type() != js.TypeString {
		return fmt.Errorf("request must be a JSON string")
	}
	if err := json.Unmarshal([]byte(args[0].String()), target); err != nil {
		return fmt.Errorf("invalid account request")
	}
	return nil
}

func clearAccountActivationSession(session *accountActivationSession) {
	if session == nil {
		return
	}
	if session.Package != nil {
		session.Package.UserShare = account.RecoveryShare{}
	}
	zeroAccountBytes(session.RequestPrivate)
	if session.GuardianShare != nil {
		*session.GuardianShare = account.RecoveryShare{}
	}
	session.RequestPrivate, session.GuardianShare = nil, nil
}

func clearAccountRecoverySession(session *accountRecoverySession) {
	if session == nil {
		return
	}
	zeroAccountBytes(session.RequestPrivate)
	zeroAccountBytes(session.Secret)
	clearWASMBackup(session.Backup)
	for _, share := range []*account.RecoveryShare{session.DKVSShare, session.UserShare, session.GuardianShare} {
		if share != nil {
			*share = account.RecoveryShare{}
		}
	}
	if session.ManagedState != nil {
		for index := range session.ManagedState.State.Wallets {
			session.ManagedState.State.Wallets[index].Mnemonic = ""
		}
		for _, item := range session.ManagedState.ManagedData.Items {
			zeroAccountBytes(item.Payload)
		}
	}
	session.Secret, session.RequestPrivate, session.Backup, session.ManagedState = nil, nil, nil, nil
	session.DKVSShare, session.UserShare, session.GuardianShare = nil, nil, nil
}

func snapshotAccountRecoveryForCommit(session *accountRecoverySession) (*accountRecoverySession, error) {
	managed := *session.ManagedState
	managed.State.Wallets = append([]account.ManagedWallet(nil), managed.State.Wallets...)
	for index := range managed.State.Wallets {
		managed.State.Wallets[index].SubAccounts = append([]account.SubAccount(nil), managed.State.Wallets[index].SubAccounts...)
	}
	snapshot := &accountRecoverySession{Locator: session.Locator,
		Secret: append([]byte(nil), session.Secret...), ManagedState: &managed}
	var err error
	managed.ManagedData, err = account.NormalizeManagedDataBundle(managed.ManagedData)
	return snapshot, err
}

func clearAccountSessions() {
	accountSessions.Lock()
	defer accountSessions.Unlock()
	for id, session := range accountSessions.activation {
		clearAccountActivationSession(session)
		delete(accountSessions.activation, id)
	}
	for id, session := range accountSessions.recovery {
		clearAccountRecoverySession(session)
		delete(accountSessions.recovery, id)
	}
}

func accountCleanupSessions() {
	now := time.Now()
	for id, session := range accountSessions.activation {
		if session == nil || now.After(session.ExpiresAt) {
			clearAccountActivationSession(session)
			delete(accountSessions.activation, id)
		}
	}
	for id, session := range accountSessions.recovery {
		if session == nil || now.After(session.ExpiresAt) {
			clearAccountRecoverySession(session)
			delete(accountSessions.recovery, id)
		}
	}
}

func accountExpectedNetwork() string {
	return _mgr.GetChain()
}

func encodeAccountLocator(value accountLocatorPayload) (string, error) {
	return walletsdk.EncodeAccountPublicLocator(value, accountExpectedNetwork())
}

func decodeAccountLocator(value string) (accountLocatorPayload, error) {
	return walletsdk.DecodeAccountPublicLocator(value, accountExpectedNetwork())
}

func accountPreflight(this js.Value, args []js.Value) any {
	var request accountPreflightRequest
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		result, err := _mgr.AccountPreflight(request.Password, request.Wallets)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountGetStorageOptions(this js.Value, args []js.Value) any {
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		options, err := _mgr.GetAccountStorageOptions()
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(map[string]any{"options": options})
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountAutopayStatus(this js.Value, args []js.Value) any {
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		status, err := _mgr.GetAccountAutopayFundingStatus()
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(status)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountFundAutopay(this js.Value, args []js.Value) any {
	var confirmed walletsdk.AccountAutopayFundingStatus
	if err := accountParseJSON(args, &confirmed); err != nil {
		return js.Global().Get("Promise").Call("resolve", createJsRet(nil, -1, err.Error()))
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		result, err := _mgr.FundAccountAutopay(confirmed)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountConfirmStorage(this js.Value, args []js.Value) any {
	var request struct {
		OptionID    string `json:"option_id"`
		RecordCount uint64 `json:"record_count,omitempty"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		authorization, err := _mgr.ConfirmAccountStorage(request.OptionID, request.RecordCount)
		if err != nil {
			var pending *walletsdk.AccountAutopayPendingError
			if errors.As(err, &pending) {
				return map[string]any{"transaction_id": pending.TransactionID, "pending": true}, -1, err.Error()
			}
			return nil, -1, err.Error()
		}
		data, err := accountStructData(authorization)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountResumeStorageAuthorization(this js.Value, args []js.Value) any {
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		authorization, err := _mgr.PendingAccountStorageAuthorization()
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(authorization)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountCancelStorageAuthorization(this js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	_mgr.CancelPendingAccountStorageAuthorization()
	return createJsRet(map[string]any{"cancelled": true}, 0, "ok")
}

func accountReusePaidStorage(this js.Value, args []js.Value) any {
	var request struct {
		RecordCount uint64 `json:"record_count,omitempty"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		authorization, err := _mgr.ReusePaidAccountStorage(request.RecordCount)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(authorization)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountGuardianIdentity(this js.Value, args []js.Value) any {
	var request struct {
		Password string `json:"password"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		identity, err := _mgr.GetOrCreateAccountGuardianIdentity(request.Password)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(identity)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, _ := json.Marshal(identity)
		data["contact"] = string(encoded)
		return data, 0, "ok"
	}))
}

func accountCreateRecovery(this js.Value, args []js.Value) any {
	var request accountCreateRequest
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		manager := _mgr
		if manager == nil {
			return nil, -1, "Manager not initialized"
		}
		var data map[string]any
		var sessionID string
		var session *accountActivationSession
		err := manager.UseAccountStorageAuthorization(walletsdk.AccountStoragePurposeRecovery,
			func(storage *walletsdk.AccountStorageAuthorization) error {
				backup, err := manager.ExportAccountBackupForPWA(request.Password, request.Wallets)
				if err != nil {
					return err
				}
				defer clearWASMBackup(&backup)
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
						Question: account.KnowledgeQuestion{ID: input.ID, Prompt: input.Prompt, CaseSensitive: input.CaseSensitive, IgnorePunctuation: input.IgnorePunctuation},
						Answer:   input.Answer, Confirmation: input.Confirmation,
					}
				}
				bootstrap, err := account.RootBootstrapBackup(backup)
				if err != nil {
					return err
				}
				options := account.CreateOptions{AccountID: accountIDProvider.AccountID(), Backup: bootstrap, RecoveryMode: request.RecoveryMode, Questions: questions}
				if request.RecoveryMode == account.RecoveryMode2Of3 {
					if request.Guardian == nil {
						return fmt.Errorf("guardian contact is required")
					}
					if request.Guardian.Network != manager.GetChain() {
						return fmt.Errorf("guardian network does not match the account")
					}
					guardianPublicKey, err := base64.RawURLEncoding.DecodeString(request.Guardian.PublicKey)
					if err != nil || len(guardianPublicKey) != 32 {
						return fmt.Errorf("invalid guardian public key")
					}
					options.GuardianMailboxID = request.Guardian.MailboxID
					options.GuardianPublicKey = guardianPublicKey
				}
				recovery := account.NewManager(repository)
				pkg, err := manager.CreateAccountRecoveryPackage(options)
				if err != nil {
					return err
				}
				if err := recovery.Publish(context.Background(), *pkg); err != nil {
					return err
				}
				if _, err := recovery.Load(context.Background(), pkg.Envelope.Locator); err != nil {
					return fmt.Errorf("verify published recovery package: %w", err)
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
				locatorText, err := walletsdk.EncodeAccountPublicLocator(locator, manager.GetChain())
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
				session = &accountActivationSession{Package: pkg, Summary: summary, Authorization: *storage,
					Locator: locator, ExpiresAt: time.Now().Add(accountSessionTTL)}
				result := map[string]any{
					"session_id": sessionID, "locator": locatorText, "user_share": userShare,
					"summary": summary, "storage": storage.Summary,
				}
				if pkg.GuardianCapsule != nil && pkg.Manifest.Guardian != nil {
					setup := accountGuardianSetupPayload{Version: account.Version, Locator: locator,
						MailboxID: pkg.Manifest.Guardian.MailboxID, Capsule: *pkg.GuardianCapsule}
					encoded, err := json.Marshal(setup)
					if err != nil {
						return err
					}
					result["guardian_setup"] = string(encoded)
				}
				data, err = accountStructData(result)
				return err
			})
		if err != nil {
			if session != nil && session.Package != nil {
				session.Package.UserShare = account.RecoveryShare{}
			}
			return nil, -1, err.Error()
		}
		// Do not expose a session from a cancelled or superseded SDK operation.
		accountSessions.Lock()
		accountSessions.activation[sessionID] = session
		accountSessions.Unlock()
		return data, 0, "ok"
	}))
}

func accountAcceptGuardianSetup(this js.Value, args []js.Value) any {
	var request struct {
		Password     string `json:"password"`
		SetupPayload string `json:"setup_payload"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		manager := _mgr
		if manager == nil {
			return nil, -1, "Manager not initialized"
		}
		var setup accountGuardianSetupPayload
		if err := json.Unmarshal([]byte(request.SetupPayload), &setup); err != nil || setup.Version != account.Version {
			return nil, -1, "invalid guardian setup payload"
		}
		var encoded []byte
		err := manager.UseAccountStorageAuthorization(walletsdk.AccountStoragePurposeGuardian,
			func(storage *walletsdk.AccountStorageAuthorization) error {
				identity, err := manager.GetOrCreateAccountGuardianIdentity(request.Password)
				if err != nil {
					return err
				}
				if identity.MailboxID != setup.MailboxID {
					return fmt.Errorf("guardian setup is addressed to another mailbox")
				}
				if err := manager.PutGuardianCapsuleForStorage(*storage, setup.MailboxID, setup.Capsule); err != nil {
					return err
				}
				receipt := accountGuardianReceipt{Version: account.Version, Location: storage.Location,
					MailboxID: setup.MailboxID, PackageID: setup.Capsule.PackageID, ShareID: setup.Capsule.ShareID,
					Storage: storage.Summary}
				encoded, err = json.Marshal(receipt)
				return err
			})
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"receipt": string(encoded)}, 0, "ok"
	}))
}

func accountCheckGuardianSetup(this js.Value, args []js.Value) any {
	var request struct {
		SessionID string `json:"session_id"`
		Receipt   string `json:"receipt"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		var receipt accountGuardianReceipt
		if err := json.Unmarshal([]byte(request.Receipt), &receipt); err != nil || receipt.Version != account.Version {
			return nil, -1, "invalid guardian receipt"
		}
		accountSessions.Lock()
		accountCleanupSessions()
		session := accountSessions.activation[request.SessionID]
		accountSessions.Unlock()
		if session == nil || session.Package == nil || session.Package.Manifest.Guardian == nil {
			return nil, -1, "activation session expired"
		}
		reference := session.Package.Manifest.Guardian
		if receipt.MailboxID != reference.MailboxID || receipt.PackageID != session.Package.Manifest.Locator.PackageID || receipt.ShareID != reference.ShareID {
			return nil, -1, "guardian receipt does not match recovery package"
		}
		encodedCapsule, err := _mgr.LoadAccountGuardianCapsule(
			receipt.Location, receipt.MailboxID, receipt.PackageID, receipt.ShareID,
		)
		if err != nil {
			return nil, -1, err.Error()
		}
		var capsule account.GuardianShareCapsule
		if err := json.Unmarshal(encodedCapsule, &capsule); err != nil {
			return nil, -1, "invalid guardian capsule"
		}
		hash, err := account.HashGuardianCapsule(capsule)
		if err != nil || hash != reference.CapsuleHash {
			return nil, -1, "guardian capsule verification failed"
		}
		session.GuardianVerified = true
		session.Locator.GuardianLocation = &receipt.Location
		if err := _mgr.SignAccountPublicLocator(&session.Locator); err != nil {
			return nil, -1, err.Error()
		}
		locatorText, err := encodeAccountLocator(session.Locator)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := accountStructData(map[string]any{"stored": true, "locator": locatorText, "storage": receipt.Storage})
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountRehearse(this js.Value, args []js.Value) any {
	var request accountAnswersRequest
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		accountSessions.Lock()
		accountCleanupSessions()
		session := accountSessions.activation[request.SessionID]
		accountSessions.Unlock()
		if session == nil || session.Package == nil {
			return nil, -1, "activation session expired"
		}
		if strings.TrimSpace(request.Password) == "" {
			return nil, -1, "wallet password is required"
		}
		if session.Package.Envelope.Locator.RecoveryMode == account.RecoveryMode2Of3 && !session.GuardianVerified {
			return nil, -1, "guardian share has not been verified"
		}
		dkvsShare, err := account.RecoverDKVSShare(session.Package.DKVSShareCapsule, session.Package.KnowledgeBundle, request.Answers)
		if err != nil {
			return nil, -1, err.Error()
		}
		companion := account.RecoveryShare{}
		if session.Package.Envelope.Locator.RecoveryMode == account.RecoveryMode2Of3 {
			if session.GuardianShare == nil {
				return nil, -1, "guardian recovery response is required for the rehearsal"
			}
			companion = *session.GuardianShare
		} else {
			supplied, err := account.DecodeRecoveryShare(request.UserShare)
			if err != nil {
				return nil, -1, err.Error()
			}
			if supplied != session.Package.UserShare {
				return nil, -1, "user recovery share does not match the activation session"
			}
			companion = session.Package.UserShare
		}
		backup, secret, err := account.RecoverAccount(session.Package.Envelope, dkvsShare, companion)
		if err != nil {
			if session.Package.Envelope.Locator.RecoveryMode == account.RecoveryMode2Of2 {
				return nil, -1, "knowledge recovery share does not match the activation package"
			}
			return nil, -1, "recovered shares do not match the activation package"
		}
		defer zeroAccountBytes(secret)
		defer clearWASMBackup(&backup)
		publicLocator, err := encodeAccountLocator(session.Locator)
		if err != nil {
			return nil, -1, err.Error()
		}
		if err := _mgr.ActivateAccountManagement(secret, request.Password, session.Authorization,
			session.Package.Envelope.Locator, publicLocator); err != nil {
			return nil, -1, err.Error()
		}
		zeroAccountBytes(session.RequestPrivate)
		session.RequestPrivate = nil
		session.GuardianShare = nil
		accountSessions.Lock()
		delete(accountSessions.activation, request.SessionID)
		accountSessions.Unlock()
		data, err := accountStructData(map[string]any{"summary": session.Summary, "verified": true})
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountLoadRecovery(this js.Value, args []js.Value) any {
	var request struct {
		Locator string `json:"locator"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		locator, err := decodeAccountLocator(request.Locator)
		if err != nil {
			return nil, -1, err.Error()
		}
		pkg, err := _mgr.LoadAccountRecoveryPackage(locator.StorageLocation, locator.Locator)
		if err != nil {
			return nil, -1, err.Error()
		}
		sessionID, err := accountRandomID(nil)
		if err != nil {
			return nil, -1, err.Error()
		}
		accountSessions.Lock()
		accountCleanupSessions()
		accountSessions.recovery[sessionID] = &accountRecoverySession{Package: pkg, Locator: locator, ExpiresAt: time.Now().Add(accountSessionTTL)}
		accountSessions.Unlock()
		questions := make([]account.KnowledgeQuestion, 0, len(pkg.KnowledgeBundle.QuestionShares))
		for _, entry := range pkg.KnowledgeBundle.QuestionShares {
			questions = append(questions, entry.Question)
		}
		data, err := accountStructData(map[string]any{"session_id": sessionID, "locator": locator.Locator, "questions": questions,
			"guardian": pkg.Manifest.Guardian, "has_guardian_location": locator.GuardianLocation != nil})
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountRecoverKnowledge(this js.Value, args []js.Value) any {
	var request accountAnswersRequest
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		accountSessions.Lock()
		accountCleanupSessions()
		session := accountSessions.recovery[request.SessionID]
		accountSessions.Unlock()
		if session == nil || session.Package == nil {
			return nil, -1, "recovery session expired"
		}
		share, err := account.RecoverDKVSShare(session.Package.DKVSShareCapsule, session.Package.KnowledgeBundle, request.Answers)
		if err != nil {
			return nil, -1, err.Error()
		}
		session.DKVSShare = &share
		return map[string]any{"recovered": true}, 0, "ok"
	}))
}

func accountSetUserShare(this js.Value, args []js.Value) any {
	var request struct {
		SessionID string `json:"session_id"`
		UserShare string `json:"user_share"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		share, err := account.DecodeRecoveryShare(request.UserShare)
		if err != nil {
			return nil, -1, err.Error()
		}
		accountSessions.Lock()
		accountCleanupSessions()
		session := accountSessions.recovery[request.SessionID]
		accountSessions.Unlock()
		if session == nil || session.Package == nil || share.PackageID != session.Package.Envelope.Locator.PackageID {
			return nil, -1, "user share does not match recovery package"
		}
		session.UserShare = &share
		return map[string]any{"accepted": true}, 0, "ok"
	}))
}

func accountCreateGuardianRequest(this js.Value, args []js.Value) any {
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		accountSessions.Lock()
		accountCleanupSessions()
		recoverySession := accountSessions.recovery[request.SessionID]
		activationSession := accountSessions.activation[request.SessionID]
		accountSessions.Unlock()
		var pkg *account.RecoveryPackage
		var locator accountLocatorPayload
		var storeKey func([]byte)
		switch {
		case recoverySession != nil:
			pkg, locator = recoverySession.Package, recoverySession.Locator
			storeKey = func(value []byte) {
				zeroAccountBytes(recoverySession.RequestPrivate)
				recoverySession.RequestPrivate = value
			}
		case activationSession != nil:
			pkg, locator = activationSession.Package, activationSession.Locator
			if !activationSession.GuardianVerified {
				return nil, -1, "guardian capsule has not been stored"
			}
			storeKey = func(value []byte) {
				zeroAccountBytes(activationSession.RequestPrivate)
				activationSession.RequestPrivate = value
			}
		default:
			return nil, -1, "guardian recovery session expired"
		}
		if pkg == nil || pkg.Manifest.Guardian == nil || locator.GuardianLocation == nil || pkg.Envelope.Locator.RecoveryMode != account.RecoveryMode2Of3 {
			return nil, -1, "guardian recovery is unavailable"
		}
		privateKey, publicKey, err := account.GenerateGuardianKey(nil)
		if err != nil {
			return nil, -1, err.Error()
		}
		storeKey(privateKey)
		reference := pkg.Manifest.Guardian
		payload := accountGuardianRecoveryRequest{Version: account.Version, Locator: locator,
			GuardianLocation: *locator.GuardianLocation, MailboxID: reference.MailboxID,
			PackageID: pkg.Envelope.Locator.PackageID, ShareID: reference.ShareID,
			RecoveryPublicKey: base64.RawURLEncoding.EncodeToString(publicKey)}
		encoded, _ := json.Marshal(payload)
		return map[string]any{"request": string(encoded)}, 0, "ok"
	}))
}

func accountCreateGuardianResponse(this js.Value, args []js.Value) any {
	var request struct {
		Password string `json:"password"`
		Request  string `json:"request"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		var payload accountGuardianRecoveryRequest
		if err := json.Unmarshal([]byte(request.Request), &payload); err != nil || payload.Version != account.Version {
			return nil, -1, "invalid guardian recovery request"
		}
		encodedCapsule, err := _mgr.LoadAccountGuardianCapsule(
			payload.GuardianLocation, payload.MailboxID, payload.PackageID, payload.ShareID,
		)
		if err != nil {
			return nil, -1, err.Error()
		}
		var stored account.GuardianShareCapsule
		if err := json.Unmarshal(encodedCapsule, &stored); err != nil {
			return nil, -1, "invalid guardian capsule"
		}
		if stored.PackageID != payload.PackageID || stored.ShareID != payload.ShareID {
			return nil, -1, "guardian capsule does not match the recovery request"
		}
		privateKey, err := _mgr.LoadAccountGuardianPrivateKey(request.Password)
		if err != nil {
			return nil, -1, err.Error()
		}
		defer zeroAccountBytes(privateKey)
		share, err := account.DecryptGuardianShare(stored, privateKey)
		if err != nil {
			return nil, -1, err.Error()
		}
		publicKey, err := base64.RawURLEncoding.DecodeString(payload.RecoveryPublicKey)
		if err != nil || len(publicKey) != 32 {
			return nil, -1, "invalid recovery public key"
		}
		response, err := account.EncryptGuardianShare(share, publicKey, nil)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, _ := json.Marshal(response)
		return map[string]any{"response": string(encoded)}, 0, "ok"
	}))
}

func accountConsumeGuardianResponse(this js.Value, args []js.Value) any {
	var request struct {
		SessionID string `json:"session_id"`
		Response  string `json:"response"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		accountSessions.Lock()
		accountCleanupSessions()
		recoverySession := accountSessions.recovery[request.SessionID]
		activationSession := accountSessions.activation[request.SessionID]
		accountSessions.Unlock()
		var pkg *account.RecoveryPackage
		var privateKey []byte
		var accept func(account.RecoveryShare)
		switch {
		case recoverySession != nil:
			pkg, privateKey = recoverySession.Package, recoverySession.RequestPrivate
			accept = func(share account.RecoveryShare) {
				recoverySession.GuardianShare = &share
				zeroAccountBytes(recoverySession.RequestPrivate)
				recoverySession.RequestPrivate = nil
			}
		case activationSession != nil:
			pkg, privateKey = activationSession.Package, activationSession.RequestPrivate
			accept = func(share account.RecoveryShare) {
				activationSession.GuardianShare = &share
				zeroAccountBytes(activationSession.RequestPrivate)
				activationSession.RequestPrivate = nil
			}
		default:
			return nil, -1, "guardian recovery request has expired"
		}
		if pkg == nil || pkg.Manifest.Guardian == nil || len(privateKey) != 32 {
			return nil, -1, "guardian recovery request has expired"
		}
		var capsule account.GuardianShareCapsule
		if err := json.Unmarshal([]byte(request.Response), &capsule); err != nil {
			return nil, -1, "invalid guardian response"
		}
		share, err := account.DecryptGuardianShare(capsule, privateKey)
		if err != nil {
			return nil, -1, err.Error()
		}
		reference := pkg.Manifest.Guardian
		if share.PackageID != pkg.Envelope.Locator.PackageID || share.Checksum != reference.ShareID ||
			share.Role != account.ShareRoleGuardian || share.Threshold != pkg.Manifest.Threshold || share.Total != pkg.Manifest.Total {
			return nil, -1, "guardian response does not match the recovery package"
		}
		accept(share)
		return map[string]any{"accepted": true}, 0, "ok"
	}))
}

func accountPreviewRecovery(this js.Value, args []js.Value) any {
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		accountSessions.Lock()
		accountCleanupSessions()
		session := accountSessions.recovery[request.SessionID]
		accountSessions.Unlock()
		if session == nil || session.Package == nil {
			return nil, -1, "recovery session expired"
		}
		shares := make([]account.RecoveryShare, 0, 2)
		if session.DKVSShare != nil {
			shares = append(shares, *session.DKVSShare)
		}
		if session.UserShare != nil && len(shares) < 2 {
			shares = append(shares, *session.UserShare)
		}
		if session.GuardianShare != nil && len(shares) < 2 {
			shares = append(shares, *session.GuardianShare)
		}
		if len(shares) < 2 {
			return nil, -1, account.ErrInsufficientShares.Error()
		}
		backup, secret, err := account.RecoverAccount(session.Package.Envelope, shares...)
		if err != nil {
			return nil, -1, err.Error()
		}
		if len(backup.Wallets) == 0 {
			zeroAccountBytes(secret)
			return nil, -1, "recovery package does not contain a root wallet"
		}
		managed, err := _mgr.LoadAccountManagementStateForRecovery(
			session.Locator.StorageLocation, session.Locator.Locator, secret, backup.Wallets[0].Mnemonic)
		if err != nil {
			zeroAccountBytes(secret)
			clearWASMBackup(&backup)
			return nil, -1, fmt.Sprintf("load latest account state: %v", err)
		}
		latestBackup, err := account.BackupFromManagedState(managed.State)
		if err != nil {
			zeroAccountBytes(secret)
			clearWASMBackup(&backup)
			return nil, -1, err.Error()
		}
		clearWASMBackup(&backup)
		accountSessions.Lock()
		accountCleanupSessions()
		if accountSessions.recovery[request.SessionID] != session {
			accountSessions.Unlock()
			zeroAccountBytes(secret)
			clearWASMBackup(&latestBackup)
			clearAccountRecoverySession(&accountRecoverySession{ManagedState: managed})
			return nil, -1, "recovery session expired"
		}
		clearWASMBackup(session.Backup)
		zeroAccountBytes(session.Secret)
		session.Secret = secret
		session.Backup = &latestBackup
		session.ManagedState = managed
		session.ExpiresAt = time.Now().Add(accountSessionTTL)
		accountSessions.Unlock()
		data, err := accountStructData(map[string]any{"summary": account.SummarizeBackup(session.Package.Envelope.Locator, latestBackup)})
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountCommitRecovery(this js.Value, args []js.Value) any {
	var request struct {
		SessionID string `json:"session_id"`
		Password  string `json:"password"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		accountSessions.Lock()
		accountCleanupSessions()
		session := accountSessions.recovery[request.SessionID]
		if session == nil || session.Backup == nil {
			accountSessions.Unlock()
			return nil, -1, "account recovery preview is required"
		}
		if session.ManagedState == nil || len(session.Secret) != 32 {
			accountSessions.Unlock()
			return nil, -1, "latest managed account state is unavailable"
		}
		// Once the user confirms restore, it owns an independent snapshot.
		// Aborting/expiring the preview must not zero buffers used by its commit.
		snapshot, err := snapshotAccountRecoveryForCommit(session)
		accountSessions.Unlock()
		defer clearAccountRecoverySession(snapshot)
		if err != nil {
			return nil, -1, err.Error()
		}
		locator := snapshot.Locator
		publicLocator, err := encodeAccountLocator(locator)
		if err != nil {
			return nil, -1, err.Error()
		}
		wallets, err := _mgr.RestoreAccountManagementState(
			*snapshot.ManagedState, snapshot.Secret, request.Password, locator.Locator,
			walletsdk.AccountManagementRestoreOptions{
				Location: locator.StorageLocation, StorageMode: locator.StorageMode,
				RecordTTL: locator.RecordTTL, AutopayContract: locator.AutopayContract,
				PublicLocator: publicLocator,
			})
		if err != nil {
			return nil, -1, err.Error()
		}
		status := _mgr.GetAccountManagementStatus()
		if !status.Active || status.RootWalletID == 0 || status.AccountID == "" {
			return nil, -1, "restored account has no explicit root wallet"
		}
		accountSessions.Lock()
		clearAccountRecoverySession(session)
		delete(accountSessions.recovery, request.SessionID)
		accountSessions.Unlock()
		data, err := accountStructData(map[string]any{
			"wallets": wallets, "root_wallet_id": status.RootWalletID,
			"account_id": status.AccountID,
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountAbortSession(this js.Value, args []js.Value) any {
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	accountSessions.Lock()
	clearAccountRecoverySession(accountSessions.recovery[request.SessionID])
	clearAccountActivationSession(accountSessions.activation[request.SessionID])
	delete(accountSessions.activation, request.SessionID)
	delete(accountSessions.recovery, request.SessionID)
	accountSessions.Unlock()
	return createJsRet(map[string]any{"aborted": true}, 0, "ok")
}

func accountStatus(this js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	data, err := accountStructData(_mgr.GetAccountManagementStatus())
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return createJsRet(data, 0, "ok")
}

func init() {
	obj := js.Global().Get("Object").New()
	obj.Set("status", js.FuncOf(accountStatus))
	obj.Set("preflight", js.FuncOf(accountPreflight))
	obj.Set("getStorageOptions", js.FuncOf(accountGetStorageOptions))
	obj.Set("autopayStatus", js.FuncOf(accountAutopayStatus))
	obj.Set("fundAutopay", js.FuncOf(accountFundAutopay))
	obj.Set("confirmStorage", js.FuncOf(accountConfirmStorage))
	obj.Set("resumeStorageAuthorization", js.FuncOf(accountResumeStorageAuthorization))
	obj.Set("cancelStorageAuthorization", js.FuncOf(accountCancelStorageAuthorization))
	obj.Set("reusePaidStorage", js.FuncOf(accountReusePaidStorage))
	obj.Set("guardianIdentity", js.FuncOf(accountGuardianIdentity))
	obj.Set("createRecovery", js.FuncOf(accountCreateRecovery))
	obj.Set("acceptGuardianSetup", js.FuncOf(accountAcceptGuardianSetup))
	obj.Set("checkGuardianSetup", js.FuncOf(accountCheckGuardianSetup))
	obj.Set("rehearse", js.FuncOf(accountRehearse))
	obj.Set("loadRecovery", js.FuncOf(accountLoadRecovery))
	obj.Set("recoverKnowledge", js.FuncOf(accountRecoverKnowledge))
	obj.Set("setUserShare", js.FuncOf(accountSetUserShare))
	obj.Set("createGuardianRequest", js.FuncOf(accountCreateGuardianRequest))
	obj.Set("createGuardianResponse", js.FuncOf(accountCreateGuardianResponse))
	obj.Set("consumeGuardianResponse", js.FuncOf(accountConsumeGuardianResponse))
	obj.Set("previewRecovery", js.FuncOf(accountPreviewRecovery))
	obj.Set("commitRecovery", js.FuncOf(accountCommitRecovery))
	obj.Set("abortSession", js.FuncOf(accountAbortSession))
	js.Global().Set("sat20account_wasm", obj)
}
