// Package fuzzy implements a finite-field secure-sketch based fuzzy key
// recovery scheme. It is a Go translation of the Decentralized Identity
// Foundation fuzzy-encryption reference implementation at commit
// 0d7205c7a444d1c4d17f57e0a706f756c1141dde (Apache-2.0).
package fuzzy

import (
	"errors"
	"fmt"
)

const (
	Version              = 1
	DefaultSetSize       = 16
	DefaultThreshold     = 11
	DefaultCorpusSize    = 32768
	DefaultDerivedKeyLen = 64
	MaxPublicPayloadSize = 64 * 1024
)

var (
	ErrInvalidParameters    = errors.New("invalid fuzzy recovery parameters")
	ErrInvalidFeatureSet    = errors.New("invalid fuzzy recovery feature set")
	ErrInsufficientMatches  = errors.New("fuzzy recovery input does not meet the threshold")
	ErrInvalidPublicPayload = errors.New("invalid fuzzy recovery public payload")
)

type Params struct {
	Version          uint32  `json:"version"`
	SetSize          int     `json:"set_size"`
	CorrectThreshold int     `json:"correct_threshold"`
	CorpusSize       int     `json:"corpus_size"`
	Prime            int64   `json:"prime"`
	Extractor        []int64 `json:"extractor"`
	Salt             []byte  `json:"salt"`
}

type Vault struct {
	Version          uint32  `json:"version"`
	SetSize          int     `json:"set_size"`
	CorrectThreshold int     `json:"correct_threshold"`
	CorpusSize       int     `json:"corpus_size"`
	Prime            int64   `json:"prime"`
	Extractor        []int64 `json:"extractor"`
	Salt             []byte  `json:"salt"`
	Sketch           []int64 `json:"sketch"`
	SetHash          []byte  `json:"set_hash"`
}

func (p Params) validate() error {
	errorThreshold := 2 * (p.SetSize - p.CorrectThreshold)
	if p.Version != Version || p.SetSize < 2 || p.SetSize > 64 ||
		p.CorrectThreshold < 2 || p.CorrectThreshold > p.SetSize ||
		p.CorpusSize < p.SetSize || p.CorpusSize > 1_000_000 ||
		p.Prime <= int64(p.CorpusSize) || !isPrime(p.Prime) ||
		len(p.Extractor) != p.SetSize || len(p.Salt) != 32 ||
		errorThreshold <= 0 || errorThreshold >= p.SetSize {
		return ErrInvalidParameters
	}
	seen := make(map[int64]struct{}, len(p.Extractor))
	for _, value := range p.Extractor {
		// Zero would collapse the multiplicative extractor key for every set
		// containing that position, so production parameters exclude it.
		if value <= 0 || value >= p.Prime {
			return ErrInvalidParameters
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%w: duplicate extractor", ErrInvalidParameters)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (v Vault) validate() error {
	params := Params{Version: v.Version, SetSize: v.SetSize, CorrectThreshold: v.CorrectThreshold,
		CorpusSize: v.CorpusSize, Prime: v.Prime, Extractor: v.Extractor, Salt: v.Salt}
	if err := params.validate(); err != nil {
		return ErrInvalidPublicPayload
	}
	if len(v.Sketch) != 2*(v.SetSize-v.CorrectThreshold) || len(v.SetHash) != DefaultDerivedKeyLen {
		return ErrInvalidPublicPayload
	}
	for _, coefficient := range v.Sketch {
		if coefficient < 0 || coefficient >= v.Prime {
			return ErrInvalidPublicPayload
		}
	}
	return nil
}
