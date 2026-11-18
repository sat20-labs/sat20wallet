package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
)

// The old SDK WASM cases are exercised at their public Go entry points.
// JS arity/type/NaN conversion, namespace exports, runtime release, IndexedDB
// scheduling and unhandled page errors remain in the separate pwa-tagged gate.
func TestSDKNativeInterfaces(t *testing.T) {
	configureSDKPWARegtestPOSV2(t)
	f := newNativeIfaceChain(t)
	runNativeIfaceIdentity(t, f)
	runNativeIfaceL1(t, f)
	runNativeIfaceInvalid(t, f)
	t.Run("real_l2", func(t *testing.T) { runNativeIfaceRealL2(t, f) })
}

func nativeIfaceCatalog(t *testing.T, m *wallet.Manager) wallet.WalletCatalogSnapshot {
	t.Helper()
	v, err := m.GetWalletCatalogSnapshot()
	require.NoError(t, err)
	return v
}

func nativeIfaceLocal(t *testing.T, m *wallet.Manager) {
	t.Helper()
	view, err := m.GetAccountView(context.Background())
	require.NoError(t, err)
	require.Equal(t, "local", view.State)
	require.Empty(t, view.Locator)
	require.Empty(t, view.RecoveryMode)
	require.Contains(t, []string{"notConfigured", "unconfirmed"}, view.BackupRisk)
}

// BIP86 output-key/address derivation uses btcd directly rather than the SDK
// address encoder or the SDK signature-verification helper under test.
func nativeIfaceAddress(t *testing.T, public []byte, params *chaincfg.Params) string {
	t.Helper()
	key, err := btcec.ParsePubKey(public)
	require.NoError(t, err)
	output := txscript.ComputeTaprootKeyNoScript(key)
	address, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(output), params)
	require.NoError(t, err)
	return address.EncodeAddress()
}

func nativeIfaceSigner(t *testing.T, m *wallet.Manager, expected, other []byte) {
	t.Helper()
	payload := []byte("SDK independent signature 2026")
	require.Equal(t, expected, m.GetPublicKey(0))
	raw, err := m.SignMessage(payload)
	require.NoError(t, err)
	sig, err := ecdsa.ParseDERSignature(raw)
	require.NoError(t, err)
	pub, err := btcec.ParsePubKey(expected)
	require.NoError(t, err)
	hash := sha256.Sum256(payload)
	tampered := sha256.Sum256(append(append([]byte(nil), payload...), '!'))
	require.True(t, sig.Verify(hash[:], pub))
	require.False(t, sig.Verify(tampered[:], pub))
	if other != nil {
		otherKey, err := btcec.ParsePubKey(other)
		require.NoError(t, err)
		require.False(t, sig.Verify(hash[:], otherKey), "cross-wallet signature must fail")
	}
	compact, err := m.SignWalletMessage(string(payload))
	require.NoError(t, err)
	require.Len(t, compact, 65)
	require.GreaterOrEqual(t, compact[0], byte(31))
	require.LessOrEqual(t, compact[0], byte(34))
	messageHash := func(message string) []byte {
		var encoded bytes.Buffer
		require.NoError(t, wire.WriteVarString(&encoded, 0, "Bitcoin Signed Message:\n"))
		require.NoError(t, wire.WriteVarString(&encoded, 0, message))
		return chainhash.DoubleHashB(encoded.Bytes())
	}
	recovered, compressed, err := ecdsa.RecoverCompact(compact, messageHash(string(payload)))
	require.NoError(t, err)
	require.True(t, compressed)
	require.Equal(t, pub.SerializeCompressed(), recovered.SerializeCompressed())
	wrong, _, err := ecdsa.RecoverCompact(compact, messageHash(string(payload)+"!"))
	if err == nil {
		require.NotEqual(t, pub.SerializeCompressed(), wrong.SerializeCompressed())
	}
}

func runNativeIfaceIdentity(t *testing.T, f *nativeIfaceChain) {
	t.Helper()
	mnemonic := dkvsClientMnemonic
	pub := keyFromMnemonic(t, mnemonic, 0).PubKey().SerializeCompressed()
	address := nativeIfaceAddress(t, pub, &chaincfg.RegressionNetParams)

	t.Run("03_mnemonic_identity", func(t *testing.T) {
		d := f.device(t, "")
		valid, err := d.mgr.ValidateMnemonic(mnemonic, "")
		require.NoError(t, err)
		require.Equal(t, address, valid.Address)
		spaced, err := d.mgr.ValidateMnemonic("  "+strings.ReplaceAll(mnemonic, " ", "  ")+"  ", "")
		require.NoError(t, err)
		require.Equal(t, valid.Fingerprint, spaced.Fingerprint)
		id, err := d.mgr.ImportWallet(mnemonic, nativeIfacePassword)
		require.NoError(t, err)
		require.Equal(t, address, d.mgr.GetWallet().GetAddressByIndex(0))
		require.Equal(t, pub, d.mgr.GetPublicKey(0))
		_, err = wallet.AddrToPkScript(address, wallet.GetChainParam())
		require.NoError(t, err)
		_, err = wallet.AddrToPkScript("not-an-address", wallet.GetChainParam())
		require.Error(t, err)
		require.Equal(t, mnemonic, d.mgr.GetMnemonic(id, nativeIfacePassword))
		require.Empty(t, d.mgr.GetMnemonic(id, "wrong"))
	})

	t.Run("04_create_password_cold", func(t *testing.T) {
		d := f.device(t, "")
		id, phrase, err := d.mgr.CreateWallet(nativeIfacePassword)
		require.NoError(t, err)
		require.True(t, bip39.IsMnemonicValid(phrase))
		nativeIfaceLocal(t, d.mgr)
		valid, err := d.mgr.ValidateMnemonic(phrase, "")
		require.NoError(t, err)
		require.Equal(t, valid.Address, d.mgr.GetWallet().GetAddress())
		before := nativeIfaceCatalog(t, d.mgr)
		const changed = "native-password-changed"
		require.Error(t, d.mgr.ChangePassword("wrong", changed))
		require.NoError(t, d.mgr.ChangePassword(nativeIfacePassword, changed))
		d.reopen(t)
		require.Nil(t, d.mgr.GetWallet())
		_, err = d.mgr.UnlockWallet(nativeIfacePassword)
		require.Error(t, err)
		require.Nil(t, d.mgr.GetWallet())
		got, err := d.mgr.UnlockWallet(changed)
		require.NoError(t, err)
		require.Equal(t, id, got)
		require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
		require.Equal(t, valid.Address, d.mgr.GetWallet().GetAddress())
		require.Equal(t, phrase, d.mgr.GetMnemonic(id, changed))
	})

	t.Run("05_subaccount_cold_metadata", func(t *testing.T) {
		d := f.device(t, mnemonic)
		before := nativeIfaceCatalog(t, d.mgr)
		require.NoError(t, d.mgr.EnsureAccount(before.CurrentWalletID, 1, "SDK child", ""))
		require.NoError(t, d.mgr.UpdateAccountMetadata(before.CurrentWalletID, 1, "SDK renamed", ""))
		require.NoError(t, d.mgr.SwitchAccount(1))
		selected := nativeIfaceCatalog(t, d.mgr)
		require.Equal(t, "SDK renamed", selected.Wallets[0].Accounts[1].Name)
		child := nativeIfaceAddress(t, keyFromMnemonic(t, mnemonic, 1).PubKey().SerializeCompressed(), &chaincfg.RegressionNetParams)
		require.NotEqual(t, address, child)
		require.Equal(t, child, d.mgr.GetWallet().GetAddressByIndex(1))
		d.reopen(t)
		_, err := d.mgr.UnlockWallet(nativeIfacePassword)
		require.NoError(t, err)
		nativeIfaceLocal(t, d.mgr)
		require.Equal(t, selected, nativeIfaceCatalog(t, d.mgr))
		require.Equal(t, before.RootAccountID, selected.RootAccountID)
		require.Equal(t, uint32(1), d.mgr.GetCurrentAccountId())
		require.Equal(t, child, d.mgr.GetWallet().GetAddress())
		require.NoError(t, d.mgr.SwitchAccount(0))
		require.Equal(t, address, d.mgr.GetWallet().GetAddress())
	})

	t.Run("06_independent_signatures", func(t *testing.T) {
		d := f.device(t, mnemonic)
		nativeIfaceSigner(t, d.mgr, pub, nil)
	})

	t.Run("09_operation_log_cold", func(t *testing.T) {
		d := f.device(t, mnemonic)
		before := nativeIfaceCatalog(t, d.mgr)
		r, err := d.mgr.BeginOperationLog(wallet.OperationLogCreate{Category: "signature", Action: "sdk-e2e", Title: "SDK log", Summary: "roundtrip", Parameters: map[string]string{"payload": "public test data"}})
		require.NoError(t, err)
		updated, err := d.mgr.UpdateOperationLog(r.ID, wallet.OperationLogUpdate{Status: wallet.OperationLogSucceeded, Message: "independently verified", Result: map[string]string{"verified": "true"}})
		require.NoError(t, err)
		require.Equal(t, wallet.OperationLogSucceeded, updated.Status)
		views, err := d.mgr.GetOperationViews()
		require.NoError(t, err)
		var want wallet.OperationView
		for _, v := range views {
			if v.ID == r.ID {
				want = v
			}
		}
		require.Equal(t, r.ID, want.ID)
		require.Equal(t, "sdk-e2e", want.Kind)
		require.Equal(t, "succeeded", want.Status)
		require.Equal(t, "succeeded", want.History[len(want.History)-1].Status)
		d.reopen(t)
		_, err = d.mgr.UnlockWallet(nativeIfacePassword)
		require.NoError(t, err)
		views, err = d.mgr.GetOperationViews()
		require.NoError(t, err)
		require.Contains(t, views, want)
		records, err := d.mgr.GetOperationLogs()
		require.NoError(t, err)
		require.Contains(t, records, updated)
		require.NoError(t, d.mgr.DeleteOperationHistory())
		views, err = d.mgr.GetOperationViews()
		require.NoError(t, err)
		require.Empty(t, views)
		require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
	})

	t.Run("10_invalid_catalog_preserved", func(t *testing.T) {
		d := f.device(t, mnemonic)
		before := nativeIfaceCatalog(t, d.mgr)
		require.Empty(t, d.mgr.GetMnemonic(before.CurrentWalletID, "wrong"))
		operations := []func() error{
			func() error { return d.mgr.ChangePassword("wrong", "new-password") },
			func() error { _, e := d.mgr.ImportWallet("invalid mnemonic", nativeIfacePassword); return e },
			func() error { _, e := d.mgr.SignPsbt("70736274ff00", false); return e },
			func() error { _, e := d.mgr.SignPsbts([]string{"70736274ff00"}, false); return e },
			func() error { return d.mgr.DeleteWallet(before.CurrentWalletID) },
		}
		for _, op := range operations {
			require.Error(t, op())
			require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
			require.Equal(t, address, d.mgr.GetWallet().GetAddress())
		}
	})

	t.Run("11_typed_account_boundaries", func(t *testing.T) {
		d := f.device(t, mnemonic)
		id := d.mgr.GetCurrentWalletId()
		require.NoError(t, d.mgr.EnsureAccount(id, 1, "boundary child", ""))
		before := nativeIfaceCatalog(t, d.mgr)
		for _, index := range []uint32{account.MaxManagedStateItems, ^uint32(0)} {
			require.Error(t, d.mgr.SwitchAccount(index))
			require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
			require.Error(t, d.mgr.EnsureAccount(id, index, "invalid", ""))
			require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
		}
		for _, index := range []uint32{0, 1} {
			require.NoError(t, d.mgr.SwitchAccount(index))
			require.Equal(t, index, d.mgr.GetCurrentAccountId())
			require.Equal(t, nativeIfaceAddress(t, d.mgr.GetPublicKey(index), &chaincfg.RegressionNetParams), d.mgr.GetWallet().GetAddressByIndex(index))
		}
	})

	t.Run("13_unknown_channel_db_miss", func(t *testing.T) {
		d := f.device(t, mnemonic)
		before := nativeIfaceCatalog(t, d.mgr)
		script, err := txscript.NewScriptBuilder().AddOp(txscript.OP_2).AddData(pub).AddData(pub).AddOp(txscript.OP_2).AddOp(txscript.OP_CHECKMULTISIG).Script()
		require.NoError(t, err)
		hash := sha256.Sum256(script)
		channel, err := btcutil.NewAddressWitnessScriptHash(hash[:], &chaincfg.RegressionNetParams)
		require.NoError(t, err)
		done := make(chan int, 1)
		go func() { done <- d.mgr.GetChannelStatus(channel.EncodeAddress()) }()
		select {
		case status := <-done:
			require.Equal(t, int(wallet.CS_UNKNOWN), status)
		case <-time.After(30 * time.Second):
			t.Fatal("unknown-channel database lookup blocked")
		}
		require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
		nativeIfaceSigner(t, d.mgr, pub, nil)
	})

	t.Run("14_private_wallet_lifecycle", func(t *testing.T) {
		d := f.device(t, "")
		ids := make([]int64, 2)
		pubs := make([][]byte, 2)
		for i := range ids {
			key := make([]byte, 32)
			key[31] = byte(i + 1)
			_, public := btcec.PrivKeyFromBytes(key)
			pubs[i] = public.SerializeCompressed()
			id, err := d.mgr.ImportWalletWithPrivateKey(hex.EncodeToString(key), nativeIfacePassword)
			require.NoError(t, err)
			ids[i] = id
			nativeIfaceLocal(t, d.mgr)
			require.False(t, d.mgr.GetAccountManagementStatus().Active)
			if i == 0 {
				before := nativeIfaceCatalog(t, d.mgr)
				_, err = d.mgr.PreflightRecovery(nativeIfacePassword, []int64{id})
				require.EqualError(t, err, "account management requires a mnemonic root wallet")
				_, err = d.mgr.ConfirmStorage(context.Background(), wallet.StorageOption{ID: wallet.AccountStorageTemporary, RecordCount: 100})
				require.Error(t, err)
				require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
			}
			require.Equal(t, nativeIfaceAddress(t, pubs[i], &chaincfg.RegressionNetParams), d.mgr.GetWallet().GetAddress())
			nativeIfaceSigner(t, d.mgr, pubs[i], nil)
		}
		require.NoError(t, d.mgr.UpdateWalletName(ids[0], "SDK standalone key"))
		require.NoError(t, d.mgr.SwitchWallet(ids[0], nativeIfacePassword))
		before := nativeIfaceCatalog(t, d.mgr)
		d.reopen(t)
		_, err := d.mgr.UnlockWallet("wrong")
		require.Error(t, err)
		require.Nil(t, d.mgr.GetWallet())
		_, err = d.mgr.UnlockWallet(nativeIfacePassword)
		require.NoError(t, err)
		require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
		require.Equal(t, "SDK standalone key", d.mgr.GetWalletCatalog()[0].Name)
		nativeIfaceSigner(t, d.mgr, pubs[0], pubs[1])
		require.NoError(t, d.mgr.DeleteWallet(ids[1]))
		require.Len(t, d.mgr.GetWalletCatalog(), 1)
		require.Error(t, d.mgr.SwitchWallet(ids[1], nativeIfacePassword))
		require.Equal(t, ids[0], d.mgr.GetCurrentWalletId())
	})

	t.Run("15_managed_private_isolation", func(t *testing.T) {
		d := f.device(t, "")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := d.mgr.RecoverAccountManagementFromRootMnemonic(ctx, mnemonic, nativeIfacePassword)
		require.ErrorIs(t, err, wallet.ErrRootAccountNotFound)
		root, err := d.mgr.ImportWallet(mnemonic, nativeIfacePassword)
		require.NoError(t, err)
		before := nativeIfaceCatalog(t, d.mgr)
		require.True(t, d.mgr.GetAccountManagementStatus().Active)
		for _, invalid := range []string{"not-hex", strings.Repeat("0", 64)} {
			_, err = d.mgr.ImportWalletWithPrivateKey(invalid, nativeIfacePassword)
			require.Error(t, err)
			require.Equal(t, before, nativeIfaceCatalog(t, d.mgr))
		}
		key := make([]byte, 32)
		key[31] = 1
		_, privatePub := btcec.PrivKeyFromBytes(key)
		private, err := d.mgr.ImportWalletWithPrivateKey(hex.EncodeToString(key), nativeIfacePassword)
		require.NoError(t, err)
		require.NotEqual(t, root, private)
		mixed := nativeIfaceCatalog(t, d.mgr)
		require.Len(t, mixed.Wallets, len(before.Wallets)+1)
		require.Equal(t, before.RootAccountID, mixed.RootAccountID)
		require.Contains(t, mixed.Wallets, before.Wallets[0])
		nativeIfaceSigner(t, d.mgr, privatePub.SerializeCompressed(), pub)
		require.NoError(t, d.mgr.SwitchWallet(root, nativeIfacePassword))
		nativeIfaceSigner(t, d.mgr, pub, privatePub.SerializeCompressed())
		require.NoError(t, d.mgr.SwitchWallet(private, nativeIfacePassword))
		d.reopen(t)
		_, err = d.mgr.UnlockWallet(nativeIfacePassword)
		require.NoError(t, err)
		nativeIfaceLocal(t, d.mgr)
		require.Equal(t, mixed, nativeIfaceCatalog(t, d.mgr))
		nativeIfaceSigner(t, d.mgr, privatePub.SerializeCompressed(), pub)
		require.NoError(t, d.mgr.SwitchWallet(root, nativeIfacePassword))
		require.Equal(t, address, d.mgr.GetWallet().GetAddress())
		require.Equal(t, before.RootAccountID, nativeIfaceCatalog(t, d.mgr).RootAccountID)
	})
}
