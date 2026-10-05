package wallet

func (p *Manager) beginAccountBackgroundTask() (<-chan struct{}, bool) {
	if p == nil { return nil, false }
	p.accountBackgroundMu.Lock()
	defer p.accountBackgroundMu.Unlock()
	if p.accountBackgroundStopped { return nil, false }
	if p.accountBackgroundStop == nil { p.accountBackgroundStop = make(chan struct{}) }
	p.accountBackgroundWG.Add(1)
	return p.accountBackgroundStop, true
}

func (p *Manager) finishAccountBackgroundTask() {
	if p != nil { p.accountBackgroundWG.Done() }
}

func (p *Manager) stopAccountBackgroundTasks() {
	if p == nil { return }
	p.accountBackgroundMu.Lock()
	if !p.accountBackgroundStopped {
		p.accountBackgroundStopped = true
		if p.accountBackgroundStop != nil {
			close(p.accountBackgroundStop)
			p.accountBackgroundStop = nil
		}
	}
	p.accountBackgroundMu.Unlock()
	p.accountBackgroundWG.Wait()
}

func (p *Manager) resumeAccountBackgroundTasks() {
	if p == nil { return }
	p.accountBackgroundMu.Lock()
	p.accountBackgroundStopped = false
	p.accountBackgroundMu.Unlock()
}
