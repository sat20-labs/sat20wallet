package account

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/sat20-labs/sat20wallet/sdk/account/fuzzy"
	"github.com/sat20-labs/sat20wallet/sdk/account/internal/shamir"
	"golang.org/x/text/unicode/norm"
)

const (
	knowledgeQuestionCount     = 3
	knowledgeQuestionThreshold = 2
	featureSetSize             = 24
	featureCorrectThreshold    = 15
	featureCorpusSize          = 65536
)

type rankedFeature struct {
	rank [32]byte
	id   int
}

func normalizeAnswer(question KnowledgeQuestion, value string) (string, error) {
	value = norm.NFKC.String(value)
	value = strings.Join(strings.Fields(value), " ")
	if !question.CaseSensitive {
		value = strings.ToLower(value)
	}
	if question.IgnorePunctuation {
		value = strings.Map(func(r rune) rune {
			if unicode.IsPunct(r) {
				return -1
			}
			return r
		}, value)
	}
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) < 8 || len(runes) > 256 {
		return "", fmt.Errorf("knowledge answer must contain 8-256 characters")
	}
	return value, nil
}

func featureTokens(value string) []string {
	runes := []rune("^" + value + "$")
	tokens := make([]string, 0, len(runes)*3)
	for i := range runes {
		tokens = append(tokens, "u:"+string(runes[i]))
		if i+1 < len(runes) {
			tokens = append(tokens, "b:"+string(runes[i:i+2]))
		}
		if i+2 < len(runes) {
			tokens = append(tokens, "t:"+string(runes[i:i+3]))
		}
	}
	for _, word := range strings.Fields(value) {
		tokens = append(tokens, "w:"+word)
	}
	return tokens
}

func featureIDs(question KnowledgeQuestion, answer string, salt []byte) ([]int, error) {
	normalized, err := normalizeAnswer(question, answer)
	if err != nil {
		return nil, err
	}
	tokens := featureTokens(normalized)
	ranked := make([]rankedFeature, 0, len(tokens))
	seenToken := map[string]struct{}{}
	seenID := map[int]struct{}{}
	for _, token := range tokens {
		if _, ok := seenToken[token]; ok {
			continue
		}
		seenToken[token] = struct{}{}
		mac := hmac.New(sha256.New, salt)
		_, _ = mac.Write([]byte(question.ID))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte(token))
		digest := mac.Sum(nil)
		var rank [32]byte
		copy(rank[:], digest)
		id := int(binary.BigEndian.Uint32(digest[:4]) % featureCorpusSize)
		if _, ok := seenID[id]; ok {
			continue
		}
		seenID[id] = struct{}{}
		ranked = append(ranked, rankedFeature{rank: rank, id: id})
	}
	sort.Slice(ranked, func(i, j int) bool { return string(ranked[i].rank[:]) < string(ranked[j].rank[:]) })
	if len(ranked) < featureSetSize {
		return nil, fmt.Errorf("answer does not provide enough independent features")
	}
	out := make([]int, featureSetSize)
	for i := range out {
		out[i] = ranked[i].id
	}
	sort.Ints(out)
	return out, nil
}

func questionAAD(packageID, questionID string) []byte {
	return []byte("sat20-question-share-v1|" + packageID + "|" + questionID)
}
func dkvsCapsuleAAD(packageID string) []byte { return []byte("sat20-dkvs-share-v1|" + packageID) }
func knowledgeKEK(key []byte, packageID, questionID string) []byte {
	return hkdfSHA256(key, []byte(packageID), []byte("sat20-fuzzy-question-v1|"+questionID), 32)
}

func createKnowledgeRecovery(packageID string, dkvsShare RecoveryShare, questions []QuestionAnswer, random io.Reader) (DKVSShareCapsule, KnowledgeRecoveryBundle, error) {
	if len(questions) != knowledgeQuestionCount {
		return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, fmt.Errorf("exactly three recovery questions are required")
	}
	if random == nil {
		random = rand.Reader
	}
	kDKVS := make([]byte, 32)
	if _, err := io.ReadFull(random, kDKVS); err != nil {
		return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
	}
	defer zero(kDKVS)
	questionShares, err := shamir.Split(kDKVS, knowledgeQuestionCount, knowledgeQuestionThreshold, random)
	if err != nil {
		return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
	}
	bundle := KnowledgeRecoveryBundle{Version: Version, PackageID: packageID, Threshold: knowledgeQuestionThreshold, Total: knowledgeQuestionCount, QuestionShares: make([]EncryptedQuestionShare, knowledgeQuestionCount)}
	seen := map[string]struct{}{}
	for i, input := range questions {
		question := input.Question
		question.ID = normalizeSpace(question.ID)
		question.Prompt = normalizeSpace(question.Prompt)
		if question.ID == "" || question.Prompt == "" {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, fmt.Errorf("invalid recovery question")
		}
		if _, ok := seen[question.ID]; ok {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, fmt.Errorf("duplicate recovery question id")
		}
		seen[question.ID] = struct{}{}
		answer, err := normalizeAnswer(question, input.Answer)
		if err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		confirmation, err := normalizeAnswer(question, input.Confirmation)
		if err != nil || answer != confirmation {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, fmt.Errorf("recovery answer confirmation does not match")
		}
		salt := make([]byte, 32)
		if _, err := io.ReadFull(random, salt); err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		ids, err := featureIDs(question, answer, salt)
		if err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		params, err := fuzzy.GenerateParams(featureSetSize, featureCorrectThreshold, featureCorpusSize, random)
		if err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		vault, err := fuzzy.Lock(params, ids)
		if err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		vaultBytes, err := fuzzy.MarshalVault(vault)
		if err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		keyMaterial, err := fuzzy.RecoverKey(vault, ids, 0)
		if err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		kek := knowledgeKEK(keyMaterial, packageID, question.ID)
		zero(keyMaterial)
		nonce, ciphertext, err := sealBytes(kek, questionShares[i], questionAAD(packageID, question.ID), random)
		zero(kek)
		if err != nil {
			return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
		}
		bundle.QuestionShares[i] = EncryptedQuestionShare{Question: question, Salt: base64.RawURLEncoding.EncodeToString(salt), Vault: vaultBytes, Nonce: nonce, Ciphertext: ciphertext}
	}
	shareBytes, err := json.Marshal(dkvsShare)
	if err != nil {
		return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
	}
	defer zero(shareBytes)
	nonce, ciphertext, err := sealBytes(kDKVS, shareBytes, dkvsCapsuleAAD(packageID), random)
	if err != nil {
		return DKVSShareCapsule{}, KnowledgeRecoveryBundle{}, err
	}
	return DKVSShareCapsule{Version: Version, PackageID: packageID, Algorithm: "aes-256-gcm", Nonce: nonce, Ciphertext: ciphertext}, bundle, nil
}

func recoverDKVSShare(capsule DKVSShareCapsule, bundle KnowledgeRecoveryBundle, answers []AnswerAttempt) (RecoveryShare, error) {
	if capsule.Version != Version || bundle.Version != Version || capsule.PackageID != bundle.PackageID || bundle.Threshold != knowledgeQuestionThreshold || bundle.Total != knowledgeQuestionCount || len(bundle.QuestionShares) != knowledgeQuestionCount {
		return RecoveryShare{}, ErrInvalidRecoveryPackage
	}
	answerMap := map[string]string{}
	for _, answer := range answers {
		answerMap[normalizeSpace(answer.QuestionID)] = answer.Answer
	}
	recovered := make([][]byte, 0, knowledgeQuestionThreshold)
	for _, entry := range bundle.QuestionShares {
		answer, ok := answerMap[entry.Question.ID]
		if !ok {
			continue
		}
		salt, err := base64.RawURLEncoding.DecodeString(entry.Salt)
		if err != nil || len(salt) != 32 {
			continue
		}
		ids, err := featureIDs(entry.Question, answer, salt)
		if err != nil {
			continue
		}
		vault, err := fuzzy.UnmarshalVault(entry.Vault)
		if err != nil {
			continue
		}
		keyMaterial, err := fuzzy.RecoverKey(vault, ids, 0)
		if err != nil {
			continue
		}
		kek := knowledgeKEK(keyMaterial, bundle.PackageID, entry.Question.ID)
		zero(keyMaterial)
		share, err := openSealed(kek, entry.Nonce, entry.Ciphertext, questionAAD(bundle.PackageID, entry.Question.ID))
		zero(kek)
		if err != nil {
			continue
		}
		recovered = append(recovered, share)
		if len(recovered) == knowledgeQuestionThreshold {
			break
		}
	}
	if len(recovered) < knowledgeQuestionThreshold {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	kDKVS, err := shamir.Combine(recovered)
	if err != nil || len(kDKVS) != 32 {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	defer zero(kDKVS)
	plaintext, err := openSealed(kDKVS, capsule.Nonce, capsule.Ciphertext, dkvsCapsuleAAD(bundle.PackageID))
	if err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	defer zero(plaintext)
	var share RecoveryShare
	if err := json.Unmarshal(plaintext, &share); err != nil {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	if _, err := validateShare(share); err != nil || share.Role != ShareRoleDKVS || share.PackageID != bundle.PackageID {
		return RecoveryShare{}, ErrRecoveryFailed
	}
	return share, nil
}
