package logger

import (
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func FromViper(v *viper.Viper) (*zap.SugaredLogger, error) {
	level := zap.InfoLevel
	if err := level.Set(v.GetString("log_level")); err != nil {
		return nil, err
	}
	return NewLogger(level)
}

func NewLogger(level zapcore.Level) (*zap.SugaredLogger, error) {
	logger, err := zap.Config{
		Level:            zap.NewAtomicLevelAt(level),
		Development:      false,
		Encoding:         "console",
		EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stdout"},
	}.Build()
	if err != nil {
		return nil, err
	}
	return logger.Sugar(), nil
}

func NewDiscardLogger() *zap.SugaredLogger {
	logger := zap.NewNop()
	return logger.Sugar()
}
