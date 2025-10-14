package wallet

import (
	"encoding/hex"
	"fmt"
	"io"
	"math/bits"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
)

// rollingMerkleTreeStore calculates the merkle root by only allocating O(logN)
// memory where N is the total amount of leaves being included in the tree.
type rollingMerkleTreeStore struct {
	// roots are where the temporary merkle roots get stored while the
	// merkle root is being calculated.
	roots []chainhash.Hash

	// numLeaves is the total leaves the store has processed.  numLeaves
	// is required for the root calculation algorithm to work.
	numLeaves uint64
}

// newRollingMerkleTreeStore returns a rollingMerkleTreeStore with the roots
// allocated based on the passed in size.
//
// NOTE: If more elements are added in than the passed in size, there will be
// additional allocations which in turn hurts performance.
func newRollingMerkleTreeStore(size uint64) rollingMerkleTreeStore {
	var alloc int
	if size != 0 {
		alloc = bits.Len64(size - 1)
	}
	return rollingMerkleTreeStore{roots: make([]chainhash.Hash, 0, alloc)}
}

// add adds a single hash to the merkle tree store.  Refer to algorithm 1 "AddOne" in
// the utreexo paper (https://eprint.iacr.org/2019/611.pdf) for the exact algorithm.
func (s *rollingMerkleTreeStore) add(add chainhash.Hash) {
	// We can tell where the roots are by looking at the binary representation
	// of the numLeaves.  Wherever there's a 1, there's a root.
	//
	// numLeaves of 8 will be '1000' in binary, so there will be one root at
	// row 3. numLeaves of 3 will be '11' in binary, so there's two roots.  One at
	// row 0 and one at row 1.  Row 0 is the leaf row.
	//
	// In this loop below, we're looking for these roots by checking if there's
	// a '1', starting from the LSB.  If there is a '1', we'll hash the root being
	// added with that root until we hit a '0'.
	newRoot := add
	for h := uint8(0); (s.numLeaves>>h)&1 == 1; h++ {
		// Pop off the last root.
		var root chainhash.Hash
		root, s.roots = s.roots[len(s.roots)-1], s.roots[:len(s.roots)-1]

		// Calculate the hash of the new root and append it.
		newRoot = hashMerkleBranches(&root, &newRoot)
	}
	s.roots = append(s.roots, newRoot)
	s.numLeaves++
}

func calcHash(data string) chainhash.Hash {
	return chainhash.DoubleHashH([]byte(data))
}

func calcHash2(data []byte) chainhash.Hash {
	return chainhash.DoubleHashH((data))
}

func (s *rollingMerkleTreeStore) calcRoot() chainhash.Hash {
	// If we still have more than 1 root after adding on the last tx again,
	// we need to do the same for the upper rows.
	//
	// For example, the below tree has 6 leaves.  For row 1, you'll need to
	// hash 'F' with itself to create 'C' so you have something to hash with
	// 'B'.  For bigger trees we may need to do the same in rows 2 or 3 as
	// well.
	//
	// row :3         A
	//              /   \
	// row :2     B       C
	//           / \     / \
	// row :1   D   E   F   F
	//         / \ / \ / \
	// row :0  1 2 3 4 5 6
	for len(s.roots) > 1 {
		// If we have to keep adding the last node in the set, bitshift
		// the num leaves right by 1.  This effectively moves the row up
		// for calculation.  We do this until we reach a row where there's
		// an odd number of leaves.
		//
		// row :3         A
		//              /   \
		// row :2     B       C        D
		//           / \     / \     /   \
		// row :1   E   F   G   H   I     J
		//         / \ / \ / \ / \ / \   / \
		// row :0  1 2 3 4 5 6 7 8 9 10 11 12
		//
		// In the above tree, 12 leaves were added and there's an odd amount
		// of leaves at row 2.  Because of this, we'll bitshift right twice.
		currentLeaves := s.numLeaves
		for h := uint8(0); (currentLeaves>>h)&1 == 0; h++ {
			s.numLeaves >>= 1
		}

		// Add the last root again so that it'll get hashed with itself.
		h := s.roots[len(s.roots)-1]
		s.add(h)
	}

	return s.roots[0]
}


// hashMerkleBranches takes two hashes, treated as the left and right tree
// nodes, and returns the hash of their concatenation.  This is a helper
// function used to aid in the generation of a merkle tree.
func hashMerkleBranches(left, right *chainhash.Hash) chainhash.Hash {
	// Concatenate the left and right nodes.
	var hash [chainhash.HashSize * 2]byte
	copy(hash[:chainhash.HashSize], left[:])
	copy(hash[chainhash.HashSize:], right[:])

	return chainhash.DoubleHashRaw(func(w io.Writer) error {
		_, err := w.Write(hash[:])
		return err
	})
}

// CalcMerkleRoot computes the merkle root over a set of hashed leaves. The
// interior nodes are computed opportunistically as the leaves are added to the
// abstract tree to reduce the total number of allocations. Throughout the
// computation, this computation only requires storing O(log n) interior
// nodes.
//
// This method differs from BuildMerkleTreeStore in that the interior nodes are
// discarded instead of being returned along with the root. CalcMerkleRoot is
// slightly faster than BuildMerkleTreeStore and requires significantly less
// memory and fewer allocations.
//
// A merkle tree is a tree in which every non-leaf node is the hash of its
// children nodes. A diagram depicting how this works for bitcoin transactions
// where h(x) is a double sha256 follows:
//
//	         root = h1234 = h(h12 + h34)
//	        /                           \
//	  h12 = h(h1 + h2)            h34 = h(h3 + h4)
//	   /            \              /            \
//	h1 = h(tx1)  h2 = h(tx2)    h3 = h(tx3)  h4 = h(tx4)
//
// The additional bool parameter indicates if we are generating the merkle tree
// using witness transaction id's rather than regular transaction id's. This
// also presents an additional case wherein the wtxid of the coinbase transaction
// is the zeroHash.

func CalcContractStaticMerkleRoot(contract Contract) []byte {
	buf, err := contract.Encode()
	if err != nil {
		Log.Panicf("contract %s encode failed. %v", contract.Content(), err)
	}
	hash := chainhash.DoubleHashH(buf)
	return hash.CloneBytes()
}

func CalcContractRuntimeBaseMerkleRoot(r *ContractRuntimeBase) []byte {
	var buf []byte

	buf2 := fmt.Sprintf("%d %d %d %d ", r.DeployTime, r.Status, r.EnableBlock, r.EnableBlockL1)
	buf = append(buf, buf2...)
	buf = append(buf, []byte(r.EnableTxId)...)
	buf = append(buf, []byte(r.Deployer)...)
	buf = append(buf, []byte(r.ChannelAddr)...)
	buf2 = fmt.Sprintf("%d %d %d %d ", r.ResvId, r.InvokeCount, r.Divisibility, r.N)
	buf = append(buf, buf2...)

	Log.Debugf("ContractRuntimeBase: %d %s", r.InvokeCount, string(buf))

	hash := chainhash.DoubleHashH(buf)
	result := hash.CloneBytes()
	Log.Debugf("hash: %s", hex.EncodeToString(result))
	return result
}

// 只计算在 calcAssetMerkleRoot 之前已经确定的数据，其他在广播TX之后才修改的数据暂时不要管，不然容易导致数据不一致
func CalcSwapContractRunningDataMerkleRoot(r *SwapContractRunningData) []byte {
	var buf []byte

	buf2 := fmt.Sprintf("%s %d %s %d ", r.AssetAmtInPool.String(), r.SatsValueInPool,
		r.TotalInputAssets.String(), r.TotalInputSats)
	buf = append(buf, buf2...)

	// buf2 = fmt.Sprintf("%s %d %d %d %d ", r.TotalDealAssets.String(), r.TotalDealSats, r.TotalDealCount,
	// 	r.TotalDealTx, r.TotalDealTxFee)
	// buf = append(buf, buf2...)

	// buf2 = fmt.Sprintf("%s %d %d %d ", r.TotalRefundAssets.String(), r.TotalRefundSats, r.TotalRefundTx,
	// 	r.TotalRefundTxFee)
	// buf = append(buf, buf2...)

	// buf2 = fmt.Sprintf("%s %d %d %d ", r.TotalProfitAssets.String(), r.TotalProfitSats, r.TotalProfitTx,
	// 	r.TotalProfitTxFee)
	// buf = append(buf, buf2...)

	// buf2 = fmt.Sprintf("%s %d %d %d ", r.TotalDepositAssets.String(), r.TotalDepositSats, r.TotalDepositTx,
	// 	r.TotalDepositTxFee)
	// buf = append(buf, buf2...)

	// buf2 = fmt.Sprintf("%s %d %d %d ", r.TotalWithdrawAssets.String(), r.TotalWithdrawSats, r.TotalWithdrawTx,
	// 	r.TotalWithdrawTxFee)
	// buf = append(buf, buf2...)

	// buf2 = fmt.Sprintf("%s %d ", r.TotalStakeAssets.String(), r.TotalStakeSats)
	// buf = append(buf, buf2...)

	// buf2 = fmt.Sprintf("%s %d %d %d ", r.TotalUnstakeAssets.String(), r.TotalUnstakeSats, r.TotalUnstakeTx,
	// 	r.TotalUnstakeTxFee)
	// buf = append(buf, buf2...)

	buf2 = fmt.Sprintf("%s %s %s %s ", r.TotalLptAmt.String(), r.BaseLptAmt.String(),
		r.TotalAddedLptAmt.String(), r.TotalRemovedLptAmt)
	buf = append(buf, buf2...)

	Log.Debugf("SwapContractRunningData: %s", string(buf))

	hash := chainhash.DoubleHashH(buf)
	result := hash.CloneBytes()
	Log.Debugf("hash: %s", hex.EncodeToString(result))
	return result
}

func CalcLaunchPoolInstallDataMerkleRoot(r *LaunchPoolInstallData) []byte {
	var buf []byte

	buf2 := fmt.Sprintf("%d %d %d %d ", r.HasDeployed, r.HasMinted, r.HasExpanded, r.HasRun)
	buf = append(buf, buf2...)
	buf = append(buf, []byte(r.DeployTickerTxId)...)
	buf = append(buf, []byte(r.MintTxId)...)
	buf = append(buf, []byte(r.AnchorTxId)...)

	Log.Debugf("CalcLaunchPoolInstallDataMerkleRoot: %s", string(buf))

	hash := chainhash.DoubleHashH(buf)
	result := hash.CloneBytes()
	Log.Debugf("hash: %s", hex.EncodeToString(result))
	return result
}