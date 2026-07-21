// Copyright 2021 The Decentralized Identity Foundation Project Authors.
// Copyright 2026 SAT20 Labs contributors.
// Licensed under the Apache License, Version 2.0.
package fuzzy

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
)

func TestRecoverWithinThreshold(t *testing.T) {
	params, err := GenerateParams(9, 6, 7776, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	original := []int{1, 2, 3, 4, 5, 6, 7, 8, 9}
	vault, err := Lock(params, original)
	if err != nil {
		t.Fatal(err)
	}
	want, err := RecoverKey(vault, original, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RecoverKey(vault, []int{1, 2, 3, 4, 5, 66, 77, 8, 99}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, got) {
		t.Fatal("key mismatch")
	}
	if _, err := RecoverKey(vault, []int{1, 2, 3, 4, 5, 66, 77, 88, 99}, 0); !errors.Is(err, ErrInsufficientMatches) {
		t.Fatalf("expected threshold failure, got %v", err)
	}
}

func TestCodecRejectsMalformed(t *testing.T) {
	for _, input := range [][]byte{nil, {1, 2, 3}, []byte(`{"version":1}`), bytes.Repeat([]byte{'x'}, MaxPublicPayloadSize+1)} {
		if _, err := UnmarshalVault(input); err == nil {
			t.Fatalf("accepted %d bytes", len(input))
		}
	}
}

func FuzzUnmarshalVault(f *testing.F) {
	f.Add([]byte(`{"version":1}`))
	f.Fuzz(func(t *testing.T, input []byte) { _, _ = UnmarshalVault(input) })
}

func TestDIFReferenceVector(t *testing.T) {
	var randomBytes []byte
	for _, value := range []string{
		"3218C8B6681167BC81BBCA7523FEFCBD9533F8B31EEA493296B1E089FA0E2E04",
		"E9DA670216EBDA73F1626012E64576C06D00D0F3A96BC9256972B4C314729D29",
		"C765880C27EC4EED06155B85C43D35264E7DEF17FC01B8C3EDB5F0B3E2E1EFBE",
	} {
		decoded, err := hex.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		randomBytes = append(randomBytes, decoded...)
	}
	params, err := GenerateParams(9, 6, 7776, bytes.NewReader(randomBytes))
	if err != nil {
		t.Fatal(err)
	}
	if params.Prime != 7789 {
		t.Fatalf("prime %d", params.Prime)
	}
	wantExtractor := []int64{5872, 5619, 3771, 5627, 6969, 4982, 7600, 6525, 7369}
	if !reflect.DeepEqual(params.Extractor, wantExtractor) {
		t.Fatalf("extractor %v", params.Extractor)
	}
	vault, err := Lock(params, []int{1, 2, 3, 4, 5, 6, 7, 8, 9})
	if err != nil {
		t.Fatal(err)
	}
	wantSketch := []int64{7092, 3290, 961, 6128, 870, 7744}
	if !reflect.DeepEqual(vault.Sketch, wantSketch) {
		t.Fatalf("sketch %v", vault.Sketch)
	}
	if got := hex.EncodeToString(vault.SetHash); got != "4243504726b98926063b7c0aa65d32804c34076804775362819c71aa456d7157e31cb2a2e4851cd29f42839b81e64bfed7553a65b5958bbd4e8ef24c64cb9179" {
		t.Fatalf("hash %s", got)
	}
	key, err := RecoverKey(vault, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(key); got != "781c17d990bb740b6fbe15aa7a69bac2e57dc8868dac36fcb5732bda6100a105af251a1f34c43ecbd1c3fac5609e66bb1a6dc658eed3aebca0923f2d14ea7a89" {
		t.Fatalf("key %s", got)
	}
}
