// Package cli implements latchd's command-line subcommands. The run command
// starts the daemon; the others are thin clients of its management API.
package cli

import (
	"flag"
	"io"
	"log/slog"
	"os"

	"gopkg.in/natefinch/lumberjack.v2"
)

// RunConfig holds parsed `latchd run` options.
type RunConfig struct {
	DataDir   string
	Listen    string
	LogLevel  string
	LogFormat string
	LogFile   string
}

// ParseRun parses the run subcommand flags.
func ParseRun(args []string) (RunConfig, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c RunConfig
	fs.StringVar(&c.DataDir, "data-dir", "", "data directory (default: platform-specific)")
	fs.StringVar(&c.Listen, "listen", "", "management API address (default: from config, 127.0.0.1:4040)")
	fs.StringVar(&c.LogLevel, "log-level", "info", "log level: debug, info, warn, error")
	fs.StringVar(&c.LogFormat, "log-format", "text", "log format: text or json")
	fs.StringVar(&c.LogFile, "log-file", "", "write structured logs to this file (rotated); default stderr")
	if err := fs.Parse(args); err != nil {
		return RunConfig{}, err
	}
	return c, nil
}

// NewLogger builds a logger from the run config. With a log file it writes
// rotated JSON; otherwise it writes to stderr in the chosen format.
func NewLogger(c RunConfig) *slog.Logger {
	var lv slog.Level
	switch c.LogLevel {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv}
	var w io.Writer = os.Stderr
	format := c.LogFormat
	if c.LogFile != "" {
		w = &lumberjack.Logger{Filename: c.LogFile, MaxSize: 100, MaxBackups: 5, MaxAge: 14, Compress: true}
		format = "json"
	}
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
