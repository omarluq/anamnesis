package di

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"github.com/samber/lo"
	"github.com/samber/oops"
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

// NewLoggerService configures application logging from the resolved config. The
// loggers write to a file rather than the terminal: anamnesis runs a full-screen
// tcell TUI, so any stray write to stdout or stderr would corrupt the rendered
// screen. Both the zerolog logger and the slog handler built atop it share the
// same file writer, and slog.SetDefault installs the handler process-wide.
func NewLoggerService(injector do.Injector) (*LoggerService, error) {
	cfg := do.MustInvoke[*ConfigService](injector).Get()

	writer, err := openLogFile(cfg.Logging.File)
	if err != nil {
		return nil, err
	}

	zerologLogger := newZerologLogger(cfg, writer)

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

// openLogFile opens the resolved log destination for appended writes, creating the
// parent directory when it is missing. The handle stays open for the process
// lifetime; the OS reclaims it at exit, matching the container's lack of a
// shutdown path.
func openLogFile(configured string) (io.Writer, error) {
	path := resolveLogPath(configured)

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, oops.In("di").Code("log_dir_create").Wrapf(err, "create log directory")
	}

	//nolint:gosec // path is operator-configured application logging, not untrusted input.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, oops.In("di").Code("log_file_open").Wrapf(err, "open log file %s", path)
	}

	return file, nil
}

// resolveLogPath returns the configured log path, or the XDG state default
// ($XDG_STATE_HOME/ana/ana.log, falling back to the home or temp directory) when
// the configured path is empty.
func resolveLogPath(configured string) string {
	if configured != "" {
		return configured
	}

	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, ".local", "state")
		} else {
			base = os.TempDir()
		}
	}

	return filepath.Join(base, "ana", "ana.log")
}

// newZerologLogger builds the zerolog logger writing to writer at the configured
// level, rendering JSON or a no-color console format per cfg.Logging.Format.
func newZerologLogger(cfg *config.Config, writer io.Writer) zerolog.Logger {
	level := parseZerologLevel(cfg.Logging.Level)

	if cfg.Logging.Format == formatJSON {
		return zerolog.New(writer).With().Timestamp().Logger().Level(level)
	}

	return zerolog.New(zerolog.ConsoleWriter{Out: writer, NoColor: true}).
		With().Timestamp().Logger().Level(level)
}

func parseZerologLevel(level string) zerolog.Level {
	return lo.ValueOr(zerologLevels, level, zerolog.InfoLevel)
}

func slogLevel(level string) slog.Level {
	return lo.ValueOr(slogLevels, level, slog.LevelInfo)
}
