package wallet

import (
	"encoding/hex"
	"fmt"
	"github.com/sat20-labs/sat20wallet/sdk/account"
)

func accountRecordMaterial(typ int, secret []byte) (account.WalletType, []byte, error) {
	switch typ {
	case WALLET_TYPE_MNEMONIC:
		return account.WalletMnemonic, append([]byte(nil), secret...), nil
	case WALLET_TYPE_PRIVKEY:
		value := make([]byte, hex.DecodedLen(len(secret)))
		n, err := hex.Decode(value, secret)
		if err != nil || account.ValidatePrivateKey(value[:n]) != nil {
			zeroBytes(value)
			return 0, nil, fmt.Errorf("invalid private wallet material")
		}
		return account.WalletPrivateKey, value[:n], nil
	default:
		return 0, nil, fmt.Errorf("unsupported wallet type")
	}
}

func accountWalletMaterial(typ account.WalletType, secret []byte) (*InternalWallet, int, []byte, error) {
	switch typ {
	case account.WalletMnemonic:
		value := NewInternalWalletWithMnemonic(string(secret), "", GetChainParam())
		if value == nil {
			return nil, 0, nil, fmt.Errorf("invalid mnemonic wallet material")
		}
		return value, WALLET_TYPE_MNEMONIC, append([]byte(nil), secret...), nil
	case account.WalletPrivateKey:
		if account.ValidatePrivateKey(secret) != nil {
			return nil, 0, nil, fmt.Errorf("invalid private wallet material")
		}
		value, _, err := NewInternalWalletWithPrivKey(secret, GetChainParam())
		if err != nil {
			return nil, 0, nil, fmt.Errorf("invalid private wallet material")
		}
		local := make([]byte, hex.EncodedLen(len(secret)))
		hex.Encode(local, secret)
		return value, WALLET_TYPE_PRIVKEY, local, nil
	default:
		return nil, 0, nil, fmt.Errorf("unsupported wallet type")
	}
}
