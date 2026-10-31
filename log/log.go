// Package log builds the application logger from configuration.
package log

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/maxid/glogs"

	"github.com/maxid/gable/config"
)

// New builds a glogs logger from cfg.
func New(cfg config.Log, appName string) (*glogs.Logger, error) {
	gl := glogs.DefaultConfig(appName)
	if cfg.Dir != "" {
		gl.Dir = cfg.Dir
	}

	switch strings.ToLower(cfg.Level) {
	case "debug":
		gl.Level = slog.LevelDebug
	case "warn":
		gl.Level = slog.LevelWarn
	case "error":
		gl.Level = slog.LevelError
	}

	if strings.EqualFold(cfg.Format, "json") {
		gl.Root.Format = glogs.FormatJSON
	}

	logger, err := glogs.New(gl)
	if err != nil {
		return nil, fmt.Errorf("init logger: %w", err)
	}
	return logger, nil
}
