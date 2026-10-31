package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunNewScaffoldsABuildableApp(t *testing.T) {
	gablePath := findGablePath()
	if gablePath == "" {
		t.Skip("not running inside the gable module")
	}

	target := filepath.Join(t.TempDir(), "demo")
	cmd := newNewCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	err := runNew(cmd, target, &newOptions{gablePath: gablePath})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"go.mod", "main.go", "gable.yaml", "README.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	modData, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"module example.com/demo", "replace github.com/maxid/gable =>"} {
		if !bytes.Contains(modData, []byte(want)) {
			t.Fatalf("go.mod missing %q:\n%s", want, modData)
		}
	}

	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = target
	tidy.Env = append(os.Environ(), "GOWORK=off")
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("scaffold tidy failed: %v\n%s", err, output)
	}

	build := exec.Command("go", "build", "./...")
	build.Dir = target
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("scaffold does not build: %v\n%s", err, output)
	}
}

func TestRunNewRejectsNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newNewCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := runNew(cmd, dir, &newOptions{gablePath: t.TempDir()}); err == nil {
		t.Fatal("expected non-empty directory error")
	}
}

func TestWatched(t *testing.T) {
	cases := map[string]bool{
		"main.go":     true,
		"gable.yaml":  true,
		"config.json": true,
		".hidden.go":  false,
		"backup.go~":  false,
		"README.md":   false,
	}
	for path, want := range cases {
		if got := watched(path); got != want {
			t.Fatalf("watched(%q) = %v, want %v", path, got, want)
		}
	}
}
