package wallet

import (
	"bytes"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

func (p *Manager) enableChannel(channel *Channel) {
	if err := p.EnableChannel(channel); err != nil {
		Log.Errorf("EnableChannel %s failed. %v", channel.ChannelId, err)
	}
}

func (p *Manager) EnableChannel(channel *Channel) error {
	if channel == nil {
		return fmt.Errorf("channel is nil")
	}
	p.mutex.Lock()
	p.installChannelLocked(channel)
	p.mutex.Unlock()

	// The current remote commitment is valid, not revoked. Keep this invariant
	// inside the SDK so every client, including the PWA, restores the same
	// watchtower state without relying on an upper STP service callback.
	if tower := p.GetWatchTower(); tower != nil {
		tower.CleanCurrentRemoteCommitTx(channel)
	}
	return nil
}

func (p *Manager) disableChannel(channel *Channel) {
	p.DisableChannel(channel)
}

func (p *Manager) DisableChannel(channel *Channel) {
	if channel == nil {
		return
	}
	p.mutex.Lock()
	current := p.channelMap[channel.ChannelId]
	if current != channel {
		p.mutex.Unlock()
		Log.Warnf("DisableChannel ignored stale channel %s", channel.ChannelId)
		return
	}
	key := getNodeMapKeyWithChannel(channel)
	if p.nodeMap[key] == channel.ChannelId {
		delete(p.nodeMap, key)
	}
	delete(p.channelMap, channel.ChannelId)
	p.mutex.Unlock()
}

func (p *Manager) installChannelLocked(channel *Channel) {
	if current := p.channelMap[channel.ChannelId]; current != nil {
		oldKey := getNodeMapKeyWithChannel(current)
		if oldKey != getNodeMapKeyWithChannel(channel) && p.nodeMap[oldKey] == channel.ChannelId {
			delete(p.nodeMap, oldKey)
		}
	}
	p.nodeMap[getNodeMapKeyWithChannel(channel)] = channel.ChannelId
	p.channelMap[channel.ChannelId] = channel
	p.rebindReservationChannelsLocked(channel)
}

func (p *Manager) rebindReservationChannelsLocked(channel *Channel) {
	for _, resv := range p.fundingChannelMap {
		if resv != nil && resv.ChannelId == channel.ChannelId {
			resv.Channel = channel
		}
	}
	for _, resv := range p.closingChannelMap {
		if resv != nil && resv.ChannelId == channel.ChannelId {
			resv.Channel = channel
		}
	}
	for _, resv := range p.paymentChannelMap {
		if resv != nil && resv.ChannelId == channel.ChannelId {
			resv.Channel = channel
		}
	}
	for _, resv := range p.splicingChannelMap {
		if resv != nil && resv.ChannelId == channel.ChannelId {
			resv.Channel = channel
		}
	}
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
		if c != nil && c.ChannelId == channelId && c.Channel != nil {
			return c.Channel
		}
	}

	for _, c := range p.GetSplicingReservations() {
		if c != nil && c.ChannelId == channelId && c.Channel != nil {
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
		if c != nil && c.ChannelId == channelId && c.Channel != nil {
			return c.Channel
		}
	}
	for _, c := range p.GetClosingReservations() {
		if c != nil && c.ChannelId == channelId && c.Channel != nil {
			return c.Channel
		}
	}
	return nil
}

func reservationMatchesChannelContext(resv Reservation, channelID string, walletID common.WalletId) bool {
	if resv == nil || resv.GetStatus() <= RS_CLOSED || resv.GetStatus() == RS_CONFIRMED {
		return false
	}
	resvWalletID := resv.GetWalletId()
	if resvWalletID.Id != 0 && resvWalletID != walletID {
		return false
	}
	switch value := resv.(type) {
	case *FundingReservation:
		return value.ChannelId == channelID
	case *ClosingReservation:
		return value.ChannelId == channelID
	case *PaymentReservation:
		return value.ChannelId == channelID
	case *SplicingReservation:
		return value.ChannelId == channelID
	default:
		return false
	}
}

func (p *Manager) hasPendingFundingReservation(channelID string, walletID common.WalletId) bool {
	for _, resv := range p.GetFundingReservations() {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return true
		}
	}
	return false
}

func (p *Manager) hasPendingClosingReservation(channelID string, walletID common.WalletId) bool {
	for _, resv := range p.GetClosingReservations() {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return true
		}
	}
	return false
}

func (p *Manager) hasPendingPaymentReservation(channelID string, walletID common.WalletId) bool {
	for _, resv := range p.GetPaymentReservations() {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return true
		}
	}
	return false
}

func (p *Manager) hasPendingSplicingReservation(channelID string, walletID common.WalletId) bool {
	for _, resv := range p.GetSplicingReservations() {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return true
		}
	}
	return false
}

func (p *Manager) hasPendingLockWithExpand(channelID string) bool {
	for _, reservation := range p.GetLocalActionReservations() {
		resv, ok := reservation.(*LocalActionPerformData)
		if !ok || resv == nil || resv.Status <= RS_CLOSED || resv.Status == RS_PERFORM_ACTION_COMPLETED ||
			resv.Action != LOCAL_ACTION_LOCK_WITH_EXPAND || !p.localActionBelongsToCurrentWallet(resv) {
			continue
		}
		param, ok := resv.ActionParam.(*LocalActionParam_Expand)
		if ok && param != nil && param.ChannelId == channelID {
			return true
		}
	}
	return false
}

func (p *Manager) rejectUnfinishedChannelLifecycle(channelID string) error {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.wallet == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	paymentKey := p.wallet.GetPaymentPubKey()
	var paymentPubKey []byte
	if paymentKey != nil {
		paymentPubKey = paymentKey.SerializeCompressed()
	}
	return p.rejectUnfinishedChannelLifecycleLocked(channelID, p.wallet.GetWalletId(), paymentPubKey)
}

func (p *Manager) rejectUnfinishedChannelLifecycleLocked(channelID string, walletID common.WalletId,
	paymentPubKey []byte) error {
	for _, resv := range p.fundingChannelMap {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return fmt.Errorf("channel open is already in progress")
		}
	}
	for _, resv := range p.closingChannelMap {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return fmt.Errorf("channel close is already in progress")
		}
	}
	for _, resv := range p.paymentChannelMap {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return fmt.Errorf("channel payment is already in progress")
		}
	}
	for _, resv := range p.splicingChannelMap {
		if reservationMatchesChannelContext(resv, channelID, walletID) {
			return fmt.Errorf("channel splicing is already in progress")
		}
	}
	for _, reservation := range p.localActionPerformMap {
		resv, ok := reservation.(*LocalActionPerformData)
		if !ok || resv == nil || resv.Status <= RS_CLOSED || resv.Status == RS_PERFORM_ACTION_COMPLETED ||
			resv.Action != LOCAL_ACTION_LOCK_WITH_EXPAND {
			continue
		}
		belongsToWallet := resv.WalletId == (common.WalletId{}) || resv.WalletId == walletID
		if len(resv.ReqPubKey) != 0 {
			belongsToWallet = len(paymentPubKey) != 0 && bytes.Equal(resv.ReqPubKey, paymentPubKey)
		}
		param, ok := resv.ActionParam.(*LocalActionParam_Expand)
		if belongsToWallet && ok && param != nil && param.ChannelId == channelID {
			return fmt.Errorf("channel lock-with-expand is already in progress")
		}
	}
	return nil
}

func (p *Manager) rejectUnfinishedChannelLifecycleForWallet(channelID string, wallet common.Wallet) error {
	if wallet == nil || wallet.GetPaymentPubKey() == nil {
		return fmt.Errorf("wallet is not created/unlocked")
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	return p.rejectUnfinishedChannelLifecycleLocked(channelID, wallet.GetWalletId(),
		wallet.GetPaymentPubKey().SerializeCompressed())
}

func (p *Manager) FindChannel(channelId string) *Channel {
	// A locked manager has only persisted wallet metadata.  Do not load a
	// channel runtime before the wallet secrets have been hydrated.
	if p.wallet == nil {
		return nil
	}
	c := p.GetActiveChannelWithId(channelId)
	if c != nil {
		return c
	}

	for _, c := range p.GetFundingReservations() {
		if c != nil && c.ChannelId == channelId && c.Channel != nil {
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
		if c == nil {
			continue
		}
		result[c.ChannelId] = c
	}
	for _, c := range p.fundingChannelMap {
		if c == nil || c.Channel == nil {
			continue
		}
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
	if c == nil {
		return fmt.Errorf("channel is nil")
	}
	if err := p.SaveChannelInDB(&c.ChannelInDB); err != nil {
		return err
	}
	p.backupChannel(c)
	return nil
}

// backupChannel is an optional, best-effort service backup. Its failure must
// not turn a successful local save or peer sync into an operation failure.
func (p *Manager) backupChannel(c *Channel) {
	p.mutex.RLock()
	handler := p.channelBackupHandler
	p.mutex.RUnlock()
	if handler == nil {
		return
	}
	buf, err := EncodeToBytes(&c.ChannelInDB)
	if err != nil {
		Log.Warningf("backup channel %s encoding failed: %v", c.ChannelId, err)
		return
	}
	if err := handler.BackupChannel(c, buf); err != nil {
		Log.Warningf("backup channel %s failed: %v", c.ChannelId, err)
	}
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
