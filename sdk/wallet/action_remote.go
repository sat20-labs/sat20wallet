package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	"github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type remoteActionRPCClient interface {
	SendPerformRemoteActionReq(info *RemoteActionPerformReservation) error
	SendPerformRemoteActionAckReq(info *RemoteActionPerformReservation) error
	SendActionResultNfty(msgId int64, msg string, result int, reason string) error
}

// PerformRemoteAction requests a server-side STP action, lets the wallet build
// and broadcast the required fee/action tx, and then ACKs the server.
func (p *Manager) PerformRemoteAction(doAction StartRemoteAction, action string, actionParam, more []byte,
	feeRate int64, sendTxInL1, toBootstrap bool) (string, int64, []byte, error) {

	start := time.Now()
	Log.Infof("PerformRemoteAction %s", action)
	if p.wallet == nil {
		return "", -1, nil, fmt.Errorf("wallet is not created/unlocked")
	}
	if p.serverNode == nil || p.serverNode.client == nil {
		return "", -1, nil, fmt.Errorf("server node is not initialized")
	}
	if toBootstrap && len(p.bootstrapNode) == 0 {
		return "", -1, nil, fmt.Errorf("bootstrap node is not initialized")
	}
	if !p.checkSuperNodeStatus() {
		return "", -1, nil, fmt.Errorf("peer is offline")
	}

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	resv, client, serverPubkey, err := p.newRemoteAction(action, actionParam, more, feeRate, sendTxInL1, toBootstrap)
	if err != nil {
		return "", -1, nil, err
	}
	msg, err := json.Marshal(resv.req)
	if err != nil {
		return "", -1, nil, err
	}
	resv.ReqSig, err = p.wallet.SignMessageWithIndex(msg, 0) // node key signs when NodeId is present.
	if err != nil {
		return "", -1, nil, err
	}

	for {
		err = client.SendPerformRemoteActionReq(resv)
		if err != nil {
			Log.Errorf("SendPerformRemoteActionReq failed. %v", err)
			break
		}

		err = p.sendFeeTxForRemoteAction(resv, serverPubkey, doAction)
		if err != nil {
			Log.Errorf("sendFeeTxForRemoteAction failed. %v", err)
			break
		}

		err = client.SendPerformRemoteActionAckReq(resv)
		if err != nil {
			Log.Errorf("SendPerformRemoteActionAckReq failed. %v, try again", err)
			err = client.SendPerformRemoteActionAckReq(resv)
			if err != nil {
				Log.Errorf("SendPerformRemoteActionAckReq failed. %v", err)
				break
			}
		}

		p.addResv(resv)
		if err = p.SaveWalletReservation(resv); err != nil {
			Log.Errorf("SaveReservation failed. %v", err)
			break
		}

		break
	}

	if err != nil {
		if resv.Id != 0 {
			_ = client.SendActionResultNfty(resv.Id, RESV_TYPE_REMOTEACTION, -1, err.Error())
		}
		return "", resv.Id, nil, err
	}

	Log.Infof("PerformRemoteAction finished, %v", time.Since(start))
	return resv.FeeTxId, resv.Id, resv.ActionResult, nil
}

func (p *Manager) newRemoteAction(action string, actionParam, more []byte,
	feeRate int64, sendTxInL1, toBootstrap bool) (*RemoteActionPerformReservation, remoteActionRPCClient, *secp256k1.PublicKey, error) {

	client, node, err := p.remoteActionClient(toBootstrap)
	if err != nil {
		return nil, nil, nil, err
	}

	resv := &RemoteActionPerformReservation{
		ReservationBase:     newReservationBase(0, RS_INIT, p.wallet),
		Action:              action,
		ActionParam:         actionParam,
		FeeRate:             feeRate,
		ReqTime:             time.Now().Unix(),
		ReqPubKey:           p.wallet.GetPaymentPubKey().SerializeCompressed(),
		SendTxInL1:          sendTxInL1,
		SendToBootstrapNode: toBootstrap,
		MoreData:            more,
	}
	resv.req = &wwire.PerformActionRequest{
		MsgHeader:   wwire.NewMsgHeader(),
		Action:      action,
		ActionParam: actionParam,
		FeeRate:     resv.FeeRate,
		ReqTime:     resv.ReqTime,
		SendTxInL1:  sendTxInL1,
		MoreData:    more,
		PubKey:      resv.ReqPubKey,
		NodeId:      p.wallet.GetNodePubKey().SerializeCompressed(),
	}

	return resv, client, node.Pubkey, nil
}

func (p *Manager) remoteActionClient(toBootstrap bool) (remoteActionRPCClient, *Node, error) {
	node := p.serverNode
	if toBootstrap {
		if len(p.bootstrapNode) == 0 {
			return nil, nil, fmt.Errorf("bootstrap node is not initialized")
		}
		node = p.bootstrapNode[0]
	}
	if node == nil || node.client == nil {
		return nil, nil, fmt.Errorf("server node is not initialized")
	}
	client, ok := node.client.(remoteActionRPCClient)
	if !ok {
		return nil, nil, fmt.Errorf("node client does not support remote action")
	}
	return client, node, nil
}

func (p *Manager) sendFeeTxForRemoteAction(resv *RemoteActionPerformReservation,
	serverPubkey *secp256k1.PublicKey, doAction StartRemoteAction) error {

	if resv.Id == 0 {
		return fmt.Errorf("invalid message id %d", resv.Id)
	}
	if resv.Invoice == nil || resv.InvoiceSig == nil {
		return fmt.Errorf("invalid parameters")
	}
	if !VerifyMessage(serverPubkey, resv.Invoice, resv.InvoiceSig) {
		return fmt.Errorf("VerifyMessage failed")
	}

	tx, txId, err := doAction(resv)
	if err != nil {
		return err
	}

	resv.FeeTx = tx
	resv.FeeTxId = txId
	return nil
}

func (p *Manager) HandleRemoteActionStatus(sendTxInL1 bool) {
	p.mutex.RLock()
	remoteActionMap := make(map[int64]*RemoteActionPerformReservation, len(p.remoteActionPerformMap))
	for id, resv := range p.remoteActionPerformMap {
		remoteActionMap[id] = resv
	}
	p.mutex.RUnlock()

	for _, resv := range remoteActionMap {
		if resv == nil || resv.Status <= RS_CLOSED || resv.SendTxInL1 != sendTxInL1 {
			continue
		}
		wasCompleted := resv.Status == RS_PERFORM_ACTION_COMPLETED
		if err := p.handleRemoteActionStatus(resv); err != nil {
			Log.Errorf("handleRemoteActionStatus %d failed. %v", resv.Id, err)
			continue
		}
		if resv.Status < RS_CLOSED {
			p.notifyActionStatus(&ActionStatusEvent{
				Event:      ACTION_STATUS_EVENT_FAILED,
				Resv:       resv,
				ResvType:   RESV_TYPE_REMOTEACTION,
				Action:     resv.Action,
				Status:     resv.Status,
				SendTxInL1: sendTxInL1,
			})
			continue
		}
		if wasCompleted && resv.Status == RS_CLOSED {
			p.notifyActionStatus(&ActionStatusEvent{
				Event:      ACTION_STATUS_EVENT_COMPLETED,
				Resv:       resv,
				ResvType:   RESV_TYPE_REMOTEACTION,
				Action:     resv.Action,
				Status:     RS_PERFORM_ACTION_COMPLETED,
				SendTxInL1: sendTxInL1,
			})
		}
	}
}

func (p *Manager) handleRemoteActionStatus(resv *RemoteActionPerformReservation) error {
	if resv == nil {
		return nil
	}
	if resv.Status <= RS_CLOSED {
		return nil
	}

	client, _, err := p.remoteActionClient(resv.SendToBootstrapNode)
	if err != nil {
		return err
	}

	if resv.Status != RS_PERFORM_ACTION_COMPLETED {
		if err := client.SendPerformRemoteActionAckReq(resv); err != nil {
			return err
		}
	}

	if resv.Status == RS_PERFORM_ACTION_COMPLETED {
		if err := client.SendActionResultNfty(resv.Id, RESV_TYPE_REMOTEACTION, 0, ""); err != nil {
			return err
		}
		resv.Status = RS_CLOSED
	}

	return p.SaveWalletReservation(resv)
}

func (p *Manager) DeployRunes_Remote(assetName string, symbol int32, maxSupply int64,
	limit int64, selfMint bool, destAddr string, feeRate int64) (string, int64, string, error) {
	if p.wallet == nil {
		return "", 0, "", fmt.Errorf("wallet is not created/unlocked")
	}
	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	param, err := EncodeRemoteDeployRunesParam(&RemoteDeployRunesParam{
		AssetName: normalizeRemoteDeployRunesAssetName(assetName),
		Symbol:    symbol,
		MaxSupply: maxSupply,
		Limit:     limit,
		SelfMint:  selfMint,
		DestAddr:  destAddr,
	})
	if err != nil {
		return "", 0, "", err
	}

	doAction := func(resv *RemoteActionPerformReservation) (string, string, error) {
		signedScript, err := SignedPerformRemoteActionInvoice(resv.Action, resv.InvoiceSig)
		if err != nil {
			return "", "", fmt.Errorf("SignedPerformRemoteActionInvoice failed. %v", err)
		}

		nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_PERFORMACTION, signedScript)
		if err != nil {
			return "", "", fmt.Errorf("NullDataScript failed. %v", err)
		}

		tx, err := p.SendAssets(resv.ServiceAddr, ASSET_PLAIN_SAT.String(),
			fmt.Sprintf("%d", resv.ServiceFee), resv.FeeRate, nullDataScript)
		if err != nil {
			Log.Errorf("SendAssets %s %d failed, %v", resv.ServiceAddr, resv.ServiceFee, err)
			return "", "", err
		}
		txHex, err := EncodeMsgTx(tx)
		if err != nil {
			return "", "", err
		}

		return txHex, tx.TxID(), nil
	}

	txId, resvId, result, err := p.PerformRemoteAction(doAction, REMOTE_ACTION_DEPLOY_RUNES, param, nil, feeRate, true, false)
	if err != nil {
		Log.Errorf("DeployRunes_Remote %s failed: %v", assetName, err)
		return "", 0, "", err
	}

	return txId, resvId, string(result), nil
}

func normalizeRemoteDeployRunesAssetName(assetName string) string {
	asset := ParseAssetString(assetName)
	if asset != nil && asset.Protocol == indexer.PROTOCOL_NAME_RUNES {
		return asset.String()
	}
	if strings.Contains(assetName, ":") {
		return assetName
	}
	return fmt.Sprintf("%s:%s:%s", indexer.PROTOCOL_NAME_RUNES, indexer.ASSET_TYPE_FT, assetName)
}

// 在主网质押足够的资产，以便成为一个Miner
// 优先检查通道中已经存在的质押资产
func (p *Manager) StakeToBeMinner(bCoreNode bool, feeRate int64) (string, int64, error) {
	Log.Infof("StakeToBeMinner")
	if p.wallet == nil {
		return "", -1, fmt.Errorf("wallet is not created/unlocked")
	}
	if !p.checkSuperNodeStatus() {
		return "", -1, fmt.Errorf("peer is offline")
	}

	assetName := indexer.GetStakeAssetName(p.GetSyncHeightL1())
	asset := ParseAssetString(assetName)
	if asset == nil {
		return "", 0, fmt.Errorf("invalid asset name %s", assetName)
	}

	var serverPubkey []byte
	if bCoreNode {
		serverPubkey, _ = hex.DecodeString(indexer.GetBootstrapPubKey())
	} else {
		serverPubkey = p.serverNode.Pubkey.SerializeCompressed()
		isServerCoreNode, err := p.l2IndexerClient.IsCoreNode(serverPubkey)
		if err != nil {
			return "", 0, err
		}
		if !isServerCoreNode {
			return "", 0, fmt.Errorf("server node %s is not a core node", hex.EncodeToString(serverPubkey))
		}
	}
	localPubkey := p.wallet.GetPubKey().SerializeCompressed()
	channelAddr, err := GetP2WSHaddress(localPubkey, serverPubkey)
	if err != nil {
		return "", 0, err
	}

	assetAmt := indexer.GetStakeAssetAmt(p.GetSyncHeightL1())
	var ascendingTxId string
	dAmt := indexer.NewDefaultDecimal(assetAmt)
	utxo, err := p.getStakeUtxo(channelAddr, dAmt, asset)
	if err == nil {
		_, err = p.l2IndexerClient.GetAscendData(utxo)
		if err != nil {
			parts := strings.Split(utxo, ":")
			ascendingTxId = parts[0]
		}
	}

	if ascendingTxId == "" {
		available, _ := p.GetAssetAmount("", asset, nil)
		if available == nil {
			available = indexer.NewDefaultDecimal(0)
		}
		if available.Int64() < assetAmt {
			return "", 0, fmt.Errorf("no enough asset %s, required %d but only %d", asset.String(), assetAmt, available.Int64())
		}
	}

	doAction := func(resv *RemoteActionPerformReservation) (string, string, error) {
		if ascendingTxId != "" {
			return "", ascendingTxId, nil
		}
		signedScript, err := SignedPerformRemoteActionInvoice(resv.Action, resv.InvoiceSig)
		if err != nil {
			return "", "", fmt.Errorf("SignedPerformRemoteActionInvoice failed. %v", err)
		}

		nullDataScript, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_PERFORMACTION, signedScript)
		if err != nil {
			return "", "", fmt.Errorf("NullDataScript failed. %v", err)
		}

		var txHex, txId string
		if resv.SendTxInL1 {
			tx, err := p.SendAssets(channelAddr, assetName,
				fmt.Sprintf("%d", assetAmt), resv.FeeRate, nullDataScript)
			if err != nil {
				Log.Errorf("SendAssets %s %d failed, %v", channelAddr, resv.ServiceFee, err)
				return "", "", err
			}
			txId = tx.TxID()
			txHex, err = EncodeMsgTx(tx)
			if err != nil {
				return "", "", err
			}
		} else {
			tx, err := p.SendAssets_SatsNet(channelAddr, assetName,
				fmt.Sprintf("%d", assetAmt), nullDataScript)
			if err != nil {
				Log.Errorf("SendAssets_SatsNet %s %d failed, %v", channelAddr, resv.ServiceFee, err)
				return "", "", err
			}
			txId = tx.TxID()
			txHex, err = EncodeMsgTx_SatsNet(tx)
			if err != nil {
				return "", "", err
			}
		}

		return txHex, txId, nil
	}

	param, err := txscript.NewScriptBuilder().
		AddData([]byte(assetName)).
		AddData([]byte(fmt.Sprintf("%d", assetAmt))).Script()
	if err != nil {
		return "", -1, err
	}

	invoice, err := sindexer.CreateStakeInvoice(asset, dAmt)
	if err != nil {
		return "", -1, err
	}
	more, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_STAKE, invoice)
	if err != nil {
		return "", -1, err
	}

	txId, resvId, _, err := p.PerformRemoteAction(doAction, REMOTE_ACTION_ASCEND, param, more, feeRate, true, bCoreNode)
	if err != nil {
		Log.Errorf("PerformRemoteAction %s failed: %v", REMOTE_ACTION_ASCEND, err)
		return "", 0, err
	}

	return txId, resvId, nil
}

func (p *Manager) getStakeUtxo(address string, amt *Decimal, asset *swire.AssetName) (string, error) {
	outputs := p.l1IndexerClient.GetUtxoListWithTicker(address, asset)
	for _, u := range outputs {
		assets := u.ToTxAssets()
		found, _ := assets.Find(asset)
		if found != nil && found.Amount.Cmp(amt) >= 0 {
			return u.OutPoint, nil
		}
	}

	return "", fmt.Errorf("no enough asset %s at %s", asset.String(), address)
}
