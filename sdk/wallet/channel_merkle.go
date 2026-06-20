package wallet

import (
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

func (s *rollingMerkleTreeStore) calcLocalAssetMerkleRoot(channel *ChannelInDB) chainhash.Hash {
	data := fmt.Sprintf("%s %d %d %d", channel.ChannelId, channel.CommitHeight, channel.Capacity, channel.CsvDelay)

	s.add(calcHash(data))

	// commitent
	localValue := channel.LocalCommitment.LocalBalance

	// sort the value
	type Asset struct {
		name AssetName
		amt  *Decimal
	}
	local := make([]*Asset, 0)
	for k, v := range localValue {
		local = append(local, &Asset{name: k, amt: v.Clone()})
	}
	sort.Slice(local, func(i, j int) bool {
		return local[i].name.String() < local[j].name.String()
	})

	for _, v := range local {
		if v.amt.Sign() != 0 {
			s.add(calcHash(fmt.Sprintf("%s-%s", v.name.String(), v.amt.String())))
		}
	}

	// If we only have one leaf, then the hash of that tx is the merkle root.
	if s.numLeaves == 1 {
		return s.roots[0]
	}

	// Add on the payment tx again if there's an odd number of leaves.
	if s.numLeaves%2 != 0 {
		s.add(chainhash.DoubleHashH(channel.StaticMerkleRoot))
	}

	return s.calcRoot()
}

func (s *rollingMerkleTreeStore) calcRemoteAssetMerkleRoot(channel *ChannelInDB) chainhash.Hash {
	data := fmt.Sprintf("%s %d %d %d", channel.ChannelId, channel.CommitHeight, channel.Capacity, channel.CsvDelay)

	s.add(calcHash(data))

	// commitent
	remoteValue := channel.LocalCommitment.RemoteBalance

	// sort the value
	type Asset struct {
		name AssetName
		amt  *Decimal
	}
	remote := make([]*Asset, 0)
	for k, v := range remoteValue {
		remote = append(remote, &Asset{name: k, amt: v.Clone()})
	}
	sort.Slice(remote, func(i, j int) bool {
		return remote[i].name.String() < remote[j].name.String()
	})

	for _, v := range remote {
		if v.amt.Sign() != 0 {
			s.add(calcHash(fmt.Sprintf("%s-%s", v.name.String(), v.amt.String())))
		}
	}

	// If we only have one leaf, then the hash of that tx is the merkle root.
	if s.numLeaves == 1 {
		return s.roots[0]
	}

	// Add on the payment tx again if there's an odd number of leaves.
	if s.numLeaves%2 != 0 {
		s.add(chainhash.DoubleHashH(channel.StaticMerkleRoot))
	}

	return s.calcRoot()
}

func (s *rollingMerkleTreeStore) calcStaticMerkleRoot(channel *ChannelInDB) chainhash.Hash {

	s.add(calcHash(channel.ChannelId))
	s.add(calcHash(fmt.Sprintf("%d %d %d %d", channel.Version,
		channel.ChannelType, channel.FundingTime, channel.CsvDelay)))
	s.add(calcHash(channel.Address))

	s.add(chainhash.DoubleHashH(channel.Contract))
	s.add(chainhash.DoubleHashH(channel.RedeemScript))
	// s.add(chainhash.DoubleHashH(channel.PeerNodeId)) 确保两边数据一致

	local := channel.LocalChanCfg
	remote := channel.RemoteChanCfg
	if !channel.IsInitiator {
		local, remote = remote, local
	}

	// 不能使用 EncodeToBytes 进行编解码，可能会导致字节码不一致
	buf := fmt.Sprintf("%d%d%s%s", local.InitialBalance, local.WalletId,
		hex.EncodeToString(local.PaymentKey.SerializeCompressed()),
		hex.EncodeToString(local.RevocationBasePoint.SerializeCompressed()))
	s.add(chainhash.DoubleHashH([]byte(buf)))

	buf = fmt.Sprintf("%d%d%s%s", remote.InitialBalance, remote.WalletId,
		hex.EncodeToString(remote.PaymentKey.SerializeCompressed()),
		hex.EncodeToString(remote.RevocationBasePoint.SerializeCompressed()))
	s.add(chainhash.DoubleHashH([]byte(buf)))

	// If we only have one leaf, then the hash of that tx is the merkle root.
	if s.numLeaves == 1 {
		return s.roots[0]
	}

	// Add on the payment tx again if there's an odd number of leaves.
	if s.numLeaves%2 != 0 {
		s.add(calcHash(channel.Memo))
	}

	return s.calcRoot()
}

// hashMerkleBranches takes two hashes, treated as the left and right tree
// nodes, and returns the hash of their concatenation.  This is a helper
func CalcChannelRemoteAssetsMerkleRoot(channel *ChannelInDB) []byte {

	s := newRollingMerkleTreeStore(uint64(2 + 2*len(channel.LocalCommitment.LocalBalance)))
	merkleHash := s.calcRemoteAssetMerkleRoot(channel)

	// log.Debugf("merkleHash: %s", merkleHash.String())
	return merkleHash.CloneBytes()
}

func CalcChannelLocalAssetsMerkleRoot(channel *ChannelInDB) []byte {

	s := newRollingMerkleTreeStore(uint64(2 + 2*len(channel.LocalCommitment.LocalBalance)))
	merkleHash := s.calcLocalAssetMerkleRoot(channel)

	// log.Debugf("merkleHash: %s", merkleHash.String())
	return merkleHash.CloneBytes()
}

func CalcChannelStaticMerkleRoot(channel *ChannelInDB) []byte {

	s := newRollingMerkleTreeStore(uint64(10))
	merkleHash := s.calcStaticMerkleRoot(channel)

	// log.Debugf("merkleHash: %s", merkleHash.String())
	return merkleHash.CloneBytes()
}

func CalcChannelHash(c *ChannelInDB) []byte {

	var buf []byte
	outputs := c.GetAllFundingOutput()
	for _, outputMap := range outputs {
		assetName := outputMap.AssetName
		buf = append(buf, []byte(assetName.String())...)
		for _, output := range outputMap.Outputs {
			for _, asset := range output.Assets {
				buf2 := fmt.Sprintf("%s%s%d", asset.Name.String(), asset.Amount.String(), asset.BindingSat)
				buf = append(buf, buf2...)
				offsets, ok := output.Offsets[asset.Name]
				if ok {
					for _, offset := range offsets {
						buf2 := fmt.Sprintf("%d%d", offset.Start, offset.End)
						buf = append(buf, buf2...)
					}
				}
			}
			buf2 := fmt.Sprintf("%d%s%s", output.OutValue.Value,
				hex.EncodeToString(output.OutValue.PkScript), output.OutPointStr)
			buf = append(buf, buf2...)
		}
	}

	localCommit := c.LocalCommitment
	buf2 := fmt.Sprintf("%v%v%v", localCommit.CommitSig, localCommit.DeAnchorSig, localCommit.Revocation)
	buf = append(buf, buf2...)
	if localCommit.CommitTx != nil {
		txStr, _ := EncodeMsgTx(localCommit.CommitTx)
		buf = append(buf, txStr...)
	}
	if localCommit.DeAnchorTx != nil {
		txStr, _ := EncodeMsgTx_SatsNet(localCommit.DeAnchorTx)
		buf = append(buf, txStr...)
	}
	remoteCommit := c.RemoteCommitment
	buf2 = fmt.Sprintf("%v%v%v", remoteCommit.CommitSig, remoteCommit.DeAnchorSig, remoteCommit.Revocation)
	buf = append(buf, buf2...)
	if remoteCommit.CommitTx != nil {
		txStr, _ := EncodeMsgTx(remoteCommit.CommitTx)
		buf = append(buf, txStr...)
	}
	if remoteCommit.DeAnchorTx != nil {
		txStr, _ := EncodeMsgTx_SatsNet(remoteCommit.DeAnchorTx)
		buf = append(buf, txStr...)
	}

	// 确保之前已经计算好
	buf = append(buf, c.StaticMerkleRoot...)
	buf = append(buf, c.LocalAssetsMerkleRoot...)
	buf = append(buf, c.RemoteAssetsMerkleRoot...)

	hash := chainhash.DoubleHashH(buf)

	//Log.Infof("channel %s buf\n%s\nhash:\n%s", c.ChannelId, hex.EncodeToString(buf), hash)

	return hash.CloneBytes()
}
