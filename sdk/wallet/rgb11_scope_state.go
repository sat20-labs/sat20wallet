package wallet

import (
	"sync"
	"time"
)

type rgb11ScopeBackupState struct {
	Status              string
	Mode                string
	TTL                 uint64
	AutoBackup          *RGB11AutoBackupPolicy
	ReconciliationState string
}

type rgb11ScopeReconciliation struct {
	running  bool
	stopped  bool
	stop     chan struct{}
	wake     chan struct{}
	attempts uint64
}

type rgb11ScopeStateRegistry struct {
	mu               sync.RWMutex
	states           map[string]rgb11ScopeBackupState
	reconciliations  map[string]*rgb11ScopeReconciliation
	retryDelay       time.Duration
	reconciliationWG sync.WaitGroup
	chainRefresh     sync.Mutex
	stopping         bool
}

func newRGB11ScopeStateRegistry() *rgb11ScopeStateRegistry {
	return &rgb11ScopeStateRegistry{
		states:          make(map[string]rgb11ScopeBackupState),
		reconciliations: make(map[string]*rgb11ScopeReconciliation),
		retryDelay:      rgb11ChainReconciliationRetryDelay,
	}
}

func cloneRGB11AutoBackupPolicy(policy *RGB11AutoBackupPolicy) *RGB11AutoBackupPolicy {
	if policy == nil {
		return nil
	}
	cloned := *policy
	return &cloned
}

func (r *rgb11ScopeStateRegistry) load(scope string) rgb11ScopeBackupState {
	if r == nil || scope == "" {
		return rgb11ScopeBackupState{Status: "offline"}
	}
	r.mu.RLock()
	state, ok := r.states[scope]
	r.mu.RUnlock()
	if !ok {
		state.Status = "offline"
	}
	if state.ReconciliationState == "" {
		state.ReconciliationState = "idle"
	}
	state.AutoBackup = cloneRGB11AutoBackupPolicy(state.AutoBackup)
	return state
}

func (r *rgb11ScopeStateRegistry) update(scope string, apply func(*rgb11ScopeBackupState)) {
	if r == nil || scope == "" || apply == nil {
		return
	}
	r.mu.Lock()
	state := r.states[scope]
	if state.Status == "" {
		state.Status = "offline"
	}
	if state.ReconciliationState == "" {
		state.ReconciliationState = "idle"
	}
	apply(&state)
	state.AutoBackup = cloneRGB11AutoBackupPolicy(state.AutoBackup)
	r.states[scope] = state
	r.mu.Unlock()
}

func (p *rgb11Manager) rgb11ScopeKey() string {
	if p == nil || p.status == nil {
		return ""
	}
	return rgb11StorageScope(p.status.CurrentWallet, p.status.CurrentAccount)
}

func (p *rgb11Manager) rgb11ScopeState() rgb11ScopeBackupState {
	if p == nil || p.scopeStates == nil {
		return rgb11ScopeBackupState{Status: "offline"}
	}
	return p.scopeStates.load(p.rgb11ScopeKey())
}

func (p *rgb11Manager) updateRGB11ScopeState(apply func(*rgb11ScopeBackupState)) {
	if p == nil || p.scopeStates == nil {
		return
	}
	p.scopeStates.update(p.rgb11ScopeKey(), apply)
}

func (p *rgb11Manager) setRGB11DKVSStatus(status string) {
	p.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.Status = status
	})
}

func (p *rgb11Manager) rgb11AutoBackupPolicy() *RGB11AutoBackupPolicy {
	return p.rgb11ScopeState().AutoBackup
}
