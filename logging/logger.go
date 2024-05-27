package logging

import (
	"context"
	"io"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NopLogger is the default zap.Logger returned when
var NopLogger = zap.NewNop()

type contextKey uint8

const (
	loggerCoreKey contextKey = iota
)

// Create a new Logger and attach it to the given Context
func Create(ctx context.Context, out io.Writer, level string, fields ...zapcore.Field) (context.Context, *zap.Logger, error) {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, nil, err
	}

	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(out), lvl)

	if len(fields) > 0 {
		core = core.With(fields)
	}

	return context.WithValue(ctx, loggerCoreKey, core), zap.New(core), nil
}

// WithLogger adds a zapcore.Core value from a zap.Logger to the given Context
func WithLogger(ctx context.Context, logger *zap.Logger, fields ...zapcore.Field) (context.Context, *zap.Logger) {
	if len(fields) > 0 {
		logger = logger.With(fields...)
	}

	return context.WithValue(ctx, loggerCoreKey, logger.Core()), logger
}

// Logger attempts to retrieve a zap.Logger instance from the given Context
func Logger(ctx context.Context, fields ...zapcore.Field) (context.Context, *zap.Logger) {
	if core, has := ctx.Value(loggerCoreKey).(zapcore.Core); has {
		if len(fields) > 0 {
			core = core.With(fields)
			ctx = context.WithValue(ctx, loggerCoreKey, core)
		}

		return ctx, zap.New(core)
	}

	return ctx, NopLogger
}

// Debug is a helper that gets the Context's logger and calls its Debug method
func Debug(ctx context.Context, msg string, fields ...zapcore.Field) {
	_, logger := Logger(ctx)
	logger.Debug(msg, fields...)
}

// Info is a helper that gets the Context's logger and calls its Info method
func Info(ctx context.Context, msg string, fields ...zapcore.Field) {
	_, logger := Logger(ctx)
	logger.Info(msg, fields...)
}

// Warn is a helper that gets the Context's logger and calls its Warn method
func Warn(ctx context.Context, msg string, fields ...zapcore.Field) {
	_, logger := Logger(ctx)
	logger.Warn(msg, fields...)
}

// Error is a helper that gets the Context's logger and calls its Error method
func Error(ctx context.Context, msg string, fields ...zapcore.Field) {
	_, logger := Logger(ctx)
	logger.Error(msg, fields...)
}
