package wallet

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInscribeNameRejectsInvalidNameBeforeWalletOrFunding(t *testing.T) {
	for _, name := range []string{"", "   ", "invalid/name", " invalid/name ", "a.b.c", "name with space", "name\nline", strings.Repeat("a", 256)} {
		t.Run(name, func(t *testing.T) {
			manager := &Manager{}
			result, err := manager.InscribeName(name, 1)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
}
