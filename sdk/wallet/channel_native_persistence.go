package wallet

// Native channel signatures create durable proof and recovery obligations.
// Confirm the database can publish coupled wallet state in one transaction
// before accepting funding or releasing a signature. Merely constructing and
// closing the transaction has no persistent effect.
func (p *Manager) requireSTPNativePersistence() error {
	if p == nil {
		return ErrAtomicPersistenceUnavailable
	}
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return err
	}
	batch.Close()
	return nil
}
