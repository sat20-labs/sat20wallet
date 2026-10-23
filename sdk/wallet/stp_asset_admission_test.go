package wallet

import (
	"errors"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
)

func TestSTPChannelAdmissionBeforeIO(t *testing.T) {
	rune := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "TOKEN"}}
	rgb := AssetName{AssetName: indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("a", 64)}, N: 2}
	steps := []struct {
		name string
		run  func(*Manager, *Channel, *AssetName) error
	}{
		{"client splice-in", func(manager *Manager, channel *Channel, incoming *AssetName) error {
			_, _, err := manager.FunderInitSplicingInProcess(channel, &incoming.AssetName, indexer.NewDefaultDecimal(1), 1, nil, nil, nil, nil, "")
			return err
		}},
		{"client expansion", func(manager *Manager, channel *Channel, incoming *AssetName) error {
			_, _, _, err := manager.FunderInitExpandingProcess(channel, &incoming.AssetName, "unused:0", "", nil)
			return err
		}},
		{"peer expansion", func(manager *Manager, channel *Channel, incoming *AssetName) error {
			resv := &SplicingReservation{}
			resv.Channel = channel
			resv.AssetName = incoming
			return manager.AllowExpand(resv, "unused:0")
		}},
		{"prepare funding", func(manager *Manager, channel *Channel, incoming *AssetName) error {
			_, err := manager.PrepareSTPChannelFunding(channel, incoming, nil)
			return err
		}},
		{"accept peer funding", func(manager *Manager, channel *Channel, incoming *AssetName) error {
			return manager.AcceptSTPChannelFunding(channel, incoming, nil, nil, [32]byte{})
		}},
	}
	for _, pair := range [][2]AssetName{{rune, rgb}, {rgb, rune}} {
		t.Run(pair[0].Protocol+"-then-"+pair[1].Protocol, func(t *testing.T) {
			for _, step := range steps {
				t.Run(step.name, func(t *testing.T) {
					channel := &Channel{ChannelInDB: ChannelInDB{
						FundingUtxos: map[AssetName][]*TxOutput{pair[0]: {indexer.NewTxOutput(1000)}},
					}}
					incoming := pair[1]
					// A nil manager proves rejection happens before any database,
					// network, wallet-signing or reservation operation is possible.
					var manager *Manager
					// Isolate each entry point so a missing admission check cannot
					// prevent the remaining entry points from being exercised.
					defer func() {
						if failure := recover(); failure != nil {
							t.Fatalf("expected admission conflict before I/O, panicked: %v", failure)
						}
					}()
					if err := step.run(manager, channel, &incoming); !errors.Is(err, ErrSTPAssetConflict) {
						t.Fatalf("expected admission conflict before I/O, got %v", err)
					}
					if len(channel.AssetFunding) != 0 || channel.AssetEntropy != ([32]byte{}) || channel.ResvId != 0 {
						t.Fatal("rejected admission changed channel state")
					}
				})
			}
		})
	}
}

func TestSTPChannelAdmissionRejectsMissingContext(t *testing.T) {
	asset := &AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_RUNES, Type: indexer.ASSET_TYPE_FT, Ticker: "TOKEN"}}
	var manager *Manager
	for _, test := range []struct {
		name string
		run  func() error
		want string
	}{
		{"client splice-in missing channel", func() error {
			_, _, err := manager.FunderInitSplicingInProcess(nil, &asset.AssetName, indexer.NewDefaultDecimal(1), 1, nil, nil, nil, nil, "")
			return err
		}, "missing STP channel"},
		{"client splice-in missing asset", func() error {
			_, _, err := manager.FunderInitSplicingInProcess(&Channel{}, nil, indexer.NewDefaultDecimal(1), 1, nil, nil, nil, nil, "")
			return err
		}, "nil STP asset"},
		{"client expansion missing channel", func() error {
			_, _, _, err := manager.FunderInitExpandingProcess(nil, &asset.AssetName, "unused:0", "", nil)
			return err
		}, "missing STP channel"},
		{"client expansion missing asset", func() error {
			_, _, _, err := manager.FunderInitExpandingProcess(&Channel{}, nil, "unused:0", "", nil)
			return err
		}, "nil STP asset"},
		{"peer expansion missing reservation", func() error {
			return manager.AllowExpand(nil, "unused:0")
		}, "missing channel expansion context"},
		{"peer expansion missing channel", func() error {
			resv := &SplicingReservation{}
			resv.AssetName = asset
			return manager.AllowExpand(resv, "unused:0")
		}, "missing STP channel"},
		{"peer expansion missing asset", func() error {
			resv := &SplicingReservation{}
			resv.Channel = &Channel{}
			return manager.AllowExpand(resv, "unused:0")
		}, "missing channel expansion context"},
		{"prepare funding missing channel", func() error {
			_, err := manager.PrepareSTPChannelFunding(nil, asset, nil)
			return err
		}, "missing channel funding context"},
		{"prepare funding missing asset", func() error {
			_, err := manager.PrepareSTPChannelFunding(&Channel{}, nil, nil)
			return err
		}, "missing channel funding context"},
		{"accept peer funding missing channel", func() error {
			return manager.AcceptSTPChannelFunding(nil, asset, nil, nil, [32]byte{})
		}, "missing channel funding context"},
		{"accept peer funding missing asset", func() error {
			return manager.AcceptSTPChannelFunding(&Channel{}, nil, nil, nil, [32]byte{})
		}, "missing channel funding context"},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if failure := recover(); failure != nil {
					t.Fatalf("missing context must be rejected before I/O, panicked: %v", failure)
				}
			}()
			if err := test.run(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected context error containing %q, got %v", test.want, err)
			}
		})
	}
}
