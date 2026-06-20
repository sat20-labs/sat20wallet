package wallet

func (p *Manager) enableChannel(channel *Channel) {
	p.EnableChannel(channel)
}

func (p *Manager) EnableChannel(channel *Channel) {
	p.AddChannelToNode(channel)

	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.channelMap[channel.ChannelId] = channel
}

func (p *Manager) disableChannel(channel *Channel) {
	p.DisableChannel(channel)
}

func (p *Manager) DisableChannel(channel *Channel) {
	p.RemoveChannelInNode(getNodeMapKeyWithChannel(channel))

	p.mutex.Lock()
	defer p.mutex.Unlock()
	delete(p.channelMap, channel.ChannelId)
}

func (p *Manager) AddChannelToNode(c *Channel) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.nodeMap[getNodeMapKeyWithChannel(c)] = c.ChannelId
}

func getNodeMapKey(localAddress, remoteAddress string) string {
	return remoteAddress + "-" + localAddress
}

func GetNodeMapKey(localAddress, remoteAddress string) string {
	return getNodeMapKey(localAddress, remoteAddress)
}

func getNodeMapKeyWithChannel(c *Channel) string {
	return getNodeMapKey(c.GetLocalAddress(), c.GetRemoteAddress())
}

func GetNodeMapKeyWithChannel(c *Channel) string {
	return getNodeMapKeyWithChannel(c)
}

func (p *Manager) getNodeMapKey(remoteAddress string) string {
	return getNodeMapKey(p.wallet.GetAddress(), remoteAddress)
}

func (p *Manager) GetNodeMapKey(remoteAddress string) string {
	return p.getNodeMapKey(remoteAddress)
}

func (p *Manager) RemoveChannelInNode(key string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	delete(p.nodeMap, key)
}

func (p *Manager) getChannelByPeerWallet(remoteAddress string) *Channel {
	return p.GetChannelByPeerWallet(remoteAddress)
}

func (p *Manager) GetChannelByPeerWallet(remoteAddress string) *Channel {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.channelMap[p.nodeMap[p.getNodeMapKey(remoteAddress)]]
}

func (p *Manager) getChannel(channelId string) *Channel {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.channelMap[channelId]
}

func (p *Manager) GetChannel(channelId string) *Channel {
	return p.getChannel(channelId)
}

func (p *Manager) HasChannel() bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	return len(p.channelMap) != 0 || len(p.fundingChannelMap) != 0
}

func (p *Manager) GetActiveChannel() *Channel {
	channelId, err := p.GetChannelAddress()
	if err != nil {
		return nil
	}
	return p.GetActiveChannelWithId(channelId)
}

func (p *Manager) GetActiveChannelWithId(channelId string) *Channel {
	c := p.GetChannel(channelId)
	if c != nil {
		return c
	}

	for _, c := range p.GetPaymentReservations() {
		if c.ChannelId == channelId {
			return c.Channel
		}
	}

	for _, c := range p.GetSplicingReservations() {
		if c.ChannelId == channelId {
			return c.Channel
		}
	}
	return nil
}

func (p *Manager) GetCurrentChannel() *Channel {
	if p.wallet == nil {
		return nil
	}

	channelId, err := p.GetChannelAddress()
	if err != nil {
		return nil
	}
	c := p.GetActiveChannelWithId(channelId)
	if c != nil {
		return c
	}

	for _, c := range p.GetFundingReservations() {
		if c.ChannelId == channelId {
			return c.Channel
		}
	}
	return nil
}

func (p *Manager) FindChannel(channelId string) *Channel {
	c := p.GetActiveChannelWithId(channelId)
	if c != nil {
		return c
	}

	for _, c := range p.GetFundingReservations() {
		if c.ChannelId == channelId {
			return c.Channel
		}
	}

	c, err := p.LoadChannel(channelId)
	if err != nil {
		return nil
	}
	return c
}

func (p *Manager) GetChannelStatus(channelId string) int {
	c := p.FindChannel(channelId)
	if c == nil {
		return int(CS_UNKNOWN)
	}
	return int(c.Status)
}

func (p *Manager) GetAllChannels() map[string]*Channel {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	result := make(map[string]*Channel, len(p.channelMap))
	for _, c := range p.channelMap {
		result[c.ChannelId] = c
	}
	for _, c := range p.fundingChannelMap {
		result[c.ChannelId] = c.Channel
	}
	return result
}

func (p *Manager) GetPeerNodeClient(channel *ChannelInDB) NodeRPCClient {
	if p.serverNode == nil {
		return nil
	}
	return p.serverNode.client
}

func (p *Manager) saveChannelToDB(c *Channel) error {
	return p.SaveChannelToDB(c)
}

func (p *Manager) SetChannelBackupHandler(handler ChannelBackupHandler) {
	if handler == nil {
		handler = noopChannelBackupHandler{}
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.channelBackupHandler = handler
}

func (p *Manager) SaveChannelToDB(c *Channel) error {
	if err := p.SaveChannelInDB(&c.ChannelInDB); err != nil {
		return err
	}
	if p.channelBackupHandler == nil {
		return nil
	}
	buf, err := EncodeToBytes(&c.ChannelInDB)
	if err != nil {
		Log.Errorf("SaveChannelToDB EncodeToBytes failed. %v", err)
		return err
	}
	return p.channelBackupHandler.BackupChannel(c, buf)
}

func (p *Manager) loadChannel(channelId string) (*Channel, error) {
	return p.LoadChannel(channelId)
}

func (p *Manager) LoadChannel(channelId string) (*Channel, error) {
	newChannel, err := p.LoadChannelInDB(channelId)
	if err != nil {
		return nil, err
	}
	return NewChannel(newChannel, p), nil
}

func (p *Manager) loadAllChannelsFromDB() (map[string]*Channel, error) {
	return p.LoadAllChannelsFromDB()
}

func (p *Manager) LoadAllChannelsFromDB() (map[string]*Channel, error) {
	result := make(map[string]*Channel)
	channels, err := p.LoadAllChannelInDBFromDB()
	if err != nil {
		return nil, err
	}
	for channelId, c := range channels {
		if c.Status <= CS_CLOSED {
			Log.Infof("channel %s closed", c.ChannelId)
			continue
		}

		result[channelId] = NewChannel(c, p)
	}

	return result, nil
}
