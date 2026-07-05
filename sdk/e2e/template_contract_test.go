package e2e

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	"github.com/sat20-labs/satoshinet/txscript"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetTemplateLimitOrder(t *testing.T) {
	f := newTemplateFixture(t, map[string]int64{"ordx:f:e2elimit": 1000})
	const asset = "ordx:f:e2elimit"
	gas := contractcommon.GetGasAssetName()

	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{200000, 200000, 200000}, []int64{1000, 1000, 1000},
		[]*templateActor{f.A, f.A, f.B})
	assetOuts := f.split(t, f.A, f.assetAnchors[asset], asset,
		[]int64{10, 990}, []int64{1000, 1000},
		[]*templateActor{f.A, f.A})

	content, err := contractcommon.EncodeTemplateLimitOrderContent(asset)
	require.NoError(t, err)
	deployTx, contract := buildTemplateDeployFor(t, f.A,
		contractcommon.TemplateLimitOrder, content, f.A.Address, []byte("limit-order"),
		[]wire.OutPoint{gasOuts[0]}, wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	f.Network.sendAndMine(t, deployTx, 2)

	sellTx := buildTemplateInvokeFor(t, f.A, contract, 1, contractcommon.TemplateInvokeAPISwap,
		templateLimitOrderParam(t, asset, contractcommon.OrderTypeSell, "10", "10"),
		[]wire.OutPoint{assetOuts[0], gasOuts[1]}, wire.TxOut{
			Value:  0,
			Assets: txAssets(templateFunding(t, gas, 100000), templateFunding(t, asset, 10)),
		})
	f.Network.sendAndMine(t, sellTx, 3)

	buyTx := buildTemplateInvokeFor(t, f.B, contract, 1, contractcommon.TemplateInvokeAPISwap,
		templateLimitOrderParam(t, asset, contractcommon.OrderTypeBuy, "10", "10"),
		[]wire.OutPoint{gasOuts[2]}, wire.TxOut{
			Value:  100,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	f.Network.sendAndMine(t, buyTx, 4)

	requireAssetSummaryAtLeast(t, f.Network.Bootstrap, f.B.Address, asset, "10")
	f.requireSynced(t)
}

func TestTemplateLimitRefundBuy(t *testing.T) {
	f := newTemplateFixture(t, map[string]int64{"ordx:f:e2erefund": 1000})
	const asset = "ordx:f:e2erefund"
	gas := contractcommon.GetGasAssetName()

	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{200000, 200000, 200000}, []int64{1000, 1000, 1000},
		[]*templateActor{f.A, f.B, f.B})

	content, err := contractcommon.EncodeTemplateLimitOrderContent(asset)
	require.NoError(t, err)
	deployTx, contract := buildTemplateDeployFor(t, f.A,
		contractcommon.TemplateLimitOrder, content, f.A.Address, []byte("limit-refund-buy"),
		[]wire.OutPoint{gasOuts[0]}, wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	deployBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{deployTx}, 2)
	requireSingleResultTx(t, deployBlock)

	buyTx := buildTemplateInvokeFor(t, f.B, contract, 1, contractcommon.TemplateInvokeAPISwap,
		templateLimitOrderParam(t, asset, contractcommon.OrderTypeBuy, "10", "10"),
		[]wire.OutPoint{gasOuts[1]}, wire.TxOut{
			Value:  100,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	f.Network.sendAndMine(t, buyTx, 3)

	state := fetchTemplateLimitOrderView(t, f.Network.Bootstrap, contract.MustEncode())
	require.Equal(t, uint64(1), state.InvokeCount)
	require.Equal(t, 1, state.ActiveBuyCount)
	require.Empty(t, state.SellDepth)
	require.Len(t, state.BuyDepth, 1)
	require.Equal(t, "10", state.BuyDepth[0].Price)
	require.Equal(t, "10", state.BuyDepth[0].Amt)
	require.Equal(t, int64(100), state.BuyDepth[0].Value)

	refundTx := buildTemplateInvokeFor(t, f.B, contract, 2, contractcommon.TemplateInvokeAPIRefund,
		templateRefundParam(t, []int64{0}),
		[]wire.OutPoint{gasOuts[2]}, wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	refundBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{refundTx}, 4)
	refundResult := requireSingleResultTx(t, refundBlock)
	requireTxOutputValueAmount(t, refundResult, f.B.Address, 100)
	requireTxOutputAssetAmount(t, refundResult, f.B.Address, asset, "")

	state = fetchTemplateLimitOrderView(t, f.Network.Bootstrap, contract.MustEncode())
	require.Equal(t, uint64(2), state.InvokeCount)
	require.Equal(t, 0, state.ActiveBuyCount)
	require.Empty(t, state.BuyDepth)
	f.requireSynced(t)
}

func templateDeployNonce(random []byte) uint64 {
	if len(random) >= 8 {
		return binary.BigEndian.Uint64(random[:8])
	}
	var buf [8]byte
	copy(buf[8-len(random):], random)
	return binary.BigEndian.Uint64(buf[:])
}

func TestRealSatoshiNetTemplateAMMMultiLiquidity(t *testing.T) {
	f := newTemplateFixture(t, map[string]int64{"ordx:f:e2eamm": 100000})
	const asset = "ordx:f:e2eamm"
	gas := contractcommon.GetGasAssetName()

	gasOuts := f.split(t, f.A, f.gasAnchor, gas,
		[]int64{200000, 200000, 200000, 200000, 200000},
		[]int64{10000, 1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.B, f.A, f.B, f.B})
	assetOuts := f.split(t, f.A, f.assetAnchors[asset], asset,
		[]int64{1000, 1000, 1000, 97000},
		[]int64{1000, 1000, 1000, 1000},
		[]*templateActor{f.A, f.B, f.B, f.A})

	content, err := contractcommon.EncodeTemplateAMMContent(asset, "1000", 1000, "1000000")
	require.NoError(t, err)
	deployTx, contract := buildTemplateDeployFor(t, f.A,
		contractcommon.TemplateAMM, content, f.A.Address, []byte("amm-multi"),
		[]wire.OutPoint{assetOuts[0], gasOuts[0]}, wire.TxOut{
			Value: 1000,
			Assets: txAssets(
				templateFunding(t, gas, 100000),
				templateFunding(t, asset, 1000),
			),
		})
	f.Network.sendAndMine(t, deployTx, 2)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, contract.MustEncode(), asset, "1000")

	addByB := buildTemplateInvokeFor(t, f.B, contract, 1, contractcommon.TemplateInvokeAPIAddLiquidity,
		templateAddLiquidityParam(t, asset, "1000", 1000),
		[]wire.OutPoint{assetOuts[1], gasOuts[1]}, wire.TxOut{
			Value: 1000,
			Assets: txAssets(
				templateFunding(t, gas, 100000),
				templateFunding(t, asset, 1000),
			),
		})
	f.Network.sendAndMine(t, addByB, 3)
	requireAssetSummaryAmount(t, f.Network.Bootstrap, contract.MustEncode(), asset, "2000")

	partialRemove := buildTemplateInvokeFor(t, f.B, contract, 2, contractcommon.TemplateInvokeAPIRemoveLiquidity,
		templateRemoveLiquidityParam(t, asset, "1"),
		[]wire.OutPoint{gasOuts[3]}, wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	f.Network.sendAndMine(t, partialRemove, 4)
	requirePositiveAssetSummary(t, f.Network.Bootstrap, f.B.Address, asset)

	safeRemove := buildTemplateInvokeFor(t, f.B, contract, 3, contractcommon.TemplateInvokeAPIRemoveLiquidity,
		templateRemoveLiquidityParam(t, asset, "999999999"),
		[]wire.OutPoint{gasOuts[4]}, wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	f.Network.sendAndMine(t, safeRemove, 5)
	f.requireSynced(t)
}

func TestRealSatoshiNetTemplateExchangeGasForSatoshiAndClose(t *testing.T) {
	f := newTemplateFixture(t, nil)
	gas := contractcommon.GetGasAssetName()
	plainSat := contractcommon.SatoshiAssetName

	amounts := []int64{300000, 300000, 300000, 300000, 300000, 300000, 300000, 300000, 300000, 300000, 300000, 300000, 300000}
	values := make([]int64, len(amounts))
	recipients := make([]*templateActor, len(amounts))
	for i := range amounts {
		values[i] = 1000
		recipients[i] = f.B
	}
	recipients[0] = f.A
	recipients[1] = f.A
	recipients[12] = f.A
	gasOuts := f.split(t, f.A, f.gasAnchor, gas, amounts, values, recipients)

	steps := make([]contractcommon.TemplateExchangePriceStep, 0, 10)
	prices := []string{"0.0001", "0.0001111111", "0.0001234568", "0.0001371742", "0.0001524158",
		"0.0001693509", "0.0001881676", "0.0002090751", "0.0002323057", "0.0002581174"}
	for i, price := range prices {
		steps = append(steps, contractcommon.TemplateExchangePriceStep{
			Threshold: fmt.Sprintf("%d", i),
			BPerA:     price,
		})
	}
	content, err := contractcommon.EncodeTemplateExchangeContent(contractcommon.TemplateExchangeContract{
		AssetAName: gas,
		AssetBName: plainSat,
		PriceMode:  contractcommon.ExchangePriceModeHeight,
		Steps:      steps,
	})
	require.NoError(t, err)
	deployTx, contract := buildTemplateDeployFor(t, f.A, contractcommon.TemplateExchange, content, f.A.Address, []byte("exchange-gas-sat"),
		[]wire.OutPoint{gasOuts[0]}, wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	f.Network.sendAndMine(t, deployTx, 2)

	fundTx := buildTemplateDefaultInvokeFor(t, f.A, contract, []wire.OutPoint{gasOuts[1]}, wire.TxOut{
		Value:  0,
		Assets: wire.TxAssets{templateFunding(t, gas, 200000)},
	})
	require.False(t, txHasContractOpReturn(fundTx))
	f.Network.sendAndMine(t, fundTx, 3)

	for i := 0; i < 10; i++ {
		buyTx := buildTemplateDefaultInvokeFor(t, f.B, contract, []wire.OutPoint{gasOuts[i+2]}, wire.TxOut{
			Value:  1,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
		require.False(t, txHasContractOpReturn(buyTx))
		f.Network.sendAndMine(t, buyTx, int32(4+i))
	}
	requirePositiveAssetSummary(t, f.Network.Bootstrap, f.B.Address, gas)

	closeTx := buildTemplateInvokeFor(t, f.A, contract, 1, contractcommon.TemplateInvokeAPIClose, nil,
		[]wire.OutPoint{gasOuts[12]}, wire.TxOut{
			Value:  0,
			Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
		})
	f.Network.sendAndMine(t, closeTx, 15)
	requireAssetSummaryZero(t, f.Network.Bootstrap, contract.MustEncode(), gas)
	f.requireSynced(t)
}

func TestTemplateAutopayPaysAndCloses(t *testing.T) {
	const feeAsset = "ordx:f:e2eautopay"
	f := newTemplateFixture(t, map[string]int64{feeAsset: 1000})
	gas := contractcommon.GetGasAssetName()
	deployerAddr := f.A.Address
	recipientAddr := f.B.Address

	gasBank := f.splitGasBank(t, 3)
	feeOuts := f.split(t, f.A, f.assetAnchors[feeAsset], feeAsset,
		[]int64{30, 15}, []int64{10000, 10000}, []*templateActor{f.A, f.C})

	content, err := contractcommon.EncodeTemplateAutopayContent(contractcommon.TemplateAutopayContract{
		Recipient:    recipientAddr,
		FeeAssetName: feeAsset,
		ScheduleMode: contractcommon.AutopayScheduleFixed,
		BaseAmount:   "10",
		EndHeight:    0,
	})
	require.NoError(t, err)
	deployAssets := txAsset(gas, 290000)
	deployAssets = append(deployAssets, txAsset(feeAsset, 30)...)
	deployTx, contract := buildTemplateDeployFor(t, f.A,
		contractcommon.TemplateAutopay, content, deployerAddr, []byte("autopay"),
		[]wire.OutPoint{gasBank.take(t, f.A), feeOuts[0]},
		wire.TxOut{Value: 10000, Assets: deployAssets})
	deployBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{deployTx}, 0)
	requireSingleResultTx(t, deployBlock)

	heartbeatTx := buildTemplateAssetTransfer(t, f.A, gasBank.take(t, f.A), gas, 290000, 9000, f.A)
	payBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{heartbeatTx}, 0)
	payResult := requireSingleResultTx(t, payBlock)
	requireTxOutputAssetAmount(t, payResult, recipientAddr, feeAsset, "10")

	fundTx := buildTemplateDefaultInvokeFor(t, f.C, contract, []wire.OutPoint{feeOuts[1]},
		wire.TxOut{Value: 10000, Assets: txAsset(feeAsset, 15)})
	f.Network.sendAndMine(t, fundTx, 0)
	state := fetchTemplateAutopayView(t, f.Network.Bootstrap, contract.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
	require.Equal(t, "25", state.FeeBalance)

	closeTx := buildTemplateInvokeFor(t, f.A, contract, 1, contractcommon.TemplateInvokeAPIClose, nil,
		[]wire.OutPoint{gasBank.take(t, f.A)},
		wire.TxOut{Value: 10000, Assets: txAsset(gas, 290000)})
	closeBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{closeTx}, 0)
	closeResult := requireSingleResultTx(t, closeBlock)
	requireTxOutputAssetAmount(t, closeResult, deployerAddr, feeAsset, "25")
	requireTxOutputAssetPositive(t, closeResult, deployerAddr, gas)
	state = fetchTemplateAutopayView(t, f.Network.Bootstrap, contract.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusClosed, state.Status)
}

func TestTemplateLimitOrderAssetMatrix(t *testing.T) {
	f := newTemplateFixtureWithProfiles(t, templateProfiles())
	gas := contractcommon.GetGasAssetName()
	gasBank := f.splitGasBank(t, 24)

	for _, profile := range sortedProfiles(templateProfiles()) {
		t.Run(profile.Name, func(t *testing.T) {
			content, err := contractcommon.EncodeTemplateLimitOrderContent(profile.Asset)
			require.NoError(t, err)
			deployTx, contract := buildTemplateDeployFor(t, f.A,
				contractcommon.TemplateLimitOrder, content, f.A.Address, []byte("limit-"+profile.Name),
				[]wire.OutPoint{gasBank.take(t, f.A)}, wire.TxOut{
					Value:  1,
					Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
				})
			f.Network.sendAndMine(t, deployTx, 2)

			assetOuts := f.splitProfile(t, f.A, f.assetAnchors[profile.Asset], profile,
				[]string{"1000", "500", "9998500"},
				[]int64{
					profileAssetCarrierValue(profile, "1000"),
					profileAssetCarrierValue(profile, "500"),
					profileAssetCarrierValue(profile, "9998500"),
				},
				[]*templateActor{f.B, f.C, f.A})

			bSell := buildTemplateInvokeFor(t, f.B, contract, 1, contractcommon.TemplateInvokeAPISwap,
				templateLimitOrderParam(t, profile.Asset, contractcommon.OrderTypeSell, "1000", "2"),
				[]wire.OutPoint{assetOuts[0], gasBank.take(t, f.B)}, wire.TxOut{
					Value: profileAssetCarrierValue(profile, "1000"),
					Assets: txAssets(
						templateFunding(t, gas, 100000),
						templateFundingProfile(t, profile, "1000"),
					),
				})
			f.Network.sendAndMine(t, bSell, 3)

			dBuy := buildTemplateInvokeFor(t, f.D, contract, 1, contractcommon.TemplateInvokeAPISwap,
				templateLimitOrderParam(t, profile.Asset, contractcommon.OrderTypeBuy, "600", "2"),
				[]wire.OutPoint{gasBank.take(t, f.D)}, wire.TxOut{
					Value:  limitOrderBuyFunding(600, 2),
					Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
				})
			firstBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{dBuy}, 4)
			firstResult := requireSingleResultTx(t, firstBlock)
			requireTxOutputAssetAmount(t, firstResult, f.D.Address, profile.Asset, "600")
			requireTxOutputValueAmount(t, firstResult, f.B.Address, 1200)

			cSell := buildTemplateInvokeFor(t, f.C, contract, 1, contractcommon.TemplateInvokeAPISwap,
				templateLimitOrderParam(t, profile.Asset, contractcommon.OrderTypeSell, "500", "1"),
				[]wire.OutPoint{assetOuts[1], gasBank.take(t, f.C)}, wire.TxOut{
					Value: profileAssetCarrierValue(profile, "500"),
					Assets: txAssets(
						templateFunding(t, gas, 100000),
						templateFundingProfile(t, profile, "500"),
					),
				})
			eBuy := buildTemplateInvokeFor(t, f.E, contract, 1, contractcommon.TemplateInvokeAPISwap,
				templateLimitOrderParam(t, profile.Asset, contractcommon.OrderTypeBuy, "800", "2"),
				[]wire.OutPoint{gasBank.take(t, f.E)}, wire.TxOut{
					Value:  limitOrderBuyFunding(800, 2),
					Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
				})
			secondBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{cSell, eBuy}, 5)
			secondResult := requireSingleResultTx(t, secondBlock)
			requireTxOutputAssetAmount(t, secondResult, f.E.Address, profile.Asset, "800")
			requireTxOutputValueAmount(t, secondResult, f.C.Address, 500)
			requireTxOutputValueAmount(t, secondResult, f.B.Address, 600)
			requireTxOutputValueAmount(t, secondResult, f.E.Address, 500)

			state := fetchTemplateLimitOrderView(t, f.Network.Bootstrap, contract.MustEncode())
			require.Equal(t, uint64(4), state.InvokeCount)
			require.Equal(t, "100", state.AssetAInPool)
			requireAssetSummaryAtLeast(t, f.Network.Bootstrap, f.D.Address, profile.Asset, "600")
			requireAssetSummaryAtLeast(t, f.Network.Bootstrap, f.E.Address, profile.Asset, "800")
		})
	}
	f.requireSynced(t)
}

func TestTemplateAMMAssetMatrix(t *testing.T) {
	f := newTemplateFixtureWithProfiles(t, templateProfiles())
	gas := contractcommon.GetGasAssetName()
	gasBank := f.splitGasBank(t, 40)

	for _, profile := range sortedProfiles(templateProfiles()) {
		t.Run(profile.Name, func(t *testing.T) {
			assetOuts := f.splitProfile(t, f.A, f.assetAnchors[profile.Asset], profile,
				[]string{"1000000", "220000", "500000", "10000", "8270000"},
				[]int64{
					profileAssetCarrierValue(profile, "1000000") + 100000,
					profileAssetCarrierValue(profile, "220000") + 20000,
					profileAssetCarrierValue(profile, "500000") + 50000,
					profileAssetCarrierValue(profile, "10000"),
					profileAssetCarrierValue(profile, "8270000"),
				},
				[]*templateActor{f.A, f.B, f.C, f.E, f.A})

			content, err := contractcommon.EncodeTemplateAMMContent(profile.Asset, "1000000", 100000, "100000000000")
			require.NoError(t, err)
			deployTx, contract := buildTemplateDeployFor(t, f.A,
				contractcommon.TemplateAMM, content, f.A.Address, []byte("amm-"+profile.Name),
				[]wire.OutPoint{assetOuts[0], gasBank.take(t, f.A)}, wire.TxOut{
					Value: profileAssetCarrierValue(profile, "1000000") + 100000,
					Assets: txAssets(
						templateFunding(t, gas, 100000),
						templateFundingProfile(t, profile, "1000000"),
					),
				})
			f.Network.sendAndMine(t, deployTx, 2)

			addExcess := buildTemplateInvokeFor(t, f.B, contract, 1, contractcommon.TemplateInvokeAPIAddLiquidity,
				templateAddLiquidityParam(t, profile.Asset, "220000", 20000),
				[]wire.OutPoint{assetOuts[1], gasBank.take(t, f.B)}, wire.TxOut{
					Value: profileAssetCarrierValue(profile, "220000") + 20000,
					Assets: txAssets(
						templateFunding(t, gas, 100000),
						templateFundingProfile(t, profile, "220000"),
					),
				})
			addExcessBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{addExcess}, 3)
			addExcessResult := requireSingleResultTx(t, addExcessBlock)
			requireTxOutputAssetAmount(t, addExcessResult, f.B.Address, profile.Asset, "20000")

			addNormal := buildTemplateInvokeFor(t, f.C, contract, 1, contractcommon.TemplateInvokeAPIAddLiquidity,
				templateAddLiquidityParam(t, profile.Asset, "500000", 50000),
				[]wire.OutPoint{assetOuts[2], gasBank.take(t, f.C)}, wire.TxOut{
					Value: profileAssetCarrierValue(profile, "500000") + 50000,
					Assets: txAssets(
						templateFunding(t, gas, 100000),
						templateFundingProfile(t, profile, "500000"),
					),
				})
			f.Network.sendAndMine(t, addNormal, 4)

			buyD := buildTemplateDefaultInvokeFor(t, f.D, contract, []wire.OutPoint{gasBank.take(t, f.D)}, wire.TxOut{
				Value:  1000,
				Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
			})
			buyE := buildTemplateDefaultInvokeFor(t, f.E, contract, []wire.OutPoint{gasBank.take(t, f.E)}, wire.TxOut{
				Value:  1000,
				Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
			})
			buyF := buildTemplateDefaultInvokeFor(t, f.F, contract, []wire.OutPoint{gasBank.take(t, f.F)}, wire.TxOut{
				Value:  1000,
				Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
			})
			defaultBuyBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{buyD, buyE, buyF}, 5)
			defaultBuyResult := requireSingleResultTx(t, defaultBuyBlock)
			dOut := requireTxOutputAssetPositive(t, defaultBuyResult, f.D.Address, profile.Asset)
			eOut := requireTxOutputAssetPositive(t, defaultBuyResult, f.E.Address, profile.Asset)
			fOut := requireTxOutputAssetPositive(t, defaultBuyResult, f.F.Address, profile.Asset)
			require.Equal(t, dOut, eOut)
			require.Equal(t, dOut, fOut)
			requireTxOutputValueAmount(t, defaultBuyResult, f.D.Address, 0)
			requireTxOutputValueAmount(t, defaultBuyResult, f.E.Address, 0)
			requireTxOutputValueAmount(t, defaultBuyResult, f.F.Address, 0)

			sellE := buildTemplateInvokeFor(t, f.E, contract, 1, contractcommon.TemplateInvokeAPISwap,
				templateLimitOrderParam(t, profile.Asset, contractcommon.OrderTypeSell, "1", "1"),
				[]wire.OutPoint{assetOuts[3], gasBank.take(t, f.E)}, wire.TxOut{
					Value: profileAssetCarrierValue(profile, "10000"),
					Assets: txAssets(
						templateFunding(t, gas, 100000),
						templateFundingProfile(t, profile, "10000"),
					),
				})
			sellBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{sellE}, 6)
			sellResult := requireSingleResultTx(t, sellBlock)
			requireTxOutputValueAtLeast(t, sellResult, f.E.Address, 1)
			requireTxOutputAssetAmount(t, sellResult, f.E.Address, profile.Asset, "")

			removeC := buildTemplateInvokeFor(t, f.C, contract, 1, contractcommon.TemplateInvokeAPIRemoveLiquidity,
				templateRemoveLiquidityParam(t, profile.Asset, "100000"),
				[]wire.OutPoint{gasBank.take(t, f.C)}, wire.TxOut{
					Value:  0,
					Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
				})
			removeCBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{removeC}, 7)
			removeCResult := requireSingleResultTx(t, removeCBlock)
			requireTxOutputValueAtLeast(t, removeCResult, f.C.Address, 1)
			requireTxOutputAssetPositive(t, removeCResult, f.C.Address, profile.Asset)

			safeRemoveB := buildTemplateInvokeFor(t, f.B, contract, 1, contractcommon.TemplateInvokeAPIRemoveLiquidity,
				templateRemoveLiquidityParam(t, profile.Asset, "999999999999"),
				[]wire.OutPoint{gasBank.take(t, f.B)}, wire.TxOut{
					Value:  0,
					Assets: wire.TxAssets{templateFunding(t, gas, 100000)},
				})
			safeRemoveBBlock := f.Network.sendManyAndMine(t, []*wire.MsgTx{safeRemoveB}, 8)
			safeRemoveBResult := requireSingleResultTx(t, safeRemoveBBlock)
			requireTxOutputValueAtLeast(t, safeRemoveBResult, f.B.Address, 1)
			requireTxOutputAssetPositive(t, safeRemoveBResult, f.B.Address, profile.Asset)

			state := fetchTemplateAMMView(t, f.Network.Bootstrap, contract.MustEncode())
			require.True(t, state.TradingReady)
			requirePositiveDecimalText(t, state.AssetAInPool)
			requirePositiveDecimalText(t, state.AssetBInPool)
			requireZeroDecimalText(t, state.LPBalances[f.B.Address])
			requirePositiveDecimalText(t, state.LPBalances[f.C.Address])
		})
	}
	f.requireSynced(t)
}

type templateActor struct {
	Key          *btcec.PrivateKey
	PkScript     []byte
	Address      string
	RedeemScript []byte
	ControlBlock []byte
}

type templateAssetProfile struct {
	Name       string
	Asset      string
	Precision  int
	BindingSat int
	Supply     string
}

type templateFixture struct {
	Network      *realSatoshiNet
	A            *templateActor
	B            *templateActor
	C            *templateActor
	D            *templateActor
	E            *templateActor
	F            *templateActor
	gasAnchor    *wire.MsgTx
	assetAnchors map[string]*wire.MsgTx
	profiles     map[string]templateAssetProfile
}

func newTemplateFixture(t *testing.T, assets map[string]int64) *templateFixture {
	return newTemplateFixtureWithArgs(t, assets, nil, nil, nil)
}

func newTemplateFixtureWithArgs(t *testing.T, assets map[string]int64, bootstrapArgs, coreArgs, minerArgs []string) *templateFixture {
	t.Helper()
	profiles := make([]templateAssetProfile, 0, len(assets))
	for _, asset := range sortedAssetNames(assets) {
		profiles = append(profiles, templateAssetProfile{
			Name:      asset,
			Asset:     asset,
			Precision: 0,
			Supply:    fmt.Sprintf("%d", assets[asset]),
		})
	}
	return newTemplateFixtureWithProfilesAndArgs(t, profiles, bootstrapArgs, coreArgs, minerArgs)
}

func newTemplateFixtureWithProfiles(t *testing.T, profiles []templateAssetProfile) *templateFixture {
	return newTemplateFixtureWithProfilesAndArgs(t, profiles, nil, nil, nil)
}

func newTemplateFixtureWithProfilesAndArgs(t *testing.T, profiles []templateAssetProfile, bootstrapArgs, coreArgs, minerArgs []string) *templateFixture {
	t.Helper()
	oldEnableTesting := indexercommon.ENABLE_TESTING
	indexercommon.ENABLE_TESTING = true
	t.Cleanup(func() {
		indexercommon.ENABLE_TESTING = oldEnableTesting
	})

	const lockedValue = int64(20_000_000)
	gas := contractcommon.GetGasAssetName()
	bootstrapKey := keyFromMnemonic(t, bootstrapMnemonic, 0)
	coreKey := keyFromMnemonic(t, coreMnemonic, 0)
	actorA := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 1))
	actorB := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 2))
	actorC := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 3))
	actorD := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 4))
	actorE := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 5))
	actorF := newTemplateActor(t, keyFromMnemonic(t, bootstrapMnemonic, 6))

	witnessScript, lockedPkScript, err := getP2WSHScript(
		bootstrapKey.PubKey().SerializeCompressed(),
		coreKey.PubKey().SerializeCompressed(),
	)
	require.NoError(t, err)

	l1Assets := []fakeL1Asset{{
		Utxo:  templateLockedOutPoint("gas", 0),
		Value: lockedValue,
		Assets: map[string]string{
			gas: "100000000",
		},
	}}
	profileMap := make(map[string]templateAssetProfile, len(profiles))
	for i, profile := range sortedProfiles(profiles) {
		profileMap[profile.Asset] = profile
		l1Assets = append(l1Assets, fakeL1Asset{
			Utxo:  templateLockedOutPoint(profile.Asset, i+1),
			Value: lockedValue,
			Assets: map[string]string{
				profile.Asset: profile.Supply,
			},
			Metadata: map[string]fakeL1AssetMeta{
				profile.Asset: {
					Precision:  profile.Precision,
					BindingSat: profile.BindingSat,
				},
			},
		})
	}
	fakeL1 := newFakeL1Indexer(t, hex.EncodeToString(bootstrapKey.PubKey().SerializeCompressed()), lockedPkScript, l1Assets)
	network := newRealSatoshiNetWithArgs(t, fakeL1, bootstrapArgs, coreArgs, minerArgs)

	gasAnchor := buildAnchorTx(t, templateLockedOutPoint("gas", 0), lockedValue,
		txAsset(gas, 100000000), gas+"-100000000-0-1",
		witnessScript, bootstrapKey, actorA.PkScript)
	network.sendAndMine(t, gasAnchor, 1)

	assetAnchors := make(map[string]*wire.MsgTx)
	for i, profile := range sortedProfiles(profiles) {
		anchor := buildAnchorTx(t, templateLockedOutPoint(profile.Asset, i+1), lockedValue,
			txAssetProfile(t, profile, profile.Supply), fmt.Sprintf("%s-%s-%d-%d", profile.Asset, profile.Supply, profile.Precision, profile.BindingSat),
			witnessScript, bootstrapKey, actorA.PkScript)
		network.sendAndMine(t, anchor, 1)
		assetAnchors[profile.Asset] = anchor
	}

	return &templateFixture{
		Network:      network,
		A:            actorA,
		B:            actorB,
		C:            actorC,
		D:            actorD,
		E:            actorE,
		F:            actorF,
		gasAnchor:    gasAnchor,
		assetAnchors: assetAnchors,
		profiles:     profileMap,
	}
}

func newTemplateActor(t *testing.T, key *btcec.PrivateKey) *templateActor {
	t.Helper()
	pkScript, address, redeemScript, controlBlock := callerTaprootScript(t, key)
	return &templateActor{
		Key:          key,
		PkScript:     pkScript,
		Address:      address,
		RedeemScript: redeemScript,
		ControlBlock: controlBlock,
	}
}

func (f *templateFixture) split(t *testing.T, owner *templateActor, tx *wire.MsgTx, asset string,
	amounts []int64, values []int64, recipients []*templateActor) []wire.OutPoint {

	t.Helper()
	require.Len(t, values, len(amounts))
	require.Len(t, recipients, len(amounts))
	outputs := make([]*wire.TxOut, 0, len(amounts))
	for i := range amounts {
		outputs = append(outputs, wire.NewTxOut(values[i], txAsset(asset, amounts[i]), recipients[i].PkScript))
	}
	splitTx := wire.NewMsgTx(2)
	splitTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: tx.TxHash(), Index: 0}})
	for _, output := range outputs {
		splitTx.AddTxOut(output)
	}
	signTaprootInputs(t, splitTx, owner.Key, owner.RedeemScript, owner.ControlBlock)
	f.Network.sendAndMine(t, splitTx, 1)
	return collectSpendableOutPoints(t, splitTx, outputs)
}

func (f *templateFixture) splitProfile(t *testing.T, owner *templateActor, tx *wire.MsgTx, profile templateAssetProfile,
	amounts []string, values []int64, recipients []*templateActor) []wire.OutPoint {

	t.Helper()
	require.Len(t, values, len(amounts))
	require.Len(t, recipients, len(amounts))
	outputs := make([]*wire.TxOut, 0, len(amounts))
	for i := range amounts {
		outputs = append(outputs, wire.NewTxOut(values[i], txAssetProfile(t, profile, amounts[i]), recipients[i].PkScript))
	}
	splitTx := wire.NewMsgTx(2)
	splitTx.AddTxIn(&wire.TxIn{PreviousOutPoint: wire.OutPoint{Hash: tx.TxHash(), Index: 0}})
	for _, output := range outputs {
		splitTx.AddTxOut(output)
	}
	signTaprootInputs(t, splitTx, owner.Key, owner.RedeemScript, owner.ControlBlock)
	f.Network.sendAndMine(t, splitTx, 1)
	return collectSpendableOutPoints(t, splitTx, outputs)
}

type templateGasBank struct {
	outs map[*templateActor][]wire.OutPoint
}

func (f *templateFixture) splitGasBank(t *testing.T, perActor int) *templateGasBank {
	t.Helper()
	gas := contractcommon.GetGasAssetName()
	actors := []*templateActor{f.A, f.B, f.C, f.D, f.E, f.F}
	count := perActor * len(actors)
	amounts := make([]int64, count)
	values := make([]int64, count)
	recipients := make([]*templateActor, count)
	for actorIndex, actor := range actors {
		for i := 0; i < perActor; i++ {
			outIndex := actorIndex*perActor + i
			amounts[outIndex] = 300000
			values[outIndex] = 10000
			recipients[outIndex] = actor
		}
	}
	outpoints := f.split(t, f.A, f.gasAnchor, gas, amounts, values, recipients)
	bank := &templateGasBank{outs: make(map[*templateActor][]wire.OutPoint, len(actors))}
	for actorIndex, actor := range actors {
		start := actorIndex * perActor
		bank.outs[actor] = append([]wire.OutPoint(nil), outpoints[start:start+perActor]...)
	}
	return bank
}

func (b *templateGasBank) take(t *testing.T, actor *templateActor) wire.OutPoint {
	t.Helper()
	require.NotNil(t, b)
	require.NotEmpty(t, b.outs[actor])
	out := b.outs[actor][0]
	b.outs[actor] = b.outs[actor][1:]
	return out
}

func (f *templateFixture) requireSynced(t *testing.T) {
	t.Helper()
	bestHash, bestHeight, err := f.Network.Bootstrap.Client.GetBestBlock()
	require.NoError(t, err)
	for _, node := range f.Network.Nodes[1:] {
		nodeHash, nodeHeight, err := node.Client.GetBestBlock()
		require.NoError(t, err)
		require.Equal(t, bestHeight, nodeHeight)
		require.Equal(t, bestHash, nodeHash)
	}
}

func buildTemplateDeployFor(t *testing.T, actor *templateActor, templateName string, content []byte,
	deployer string, random []byte, inputs []wire.OutPoint, funding wire.TxOut) (*wire.MsgTx, contractcommon.ContractAddress) {

	t.Helper()
	tx, address, err := contractcommon.BuildDeployTx(contractcommon.DeployTxBuildRequest{
		ContractPrefix:  contractcommon.TestnetContractPrefix,
		Type:            contractcommon.ContractTypeTemplate,
		SubType:         templateName,
		Version:         contractcommon.CurrentTemplateVersion,
		ContractContent: content,
		Deployer:        deployer,
		DeployNonce:     templateDeployNonce(random),
		GasLimit:        contractcommon.DeployBaseGas,
		Funding:         funding,
		Inputs:          inputs,
	})
	require.NoError(t, err)
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	return tx, address
}

func buildTemplateInvokeFor(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	nonce uint64, action string, param []byte, inputs []wire.OutPoint, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	tx, err := contractcommon.BuildInvokeTx(contractcommon.InvokeTxBuildRequest{
		Contract:  contract,
		GasLimit:  100000,
		CallNonce: nonce,
		Action:    action,
		Param:     param,
		Funding:   funding,
		Inputs:    inputs,
	})
	require.NoError(t, err)
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	return tx
}

func buildTemplateDefaultInvokeFor(t *testing.T, actor *templateActor, contract contractcommon.ContractAddress,
	inputs []wire.OutPoint, funding wire.TxOut) *wire.MsgTx {

	t.Helper()
	pkScript, err := contractcommon.ContractPkScript(contract)
	require.NoError(t, err)
	funding.PkScript = pkScript
	tx := wire.NewMsgTx(wire.TxVersion)
	for _, input := range inputs {
		tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
	}
	tx.AddTxOut(&funding)
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	return tx
}

func buildTemplateAssetTransfer(t *testing.T, actor *templateActor, input wire.OutPoint,
	asset string, amount int64, value int64, recipient *templateActor) *wire.MsgTx {

	t.Helper()
	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&input, nil, nil))
	tx.AddTxOut(wire.NewTxOut(value, txAsset(asset, amount), recipient.PkScript))
	signTaprootInputs(t, tx, actor.Key, actor.RedeemScript, actor.ControlBlock)
	return tx
}

func templateLimitOrderParam(t *testing.T, assetName string, orderType int, amt, unitPrice string) []byte {
	t.Helper()
	param := contractcommon.TemplateLimitOrderInvokeParam{
		OrderType: orderType,
		AssetName: assetName,
		Amt:       amt,
		UnitPrice: unitPrice,
	}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func templateRefundParam(t *testing.T, itemIDs []int64) []byte {
	t.Helper()
	param := contractcommon.TemplateRefundInvokeParam{ItemIDs: itemIDs}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func templateAddLiquidityParam(t *testing.T, assetName, amt string, value int64) []byte {
	t.Helper()
	param := contractcommon.TemplateAddLiquidityInvokeParam{
		OrderType: contractcommon.OrderTypeAddLiquidity,
		AssetName: assetName,
		Amt:       amt,
		Value:     value,
	}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func templateRemoveLiquidityParam(t *testing.T, assetName, lptAmt string) []byte {
	t.Helper()
	param := contractcommon.TemplateRemoveLiquidityInvokeParam{
		OrderType: contractcommon.OrderTypeRemoveLiquidity,
		AssetName: assetName,
		LptAmt:    lptAmt,
	}
	encoded, err := param.Encode()
	require.NoError(t, err)
	return encoded
}

func templateFunding(t *testing.T, assetName string, amount int64) wire.AssetInfo {
	t.Helper()
	return wire.AssetInfo{
		Name:   *wire.NewAssetNameFromString(assetName),
		Amount: *indexercommon.NewDefaultDecimal(amount),
	}
}

func templateFundingProfile(t *testing.T, profile templateAssetProfile, amount string) wire.AssetInfo {
	t.Helper()
	parsed, err := indexercommon.NewDecimalFromString(amount, profile.Precision)
	require.NoError(t, err)
	return wire.AssetInfo{
		Name:       *wire.NewAssetNameFromString(profile.Asset),
		Amount:     *parsed,
		BindingSat: uint32(profile.BindingSat),
	}
}

func txAssetProfile(t *testing.T, profile templateAssetProfile, amount string) wire.TxAssets {
	t.Helper()
	return wire.TxAssets{templateFundingProfile(t, profile, amount)}
}

func txAssets(assets ...wire.AssetInfo) wire.TxAssets {
	var out wire.TxAssets
	for _, asset := range assets {
		_ = out.Add(&asset)
	}
	return out
}

func collectSpendableOutPoints(t *testing.T, tx *wire.MsgTx, outputs []*wire.TxOut) []wire.OutPoint {
	t.Helper()
	txHash := tx.TxHash()
	result := make([]wire.OutPoint, 0, len(outputs))
	for vout, txOut := range tx.TxOut {
		for _, want := range outputs {
			if want == nil {
				continue
			}
			if bytes.Equal(txOut.PkScript, want.PkScript) && txOut.Value == want.Value &&
				(&txOut.Assets).Equal(want.Assets) {
				result = append(result, wire.OutPoint{Hash: txHash, Index: uint32(vout)})
				break
			}
		}
	}
	require.Len(t, result, len(outputs))
	return result
}

func requireAssetSummaryAmount(t *testing.T, node *testHarness, address, assetName, amount string) {
	t.Helper()
	want, err := indexercommon.NewDecimalFromString(amount, 8)
	require.NoError(t, err)
	var lastSummary map[string]string
	var lastAmount *indexercommon.Decimal
	var lastErr error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		summary, got, err := fetchAssetBalance(node, address, assetName)
		lastSummary, lastAmount, lastErr = summary, got, err
		if err == nil && got.Cmp(want) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.Equal(t, amount, lastAmount.String(), "address=%s asset=%s summary=%v", address, assetName, lastSummary)
}

func requireAssetSummaryAtLeast(t *testing.T, node *testHarness, address, assetName, amount string) {
	t.Helper()
	want, err := indexercommon.NewDecimalFromString(amount, 8)
	require.NoError(t, err)
	var lastSummary map[string]string
	var lastAmount *indexercommon.Decimal
	var lastErr error
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		summary, got, err := fetchAssetBalance(node, address, assetName)
		lastSummary, lastAmount, lastErr = summary, got, err
		if err == nil && got.Cmp(want) >= 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.Failf(t, "asset summary too small", "address=%s asset=%s want=%s got=%s summary=%v",
		address, assetName, amount, lastAmount.String(), lastSummary)
}

func requireAssetSummaryZero(t *testing.T, node *testHarness, address, assetName string) {
	t.Helper()
	var lastSummary map[string]string
	var lastAmount *indexercommon.Decimal
	var lastErr error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		summary, got, err := fetchAssetBalance(node, address, assetName)
		lastSummary, lastAmount, lastErr = summary, got, err
		if err == nil && got.Sign() == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.Zero(t, lastAmount.Sign(), "address=%s asset=%s got=%s summary=%v",
		address, assetName, lastAmount.String(), lastSummary)
}

func requirePositiveAssetSummary(t *testing.T, node *testHarness, address, assetName string) {
	t.Helper()
	var lastSummary map[string]string
	var lastAmount *indexercommon.Decimal
	var lastErr error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		summary, got, err := fetchAssetBalance(node, address, assetName)
		lastSummary, lastAmount, lastErr = summary, got, err
		if err == nil && got.Sign() > 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, lastErr)
	require.Positive(t, lastAmount.Sign(), "address=%s asset=%s got=%s summary=%v",
		address, assetName, lastAmount.String(), lastSummary)
}

func fetchTemplateLimitOrderView(t *testing.T, node *testHarness, contract string) templateruntime.LimitOrderStateView {
	t.Helper()
	raw := fetchTemplateStateRaw(t, node, contract)
	var state templateruntime.LimitOrderStateView
	require.NoError(t, json.Unmarshal(raw, &state))
	return state
}

func fetchTemplateAMMView(t *testing.T, node *testHarness, contract string) templateruntime.AMMStateView {
	t.Helper()
	raw := fetchTemplateStateRaw(t, node, contract)
	var state templateruntime.AMMStateView
	require.NoError(t, json.Unmarshal(raw, &state))
	return state
}

func fetchTemplateAutopayView(t *testing.T, node *testHarness, contract string) templateruntime.AutopayStateView {
	t.Helper()
	raw := fetchTemplateStateRaw(t, node, contract)
	var state templateruntime.AutopayStateView
	require.NoError(t, json.Unmarshal(raw, &state))
	return state
}

func fetchTemplateStateRaw(t *testing.T, node *testHarness, contract string) json.RawMessage {
	t.Helper()
	param, err := json.Marshal(contract)
	require.NoError(t, err)
	raw, err := node.Client.RawRequest("getcontractstate", []json.RawMessage{param})
	require.NoError(t, err)
	var result struct {
		State   json.RawMessage        `json:"state"`
		Details map[string]interface{} `json:"details"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	if exists, ok := result.Details["exists"].(bool); ok {
		require.True(t, exists, "contract %s does not exist", contract)
	}
	require.NotEmpty(t, result.State)
	return result.State
}

func mineTemplateBlock(t *testing.T, f *templateFixture) *wire.MsgBlock {
	t.Helper()
	hashes, err := f.Network.Bootstrap.Client.Generate(1)
	require.NoError(t, err)
	require.NotEmpty(t, hashes)
	require.NoError(t, joinBlocks(f.Network.Nodes))
	block, err := f.Network.Bootstrap.Client.GetBlock(hashes[0])
	require.NoError(t, err)
	return block
}

func requireSingleResultTx(t *testing.T, block *wire.MsgBlock) *wire.MsgTx {
	t.Helper()
	results := contractResultTxs(block)
	require.Len(t, results, 1, "block should contain exactly one contract result tx")
	payload := requireResultPayload(t, results[0])
	require.Equal(t, contractcommon.ResultStatusSuccess, payload.Status)
	require.Greater(t, payload.ResultCount, uint16(0))
	return results[0]
}

func contractResultTxs(block *wire.MsgBlock) []*wire.MsgTx {
	if block == nil {
		return nil
	}
	var out []*wire.MsgTx
	for _, tx := range block.Transactions {
		if tx == nil {
			continue
		}
		for _, txOut := range tx.TxOut {
			if txOut == nil {
				continue
			}
			if _, err := contractcommon.ReadResultNullDataScript(txOut.PkScript); err == nil {
				out = append(out, tx)
				break
			}
		}
	}
	return out
}

func requireResultPayload(t *testing.T, tx *wire.MsgTx) contractcommon.ResultPayload {
	t.Helper()
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		payload, err := contractcommon.ReadResultNullDataScript(txOut.PkScript)
		if err == nil {
			return payload
		}
	}
	require.FailNow(t, "missing contract result payload", tx.TxHash().String())
	return contractcommon.ResultPayload{}
}

func requireTxOutputAssetAmount(t *testing.T, tx *wire.MsgTx, address, assetName, amount string) {
	t.Helper()
	total := indexercommon.NewDefaultDecimal(0)
	var seen []string
	for _, txOut := range tx.TxOut {
		gotAddr := txOutputAddress(txOut)
		if gotAddr != "" {
			seen = append(seen, describeTxOutput(txOut))
		}
		if gotAddr != address {
			continue
		}
		got := txOutputAssetAmount(txOut, assetName)
		if got != nil {
			total = indexercommon.DecimalAdd(total, got)
		}
	}
	if amount == "" {
		require.Zero(t, total.Sign(), "tx=%s address=%s asset=%s got=%s seen=%v",
			tx.TxHash(), address, assetName, total.String(), seen)
		return
	}
	want, err := indexercommon.NewDecimalFromString(amount, 10)
	require.NoError(t, err)
	require.Equal(t, 0, total.Cmp(want), "tx=%s address=%s asset=%s want=%s got=%s seen=%v",
		tx.TxHash(), address, assetName, amount, total.String(), seen)
}

func requireTxOutputAssetPositive(t *testing.T, tx *wire.MsgTx, address, assetName string) string {
	t.Helper()
	var seen []string
	for _, txOut := range tx.TxOut {
		gotAddr := txOutputAddress(txOut)
		if gotAddr != "" {
			seen = append(seen, describeTxOutput(txOut))
		}
		if gotAddr != address {
			continue
		}
		got := txOutputAssetAmount(txOut, assetName)
		if got != nil && got.Sign() > 0 {
			return got.String()
		}
	}
	require.Failf(t, "missing positive asset output", "tx=%s address=%s asset=%s seen=%v", tx.TxHash(), address, assetName, seen)
	return ""
}

func requireTxOutputValueAtLeast(t *testing.T, tx *wire.MsgTx, address string, value int64) {
	t.Helper()
	var total int64
	var seen []string
	for _, txOut := range tx.TxOut {
		gotAddr := txOutputAddress(txOut)
		if gotAddr != "" {
			seen = append(seen, describeTxOutput(txOut))
		}
		if gotAddr == address {
			total += txOut.Value
		}
	}
	require.GreaterOrEqual(t, total, value, "tx=%s address=%s seen=%v", tx.TxHash(), address, seen)
}

func requireTxOutputValueAmount(t *testing.T, tx *wire.MsgTx, address string, value int64) {
	t.Helper()
	var total int64
	var seen []string
	for _, txOut := range tx.TxOut {
		gotAddr := txOutputAddress(txOut)
		if gotAddr != "" {
			seen = append(seen, describeTxOutput(txOut))
		}
		if gotAddr == address {
			total += txOut.Value
		}
	}
	require.Equal(t, value, total, "tx=%s address=%s seen=%v", tx.TxHash(), address, seen)
}

func txOutputAddress(txOut *wire.TxOut) string {
	if txOut == nil {
		return ""
	}
	_, addrs, _, err := txscript.ExtractPkScriptAddrs(txOut.PkScript, &chaincfg.TestNetParams)
	if err != nil || len(addrs) == 0 {
		return ""
	}
	return addrs[0].EncodeAddress()
}

func txOutputAssetAmount(txOut *wire.TxOut, assetName string) *indexercommon.Decimal {
	if txOut == nil || assetName == "" {
		return nil
	}
	name := wire.NewAssetNameFromString(assetName)
	if name == nil {
		return nil
	}
	asset, err := (&txOut.Assets).Find(name)
	if err != nil || asset == nil {
		return nil
	}
	return asset.Amount.Clone()
}

func describeTxOutput(txOut *wire.TxOut) string {
	if txOut == nil {
		return "<nil>"
	}
	parts := []string{fmt.Sprintf("%s:%d", txOutputAddress(txOut), txOut.Value)}
	for _, asset := range txOut.Assets {
		parts = append(parts, fmt.Sprintf("%s=%s(n=%d)", asset.Name.String(), asset.Amount.String(), asset.BindingSat))
	}
	return strings.Join(parts, " ")
}

func requireDecimalText(t *testing.T, want string, got *indexercommon.Decimal) {
	t.Helper()
	require.NotNil(t, got)
	require.Equal(t, want, got.String())
}

func requireDecimalPositive(t *testing.T, got *indexercommon.Decimal) {
	t.Helper()
	require.NotNil(t, got)
	require.Positive(t, got.Sign())
}

func requirePositiveDecimalText(t *testing.T, got string) {
	t.Helper()
	decimal, err := indexercommon.NewDecimalFromString(got, 10)
	require.NoError(t, err)
	require.Positive(t, decimal.Sign(), "got=%s", got)
}

func requireDecimalZero(t *testing.T, got *indexercommon.Decimal) {
	t.Helper()
	if got == nil {
		return
	}
	require.Zero(t, got.Sign(), "got=%s", got.String())
}

func requireZeroDecimalText(t *testing.T, got string) {
	t.Helper()
	if got == "" {
		return
	}
	decimal, err := indexercommon.NewDecimalFromString(got, 10)
	require.NoError(t, err)
	require.Zero(t, decimal.Sign(), "got=%s", got)
}

func fetchAssetBalance(node *testHarness, address, assetName string) (map[string]string, *indexercommon.Decimal, error) {
	summary, err := fetchAssetSummary(node, address)
	if err != nil {
		return nil, indexercommon.NewDefaultDecimal(0), err
	}
	if amount := summary[assetName]; amount != "" {
		got, parseErr := indexercommon.NewDecimalFromString(amount, 8)
		if parseErr == nil {
			return summary, got, nil
		}
	}
	got, err := fetchAssetUTXOBalance(node, address, assetName)
	if err != nil {
		return summary, indexercommon.NewDefaultDecimal(0), err
	}
	return summary, got, nil
}

func fetchAssetUTXOBalance(node *testHarness, address, assetName string) (*indexercommon.Decimal, error) {
	baseURL, err := node.IndexerURL("testnet")
	if err != nil {
		return nil, err
	}
	resp, err := http.Get(baseURL + "/v3/address/asset/" + url.PathEscape(address) + "/" + url.PathEscape(assetName))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected indexer status %d", resp.StatusCode)
	}

	var out indexerwire.UtxosWithAssetRespV3
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("indexer response code %d: %s", out.Code, out.Msg)
	}
	total := indexercommon.NewDefaultDecimal(0)
	for _, utxo := range out.Data {
		if utxo == nil {
			continue
		}
		for _, asset := range utxo.Assets {
			if asset == nil || asset.AssetName.String() != assetName {
				continue
			}
			amount, err := indexercommon.NewDecimalFromString(asset.Amount, 8)
			if err != nil {
				return nil, err
			}
			total = indexercommon.DecimalAdd(total, amount)
		}
	}
	return total, nil
}

func fetchAssetSummary(node *testHarness, address string) (map[string]string, error) {
	baseURL, err := node.IndexerURL("testnet")
	if err != nil {
		return nil, err
	}
	resp, err := http.Get(baseURL + "/v3/address/summary/" + url.PathEscape(address))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected indexer status %d", resp.StatusCode)
	}

	var out indexerwire.AssetSummaryRespV3
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.Code != 0 {
		return nil, fmt.Errorf("indexer response code %d: %s", out.Code, out.Msg)
	}
	result := make(map[string]string)
	for _, asset := range out.Data {
		if asset != nil {
			result[asset.AssetName.String()] = asset.Amount
		}
	}
	return result, nil
}

func txHasContractOpReturn(tx *wire.MsgTx) bool {
	for _, txOut := range tx.TxOut {
		if txOut == nil {
			continue
		}
		if _, _, err := contractcommon.ReadNullDataScript(txOut.PkScript); err == nil {
			return true
		}
	}
	return false
}

func templateLockedOutPoint(asset string, index int) string {
	sum := chainhash.HashH([]byte(fmt.Sprintf("transcend-template-e2e:%s:%d", asset, index)))
	return sum.String() + ":0"
}

func sortedAssetNames(assets map[string]int64) []string {
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedProfiles(profiles []templateAssetProfile) []templateAssetProfile {
	out := append([]templateAssetProfile(nil), profiles...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func templateProfiles() []templateAssetProfile {
	return []templateAssetProfile{
		{Name: "ordx_n1", Asset: "ordx:f:e2eordx1", Precision: 0, BindingSat: 1, Supply: "10000000"},
		{Name: "ordx_n1000", Asset: "ordx:f:e2eordx1000", Precision: 0, BindingSat: 1000, Supply: "10000000"},
		{Name: "runes_p0", Asset: "runes:f:E2E•RUNE•ZERO", Precision: 0, BindingSat: 0, Supply: "10000000"},
		{Name: "runes_p2", Asset: "runes:f:E2E•RUNE•TWO", Precision: 2, BindingSat: 0, Supply: "10000000"},
		{Name: "brc20_p0", Asset: "brc20:f:e2eb0", Precision: 0, BindingSat: 0, Supply: "10000000"},
		{Name: "brc20_p8", Asset: "brc20:f:e2eb8", Precision: 8, BindingSat: 0, Supply: "10000000"},
	}
}

func bindingValue(profile templateAssetProfile, amount string) int64 {
	if profile.BindingSat <= 0 {
		return 0
	}
	amt, err := indexercommon.NewDecimalFromString(amount, profile.Precision)
	if err != nil {
		return 0
	}
	return indexercommon.GetBindingSatNum(amt, uint32(profile.BindingSat))
}

func profileAssetCarrierValue(profile templateAssetProfile, amount string) int64 {
	return bindingValue(profile, amount)
}

func limitOrderBuyFunding(assetAmount int64, unitPrice int64) int64 {
	value := assetAmount * unitPrice
	return value + value*8/1000
}
