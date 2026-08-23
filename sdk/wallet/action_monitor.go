package wallet

import (
	"context"
	"time"
)

const (
	actionMonitorIntervalL1 = 10 * time.Second
	actionMonitorIntervalL2 = 3 * time.Second
)

func (p *Manager) RegisterActionStatusCallback(callback ActionStatusCallback) {
	p.actionCallback = callback
}

func (p *Manager) RegisterChannelStatusCallback(callback ActionStatusCallback) {
	p.channelStatusCallback = callback
}

func (p *Manager) RegisterMonitorTickCallback(callback MonitorTickCallback) {
	if callback != nil {
		p.monitorTicks = append(p.monitorTicks, callback)
	}
}

func (p *Manager) notifyActionStatus(event *ActionStatusEvent) {
	p.handleOperationLogActionStatusEvent(event)
	if p.actionCallback != nil {
		p.actionCallback(event)
	}
}

func (p *Manager) notifyChannelStatus(event *ActionStatusEvent) {
	p.handleOperationLogActionStatusEvent(event)
	if p.channelStatusCallback != nil {
		p.channelStatusCallback(event)
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
	ctx, cancel := context.WithCancel(context.Background())
	p.actionMonitorStop = stop
	p.actionMonitorCancel = cancel
	p.actionMonitorRunning = true

	p.actionMonitorWG.Add(2)
	go p.actionMonitorThread(ctx, stop, true)
	go p.actionMonitorThread(ctx, stop, false)
}

func (p *Manager) stopActionMonitor() {
	p.actionMonitorLock.Lock()
	if !p.actionMonitorRunning {
		p.actionMonitorLock.Unlock()
		return
	}

	stop := p.actionMonitorStop
	cancel := p.actionMonitorCancel
	p.actionMonitorRunning = false
	p.actionMonitorStop = nil
	p.actionMonitorCancel = nil
	cancel()
	close(stop)
	p.actionMonitorLock.Unlock()

	p.actionMonitorWG.Wait()
}

func (p *Manager) actionMonitorThread(ctx context.Context, stop <-chan struct{}, sendTxInL1 bool) {
	defer p.actionMonitorWG.Done()

	ticker := time.NewTicker(actionMonitorInterval(sendTxInL1))
	defer ticker.Stop()

	tick := func() {
		if ctx.Err() != nil {
			return
		}
		unlock, ok := p.tryLockActionMonitorTick(sendTxInL1)
		if !ok {
			return
		}
		defer unlock()

		if sendTxInL1 {
			if err := p.handleRGB11L1MonitorTick(ctx); err != nil && ctx.Err() == nil {
				Log.Warningf("RGB11 L1 monitor tick failed: %v", err)
			}
			if ctx.Err() != nil {
				return
			}
			p.HandleChannelSafetyStatus()
		}
		if ctx.Err() != nil {
			return
		}
		p.HandleChannelReservationStatus(sendTxInL1)
		if ctx.Err() != nil {
			return
		}
		p.HandleRemoteActionStatus(sendTxInL1)
		if ctx.Err() != nil {
			return
		}
		p.HandleLocalActionStatus(sendTxInL1)
		p.handleBTCLuckyMonitorTick(sendTxInL1)
		p.notifyMonitorTick(sendTxInL1)
	}

	for {
		select {
		case <-ctx.Done():
			return
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
