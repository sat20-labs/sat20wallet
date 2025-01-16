module github.com/sat20-labs/sat20wallet/sdk

go 1.22.1

// custom versions that add testnet4 support
replace github.com/btcsuite/btcd => github.com/sat20-labs/btcd v0.24.3-beta-rc1

replace github.com/btcsuite/btcwallet => github.com/sat20-labs/btcwallet v0.16.11

replace github.com/btcsuite/btcd/btcutil => github.com/sat20-labs/btcd/btcutil v1.1.7

replace github.com/sat20-labs/satsnet_btcd => ../../satsnet_btcd

require (
	github.com/btcsuite/btcd v0.24.2
	github.com/btcsuite/btcd/btcec/v2 v2.3.4
	github.com/btcsuite/btcd/btcutil v1.1.6
	github.com/btcsuite/btcd/btcutil/psbt v1.1.9
	github.com/btcsuite/btcd/chaincfg/chainhash v1.1.0
	github.com/btcsuite/btcwallet v0.0.0-00010101000000-000000000000
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.3.0
	github.com/lestrrat-go/file-rotatelogs v2.4.0+incompatible
	github.com/tyler-smith/go-bip39 v1.1.0
	golang.org/x/crypto v0.31.0
	lukechampine.com/uint128 v1.3.0

)

require (
	github.com/btcsuite/btclog v0.0.0-20170628155309-84c8d2346e9f // indirect
	github.com/btcsuite/btcwallet/walletdb v1.4.4 // indirect
	github.com/dgraph-io/ristretto/v2 v2.0.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/flatbuffers v24.3.25+incompatible // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/jonboulle/clockwork v0.4.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/lestrrat-go/strftime v1.1.0 // indirect
	github.com/lightninglabs/neutrino/cache v1.1.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/net v0.31.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/crypto/blake256 v1.0.1 // indirect
	github.com/dgraph-io/badger/v4 v4.5.0
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/lightningnetwork/lnd/tlv v1.0.2 // indirect
	github.com/sat20-labs/satsnet_btcd v0.0.0-20241213071731-f5e1b98a4654
	github.com/sirupsen/logrus v1.9.3
	gopkg.in/yaml.v2 v2.4.0
)
