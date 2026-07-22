//go:build js && wasm
// +build js,wasm

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	walletsdk "github.com/sat20-labs/sat20wallet/sdk/wallet"
)

const (
	accountLocatorPrefix = "sat20account1:"
	accountSessionTTL    = 20 * time.Minute
)

type accountLocatorPayload struct {
	Version          uint32                            `json:"version"`
	Network          string                            `json:"network"`
	StorageLocation  walletsdk.AccountIndexerLocation  `json:"storage_location"`
	GuardianLocation *walletsdk.AccountIndexerLocation `json:"guardian_location,omitempty"`
	Locator          account.Locator                   `json:"locator"`
}

type accountStorageSession struct {
	Authorization walletsdk.AccountStorageAuthorization
	ExpiresAt     time.Time
}

type accountActivationSession struct {
	Package          *account.RecoveryPackage
	Authorization    walletsdk.AccountStorageAuthorization
	Locator          accountLocatorPayload
	GuardianVerified bool
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
	ExpiresAt      time.Time
}

var accountSessions = struct {
	sync.Mutex
	storage    map[string]*accountStorageSession
	activation map[string]*accountActivationSession
	recovery   map[string]*accountRecoverySession
}{
	storage:    make(map[string]*accountStorageSession),
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
	Password               string                                 `json:"password"`
	Wallets                []walletsdk.AccountWalletMetadataInput `json:"wallets"`
	RecoveryMode           account.RecoveryMode                   `json:"recovery_mode"`
	Questions              []accountQuestionInput                 `json:"questions"`
	Guardian               *accountGuardianContact                `json:"guardian,omitempty"`
	StorageAuthorizationID string                                 `json:"storage_authorization_id"`
}

type accountAnswersRequest struct {
	SessionID string                  `json:"session_id"`
	Answers   []account.AnswerAttempt `json:"answers"`
	UserShare string                  `json:"user_share,omitempty"`
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

func accountCleanupSessions() {
	now := time.Now()
	for id, session := range accountSessions.storage {
		if session == nil || now.After(session.ExpiresAt) {
			delete(accountSessions.storage, id)
		}
	}
	for id, session := range accountSessions.activation {
		if session == nil || now.After(session.ExpiresAt) {
			if session != nil && session.Package != nil {
				session.Package.UserShare = account.RecoveryShare{}
			}
			delete(accountSessions.activation, id)
		}
	}
	for id, session := range accountSessions.recovery {
		if session == nil || now.After(session.ExpiresAt) {
			if session != nil {
				zeroAccountBytes(session.RequestPrivate)
				clearWASMBackup(session.Backup)
			}
			delete(accountSessions.recovery, id)
		}
	}
}

func encodeAccountLocator(value accountLocatorPayload) (string, error) {
	if value.Version != account.Version || value.StorageLocation.Host == "" {
		return "", account.ErrInvalidRecoveryPackage
	}
	if err := account.ValidateLocator(value.Locator); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return accountLocatorPrefix + base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeAccountLocator(value string) (accountLocatorPayload, error) {
	var locator accountLocatorPayload
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, accountLocatorPrefix) {
		return locator, account.ErrInvalidRecoveryPackage
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, accountLocatorPrefix))
	if err != nil || len(encoded) > account.MaxRecoveryObjectSize {
		return locator, account.ErrInvalidRecoveryPackage
	}
	if err := json.Unmarshal(encoded, &locator); err != nil {
		return locator, account.ErrInvalidRecoveryPackage
	}
	if locator.Version != account.Version || locator.StorageLocation.Host == "" {
		return locator, account.ErrInvalidRecoveryPackage
	}
	if err := account.ValidateLocator(locator.Locator); err != nil {
		return locator, err
	}
	return locator, nil
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

func accountConfirmStorage(this js.Value, args []js.Value) any {
	var request struct {
		OptionID string `json:"option_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		authorization, err := _mgr.ConfirmAccountStorage(request.OptionID)
		if err != nil {
			return nil, -1, err.Error()
		}
		id, err := accountRandomID(nil)
		if err != nil {
			return nil, -1, err.Error()
		}
		authorization.ID = id
		accountSessions.Lock()
		accountCleanupSessions()
		accountSessions.storage[id] = &accountStorageSession{Authorization: *authorization, ExpiresAt: time.Now().Add(accountSessionTTL)}
		accountSessions.Unlock()
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
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		accountSessions.Lock()
		accountCleanupSessions()
		storage := accountSessions.storage[request.StorageAuthorizationID]
		accountSessions.Unlock()
		if storage == nil {
			return nil, -1, "account storage authorization expired"
		}
		backup, err := _mgr.ExportAccountBackupForPWA(request.Password, request.Wallets)
		if err != nil {
			return nil, -1, err.Error()
		}
		defer clearWASMBackup(&backup)
		repository, err := _mgr.NewAccountRepositoryForStorage(storage.Authorization)
		if err != nil {
			return nil, -1, err.Error()
		}
		accountIDProvider, ok := repository.(interface{ AccountID() string })
		if !ok {
			return nil, -1, "account repository does not expose account id"
		}
		questions := make([]account.QuestionAnswer, len(request.Questions))
		for index, input := range request.Questions {
			questions[index] = account.QuestionAnswer{
				Question: account.KnowledgeQuestion{ID: input.ID, Prompt: input.Prompt, CaseSensitive: input.CaseSensitive, IgnorePunctuation: input.IgnorePunctuation},
				Answer:   input.Answer, Confirmation: input.Confirmation,
			}
		}
		options := account.CreateOptions{AccountID: accountIDProvider.AccountID(), Backup: backup, RecoveryMode: request.RecoveryMode, Questions: questions}
		if request.RecoveryMode == account.RecoveryMode2Of3 {
			if request.Guardian == nil {
				return nil, -1, "guardian contact is required"
			}
			guardianPublicKey, err := base64.RawURLEncoding.DecodeString(request.Guardian.PublicKey)
			if err != nil || len(guardianPublicKey) != 32 {
				return nil, -1, "invalid guardian public key"
			}
			options.GuardianMailboxID = request.Guardian.MailboxID
			options.GuardianPublicKey = guardianPublicKey
		}
		manager := account.NewManager(repository)
		pkg, err := manager.CreateRecoveryPackage(options)
		if err != nil {
			return nil, -1, err.Error()
		}
		if err := manager.Publish(context.Background(), *pkg); err != nil {
			return nil, -1, err.Error()
		}
		if _, err := manager.Load(context.Background(), pkg.Envelope.Locator); err != nil {
			return nil, -1, fmt.Sprintf("verify published recovery package: %v", err)
		}
		locator := accountLocatorPayload{Version: account.Version, Network: walletsdk.GetChainParam_SatsNet().Name,
			StorageLocation: storage.Authorization.Location, Locator: pkg.Envelope.Locator}
		locatorText, err := encodeAccountLocator(locator)
		if err != nil {
			return nil, -1, err.Error()
		}
		userShare, err := account.EncodeRecoveryShare(pkg.UserShare)
		if err != nil {
			return nil, -1, err.Error()
		}
		sessionID, err := accountRandomID(nil)
		if err != nil {
			return nil, -1, err.Error()
		}
		accountSessions.Lock()
		accountSessions.activation[sessionID] = &accountActivationSession{Package: pkg, Authorization: storage.Authorization,
			Locator: locator, ExpiresAt: time.Now().Add(accountSessionTTL)}
		accountSessions.Unlock()
		result := map[string]any{
			"session_id": sessionID, "locator": locatorText, "user_share": userShare,
			"summary": account.SummarizeBackup(pkg.Envelope.Locator, backup), "storage": storage.Authorization.Summary,
		}
		if pkg.GuardianCapsule != nil && pkg.Manifest.Guardian != nil {
			setup := accountGuardianSetupPayload{Version: account.Version, Locator: locator,
				MailboxID: pkg.Manifest.Guardian.MailboxID, Capsule: *pkg.GuardianCapsule}
			encoded, _ := json.Marshal(setup)
			result["guardian_setup"] = string(encoded)
		}
		data, err := accountStructData(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	}))
}

func accountAcceptGuardianSetup(this js.Value, args []js.Value) any {
	var request struct {
		Password               string `json:"password"`
		SetupPayload           string `json:"setup_payload"`
		StorageAuthorizationID string `json:"storage_authorization_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _mgr == nil {
			return nil, -1, "Manager not initialized"
		}
		var setup accountGuardianSetupPayload
		if err := json.Unmarshal([]byte(request.SetupPayload), &setup); err != nil || setup.Version != account.Version {
			return nil, -1, "invalid guardian setup payload"
		}
		accountSessions.Lock()
		accountCleanupSessions()
		storage := accountSessions.storage[request.StorageAuthorizationID]
		accountSessions.Unlock()
		if storage == nil {
			return nil, -1, "account storage authorization expired"
		}
		identity, err := _mgr.GetOrCreateAccountGuardianIdentity(request.Password)
		if err != nil {
			return nil, -1, err.Error()
		}
		if identity.MailboxID != setup.MailboxID {
			return nil, -1, "guardian setup is addressed to another mailbox"
		}
		if err := _mgr.PutGuardianCapsuleForStorage(storage.Authorization, setup.MailboxID, setup.Capsule); err != nil {
			return nil, -1, err.Error()
		}
		receipt := accountGuardianReceipt{Version: account.Version, Location: storage.Authorization.Location,
			MailboxID: setup.MailboxID, PackageID: setup.Capsule.PackageID, ShareID: setup.Capsule.ShareID,
			Storage: storage.Authorization.Summary}
		encoded, _ := json.Marshal(receipt)
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
		client := walletsdk.NewSatsNetDKVSClient(receipt.Location.Scheme, receipt.Location.Host, receipt.Location.Proxy, nil)
		record, err := client.GetMailboxShare(receipt.MailboxID, receipt.PackageID, receipt.ShareID)
		if err != nil {
			return nil, -1, err.Error()
		}
		var capsule account.GuardianShareCapsule
		if err := json.Unmarshal(record.Value, &capsule); err != nil {
			return nil, -1, "invalid guardian capsule"
		}
		hash, err := account.HashGuardianCapsule(capsule)
		if err != nil || hash != reference.CapsuleHash {
			return nil, -1, "guardian capsule verification failed"
		}
		session.GuardianVerified = true
		session.Locator.GuardianLocation = &receipt.Location
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
		if session.Package.Envelope.Locator.RecoveryMode == account.RecoveryMode2Of3 && !session.GuardianVerified {
			return nil, -1, "guardian share has not been verified"
		}
		dkvsShare, err := account.RecoverDKVSShare(session.Package.DKVSShareCapsule, session.Package.KnowledgeBundle, request.Answers)
		if err != nil {
			return nil, -1, err.Error()
		}
		companion := session.Package.UserShare
		if session.Package.Envelope.Locator.RecoveryMode == account.RecoveryMode2Of2 {
			companion, err = account.DecodeRecoveryShare(request.UserShare)
			if err != nil {
				return nil, -1, err.Error()
			}
		}
		backup, secret, err := account.RecoverAccount(session.Package.Envelope, dkvsShare, companion)
		if err != nil {
			return nil, -1, err.Error()
		}
		zeroAccountBytes(secret)
		defer clearWASMBackup(&backup)
		data, err := accountStructData(map[string]any{"summary": account.SummarizeBackup(session.Package.Envelope.Locator, backup), "verified": true})
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
		client := walletsdk.NewSatsNetDKVSClient(locator.StorageLocation.Scheme, locator.StorageLocation.Host, locator.StorageLocation.Proxy, nil)
		repository, err := walletsdk.NewReadOnlyAccountDKVSRepository(client, locator.Locator.AccountID)
		if err != nil {
			return nil, -1, err.Error()
		}
		pkg, err := account.NewManager(repository).Load(context.Background(), locator.Locator)
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
		session := accountSessions.recovery[request.SessionID]
		accountSessions.Unlock()
		if session == nil || session.Package == nil || session.Package.Manifest.Guardian == nil || session.Locator.GuardianLocation == nil {
			return nil, -1, "guardian recovery is unavailable"
		}
		privateKey, publicKey, err := account.GenerateGuardianKey(nil)
		if err != nil {
			return nil, -1, err.Error()
		}
		zeroAccountBytes(session.RequestPrivate)
		session.RequestPrivate = privateKey
		reference := session.Package.Manifest.Guardian
		payload := accountGuardianRecoveryRequest{Version: account.Version, Locator: session.Locator,
			GuardianLocation: *session.Locator.GuardianLocation, MailboxID: reference.MailboxID,
			PackageID: session.Package.Envelope.Locator.PackageID, ShareID: reference.ShareID,
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
		client := walletsdk.NewSatsNetDKVSClient(payload.GuardianLocation.Scheme, payload.GuardianLocation.Host, payload.GuardianLocation.Proxy, nil)
		record, err := client.GetMailboxShare(payload.MailboxID, payload.PackageID, payload.ShareID)
		if err != nil {
			return nil, -1, err.Error()
		}
		var stored account.GuardianShareCapsule
		if err := json.Unmarshal(record.Value, &stored); err != nil {
			return nil, -1, "invalid guardian capsule"
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
		session := accountSessions.recovery[request.SessionID]
		accountSessions.Unlock()
		if session == nil || len(session.RequestPrivate) != 32 {
			return nil, -1, "guardian recovery request has expired"
		}
		var capsule account.GuardianShareCapsule
		if err := json.Unmarshal([]byte(request.Response), &capsule); err != nil {
			return nil, -1, "invalid guardian response"
		}
		share, err := account.DecryptGuardianShare(capsule, session.RequestPrivate)
		if err != nil {
			return nil, -1, err.Error()
		}
		session.GuardianShare = &share
		zeroAccountBytes(session.RequestPrivate)
		session.RequestPrivate = nil
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
		zeroAccountBytes(secret)
		clearWASMBackup(session.Backup)
		session.Backup = &backup
		session.ExpiresAt = time.Now().Add(accountSessionTTL)
		data, err := accountStructData(map[string]any{"summary": account.SummarizeBackup(session.Package.Envelope.Locator, backup)})
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
		accountSessions.Unlock()
		if session == nil || session.Backup == nil {
			return nil, -1, "account recovery preview is required"
		}
		wallets, err := _mgr.RestoreAccountBackupWithResult(*session.Backup, request.Password)
		if err != nil {
			return nil, -1, err.Error()
		}
		clearWASMBackup(session.Backup)
		zeroAccountBytes(session.RequestPrivate)
		accountSessions.Lock()
		delete(accountSessions.recovery, request.SessionID)
		accountSessions.Unlock()
		data, err := accountStructData(map[string]any{"wallets": wallets})
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
	if session := accountSessions.recovery[request.SessionID]; session != nil {
		zeroAccountBytes(session.RequestPrivate)
		clearWASMBackup(session.Backup)
	}
	if session := accountSessions.activation[request.SessionID]; session != nil && session.Package != nil {
		session.Package.UserShare = account.RecoveryShare{}
	}
	delete(accountSessions.storage, request.SessionID)
	delete(accountSessions.activation, request.SessionID)
	delete(accountSessions.recovery, request.SessionID)
	accountSessions.Unlock()
	return createJsRet(map[string]any{"aborted": true}, 0, "ok")
}

func init() {
	obj := js.Global().Get("Object").New()
	obj.Set("preflight", js.FuncOf(accountPreflight))
	obj.Set("getStorageOptions", js.FuncOf(accountGetStorageOptions))
	obj.Set("confirmStorage", js.FuncOf(accountConfirmStorage))
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
