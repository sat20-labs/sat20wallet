package wallet

import "time"

const (
	actionMonitorIntervalL1 = 10 * time.Second
	actionMonitorIntervalL2 = 3 * time.Second
)

func (p *Manager) RegisterActionStatusCallback(callback ActionStatusCallback) {
	p.actionCallback = callback
}

func (p *Manager) RegisterMonitorTickCallback(callback MonitorTickCallback) {
	if callback != nil {
		p.monitorTicks = append(p.monitorTicks, callback)
	}
}

func (p *Manager) notifyActionStatus(event *ActionStatusEvent) {
	if p.actionCallback != nil {
		p.actionCallback(event)
	}
}

func (p *Manager) notifyMonitorTick(sendTxInL1 bool) {
	for _, callback := range p.monitorTicks {
		callback(sendTxInL1)
	}
}

func (p *Manager) startActionMonitor() {
	p.actionMonitorLock.Lock()
	defer p.actionMonitorLock.Unlock()

	if p.actionMonitorRunning {
		return
	}

	stop := make(chan struct{})
	p.actionMonitorStop = stop
	p.actionMonitorRunning = true

	p.actionMonitorWG.Add(2)
	go p.actionMonitorThread(stop, true)
	go p.actionMonitorThread(stop, false)
}

func (p *Manager) stopActionMonitor() {
	p.actionMonitorLock.Lock()
	if !p.actionMonitorRunning {
		p.actionMonitorLock.Unlock()
		return
	}

	stop := p.actionMonitorStop
	p.actionMonitorRunning = false
	p.actionMonitorStop = nil
	close(stop)
	p.actionMonitorLock.Unlock()

	p.actionMonitorWG.Wait()
}

func (p *Manager) actionMonitorThread(stop <-chan struct{}, sendTxInL1 bool) {
	defer p.actionMonitorWG.Done()

	ticker := time.NewTicker(actionMonitorInterval(sendTxInL1))
	defer ticker.Stop()

	tick := func() {
		unlock, ok := p.tryLockActionMonitorTick(sendTxInL1)
		if !ok {
			return
		}
		defer unlock()

		p.HandleRemoteActionStatus(sendTxInL1)
		p.HandleLocalActionStatus(sendTxInL1)
		p.notifyMonitorTick(sendTxInL1)
	}

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			tick()
		}
	}
}

func (p *Manager) tryLockActionMonitorTick(sendTxInL1 bool) (func(), bool) {
	if sendTxInL1 {
		if !p.actionMonitorL1Lock.TryLock() {
			return nil, false
		}
		return p.actionMonitorL1Lock.Unlock, true
	}
	if !p.actionMonitorL2Lock.TryLock() {
		return nil, false
	}
	return p.actionMonitorL2Lock.Unlock, true
}

func actionMonitorInterval(sendTxInL1 bool) time.Duration {
	if ENABLE_TESTING {
		return time.Second
	}
	if sendTxInL1 {
		return actionMonitorIntervalL1
	}
	return actionMonitorIntervalL2
}
