//go:build js && wasm

package wallet

import "fmt"

// UpgradePwa038Password rewraps an imported local catalog before its first
// unlock. The old PWA supplied SHA-256 hex of the user's password.
func (p *Manager) UpgradePwa038Password(oldPassword, password string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	marker, err := p.db.Read([]byte("pwa-038-migration"))
	if err != nil || len(marker) == 0 || string(marker) == "done" ||
		p.accountProfile != nil || p.wallet != nil {
		return fmt.Errorf("0.1.38 password upgrade is unavailable")
	}
	return p.changePasswordLocked(oldPassword, password)
}
