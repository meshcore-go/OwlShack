// Package logging defines shared logging primitives used across the bot's
// packages, including the custom verbose trace level and handler setup.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// LevelTrace is a custom slog level below Debug, used for very verbose tracing
// (enabled with -vv). It is rendered as "TRACE" by the configured handler.
const LevelTrace = slog.Level(-8)

// Configure installs the process-wide slog default logger. The -v count takes
// precedence (1=debug, >=2=trace); otherwise the config's log level is used.
// With neither set, slog's built-in default logger is left in place.
func Configure(verbosity int, logLevel string) {
	if verbosity == 0 && logLevel == "" {
		return
	}

	level := slog.LevelInfo
	switch {
	case verbosity >= 2:
		level = LevelTrace
	case verbosity == 1:
		level = slog.LevelDebug
	default:
		switch strings.ToLower(logLevel) {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		case "trace":
			level = LevelTrace
		default:
			slog.Info("invalid logging level provided, defaulting to info", "level", logLevel)
		}
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey && a.Value.Any().(slog.Level) == LevelTrace {
				a.Value = slog.StringValue("TRACE")
			}
			return a
		},
	})))
}
