package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

type devOptions struct {
	dir string
}

func newDevCommand() *cobra.Command {
	opts := &devOptions{}
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "build, run and restart an application on change",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDev(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.dir, "dir", ".", "application directory")
	return cmd
}

func runDev(cmd *cobra.Command, opts *devOptions) error {
	root, err := filepath.Abs(opts.dir)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return fmt.Errorf("gable dev: %s is not a Go module", root)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()
	if err := addDirs(watcher, root); err != nil {
		return err
	}

	ctx, stop := signalContext()
	defer stop()

	binary := filepath.Join(os.TempDir(), fmt.Sprintf("gable-dev-%d", os.Getpid()))
	defer os.Remove(binary)

	var child *exec.Cmd
	buildAndStart := func() {
		stopChild(child)
		child = nil

		build := exec.Command("go", "build", "-o", binary, ".")
		build.Dir = root
		build.Stdout = cmd.OutOrStdout()
		build.Stderr = cmd.ErrOrStderr()
		if err := build.Run(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "build failed:", err)
			return
		}

		child = exec.Command(binary)
		child.Dir = root
		child.Stdout = cmd.OutOrStdout()
		child.Stderr = cmd.ErrOrStderr()
		child.Env = os.Environ()
		if err := child.Start(); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "start failed:", err)
			child = nil
			return
		}
		fmt.Fprintln(cmd.OutOrStdout(), "running", child.Process.Pid)
	}

	buildAndStart()

	var timer *time.Timer
	reload := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(300*time.Millisecond, buildAndStart)
	}

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			stopChild(child)
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				stopChild(child)
				return nil
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = addDirs(watcher, event.Name)
				}
			}
			if watched(event.Name) {
				reload()
			}

		case err, ok := <-watcher.Errors:
			if ok && err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "watch:", err)
			}
		}
	}
}

func addDirs(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && ignoredDir(entry.Name()) {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

func ignoredDir(name string) bool {
	switch name {
	case ".git", ".idea", ".gable-dev", "bin", "logs", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func watched(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") {
		return false
	}
	switch filepath.Ext(path) {
	case ".go", ".yaml", ".yml", ".json", ".tmpl", ".html":
		return true
	default:
		return false
	}
}

func stopChild(child *exec.Cmd) {
	if child == nil || child.Process == nil {
		return
	}
	_ = child.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = child.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = child.Process.Kill()
		<-done
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
