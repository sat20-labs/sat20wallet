package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/sirupsen/logrus"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)


func GetBaseDir() string {
	execPath, err := os.Executable()
	if err != nil {
		return "./."
	}
	execPath = filepath.Dir(execPath)
	// if strings.Contains(execPath, "/cli") {
	// 	execPath, _ = strings.CutSuffix(execPath, "/cli")
	// }
	return execPath
}

func InitConfig() (*common.Config, error) {
	cfgFile := GetBaseDir()+"/conf.yaml"
	fmt.Printf("cfg path: %s\n", cfgFile)
	cfg, err := LoadYamlConf(cfgFile)
	if err != nil {
		return nil, err
	}
	if cfg.DB == "" {
		cfg.DB = "./data"
	}
	fmt.Printf("db path: %s\n", cfg.DB)
	return cfg, nil
}

func LoadYamlConf(cfgPath string) (*common.Config, error) {
	confFile, err := os.Open(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cfg: %s, error: %s", cfgPath, err)
	}
	defer confFile.Close()

	cfg := &common.Config{}
	decoder := yaml.NewDecoder(confFile)
	err = decoder.Decode(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to decode cfg: %s, error: %s", cfgPath, err)
	}

	_, err = logrus.ParseLevel(cfg.Log)
	if err != nil {
		cfg.Log = "info"
	}

	return cfg, nil
}


func SaveYamlConf(conf *common.Config, filePath string) error {
	data, err := yaml.Marshal(conf)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}
