from pathlib import Path


def replace(path, before, after):
    target = Path(path)
    text = target.read_text()
    if text.count(before) != 1:
        raise SystemExit(f'Unexpected source for {path}: {text.count(before)} matches')
    target.write_text(text.replace(before, after))


replace('sdk/wallet/channel_splicing_client.go',
        'if err := ValidateSTPAsset(assetName, STPOpSpliceIn); err != nil {',
        'if err := ValidateSTPAssetForChannel(channel, assetName, STPOpSpliceIn); err != nil {')
replace('sdk/wallet/channel_expanding_client.go',
        '''func (p *Manager) FunderInitExpandingProcess(channel *Channel, assetName *swire.AssetName, utxo string,
	reason string, memo []byte) (string, *Decimal, int64, error) {
	if err := ValidateSTPAsset(assetName, STPOpExpand); err != nil {''',
        '''func (p *Manager) FunderInitExpandingProcess(channel *Channel, assetName *swire.AssetName, utxo string,
	reason string, memo []byte) (string, *Decimal, int64, error) {
	if err := ValidateSTPAssetForChannel(channel, assetName, STPOpExpand); err != nil {''')
replace('sdk/wallet/channel_expanding_client.go',
        '''func (p *Manager) AllowExpand(resv *SplicingReservation, utxo string) error {
	channel := resv.Channel
	assetName := resv.AssetName
''',
        '''func (p *Manager) AllowExpand(resv *SplicingReservation, utxo string) error {
	if resv == nil || resv.AssetName == nil {
		return fmt.Errorf("missing channel expansion context")
	}
	channel := resv.Channel
	assetName := resv.AssetName
	if err := ValidateSTPAssetForChannel(channel, &assetName.AssetName, STPOpExpand); err != nil {
		return err
	}
''')
replace('sdk/wallet/channel_asset_proof.go',
        '''func (p *Manager) PrepareSTPChannelFunding(channel *Channel, asset *AssetName, inputs []*TxOutput) (map[string][]byte, error) {
	if channel == nil || asset == nil {
		return nil, fmt.Errorf("missing channel funding context")
	}
''',
        '''func (p *Manager) PrepareSTPChannelFunding(channel *Channel, asset *AssetName, inputs []*TxOutput) (map[string][]byte, error) {
	if channel == nil || asset == nil {
		return nil, fmt.Errorf("missing channel funding context")
	}
	if err := ValidateSTPAssetForChannel(channel, &asset.AssetName, STPOpSpliceIn); err != nil {
		return nil, err
	}
''')
replace('sdk/wallet/channel_asset_proof.go',
        '''func (p *Manager) AcceptSTPChannelFunding(channel *Channel, asset *AssetName, inputs []*TxOutput, proofs map[string][]byte, entropy [32]byte) error {
	if channel == nil || asset == nil {
		return fmt.Errorf("missing channel funding context")
	}
''',
        '''func (p *Manager) AcceptSTPChannelFunding(channel *Channel, asset *AssetName, inputs []*TxOutput, proofs map[string][]byte, entropy [32]byte) error {
	if channel == nil || asset == nil {
		return fmt.Errorf("missing channel funding context")
	}
	if err := ValidateSTPAssetForChannel(channel, &asset.AssetName, STPOpSpliceIn); err != nil {
		return err
	}
''')
path = Path('sdk/wallet/stp_asset_admission_test.go')
if path.exists():
    raise SystemExit('Refusing to overwrite existing admission tests')
path.write_text('''package wallet

import (
	"errors"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestSTPChannelAdmissionBeforeIO(t *testing.T) {
	rune := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "TOKEN"}}
	rgb := AssetName{AssetName: indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("a", 64)}, N: 2}
	for _, pair := range [][2]AssetName{{rune, rgb}, {rgb, rune}} {
		t.Run(pair[0].Protocol+"-then-"+pair[1].Protocol, func(t *testing.T) {
			channel := &Channel{ChannelInDB: ChannelInDB{
				FundingUtxos: map[AssetName][]*TxOutput{pair[0]: {indexer.NewTxOutput(1000)}},
			}}
			incoming := pair[1]
			// A nil manager proves rejection happens before any database,
			// network, wallet-signing or reservation operation is possible.
			var manager *Manager
			check := func(step string, err error) {
				t.Helper()
				if !errors.Is(err, ErrSTPAssetConflict) {
					t.Fatalf("%s: expected admission conflict before I/O, got %v", step, err)
				}
			}
			_, _, err := manager.FunderInitSplicingInProcess(channel, &incoming.AssetName, indexer.NewDefaultDecimal(1), 1, nil, nil, nil, nil, "")
			check("client splice-in", err)
			_, _, _, err = manager.FunderInitExpandingProcess(channel, &incoming.AssetName, "unused:0", "", nil)
			check("client expansion", err)
			resv := &SplicingReservation{Channel: channel}
			resv.AssetName = &incoming
			check("peer expansion", manager.AllowExpand(resv, "unused:0"))
			_, err = manager.PrepareSTPChannelFunding(channel, &incoming, nil)
			check("prepare funding", err)
			check("accept peer funding", manager.AcceptSTPChannelFunding(channel, &incoming, nil, nil, [32]byte{}))
			if len(channel.AssetFunding) != 0 || channel.AssetEntropy != ([32]byte{}) || channel.ResvId != 0 {
				t.Fatal("rejected admission changed channel state")
			}
		})
	}
}
''')
