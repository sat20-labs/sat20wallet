// This generator MUST be run inside an otherwise unmodified checkout of wallet
// 1a9aa9d67d741ba66556982fa53b1b81f4d41559. It imports the actual old wallet
// package. It never declares shadow database structs or encodes the new types.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	schainhash "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const oldWalletCommit = "1a9aa9d67d741ba66556982fa53b1b81f4d41559"

type fixtureEntry struct {
	File            string
	SHA256          string
	SemanticFile    string
	SemanticSHA256  string
	Type            string
	ChannelID       string
	ReservationType string
	ReservationID   int64
}
type fixtureManifest struct {
	Format          int
	WalletCommit    string
	GoVersion       string
	GeneratorSHA256 string
	SourceSHA256    map[string]string
	TypeFields      map[string]map[string]string
	Fixtures        []fixtureEntry
}

func main() {
	sourceRoot := flag.String("source-root", "", "absolute path of the genuine old wallet checkout")
	output := flag.String("out", "", "fixture output directory")
	flag.Parse()
	if *sourceRoot == "" || *output == "" {
		panic("-source-root and -out are required")
	}
	sourceAbs, err := filepath.Abs(*sourceRoot)
	must(err)
	cwd, err := os.Getwd()
	must(err)
	if filepath.Clean(cwd) != filepath.Join(sourceAbs, "sdk") {
		panic("run from the old checkout's sdk directory")
	}
	commit, err := exec.Command("git", "-C", sourceAbs, "rev-parse", "HEAD").Output()
	must(err)
	if strings.TrimSpace(string(commit)) != oldWalletCommit {
		panic("not the pinned genuine old wallet commit")
	}
	status, err := exec.Command("git", "-C", sourceAbs, "status", "--porcelain", "--untracked-files=no").Output()
	must(err)
	if len(status) != 0 {
		panic("old tracked source must be clean")
	}
	manifest := fixtureManifest{Format: 1, WalletCommit: oldWalletCommit, GoVersion: runtime.Version(), SourceSHA256: map[string]string{}, TypeFields: map[string]map[string]string{}}
	for _, name := range []string{"channel_db.go", "channel_commitment.go", "channel_config.go", "channel_reservation.go", "channel_merkle.go", "db.go", "types.go"} {
		b, err := os.ReadFile(filepath.Join(sourceAbs, "sdk", "wallet", name))
		must(err)
		manifest.SourceSHA256["sdk/wallet/"+name] = digest(b)
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		panic("generator source unavailable")
	}
	generator, err := os.ReadFile(thisFile)
	must(err)
	manifest.GeneratorSHA256 = digest(generator)
	for _, value := range []any{wallet.ChannelInDB{}, wallet.ChannelCommitmentV1{}, wallet.ChannelCommitment{}, wallet.CommitmentTx{}, wallet.ClosingDataInDB{}, wallet.SplicingDataInDB{}} {
		typ := reflect.TypeOf(value)
		fields := map[string]string{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if legacyFixtureNativeField(typ.Name(), field.Name) {
				panic("native field exists in supposed old source: " + typ.Name() + "." + field.Name)
			}
			fields[field.Name] = field.Type.String()
		}
		manifest.TypeFields[typ.Name()] = fields
	}
	must(os.MkdirAll(*output, 0755))
	plain := oldChannel(false)
	ordx := oldChannel(true)
	closing := &wallet.ClosingDataInDB{
		ReservationBase: wallet.ReservationBase{Id: 601, IsInitiator: true, Status: wallet.ResvStatus(wallet.CS_CLOSING_BROADCASTED)},
		ChannelId:       plain.ChannelId, CloseHeight: 144, FeeRate: 3, SplicingOutTxId: "old-splice-out-id", SplicingResvId: 501,
		PreTxs: []*wire.MsgTx{oldBitcoinTx(61)}, CloseTx: oldBitcoinTx(62), DeAnchorTx: oldSatsNetTx(63),
	}
	splicing := &wallet.SplicingDataInDB{
		ReservationBase:    wallet.ReservationBase{Id: 701, IsInitiator: true, Status: wallet.RS_SPLICINGIN_BROADCASTED},
		NeedSendSplicingTx: true, ChannelId: ordx.ChannelId, OldChanPoint: ordx.ChanPoint.OutPointStr,
		AssetName: oldOrdxAsset(), Amt: indexer.NewDefaultDecimal(123), SplicingAmt: indexer.NewDefaultDecimal(120),
		SplicingValue: 1200, ServiceFee: 8, RequiredFee: 7, Memo: []byte("old-pending-splice"), SplicingChange: 31, FeeChange: 29,
		StubIsSet: true, DestAddr: "old-destination", SplicingTx: oldBitcoinTx(71), AnchorTx: oldSatsNetTx(72), PreTxs: []*wire.MsgTx{oldBitcoinTx(73)},
		RecoverAscended: true, RecoveredAnchorTxId: "old-recovered-anchor", RecoveredAnchorOutpoint: "old-recovered-anchor:0",
	}
	records := []struct {
		entry fixtureEntry
		value any
	}{
		{fixtureEntry{File: "channel_plain.gob", Type: "ChannelInDB", ChannelID: plain.ChannelId}, plain},
		{fixtureEntry{File: "channel_ordx.gob", Type: "ChannelInDB", ChannelID: ordx.ChannelId}, ordx},
		{fixtureEntry{File: "closing.gob", Type: "ClosingDataInDB", ReservationType: wallet.RESV_TYPE_CLOSE, ReservationID: 601}, closing},
		{fixtureEntry{File: "splicing.gob", Type: "SplicingDataInDB", ReservationType: wallet.RESV_TYPE_SPLICING, ReservationID: 701}, splicing},
	}
	for _, record := range records {
		raw, err := wallet.EncodeToBytes(record.value)
		must(err)
		// Keep the genuine old gob encoder unchanged. Blob hashes identify this
		// exact provenance artifact, not a cross-process compatibility condition.
		// Fixed logical data and semantic JSON are the deterministic oracle.
		semantic, err := json.MarshalIndent(legacyFixtureCanonical(reflect.ValueOf(record.value)), "", "  ")
		must(err)
		semantic = append(semantic, '\n')
		entry := record.entry
		entry.SHA256 = digest(raw)
		entry.SemanticFile = strings.TrimSuffix(entry.File, ".gob") + ".semantic.json"
		entry.SemanticSHA256 = digest(semantic)
		must(os.WriteFile(filepath.Join(*output, entry.File), raw, 0644))
		must(os.WriteFile(filepath.Join(*output, entry.SemanticFile), semantic, 0644))
		manifest.Fixtures = append(manifest.Fixtures, entry)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	must(err)
	must(os.WriteFile(filepath.Join(*output, "manifest.json"), append(encoded, '\n'), 0644))
	fmt.Printf("generated %d genuine old-type gob fixtures from %s\n", len(manifest.Fixtures), manifest.WalletCommit)
}
func digest(b []byte) string { hash := sha256.Sum256(b); return hex.EncodeToString(hash[:]) }
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func oldOrdxAsset() *wallet.AssetName {
	return &wallet.AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "legacy-fixture"}, N: 10}
}
func oldBitcoinTx(tag byte) *wire.MsgTx {
	var previous chainhash.Hash
	previous[0] = tag
	tx := wire.NewMsgTx(2)
	tx.LockTime = uint32(tag)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&previous, 0), nil, wire.TxWitness{[]byte{tag, 1}, []byte{tag, 2}}))
	tx.TxIn[0].Sequence = 144
	tx.AddTxOut(wire.NewTxOut(1000+int64(tag), []byte{0x51, tag}))
	return tx
}
func oldSatsNetTx(tag byte) *swire.MsgTx {
	var previous schainhash.Hash
	previous[0] = tag
	tx := swire.NewMsgTx(2)
	tx.LockTime = uint32(tag)
	tx.AddTxIn(&swire.TxIn{PreviousOutPoint: *swire.NewOutPoint(&previous, 0), Sequence: 144, Witness: swire.TxWitness{[]byte{tag, 3}}})
	tx.AddTxOut(swire.NewTxOut(2000+int64(tag), nil, []byte{0x51, tag}))
	return tx
}
func oldCommitment(asset wallet.AssetName, local, remote int64, tag byte) *wallet.ChannelCommitment {
	commit := wallet.NewChannelCommitment()
	commit.LocalBalance[asset] = indexer.NewDefaultDecimal(local)
	commit.RemoteBalance[asset] = indexer.NewDefaultDecimal(remote)
	commit.CommitTx = oldBitcoinTx(tag)
	commit.Revocation = []byte{tag, 4}
	commit.CommitSig = [][]byte{{tag, 5}, {tag, 6}}
	commit.DeAnchorTx = oldSatsNetTx(tag + 1)
	commit.DeAnchorSig = [][]byte{{tag, 7}}
	commit.PrevTxs = []*wire.MsgTx{oldBitcoinTx(tag + 2)}
	commit.NextTxs = []*wire.MsgTx{oldBitcoinTx(tag + 3)}
	// Others is a reserved alternative commitment group in the old design.
	// The real old constructor leaves it empty; do not invent a sweep lifecycle.
	return commit
}
func oldChannel(ordx bool) *wallet.ChannelInDB {
	c := wallet.NewChannelInDB()
	c.ChannelId = "legacy-plain-channel"
	asset := wallet.PLAIN_ASSET
	if ordx {
		c.ChannelId = "legacy-ordx-channel"
		asset = *oldOrdxAsset()
	}
	keyBytes, err := hex.DecodeString("0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
	must(err)
	remoteKey, err := btcec.ParsePubKey(keyBytes)
	must(err)
	localKey := wallet.GetBootstrapPubKey()
	c.Address = c.ChannelId
	c.Status = wallet.CS_READY
	c.ShortChannelID = 144
	c.ChannelType = wallet.CT_NORMAL
	c.Contract = []byte("old-contract-bytes")
	c.RedeemScript = []byte{0x51, 0x21, 0x52}
	c.IsInitiator = true
	c.FundingTime = 1600000000000000
	c.PeerNodeId = remoteKey.SerializeCompressed()
	c.Capacity = 60000
	c.CsvDelay = 144
	c.LocalWalletId = 71
	c.Memo = "genuine-pre-native-fixture"
	c.LocalChanCfg = wallet.ChannelConfigV2{InitialBalance: 40000, WalletId: 2, PaymentKey: localKey, RevocationBasePoint: localKey}
	c.RemoteChanCfg = wallet.ChannelConfigV2{InitialBalance: 20000, WalletId: 3, PaymentKey: remoteKey, RevocationBasePoint: remoteKey}
	c.ChanPoint = &wallet.TxOutput{UtxoId: 100, OutPointStr: strings.Repeat("1", 64) + ":0", OutValue: *wire.NewTxOut(10000, c.GetChannelPkScript())}
	funding := &wallet.TxOutput{UtxoId: 101, OutPointStr: strings.Repeat("2", 64) + ":1", OutValue: *wire.NewTxOut(50000, c.GetChannelPkScript())}
	if ordx {
		funding.Assets = indexer.TxAssets{{Name: asset.AssetName, Amount: *indexer.NewDefaultDecimal(60000), BindingSat: 10}}
	}
	c.FundingUtxos[asset] = []*wallet.TxOutput{funding}
	c.LocalCommitment = oldCommitment(asset, 40000, 20000, 11)
	c.RemoteCommitment = oldCommitment(asset, 20000, 40000, 31)
	c.LastPaymentTxId = "old-payment-tx"
	c.TotalSatSent = 101
	c.TotalSatReceived = 202
	c.CommitHeight = 9
	c.UpdateTime = 1600000000000099
	c.StaticMerkleRoot = c.CalcStaticMerkleRoot()
	must(c.PrepareForSave())
	if !c.CheckData() {
		panic("old channel fixture failed old integrity checks")
	}
	return c
}

// The semantic oracle preserves all exported legacy fields, including struct-key
// maps, decimals, public keys and exact transaction/signature bytes. Empty and
// nil containers are equivalent; gob and existing cleanup normalize those.
func legacyFixtureCanonical(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil
	}
	if v.CanInterface() {
		if key, ok := v.Interface().(interface{ SerializeCompressed() []byte }); ok {
			return map[string]any{"PublicKey": hex.EncodeToString(key.SerializeCompressed())}
		}
		if v.Type() == reflect.TypeOf(indexer.Decimal{}) {
			d := v.Interface().(indexer.Decimal)
			return map[string]any{"Precision": d.Precision, "Amount": d.String()}
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return legacyFixtureCanonical(v.Elem())
	case reflect.Struct:
		result := make(map[string]any)
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if field.PkgPath != "" || legacyFixtureNativeField(v.Type().Name(), field.Name) {
				continue
			}
			result[field.Name] = legacyFixtureCanonical(v.Field(i))
		}
		return result
	case reflect.Map:
		if v.Len() == 0 {
			return nil
		}
		type entry struct {
			Key   any
			Value any
		}
		entries := make([]entry, 0, v.Len())
		for _, key := range v.MapKeys() {
			entries = append(entries, entry{legacyFixtureCanonical(key), legacyFixtureCanonical(v.MapIndex(key))})
		}
		sort.Slice(entries, func(i, j int) bool {
			a, _ := json.Marshal(entries[i].Key)
			b, _ := json.Marshal(entries[j].Key)
			return string(a) < string(b)
		})
		return entries
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, v.Len())
			for i := range b {
				b[i] = byte(v.Index(i).Uint())
			}
			return hex.EncodeToString(b)
		}
		result := make([]any, v.Len())
		for i := range result {
			result[i] = legacyFixtureCanonical(v.Index(i))
		}
		return result
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	default:
		panic("unsupported fixture semantic kind: " + v.Kind().String())
	}
}

func legacyFixtureNativeField(typ, field string) bool {
	switch typ {
	case "ChannelInDB":
		return field == "AssetFunding" || field == "AssetEntropy" || field == "ClosingAssetProofs" || field == "NativeParents" || field == "NativePrevouts"
	case "ChannelCommitmentV1":
		return field == "AssetProofs" || field == "RecoveryProofs" || field == "RecoveryFeeIndexes"
	case "CommitmentTx":
		return field == "AssetProofs" || field == "RecoveryProofs"
	case "ClosingDataInDB":
		return field == "AssetProofs"
	case "SplicingDataInDB":
		return field == "AssetFunding" || field == "AssetProofs" || field == "AssetEntropy" || field == "NativeAnchorProof"
	}
	return false
}
