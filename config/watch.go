package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchOption configures a Watcher.
type WatchOption func(*Watcher)

// WithOnChange runs fn after a new configuration is loaded and validated. An error from fn
// keeps the previous configuration in place, the same rule as a rejected file: a reload
// that half-applies is worse than one that refuses.
func WithOnChange(fn func(*Config) error) WatchOption {
	return func(w *Watcher) { w.onChange = fn }
}

// WithOnError is told about a reload that failed. The watcher keeps running with the last
// good configuration, because a typo in a file must not take a running process down.
func WithOnError(fn func(error)) WatchOption {
	return func(w *Watcher) { w.onError = fn }
}

// WithDebounce sets how long to wait for the writer to finish. Editors truncate and rewrite,
// so the first event is usually a half-written file; 200ms is enough for a save and short
// enough that an operator does not notice.
func WithDebounce(d time.Duration) WatchOption {
	return func(w *Watcher) {
		if d > 0 {
			w.debounce = d
		}
	}
}

// Watcher reloads a configuration file when it changes on disk.
//
// It watches the directory rather than the file: most editors and Kubernetes ConfigMap
// updates replace the file, which ends the watch on the old inode.
type Watcher struct {
	path     string
	debounce time.Duration

	onChange func(*Config) error
	onError  func(error)

	mu      sync.RWMutex
	current *Config
	stop    chan struct{}
	once    sync.Once
	done    chan struct{}
}

// Watch loads the file and then watches it. The returned watcher exposes the current
// configuration; the caller owns it and must Close it.
func Watch(path string, opts ...WatchOption) (*Watcher, error) {
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	watcher := &Watcher{
		path:     path,
		debounce: 200 * time.Millisecond,
		current:  cfg,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	for _, opt := range opts {
		opt(watcher)
	}
	// Re-read once more so a change between Load and watch setup is not missed.
	if latest, err := Load(path); err == nil {
		watcher.current = latest
	}

	fileWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("config: watch %s: %w", path, err)
	}
	if err := fileWatcher.Add(filepath.Dir(path)); err != nil {
		_ = fileWatcher.Close()
		return nil, fmt.Errorf("config: watch %s: %w", path, err)
	}
	go watcher.run(fileWatcher)
	return watcher, nil
}

// Config returns the current configuration. The pointer is replaced on reload, never
// mutated, so a reader that holds it keeps a consistent snapshot.
func (w *Watcher) Config() *Config {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.current
}

// Path returns the watched file.
func (w *Watcher) Path() string { return w.path }

// Reload applies the file now, for a signal-driven reload or a test.
func (w *Watcher) Reload() error {
	cfg, err := Load(w.path)
	if err != nil {
		w.report(err)
		return err
	}
	return w.apply(cfg)
}

// Close stops watching. It is safe to call more than once.
func (w *Watcher) Close() error {
	w.once.Do(func() {
		close(w.stop)
	})
	<-w.done
	return nil
}

func (w *Watcher) run(fileWatcher *fsnotify.Watcher) {
	defer close(w.done)
	defer fileWatcher.Close()

	target := filepath.Base(w.path)
	var timer *time.Timer
	var pending <-chan time.Time

	for {
		select {
		case <-w.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-fileWatcher.Events:
			if !ok {
				return
			}
			if filepath.Base(event.Name) != target {
				continue
			}
			if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			// Collapse the burst an editor produces into one reload.
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(w.debounce)
			pending = timer.C
		case err, ok := <-fileWatcher.Errors:
			if !ok {
				return
			}
			w.report(err)
			pending = nil
		case <-pending:
			pending = nil
			// Renaming the file also ends the watch on the directory entry count; make
			// sure the directory is still watched, then reload.
			_ = fileWatcher.Add(filepath.Dir(w.path))
			_ = w.Reload()
		}
	}
}

// apply swaps in a validated configuration and tells the subscriber.
func (w *Watcher) apply(cfg *Config) error {
	if w.onChange != nil {
		if err := w.onChange(cfg); err != nil {
			w.report(err)
			return err
		}
	}
	w.mu.Lock()
	w.current = cfg
	w.mu.Unlock()
	return nil
}

func (w *Watcher) report(err error) {
	if err != nil && w.onError != nil {
		w.onError(err)
	}
}

// IsNotExist reports whether err is "the file is not there", so a caller can decide to fall
// back to defaults without importing os.
func IsNotExist(err error) bool {
	var pathErr *os.PathError
	return errors.As(err, &pathErr) && os.IsNotExist(pathErr)
}
