package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maxid/gable/config"
)

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A change on disk reaches the subscriber, and Config() returns the new value.
func TestWatchReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gable.yaml")
	writeConfig(t, path, "app: {name: first}\nhttp: {addr: ':8081'}\n")

	var mu sync.Mutex
	var seen []string
	watcher, err := config.Watch(path,
		config.WithDebounce(10*time.Millisecond),
		config.WithOnChange(func(cfg *config.Config) error {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, cfg.App.Name+"@"+cfg.HTTP.Addr)
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	if watcher.Config().App.Name != "first" {
		t.Fatalf("initial config = %+v", watcher.Config().App)
	}

	writeConfig(t, path, "app: {name: second}\nhttp: {addr: ':8082'}\n")
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := len(seen)
		mu.Unlock()
		if got > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no reload observed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if name := watcher.Config().App.Name; name != "second" {
		t.Fatalf("config after reload = %q", name)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "second@:8082" {
		t.Fatalf("subscriber saw %v", seen)
	}
}

// An invalid file keeps the last good configuration and reports the error: a typo must not
// take a running process down.
func TestWatchKeepsLastGoodConfigOnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gable.yaml")
	writeConfig(t, path, "app: {name: good}\n")

	var mu sync.Mutex
	var failures []error
	watcher, err := config.Watch(path,
		config.WithDebounce(10*time.Millisecond),
		config.WithOnError(func(err error) {
			mu.Lock()
			defer mu.Unlock()
			failures = append(failures, err)
		}))
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	writeConfig(t, path, "app: {name: broken\n")
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := len(failures)
		mu.Unlock()
		if got > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no failure reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if name := watcher.Config().App.Name; name != "good" {
		t.Fatalf("config = %q, want the last good one", name)
	}

	// The watcher is still alive: fixing the file applies.
	writeConfig(t, path, "app: {name: fixed}\n")
	deadline = time.Now().Add(5 * time.Second)
	for watcher.Config().App.Name != "fixed" {
		if time.Now().After(deadline) {
			t.Fatalf("config after fix = %q", watcher.Config().App.Name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A subscriber that rejects the new configuration keeps it from being applied.
func TestWatchRejectsWhenSubscriberFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gable.yaml")
	writeConfig(t, path, "app: {name: keep}\n")

	watcher, err := config.Watch(path,
		config.WithDebounce(10*time.Millisecond),
		config.WithOnChange(func(*config.Config) error { return errors.New("cannot rebind") }))
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()

	writeConfig(t, path, "app: {name: rejected}\n")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := watcher.Reload(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the subscriber never rejected the change")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if name := watcher.Config().App.Name; name != "keep" {
		t.Fatalf("config = %q", name)
	}
}

func TestWatchMissingFileFailsAndCloseIsIdempotent(t *testing.T) {
	if _, err := config.Watch(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("watching a missing file must fail")
	}
	if !config.IsNotExist(errFromMissingFile(t, filepath.Join(t.TempDir(), "absent.yaml"))) {
		t.Fatal("IsNotExist must recognise a missing file")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "gable.yaml")
	writeConfig(t, path, "app: {name: ok}\n")
	watcher, err := config.Watch(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
}

func errFromMissingFile(t *testing.T, path string) error {
	t.Helper()
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}
