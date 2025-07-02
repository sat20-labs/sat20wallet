package common

type Config struct {
	Env          string     `yaml:"env"`
	Chain        string     `yaml:"chain"`
	Log          string     `yaml:"log"`
	DB           string     `yaml:"db"`

	Peers        []string   `yaml:"peers"`
	IndexerL1 	 *Indexer   `yaml:"indexer_layer1"`
	SlaveIndexerL1 	*Indexer `yaml:"slave_indexer_layer1"` // 可以不设置
	IndexerL2 		*Indexer `yaml:"indexer_layer2"`
	SlaveIndexerL2 	*Indexer `yaml:"slave_indexer_layer2"`
}


type Indexer struct {
	Scheme   string `yaml:"scheme"`
	Host     string `yaml:"host"`
	Proxy    string `yaml:"proxy"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}
