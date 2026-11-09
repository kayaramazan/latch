// Package cli implements latchd's command-line subcommands. The run command
// starts the daemon; the others are thin clients of its management API.
package cli

import (
	"flag"
	"log/slog"
	"os"
)

// RunConfig holds parsed `latchd run` options.
type RunConfig struct {
	DataDir  string
	Listen   string
	LogLevel string
}

// ParseRun parses the run subcommand flags.
func ParseRun(args []string) (RunConfig, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c RunConfig
	fs.StringVar(&c.DataDir, "data-dir", "", "data directory (default: platform-specific)")
	fs.StringVar(&c.Listen, "listen", "", "management API address (default: from config, 127.0.0.1:4040)")
	fs.StringVar(&c.LogLevel, "log-level", "info", "log level: debug, info, warn, error")
	if err := fs.Parse(args); err != nil {
		return RunConfig{}, err
	}
	return c, nil
}

// NewLogger builds a text logger at the given level.
func NewLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))
}
