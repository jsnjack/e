// Package logging configures application-wide structured logging.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// LevelTrace is the most verbose logging level.
const LevelTrace = slog.Level(-8)

// Trace writes a trace-level structured event.
func Trace(message string, args ...any) {
	slog.Default().Log(context.Background(), LevelTrace, message, args...)
}

// Init configures logging and returns a cleanup function.
func Init(tracePath string, debug bool) (func(), error) {
	var writer io.Writer = os.Stderr
	cleanup := func() {}
	level := slog.LevelWarn
	if debug {
		level = slog.LevelDebug
	}
	if tracePath != "" {
		file, err := os.OpenFile(tracePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return cleanup, fmt.Errorf("open trace log: %w", err)
		}
		writer = file
		level = LevelTrace
		cleanup = func() {
			if err := file.Close(); err != nil {
				slog.Default().Log(context.Background(), LevelTrace, "close trace log", "err", err)
			}
		}
	}
	handler := slog.NewTextHandler(writer, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(handler))
	return cleanup, nil
}
