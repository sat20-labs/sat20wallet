package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v2"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sirupsen/logrus"
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

func InitConfig() *common.Config {
	cfgFile := GetBaseDir() + "/conf.yaml"
	cfg, err := LoadYamlConf(cfgFile)
	if err != nil {
		cfg = NewDefaultYamlConf()
	}
	return cfg
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

func NewDefaultYamlConf() *common.Config {
	chain := "testnet4"
	ret := &common.Config{
		Chain: chain,
		Log: "error",
	}

	return ret
}

func SaveYamlConf(conf *common.Config, filePath string) error {
	data, err := yaml.Marshal(conf)
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, data, 0644)
}
