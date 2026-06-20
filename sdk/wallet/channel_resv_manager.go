package wallet

func (p *Manager) GetFundingReservation(id int64) (*FundingReservation, bool) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	resv, ok := p.fundingChannelMap[id]
	return resv, ok
}

func (p *Manager) GetPaymentReservation(id int64) (*PaymentReservation, bool) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	resv, ok := p.paymentChannelMap[id]
	return resv, ok
}

func (p *Manager) GetClosingReservation(id int64) (*ClosingReservation, bool) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	resv, ok := p.closingChannelMap[id]
	return resv, ok
}

func (p *Manager) GetSplicingReservation(id int64) (*SplicingReservation, bool) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	resv, ok := p.splicingChannelMap[id]
	return resv, ok
}

func (p *Manager) GetFundingReservations() map[int64]*FundingReservation {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	result := make(map[int64]*FundingReservation, len(p.fundingChannelMap))
	for id, resv := range p.fundingChannelMap {
		result[id] = resv
	}
	return result
}

func (p *Manager) GetPaymentReservations() map[int64]*PaymentReservation {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	result := make(map[int64]*PaymentReservation, len(p.paymentChannelMap))
	for id, resv := range p.paymentChannelMap {
		result[id] = resv
	}
	return result
}

func (p *Manager) GetClosingReservations() map[int64]*ClosingReservation {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	result := make(map[int64]*ClosingReservation, len(p.closingChannelMap))
	for id, resv := range p.closingChannelMap {
		result[id] = resv
	}
	return result
}

func (p *Manager) GetSplicingReservations() map[int64]*SplicingReservation {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	result := make(map[int64]*SplicingReservation, len(p.splicingChannelMap))
	for id, resv := range p.splicingChannelMap {
		result[id] = resv
	}
	return result
}

func (p *Manager) HasFundingReservation() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return len(p.fundingChannelMap) != 0
}
