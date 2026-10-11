package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
)

// BIP352 uses the standard account-specific hardened branch. It must never
// reuse the ordinary BIP86 payment key or another subaccount's scan key.
const silentPayPurpose = uint32(352)
const silentPayMaxK = uint32(2323)

var ErrSilentPayUnsupported = errors.New("silent payments require an unlocked mnemonic wallet")

type SilentPayKeys struct {
	Scan  *btcec.PrivateKey
	Spend *btcec.PrivateKey
}

type SilentPayAddress struct {
	Scan  *btcec.PublicKey
	Spend *btcec.PublicKey
}

type SilentPayCandidate struct {
	Vout      uint32
	OutputKey []byte
}

type SilentPayMatch struct {
	Vout      uint32
	K         uint32
	OutputKey []byte
	// Tweak is the checked BIP0352/SharedSecret scalar. Only local wallet
	// storage may retain it; it must never be sent back to the Indexer.
	Tweak [32]byte
}

func silentPayCoinType(params *chaincfg.Params) uint32 {
	if params != nil && params.Name == chaincfg.MainNetParams.Name {
		return 0
	}
	return 1
}

func (p *InternalWallet) DeriveSilentPayKeys(index uint32) (SilentPayKeys, error) {
	if p == nil {
		return SilentPayKeys{}, ErrSilentPayUnsupported
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.masterkey == nil || p.netParamsL1 == nil || index >= hdkeychain.HardenedKeyStart {
		return SilentPayKeys{}, ErrSilentPayUnsupported
	}
	derive := func(branch uint32) (*btcec.PrivateKey, error) {
		node := p.masterkey
		for _, child := range []uint32{
			silentPayPurpose, silentPayCoinType(p.netParamsL1), index, branch,
		} {
			next, err := node.Derive(hdkeychain.HardenedKeyStart + child)
			if err != nil {
				return nil, err
			}
			node = next
		}
		node, err := node.Derive(0)
		if err != nil {
			return nil, err
		}
		return node.ECPrivKey()
	}
	scan, err := derive(1)
	if err != nil {
		return SilentPayKeys{}, err
	}
	spend, err := derive(0)
	if err != nil {
		return SilentPayKeys{}, err
	}
	return SilentPayKeys{Scan: scan, Spend: spend}, nil
}

func (p *InternalWallet) GetSilentPayAddress(index uint32) (string, error) {
	keys, err := p.DeriveSilentPayKeys(index)
	if err != nil {
		return "", err
	}
	defer keys.Scan.Zero()
	defer keys.Spend.Zero()
	return EncodeSilentPayAddress(SilentPayAddress{
		Scan: keys.Scan.PubKey(), Spend: keys.Spend.PubKey(),
	}, p.netParamsL1)
}

// Silent Payment addresses intentionally exceed the BIP173 90-character
// SegWit-address limit. Use the BIP350 checksum with its 1023-char bound.
const silentPayCharset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
const silentPayChecksum = uint32(0x2bc830a3)

func silentPayHRP(params *chaincfg.Params) string {
	if silentPayCoinType(params) == 0 {
		return "sp"
	}
	return "tsp"
}

func silentPayConvertBits(data []byte, from, to uint, pad bool) ([]byte, error) {
	var acc uint32
	var bits uint
	max := uint32((1 << to) - 1)
	var result []byte
	for _, value := range data {
		if uint(value)>>from != 0 {
			return nil, fmt.Errorf("silentpay: invalid encoding")
		}
		acc = (acc << from) | uint32(value)
		bits += from
		for bits >= to {
			bits -= to
			result = append(result, byte((acc>>bits)&max))
		}
	}
	if pad {
		if bits > 0 {
			result = append(result, byte((acc<<(to-bits))&max))
		}
	} else if bits >= from || ((acc<<(to-bits))&max) != 0 {
		return nil, fmt.Errorf("silentpay: noncanonical padding")
	}
	return result, nil
}

func silentPayPolymod(values []byte) uint32 {
	check := uint32(1)
	generators := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	for _, v := range values {
		top := check >> 25
		check = (check&0x1ffffff)<<5 ^ uint32(v)
		for j := uint32(0); j < 5; j++ {
			if top>>j&1 != 0 {
				check ^= generators[j]
			}
		}
	}
	return check
}

func silentPayHRPExpand(hrp string) []byte {
	result := make([]byte, 0, len(hrp)*2+1)
	for i := range hrp {
		result = append(result, hrp[i]>>5)
	}
	result = append(result, 0)
	for i := range hrp {
		result = append(result, hrp[i]&31)
	}
	return result
}

func EncodeSilentPayAddress(addr SilentPayAddress, params *chaincfg.Params) (string, error) {
	if addr.Scan == nil || addr.Spend == nil || params == nil {
		return "", fmt.Errorf("silentpay: missing public key or network")
	}
	payload := append(addr.Scan.SerializeCompressed(), addr.Spend.SerializeCompressed()...)
	converted, err := silentPayConvertBits(payload, 8, 5, true)
	if err != nil {
		return "", err
	}
	data := append([]byte{0}, converted...)
	hrp := silentPayHRP(params)
	input := append(silentPayHRPExpand(hrp), data...)
	input = append(input, make([]byte, 6)...)
	check := silentPayPolymod(input) ^ silentPayChecksum
	var result strings.Builder
	result.WriteString(hrp)
	result.WriteByte('1')
	for _, value := range data {
		result.WriteByte(silentPayCharset[value])
	}
	for i := uint(0); i < 6; i++ {
		result.WriteByte(silentPayCharset[(check>>(5*(5-i)))&31])
	}
	return result.String(), nil
}

func DecodeSilentPayAddress(text string, params *chaincfg.Params) (SilentPayAddress, error) {
	if params == nil || len(text) < 116 || len(text) > 1023 ||
		(text != strings.ToLower(text) && text != strings.ToUpper(text)) {
		return SilentPayAddress{}, fmt.Errorf("silentpay: invalid address")
	}
	// BIP350 accepts lowercase or uppercase addresses, but not mixed-case.
	text = strings.ToLower(text)
	sep := strings.LastIndexByte(text, '1')
	if sep < 1 || sep+7 > len(text) || text[:sep] != silentPayHRP(params) {
		return SilentPayAddress{}, fmt.Errorf("silentpay: wrong network or HRP")
	}
	hrp := text[:sep]
	data := make([]byte, 0, len(text)-sep-1)
	for _, ch := range text[sep+1:] {
		pos := strings.IndexRune(silentPayCharset, ch)
		if pos < 0 {
			return SilentPayAddress{}, fmt.Errorf("silentpay: invalid bech32m character")
		}
		data = append(data, byte(pos))
	}
	if silentPayPolymod(append(silentPayHRPExpand(hrp), data...)) != silentPayChecksum {
		return SilentPayAddress{}, fmt.Errorf("silentpay: invalid checksum")
	}
	if len(data) < 7 || data[0] != 0 {
		return SilentPayAddress{}, fmt.Errorf("silentpay: unsupported address version")
	}
	payload, err := silentPayConvertBits(data[1:len(data)-6], 5, 8, false)
	if err != nil || len(payload) != 66 {
		return SilentPayAddress{}, fmt.Errorf("silentpay: invalid v0 payload")
	}
	scan, err := btcec.ParsePubKey(payload[:33])
	if err != nil {
		return SilentPayAddress{}, err
	}
	spend, err := btcec.ParsePubKey(payload[33:])
	if err != nil {
		return SilentPayAddress{}, err
	}
	return SilentPayAddress{Scan: scan, Spend: spend}, nil
}

func silentPayTaggedHash(tag string, data ...[]byte) [32]byte {
	tagHash := sha256.Sum256([]byte(tag))
	hash := sha256.New()
	_, _ = hash.Write(tagHash[:])
	_, _ = hash.Write(tagHash[:])
	for _, part := range data {
		_, _ = hash.Write(part)
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func silentPayCheckedScalar(raw [32]byte) (btcec.ModNScalar, error) {
	var scalar btcec.ModNScalar
	if scalar.SetByteSlice(raw[:]) || scalar.IsZero() {
		return scalar, fmt.Errorf("silentpay: invalid curve scalar")
	}
	return scalar, nil
}

func silentPaySharedSecretPoint(priv *btcec.ModNScalar, pub *btcec.PublicKey) (*btcec.PublicKey, error) {
	if priv == nil || pub == nil {
		return nil, fmt.Errorf("silentpay: missing ECDH key")
	}
	var point, result btcec.JacobianPoint
	pub.AsJacobian(&point)
	btcec.ScalarMultNonConst(priv, &point, &result)
	if result.Z.IsZero() {
		return nil, fmt.Errorf("silentpay: invalid ECDH point")
	}
	result.ToAffine()
	return btcec.NewPublicKey(&result.X, &result.Y), nil
}

func silentPayOutputScalar(secret *btcec.PublicKey, k uint32) (btcec.ModNScalar, error) {
	var number [4]byte
	binary.BigEndian.PutUint32(number[:], k)
	return silentPayCheckedScalar(silentPayTaggedHash(
		"BIP0352/SharedSecret", secret.SerializeCompressed(), number[:],
	))
}

func silentPayTweakPoint(spend *btcec.PublicKey, tweak *btcec.ModNScalar) (*btcec.PublicKey, error) {
	if spend == nil || tweak == nil {
		return nil, fmt.Errorf("silentpay: missing spend public key")
	}
	var base, diff, result btcec.JacobianPoint
	spend.AsJacobian(&base)
	btcec.ScalarBaseMultNonConst(tweak, &diff)
	btcec.AddNonConst(&base, &diff, &result)
	if result.Z.IsZero() {
		return nil, fmt.Errorf("silentpay: invalid output point")
	}
	result.ToAffine()
	return btcec.NewPublicKey(&result.X, &result.Y), nil
}

// MatchSilentPayOutputs uses only public Indexer data and the local scan key.
// T is already input_hash*A, computed by the Indexer from original prevouts.
// The output index k is independent of the actual Bitcoin vout. Stop at the
// first missing k, as specified in BIP352; retain k=0 even when already spent.
func MatchSilentPayOutputs(keys SilentPayKeys, compressedT []byte, outputs []SilentPayCandidate) ([]SilentPayMatch, error) {
	if keys.Scan == nil || keys.Spend == nil || len(compressedT) != 33 {
		return nil, fmt.Errorf("silentpay: missing local key or public T")
	}
	t, err := btcec.ParsePubKey(compressedT)
	if err != nil {
		return nil, err
	}
	scanScalar := keys.Scan.Key
	secret, err := silentPaySharedSecretPoint(&scanScalar, t)
	if err != nil {
		return nil, err
	}
	var matches []SilentPayMatch
	for k := uint32(0); k < silentPayMaxK; k++ {
		scalar, err := silentPayOutputScalar(secret, k)
		if err != nil {
			return nil, err
		}
		pub, err := silentPayTweakPoint(keys.Spend.PubKey(), &scalar)
		if err != nil {
			return nil, err
		}
		xonly := schnorr.SerializePubKey(pub)
		found := false
		for _, output := range outputs {
			if len(output.OutputKey) != 32 {
				return nil, fmt.Errorf("silentpay: invalid Taproot output key")
			}
			if bytes.Equal(xonly, output.OutputKey) {
				var raw [32]byte
				scalar.PutBytes(&raw)
				matches = append(matches, SilentPayMatch{
					Vout: output.Vout, K: k, OutputKey: append([]byte(nil), output.OutputKey...), Tweak: raw,
				})
				found = true
			}
		}
		if !found {
			break
		}
	}
	return matches, nil
}

// SilentPayOutputSpendKey is derived only for a locally matched confirmed
// output. This is a direct BIP340 x-only key, NOT an ordinary BIP86 internal
// key: never run it through TapTweak again when producing a signature.
func SilentPayOutputSpendKey(keys SilentPayKeys, match SilentPayMatch) (*btcec.PrivateKey, error) {
	if keys.Spend == nil {
		return nil, ErrSilentPayUnsupported
	}
	tweak, err := silentPayCheckedScalar(match.Tweak)
	if err != nil {
		return nil, err
	}
	scalar := keys.Spend.Key
	scalar.Add(&tweak)
	if scalar.IsZero() {
		return nil, fmt.Errorf("silentpay: invalid spend key")
	}
	priv := btcec.PrivKeyFromScalar(&scalar)
	if len(match.OutputKey) != 32 || !bytes.Equal(
		schnorr.SerializePubKey(priv.PubKey()), match.OutputKey,
	) {
		return nil, fmt.Errorf("silentpay: output does not match spend key")
	}
	return priv, nil
}
