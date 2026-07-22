package fuzzy

import (
	"bytes"
	"testing"
)

func TestGenerateParamsSkipsZeroExtractorCandidates(t *testing.T) {
	randomBytes := make([]byte, 32)
	for range 3 {
		// First draw selects the zero field element and must be rejected.
		randomBytes = append(randomBytes, make([]byte, 4)...)
		// Second draw selects the next non-zero remaining element.
		randomBytes = append(randomBytes, []byte{1, 0, 0, 0}...)
	}
	params, err := GenerateParams(3, 2, 10, bytes.NewReader(randomBytes))
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[int64]struct{}, len(params.Extractor))
	for _, value := range params.Extractor {
		if value == 0 {
			t.Fatal("generated a zero extractor field element")
		}
		if _, exists := seen[value]; exists {
			t.Fatalf("generated duplicate extractor field element %d", value)
		}
		seen[value] = struct{}{}
	}
}

func TestZeroFeatureDoesNotCollapseExtractorKey(t *testing.T) {
	vault := &Vault{
		SetSize:   3,
		Prime:     11,
		Extractor: []int64{1, 2, 3},
		Salt:      make([]byte, 32),
	}
	first, err := extractorKey(vault, []int{0, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	defer zero(first)
	second, err := extractorKey(vault, []int{0, 1, 3})
	if err != nil {
		t.Fatal(err)
	}
	defer zero(second)
	if bytes.Equal(first, second) {
		t.Fatal("zero feature collapsed distinct extractor inputs to the same key")
	}
}
