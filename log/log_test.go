package log

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/maxid/glogs"

	"github.com/maxid/gable/config"
)

func TestNewHonorsLogDir(t *testing.T) {
	dir := t.TempDir()
	logger, err := New(config.Log{Dir: dir, Level: "info", Format: "text"}, "demo")
	if err != nil {
		t.Fatal(err)
	}
	logger.Channel(glogs.ChannelApp).Info("hello")
	if err := logger.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "demo", "common", "app", "app.log")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("log file not written under the configured dir: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("log file is empty")
	}
}
