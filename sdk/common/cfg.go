package common

type Config struct {
	Env                  string      `yaml:"env"`
	Chain                string      `yaml:"chain"`
	Mode                 string      `yaml:"mode"`
	Log                  string      `yaml:"log"`
	DB                   string      `yaml:"db"`
	DBBackupFile         string      `yaml:"dbBackupFile"`
	DBRestoreFile        string      `yaml:"dbRestoreFile"`
	SyncChannel          string      `yaml:"syncChannel"`
	SyncContracts        []string    `yaml:"syncContracts"`
	SyncFlag             string      `yaml:"syncFlag"`
	SyncServer           *RPCService `yaml:"syncServer"`
	RebuildTraderHistory bool        `yaml:"rebuildTraderHistory"`

	Peers          []string        `yaml:"peers"`
	IndexerL1      *Indexer        `yaml:"indexer_layer1"`
	SlaveIndexerL1 *Indexer        `yaml:"slave_indexer_layer1"` // 可以不设置
	IndexerL2      *Indexer        `yaml:"indexer_layer2"`
	SlaveIndexerL2 *Indexer        `yaml:"slave_indexer_layer2"` // 可以不设置
	RPC            RPCService      `yaml:"rpc"`
	Wallet         WalletCfg       `yaml:"wallet"`
}

type Indexer struct {
	Scheme   string `yaml:"scheme"`
	Host     string `yaml:"host"`
	Proxy    string `yaml:"proxy"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type RPCService struct {
	Scheme string `yaml:"scheme"`
	Host   string `yaml:"host"`
	Proxy  string `yaml:"proxy"`
}

type WalletCfg struct {
	Mode       string `yaml:"mode"`
	StakeAsset bool   `yaml:"stake"`
	Mnemonic   string `yaml:"mnemonic"` // 用于初始化时使用，然后删除
	Password   string `yaml:"password"` // 用于初始化时使用，然后删除
	PSFile     string `yaml:"psfile"`   // 参考lnd中对保存钱包密码的设置。
}
