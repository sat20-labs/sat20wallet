package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	//sindexer "github.com/sat20-labs/satoshinet/indexer/common"
)

const (
	REFERRER_SIG_KEY string = "referrer_sig"
)

// 获得地址上已经注册的推荐人名字列表
func (p *Manager) GetAllRegisteredReferrerName(address, serverPubKey string) ([]string, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}

	if address == "" {
		address = p.wallet.GetAddress()
	}

	var pubkey *secp256k1.PublicKey
	if serverPubKey == "" {
		pubkey = p.wallet.GetPubKey()
	} else {
		pubKeyBytes, err := hex.DecodeString(serverPubKey)
		if err != nil {
			return nil, err
		}
		pubkey, err = utils.BytesToPublicKey(pubKeyBytes)
		if err != nil {
			return nil, err
		}
	}

	names, err := p.l1IndexerClient.GetNamesWithKey(address, REFERRER_SIG_KEY)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0)
	for _, n := range names {
		verified := false
		for _, kv := range n.KVItemList {
			if kv.Key != REFERRER_SIG_KEY {
				continue
			}
			sig, err := hex.DecodeString(kv.Value)
			if err != nil {
				continue
			}
			if !VerifyMessage(pubkey, []byte(n.Name), sig) {
				continue
			}
			verified = true
			break
		}
		if verified {
			result = append(result, n.Name)
		}
	}

	return result, nil
}

// 绑定推荐人地址: 发送一个特殊的交易，由索引器识别 （L2交易，索引器从交易确认高度开始跟踪被推荐人的交易记录）
func (p *Manager) BindReferrerForServer(referrerName string, serverPubKey string) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	referrerName = strings.ToLower(referrerName)
	referrerName = strings.TrimSpace(referrerName)

	var pk []byte
	if serverPubKey == "" {
		pk = p.serverNode.Pubkey.SerializeCompressed()
	} else {
		var err error
		pk, err = hex.DecodeString(serverPubKey)
		if err != nil {
			return "", err
		}
	}

	return p.BindReferrer(referrerName, REFERRER_SIG_KEY, pk)
}

// 将某个name注册为推荐人（L1交易）
func (p *Manager) RegisterAsReferrer(name string, feeRate int64) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	name = strings.ToLower(name)
	name = strings.TrimSpace(name)

	// 暂时没有开放注册，必须由服务端提供白名单注册

	if feeRate == 0 {
		feeRate = p.GetFeeRate()
	}

	// 必须是该名字的持有人才能注册
	info, err := p.l1IndexerClient.GetNameInfo(name)
	if err != nil {
		return "", err
	}
	if info.Address != p.wallet.GetAddress() {
		return "", fmt.Errorf("name %s not belong to %s, current holder %s", name, p.wallet.GetAddress(), info.Address)
	}

	localPubKey := p.wallet.GetPubKey().SerializeCompressed()
	moredata := wwire.RemoteSignMoreData_Msg{
		LocalPubKey: localPubKey,
		Action:      "register_referrer",
		Data:    	[]byte(name),
	}
	md, err := json.Marshal(moredata)
	if err != nil {
		Log.Errorf("Marshal failed, %v", err)
		return "", err
	}

	// 请求服务端一个签名
	req := wwire.SignRequest{
		ChannelId:    "",
		CommitHeight: 0,
		Reason:       "sign",
		MoreData:     md,
		PubKey:       localPubKey,
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sig, err := p.SignMessage(msg)
	if err != nil {
		return "", err
	}

	peerSig, err := p.serverNode.client.SendSigReq(&req, sig)
	if err != nil {
		Log.Errorf("SendSigReq failed. %v", err)
		return "", err
	}
	if len(peerSig) != 1 && len(peerSig[0]) != 1 {
		return "", fmt.Errorf("invalid sig of referrer name")
	}
	if !VerifyMessage(p.serverNode.Pubkey, []byte(name), peerSig[0][0]) {
		return "", fmt.Errorf("referrer %s has no correct signatrue", name)
	}

	value := hex.EncodeToString(peerSig[0][0])

	txId, err := p.SetKeyValueToName(name, REFERRER_SIG_KEY, value, feeRate)
	if err != nil {
		Log.Errorf("SetKeyValueToName %s failed, %v", name, err)
	}

	return txId, nil
}
