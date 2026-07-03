package e2e

import (
	"encoding/binary"
	"fmt"

	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"golang.org/x/crypto/sha3"
)

type evmABIValue struct {
	static  []byte
	dynamic []byte
}

func evmABIEncode(values ...evmABIValue) []byte {
	headSize := len(values) * 32
	head := make([]byte, 0, headSize)
	tail := make([]byte, 0)
	for _, value := range values {
		if value.dynamic == nil {
			head = append(head, value.static...)
			continue
		}
		head = append(head, evmABIUint(uint64(headSize+len(tail))).static...)
		tail = append(tail, value.dynamic...)
	}
	return append(head, tail...)
}

func evmABIUint(v uint64) evmABIValue {
	out := make([]byte, 32)
	binary.BigEndian.PutUint64(out[24:], v)
	return evmABIValue{static: out}
}

func evmABIInt64(v int64) evmABIValue {
	out := make([]byte, 32)
	if v < 0 {
		for i := range out {
			out[i] = 0xff
		}
	}
	binary.BigEndian.PutUint64(out[24:], uint64(v))
	return evmABIValue{static: out}
}

func evmABIAddress(addr [20]byte) evmABIValue {
	out := make([]byte, 32)
	copy(out[12:], addr[:])
	return evmABIValue{static: out}
}

func evmABIString(v string) evmABIValue {
	return evmABIDynamic([]byte(v))
}

func evmABIBytes(v []byte) evmABIValue {
	return evmABIDynamic(v)
}

func evmABIDynamic(v []byte) evmABIValue {
	out := evmABIUint(uint64(len(v))).static
	out = append(out, v...)
	out = append(out, make([]byte, evmABIPaddedLen(len(v))-len(v))...)
	return evmABIValue{dynamic: out}
}

func evmABIPaddedLen(size int) int {
	if size%32 == 0 {
		return size
	}
	return size + (32 - size%32)
}

func appendSolidityConstructorArgs(bytecode []byte, args ...evmABIValue) []byte {
	out := append([]byte(nil), bytecode...)
	out = append(out, evmABIEncode(args...)...)
	return out
}

func solidityCall(signature string, args ...evmABIValue) []byte {
	out := SolidityFunctionSelector(signature)
	out = append(out, evmABIEncode(args...)...)
	return out
}

func evmEthAddressFromPrivateKey(key *btcec.PrivateKey) ([20]byte, error) {
	var out [20]byte
	if key == nil {
		return out, fmt.Errorf("missing private key")
	}
	pub := key.PubKey().SerializeUncompressed()
	if len(pub) != 65 || pub[0] != 0x04 {
		return out, fmt.Errorf("unexpected uncompressed public key")
	}
	digest := evmKeccak(pub[1:])
	copy(out[:], digest[12:])
	return out, nil
}

func signSolidityDigest(key *btcec.PrivateKey, digest [32]byte) ([]byte, error) {
	if key == nil {
		return nil, fmt.Errorf("missing private key")
	}
	compact := ecdsa.SignCompact(key, digest[:], false)
	if len(compact) != 65 {
		return nil, fmt.Errorf("unexpected compact signature length %d", len(compact))
	}
	recoveryID := int(compact[0]) - 27
	if recoveryID < 0 || recoveryID > 1 {
		return nil, fmt.Errorf("unsupported ethereum recovery id %d", recoveryID)
	}
	out := make([]byte, 65)
	copy(out[0:64], compact[1:65])
	out[64] = byte(27 + recoveryID)
	return out, nil
}

func signedVaultDigest(recipient string, key int64, n uint64) [32]byte {
	data := []byte(recipient)
	var packedKey [8]byte
	binary.BigEndian.PutUint64(packedKey[:], uint64(key))
	data = append(data, packedKey[:]...)
	data = append(data, evmABIUint(n).static...)
	return evmKeccak(data)
}

func evmKeccak(data []byte) [32]byte {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(data)
	var digest [32]byte
	hasher.Sum(digest[:0])
	return digest
}

func evmAddressFromContractAddress(contract contractcommon.ContractAddress) ([20]byte, error) {
	var out [20]byte
	decoded := contractcommon.ContractAddressHash(contract)
	copy(out[:], decoded[:])
	return out, nil
}
