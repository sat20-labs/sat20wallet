package account

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

const (
	recoveryStorageMagic        = "ACRP"
	guardianStorageMagic        = "AGRD"
	recoveryStorageCodecVersion = uint8(1)
	recoveryPackageCodecVersion = uint8(3)
)

func EncodeGuardianCapsuleStorage(value GuardianShareCapsule) ([]byte, error) {
	if err := validateGuardianCapsule(value); err != nil {
		return nil, err
	}
	packageID, _ := hex.DecodeString(value.PackageID)
	epk, err := decodeRecoveryBase64(value.EphemeralPublicKey)
	if err != nil {
		return nil, err
	}
	nonce, err := decodeRecoveryBase64(value.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := decodeRecoveryBase64(value.Ciphertext)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	if err := encoder.Raw([]byte(guardianStorageMagic)); err != nil {
		return nil, err
	}
	if err := encoder.U8(recoveryStorageCodecVersion); err != nil {
		return nil, err
	}
	if err := encoder.Raw(packageID); err != nil {
		return nil, err
	}
	if err := encoder.String(value.ShareID, 1, 128); err != nil {
		return nil, err
	}
	if err := encoder.Bytes(epk, 32, 32); err != nil {
		return nil, err
	}
	if err := encoder.Bytes(nonce, 1, 64); err != nil {
		return nil, err
	}
	if err := encoder.Bytes(ciphertext, 1, MaxRecoveryObjectSize); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeGuardianCapsuleStorage(value []byte) (*GuardianShareCapsule, error) {
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(guardianStorageMagic)))
	if err != nil || string(magic) != guardianStorageMagic {
		return nil, ErrInvalidRecoveryPackage
	}
	version, err := decoder.U8()
	if err != nil || version != recoveryStorageCodecVersion {
		return nil, ErrInvalidRecoveryPackage
	}
	packageID, err := decoder.Raw(16)
	if err != nil {
		return nil, err
	}
	shareID, err := decoder.String(1, 128)
	if err != nil {
		return nil, err
	}
	epk, err := decoder.Bytes(32, 32)
	if err != nil {
		return nil, err
	}
	nonce, err := decoder.Bytes(1, 64)
	if err != nil {
		return nil, err
	}
	ciphertext, err := decoder.Bytes(1, MaxRecoveryObjectSize)
	if err != nil || reader.Len() != 0 {
		return nil, ErrInvalidRecoveryPackage
	}
	result := &GuardianShareCapsule{
		Version: Version, PackageID: hex.EncodeToString(packageID), ShareID: shareID,
		Algorithm: "x25519-aes-256-gcm", EphemeralPublicKey: encodeRecoveryBase64(epk),
		Nonce: encodeRecoveryBase64(nonce), Ciphertext: encodeRecoveryBase64(ciphertext),
	}
	if err := validateGuardianCapsule(*result); err != nil {
		return nil, err
	}
	return result, nil
}

// EncodeRecoveryPackageStorage encodes the immutable DKVS recovery package as
// one deterministic record. Repeated versions, locators, algorithms and
// hashes derivable from the envelope are intentionally omitted.
func EncodeRecoveryPackageStorage(value RecoveryPackage) ([]byte, error) {
	if err := validateStoredRecoveryPackage(value); err != nil {
		return nil, err
	}
	accountID, _ := hex.DecodeString(value.Envelope.Locator.AccountID)
	packageID, _ := hex.DecodeString(value.Envelope.Locator.PackageID)
	envelopeNonce, err := decodeRecoveryBase64(value.Envelope.EncryptedBackup.Nonce)
	if err != nil {
		return nil, err
	}
	envelopeCiphertext, err := decodeRecoveryBase64(value.Envelope.EncryptedBackup.Ciphertext)
	if err != nil {
		return nil, err
	}
	dkvsNonce, err := decodeRecoveryBase64(value.DKVSShareCapsule.Nonce)
	if err != nil {
		return nil, err
	}
	dkvsCiphertext, err := decodeRecoveryBase64(value.DKVSShareCapsule.Ciphertext)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	for _, encode := range []func() error{
		func() error { return encoder.Raw([]byte(recoveryStorageMagic)) },
		func() error { return encoder.U8(recoveryPackageCodecVersion) },
		func() error { return encoder.Raw(accountID) },
		func() error { return encoder.Raw(packageID) },
		func() error { return encoder.U8(recoveryModeCode(value.Envelope.Locator.RecoveryMode)) },
		func() error { return encoder.Bytes(envelopeNonce, 1, 64) },
		func() error { return encoder.Bytes(envelopeCiphertext, 1, MaxRecoveryObjectSize) },
		func() error { return encoder.Bool(value.Manifest.Guardian != nil) },
	} {
		if err := encode(); err != nil {
			return nil, err
		}
	}
	if value.Manifest.Guardian != nil {
		mailboxID, _ := hex.DecodeString(value.Manifest.Guardian.MailboxID)
		capsuleHash, _ := hex.DecodeString(value.Manifest.Guardian.CapsuleHash)
		if err := encoder.Raw(mailboxID); err != nil {
			return nil, err
		}
		if err := encoder.String(value.Manifest.Guardian.ShareID, 1, 128); err != nil {
			return nil, err
		}
		if err := encoder.Raw(capsuleHash); err != nil {
			return nil, err
		}
	}
	var guardianBytes []byte
	if value.GuardianCapsule != nil {
		guardianBytes, err = EncodeGuardianCapsuleStorage(*value.GuardianCapsule)
		if err != nil {
			return nil, err
		}
	}
	if err := encoder.Bytes(guardianBytes, 0, MaxRecoveryObjectSize); err != nil {
		return nil, err
	}
	if err := encoder.Bytes(dkvsNonce, 1, 64); err != nil {
		return nil, err
	}
	if err := encoder.Bytes(dkvsCiphertext, 1, MaxRecoveryObjectSize); err != nil {
		return nil, err
	}
	knowledgeNonce, err := decodeRecoveryBase64(value.SealedKnowledge.Nonce)
	if err != nil || len(knowledgeNonce) != 12 {
		return nil, ErrInvalidRecoveryPackage
	}
	knowledgeCiphertext, err := decodeRecoveryBase64(value.SealedKnowledge.Ciphertext)
	if err != nil {
		return nil, err
	}
	if err := encoder.Raw(knowledgeNonce); err != nil {
		return nil, err
	}
	if err := encoder.Bytes(knowledgeCiphertext, 16, MaxRecoveryObjectSize); err != nil {
		return nil, err
	}
	if buf.Len() > MaxRecoveryObjectSize {
		return nil, ErrInvalidRecoveryPackage
	}
	return buf.Bytes(), nil
}

func DecodeRecoveryPackageStorage(value []byte) (*RecoveryPackage, error) {
	if len(value) == 0 || len(value) > MaxRecoveryObjectSize {
		return nil, ErrInvalidRecoveryPackage
	}
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(recoveryStorageMagic)))
	if err != nil || string(magic) != recoveryStorageMagic {
		return nil, ErrInvalidRecoveryPackage
	}
	codecVersion, err := decoder.U8()
	if err != nil || codecVersion != recoveryPackageCodecVersion {
		return nil, ErrInvalidRecoveryPackage
	}
	accountID, err := decoder.Raw(32)
	if err != nil {
		return nil, err
	}
	packageID, err := decoder.Raw(16)
	if err != nil {
		return nil, err
	}
	modeCode, err := decoder.U8()
	if err != nil {
		return nil, err
	}
	mode, err := recoveryModeFromCode(modeCode)
	if err != nil {
		return nil, err
	}
	locator := Locator{
		Version: Version, AccountID: hex.EncodeToString(accountID),
		PackageID: hex.EncodeToString(packageID), RecoveryMode: mode,
	}
	envelopeNonce, err := decoder.Bytes(1, 64)
	if err != nil {
		return nil, err
	}
	envelopeCiphertext, err := decoder.Bytes(1, MaxRecoveryObjectSize)
	if err != nil {
		return nil, err
	}
	hasGuardian, err := decoder.Bool()
	if err != nil {
		return nil, err
	}
	var guardian *GuardianReference
	if hasGuardian {
		mailboxID, err := decoder.Raw(32)
		if err != nil {
			return nil, err
		}
		shareID, err := decoder.String(1, 128)
		if err != nil {
			return nil, err
		}
		capsuleHash, err := decoder.Raw(32)
		if err != nil {
			return nil, err
		}
		guardian = &GuardianReference{
			MailboxID: hex.EncodeToString(mailboxID), ShareID: shareID,
			CapsuleHash: hex.EncodeToString(capsuleHash),
		}
	}
	guardianBytes, err := decoder.Bytes(0, MaxRecoveryObjectSize)
	if err != nil {
		return nil, err
	}
	var guardianCapsule *GuardianShareCapsule
	if len(guardianBytes) != 0 {
		guardianCapsule, err = DecodeGuardianCapsuleStorage(guardianBytes)
		if err != nil {
			return nil, err
		}
	}
	dkvsNonce, err := decoder.Bytes(1, 64)
	if err != nil {
		return nil, err
	}
	dkvsCiphertext, err := decoder.Bytes(1, MaxRecoveryObjectSize)
	if err != nil {
		return nil, err
	}
	knowledgeNonce, err := decoder.Raw(12)
	if err != nil {
		return nil, err
	}
	knowledgeCiphertext, err := decoder.Bytes(16, MaxRecoveryObjectSize)
	if err != nil || reader.Len() != 0 {
		return nil, ErrInvalidRecoveryPackage
	}
	envelope := Envelope{
		Version: Version, Locator: locator,
		EncryptedBackup: EncryptedBlob{
			Algorithm: "aes-256-gcm", Nonce: encodeRecoveryBase64(envelopeNonce),
			Ciphertext: encodeRecoveryBase64(envelopeCiphertext),
		},
	}
	envelopeHash, err := HashEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	total := uint8(2)
	if mode == RecoveryMode2Of3 {
		total = 3
	}
	result := &RecoveryPackage{
		Envelope: envelope, GuardianCapsule: guardianCapsule,
		Manifest: Manifest{
			Version: Version, Locator: locator, Threshold: 2, Total: total,
			EnvelopeHash: envelopeHash, Guardian: guardian,
		},
		DKVSShareCapsule: DKVSShareCapsule{
			Version: Version, PackageID: locator.PackageID, Algorithm: "aes-256-gcm",
			Nonce: encodeRecoveryBase64(dkvsNonce), Ciphertext: encodeRecoveryBase64(dkvsCiphertext),
		},
		SealedKnowledge: EncryptedBlob{
			Algorithm: "aes-256-gcm", Nonce: encodeRecoveryBase64(knowledgeNonce),
			Ciphertext: encodeRecoveryBase64(knowledgeCiphertext),
		},
	}
	if err := validateStoredRecoveryPackage(*result); err != nil {
		return nil, err
	}
	return result, nil
}

func encodeRecoveryKnowledge(value KnowledgeRecoveryBundle) ([]byte, error) {
	if value.Version != Version || value.Threshold != knowledgeQuestionThreshold ||
		value.Total != knowledgeQuestionCount || len(value.QuestionShares) != knowledgeQuestionCount {
		return nil, ErrInvalidRecoveryPackage
	}
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	shares := value.QuestionShares
	if err := encoder.Length(uint64(len(shares)), knowledgeQuestionCount); err != nil {
		return nil, err
	}
	for _, share := range shares {
		salt, err := decodeRecoveryBase64(share.Salt)
		if err != nil {
			return nil, err
		}
		nonce, err := decodeRecoveryBase64(share.Nonce)
		if err != nil {
			return nil, err
		}
		ciphertext, err := decodeRecoveryBase64(share.Ciphertext)
		if err != nil {
			return nil, err
		}
		if err := encoder.String(share.Question.ID, 1, 128); err != nil {
			return nil, err
		}
		if err := encoder.String(share.Question.Prompt, 1, 2048); err != nil {
			return nil, err
		}
		if err := encoder.Bool(share.Question.CaseSensitive); err != nil {
			return nil, err
		}
		if err := encoder.Bool(share.Question.IgnorePunctuation); err != nil {
			return nil, err
		}
		if err := encoder.Bytes(salt, 1, 64); err != nil {
			return nil, err
		}
		if err := encoder.Bytes(share.Vault, 1, MaxRecoveryObjectSize); err != nil {
			return nil, err
		}
		if err := encoder.Bytes(nonce, 1, 64); err != nil {
			return nil, err
		}
		if err := encoder.Bytes(ciphertext, 1, MaxRecoveryObjectSize); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeRecoveryKnowledge(packageID string, value []byte) (KnowledgeRecoveryBundle, error) {
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	questionCount, err := decoder.Length(knowledgeQuestionCount)
	if err != nil || questionCount != knowledgeQuestionCount {
		return KnowledgeRecoveryBundle{}, ErrInvalidRecoveryPackage
	}
	questions := make([]EncryptedQuestionShare, 0, questionCount)
	for index := uint64(0); index < questionCount; index++ {
		id, err := decoder.String(1, 128)
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		prompt, err := decoder.String(1, 2048)
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		caseSensitive, err := decoder.Bool()
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		ignorePunctuation, err := decoder.Bool()
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		salt, err := decoder.Bytes(1, 64)
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		vault, err := decoder.Bytes(1, MaxRecoveryObjectSize)
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		nonce, err := decoder.Bytes(1, 64)
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		ciphertext, err := decoder.Bytes(1, MaxRecoveryObjectSize)
		if err != nil {
			return KnowledgeRecoveryBundle{}, err
		}
		questions = append(questions, EncryptedQuestionShare{
			Question: KnowledgeQuestion{
				ID: id, Prompt: prompt, CaseSensitive: caseSensitive,
				IgnorePunctuation: ignorePunctuation,
			},
			Salt: encodeRecoveryBase64(salt), Vault: vault,
			Nonce: encodeRecoveryBase64(nonce), Ciphertext: encodeRecoveryBase64(ciphertext),
		})
	}
	if reader.Len() != 0 {
		return KnowledgeRecoveryBundle{}, ErrInvalidRecoveryPackage
	}
	return KnowledgeRecoveryBundle{
		Version: Version, PackageID: packageID, Threshold: knowledgeQuestionThreshold,
		Total: knowledgeQuestionCount, QuestionShares: questions,
	}, nil
}

func validateStoredRecoveryPackage(value RecoveryPackage) error {
	if err := validateEnvelope(value.Envelope); err != nil {
		return err
	}
	if err := validateManifest(value.Manifest, value.Envelope.Locator); err != nil {
		return err
	}
	hash, err := HashEnvelope(value.Envelope)
	if err != nil || hash != value.Manifest.EnvelopeHash {
		return ErrInvalidRecoveryPackage
	}
	packageID := value.Envelope.Locator.PackageID
	if value.DKVSShareCapsule.Version != Version || value.DKVSShareCapsule.PackageID != packageID ||
		value.DKVSShareCapsule.Algorithm != "aes-256-gcm" || value.DKVSShareCapsule.Nonce == "" ||
		value.DKVSShareCapsule.Ciphertext == "" || validateEncryptedBlob(value.SealedKnowledge) != nil {
		return ErrInvalidRecoveryPackage
	}
	if value.Envelope.Locator.RecoveryMode == RecoveryMode2Of3 &&
		(value.Manifest.Guardian == nil || value.GuardianCapsule == nil) {
		return ErrInvalidRecoveryPackage
	}
	if value.Envelope.Locator.RecoveryMode == RecoveryMode2Of2 &&
		(value.Manifest.Guardian != nil || value.GuardianCapsule != nil) {
		return ErrInvalidRecoveryPackage
	}
	if value.Manifest.Guardian != nil &&
		(!validHex(value.Manifest.Guardian.MailboxID, 64) ||
			!validHex(value.Manifest.Guardian.CapsuleHash, 64) || value.Manifest.Guardian.ShareID == "") {
		return ErrInvalidRecoveryPackage
	}
	if value.GuardianCapsule != nil {
		capsule := value.GuardianCapsule
		hash, err := HashGuardianCapsule(*capsule)
		if err != nil || capsule.PackageID != packageID ||
			capsule.ShareID != value.Manifest.Guardian.ShareID || hash != value.Manifest.Guardian.CapsuleHash {
			return ErrInvalidRecoveryPackage
		}
	}
	return nil
}

func recoveryModeCode(mode RecoveryMode) uint8 {
	if mode == RecoveryMode2Of3 {
		return 2
	}
	return 1
}

func recoveryModeFromCode(value uint8) (RecoveryMode, error) {
	switch value {
	case 1:
		return RecoveryMode2Of2, nil
	case 2:
		return RecoveryMode2Of3, nil
	default:
		return "", ErrInvalidRecoveryPackage
	}
}

func decodeRecoveryBase64(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 {
		return nil, ErrInvalidRecoveryPackage
	}
	return decoded, nil
}

func encodeRecoveryBase64(value []byte) string {
	return base64.RawURLEncoding.EncodeToString(value)
}
