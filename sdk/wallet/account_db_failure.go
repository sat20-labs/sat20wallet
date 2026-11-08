package wallet

import (
	"errors"
	"fmt"
)

var errAccountDBWrite = errors.New("account database write failed")

// ErrAccountDBStopped requires closing this Manager. A fresh Manager resumes
// from the existing durable import marker after local storage is available.
var ErrAccountDBStopped = errors.New("local database save failed; close the wallet, free device storage space, and restart; do not delete wallet or website data")

const accountDBFailureCode = "ACCOUNT_DB_WRITE_FAILED"

// Only actual database Write/Put/Delete/Flush results enter this classifier.
// Provider, validation and network errors must not be inferred from their text.
func accountDBWriteError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", errAccountDBWrite, err)
}

func (p *Manager) accountDBFailure() error {
	if p != nil {
		if failure := p.accountDBFault.Load(); failure != nil {
			return *failure
		}
	}
	return nil
}

func (p *Manager) stopAccountDB(err error) error {
	if failure := p.accountDBFailure(); failure != nil {
		return failure
	}
	if !errors.Is(err, errAccountDBWrite) {
		return err
	}
	failure := fmt.Errorf("%w: %w", ErrAccountDBStopped, err)
	p.accountDBFault.CompareAndSwap(nil, &failure)
	return p.accountDBFailure()
}
