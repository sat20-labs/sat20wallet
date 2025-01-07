package wallet

import (
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
)



type Config struct {
	Chain        string     `yaml:"chain"`
	Mode         string     `yaml:"mode"`
	Btcd         Bitcoin    `yaml:"btcd"`
	IndexerL1    Indexer    `yaml:"indexer_layer1"`
	IndexerL2    Indexer    `yaml:"indexer_layer2"`
	Log          string     `yaml:"log"`
	DB           string     `yaml:"db"`
}

type Bitcoin struct {
	Host           string `yaml:"host"`
	User           string `yaml:"user"`
	Password       string `yaml:"password"`
	Zmqpubrawblock string `yaml:"zmqpubrawblock"`
	Zmqpubrawtx    string `yaml:"zmqpubrawtx"`
}

type Indexer struct {
	Scheme string `yaml:"scheme"`
	Host     string `yaml:"host"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}


func ParsePubkey(parsedPubKey string) (*btcec.PublicKey, error) {
	
	// Decode the hex pubkey to get the raw compressed pubkey bytes.
	pubKeyBytes, err := hex.DecodeString(parsedPubKey)
	if err != nil {
		return nil, fmt.Errorf("invalid address "+
			"pubkey: %w", err)
	}

	// The compressed pubkey should have a length of exactly 33 bytes.
	if len(pubKeyBytes) != 33 {
		return nil, fmt.Errorf("invalid address pubkey: "+
			"length must be 33 bytes, found %d", len(pubKeyBytes))
	}

	// Parse the pubkey bytes to verify that it corresponds to valid public
	// key on the secp256k1 curve.
	pubKey, err := btcec.ParsePubKey(pubKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid address "+
			"pubkey: %w", err)
	}

	return pubKey, nil
}
