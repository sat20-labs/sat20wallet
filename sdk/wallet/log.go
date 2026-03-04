package wallet

import (
	"fmt"
	"io"
	"os"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sirupsen/logrus"
)

var Log = indexer.Log


func InitLog(cfg *common.Config) error {
	var writers []io.Writer
	logPath := "./log/" + cfg.Chain

	lvl, err := logrus.ParseLevel(cfg.Log)
	if err != nil {
		lvl = logrus.InfoLevel
	}
	Log.SetLevel(lvl)

	fileHook, err := rotatelogs.New(
		logPath+"/stpd-%Y%m%d%H%M.log",
		rotatelogs.WithLinkName(logPath+"/stpd.log"),
		rotatelogs.WithMaxAge(30*24*time.Hour),
		rotatelogs.WithRotationTime(24*time.Hour),
	)
	if err != nil {
		return fmt.Errorf("failed to create RotateFile hook, error: %s", err)
	}
	writers = append(writers, fileHook)
	writers = append(writers, os.Stdout)
	Log.SetOutput(io.MultiWriter(writers...))

	return nil
}
