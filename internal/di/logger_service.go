package di

import (
	"log/slog"
	"os"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"github.com/samber/lo"
	slogzerolog "github.com/samber/slog-zerolog/v2"

	"github.com/omarluq/anamnesis/internal/config"
)

// Logging level and format identifiers recognized by the service.
const (
	levelDebug = "debug"
	levelInfo  = "info"
	levelWarn  = "warn"
	levelError = "error"
	formatJSON = "json"
)

// zerologLevels maps configured level names to zerolog levels; unknown names default to info.
var zerologLevels = map[string]zerolog.Level{
	levelDebug: zerolog.DebugLevel,
	levelInfo:  zerolog.InfoLevel,
	levelWarn:  zerolog.WarnLevel,
	levelError: zerolog.ErrorLevel,
}

// slogLevels maps configured level names to slog levels; unknown names default to info.
var slogLevels = map[string]slog.Level{
	levelDebug: slog.LevelDebug,
	levelInfo:  slog.LevelInfo,
	levelWarn:  slog.LevelWarn,
	levelError: slog.LevelError,
}

// LoggerService exposes structured slog and zerolog loggers.
type LoggerService struct {
	SlogLogger    *slog.Logger
	ZerologLogger zerolog.Logger
}

// NewLoggerService configures application logging from the resolved config.
func NewLoggerService(injector do.Injector) (*LoggerService, error) {
	cfg := do.MustInvoke[*ConfigService](injector).Get()
	zerologLogger := newZerologLogger(cfg)

	logger := slog.New(slogzerolog.Option{
		Level:  slogLevel(cfg.Logging.Level),
		Logger: &zerologLogger,
	}.NewZerologHandler()).With(slog.String("app", cfg.App.Name))

	slog.SetDefault(logger)

	return &LoggerService{
		SlogLogger:    logger,
		ZerologLogger: zerologLogger,
	}, nil
}

func newZerologLogger(cfg *config.Config) zerolog.Logger {
	level := parseZerologLevel(cfg.Logging.Level)
	writer := os.Stdout

	if cfg.Logging.Format == formatJSON {
		return zerolog.New(writer).With().Timestamp().Logger().Level(level)
	}

	return zerolog.New(zerolog.ConsoleWriter{Out: writer}).With().Timestamp().Logger().Level(level)
}

func parseZerologLevel(level string) zerolog.Level {
	return lo.ValueOr(zerologLevels, level, zerolog.InfoLevel)
}

func slogLevel(level string) slog.Level {
	return lo.ValueOr(slogLevels, level, slog.LevelInfo)
}
