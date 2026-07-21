package fuzzy

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"sort"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/crypto/sha3"
)

const (
	scryptN = 1024
	scryptR = 8
	scryptP = 16
)

func GenerateParams(setSize, correctThreshold, corpusSize int, random io.Reader) (*Params, error) {
	if random == nil {
		random = rand.Reader
	}
	prime, err := firstPrimeGreaterThan(corpusSize)
	if err != nil {
		return nil, err
	}
	p := &Params{Version: Version, SetSize: setSize, CorrectThreshold: correctThreshold, CorpusSize: corpusSize, Prime: prime, Extractor: make([]int64, setSize), Salt: make([]byte, 32)}
	if _, err := io.ReadFull(random, p.Salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	candidates := make([]int64, int(prime))
	for i := range candidates {
		candidates[i] = int64(i)
	}
	buf := make([]byte, 4)
	for i := 0; i < setSize; i++ {
		if _, err := io.ReadFull(random, buf); err != nil {
			return nil, fmt.Errorf("generate extractor: %w", err)
		}
		selected := i + int(binary.LittleEndian.Uint32(buf)%uint32(len(candidates)-i))
		p.Extractor[i] = candidates[selected]
		candidates[selected] = candidates[i]
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func Lock(params *Params, featureIDs []int) (*Vault, error) {
	if params == nil {
		return nil, ErrInvalidParameters
	}
	if err := params.validate(); err != nil {
		return nil, err
	}
	features, err := normalizeFeatures(featureIDs, params.SetSize, params.CorpusSize)
	if err != nil {
		return nil, err
	}
	roots := make([]int64, len(features))
	for i, f := range features {
		roots[i] = int64(f)
	}
	poly := polyFromRoots(roots, params.Prime)
	errorThreshold := 2 * (params.SetSize - params.CorrectThreshold)
	start := params.SetSize - errorThreshold
	if start < 0 || len(poly.coeffs) != params.SetSize+1 {
		return nil, ErrInvalidParameters
	}
	setHash, err := slowSetHash(features, params.Salt)
	if err != nil {
		return nil, err
	}
	v := &Vault{Version: Version, SetSize: params.SetSize, CorrectThreshold: params.CorrectThreshold, CorpusSize: params.CorpusSize, Prime: params.Prime, Extractor: append([]int64(nil), params.Extractor...), Salt: append([]byte(nil), params.Salt...), Sketch: append([]int64(nil), poly.coeffs[start:params.SetSize]...), SetHash: setHash}
	if err := v.validate(); err != nil {
		return nil, err
	}
	return v, nil
}

func RecoverKey(v *Vault, featureIDs []int, keyIndex uint32) ([]byte, error) {
	if v == nil || v.validate() != nil {
		return nil, ErrInvalidPublicPayload
	}
	features, err := normalizeFeatures(featureIDs, v.SetSize, v.CorpusSize)
	if err != nil {
		return nil, ErrInvalidFeatureSet
	}
	recovered := append([]int(nil), features...)
	candidateHash, err := slowSetHash(recovered, v.Salt)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(candidateHash, v.SetHash) {
		recovered, err = recoverFeatureSet(v, recovered)
		if err != nil {
			return nil, err
		}
		recoveredHash, err := slowSetHash(recovered, v.Salt)
		if err != nil {
			return nil, err
		}
		if !hmac.Equal(recoveredHash, v.SetHash) {
			return nil, ErrInsufficientMatches
		}
	}
	ek, err := extractorKey(v, recovered)
	if err != nil {
		return nil, err
	}
	defer zero(ek)
	counter := make([]byte, 4)
	binary.LittleEndian.PutUint32(counter, keyIndex)
	mac := hmac.New(sha3.New512, counter)
	_, _ = mac.Write(ek)
	return mac.Sum(nil), nil
}

func normalizeFeatures(ids []int, expected, corpus int) ([]int, error) {
	if len(ids) != expected {
		return nil, ErrInvalidFeatureSet
	}
	out := append([]int(nil), ids...)
	sort.Ints(out)
	for i, value := range out {
		if value < 0 || value >= corpus {
			return nil, ErrInvalidFeatureSet
		}
		if i > 0 && out[i-1] == value {
			return nil, ErrInvalidFeatureSet
		}
	}
	return out, nil
}

func featurePass(prefix string, features []int) []byte {
	out := make([]byte, 0, len(prefix)+4*len(features))
	out = append(out, prefix...)
	buf := make([]byte, 4)
	for _, value := range features {
		binary.LittleEndian.PutUint32(buf, uint32(value))
		out = append(out, buf...)
	}
	return out
}

func slowSetHash(features []int, salt []byte) ([]byte, error) {
	pass := featurePass("original_words:", features)
	defer zero(pass)
	out, err := scrypt.Key(pass, salt, scryptN, scryptR, scryptP, DefaultDerivedKeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive set hash: %w", err)
	}
	return out, nil
}

func extractorKey(v *Vault, features []int) ([]byte, error) {
	product := int64(1)
	for i := 0; i < v.SetSize; i++ {
		product = modMul(product, modMul(int64(features[i]), v.Extractor[i], v.Prime), v.Prime)
	}
	pass := []byte("key:")
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, uint32(product))
	pass = append(pass, buf...)
	defer zero(pass)
	out, err := scrypt.Key(pass, v.Salt, scryptN, scryptR, scryptP, DefaultDerivedKeyLen)
	if err != nil {
		return nil, fmt.Errorf("derive extractor key: %w", err)
	}
	return out, nil
}

func recoverFeatureSet(v *Vault, candidates []int) ([]int, error) {
	n := v.SetSize
	errorThreshold := 2 * (v.SetSize - v.CorrectThreshold)
	if errorThreshold%2 != 0 || errorThreshold <= 0 || errorThreshold >= n {
		return nil, ErrInvalidPublicPayload
	}
	coeffs := make([]int64, n+1)
	start := n - errorThreshold
	copy(coeffs[start:n], v.Sketch)
	coeffs[n] = 1
	pHigh := newPolynomial(v.Prime, coeffs)
	xs := make([]int64, n)
	ys := make([]int64, n)
	for i, candidate := range candidates {
		xs[i] = int64(candidate)
		ys[i] = pHigh.eval(xs[i])
	}
	pLow, err := berlekampWelch(xs, ys, n-errorThreshold, errorThreshold/2, v.Prime)
	if err != nil {
		return nil, ErrInsufficientMatches
	}
	roots, err := findRoots(pHigh.sub(pLow), n)
	if err != nil {
		return nil, ErrInsufficientMatches
	}
	out := make([]int, n)
	for i, root := range roots {
		if root < 0 || root >= int64(v.CorpusSize) {
			return nil, ErrInsufficientMatches
		}
		out[i] = int(root)
	}
	sort.Ints(out)
	return out, nil
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
