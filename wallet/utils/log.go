package utils

import (
	"github.com/sirupsen/logrus"
)

var Log = logrus.StandardLogger()

func init() {
	logrus.SetReportCaller(true)
	Log.SetLevel(logrus.InfoLevel)
	Log.SetFormatter(&CustomFormatter{
		logrus.TextFormatter{
			TimestampFormat: "01/02-15:04:05",
			FullTimestamp:   true,
			ForceColors:     true,
			DisableColors:   false,
			DisableQuote:    false,
			ForceQuote:      true,
			PadLevelText:    false,
		},
	})
}

type CustomFormatter struct {
	logrus.TextFormatter
}

func (f *CustomFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	switch entry.Level {
	case logrus.TraceLevel:
		fallthrough
	case logrus.DebugLevel:
		fallthrough
	case logrus.InfoLevel:
		entry.Caller = nil
	case logrus.WarnLevel:
	case logrus.ErrorLevel:
	case logrus.FatalLevel:
	case logrus.PanicLevel:
	}
	return f.TextFormatter.Format(entry)
}
