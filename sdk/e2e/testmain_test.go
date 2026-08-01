package e2e

import (
	"flag"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if !flag.Parsed() {
		flag.Parse()
	}
	const minimumTimeout = 45 * time.Minute
	if timeoutFlag := flag.Lookup("test.timeout"); timeoutFlag != nil {
		if current, err := time.ParseDuration(timeoutFlag.Value.String()); err == nil &&
			(current == 0 || current < minimumTimeout) {
			if err := flag.Set("test.timeout", minimumTimeout.String()); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
	os.Exit(m.Run())
}
