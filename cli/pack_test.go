package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maxid/gable/auth"
)

func packFixture(t *testing.T) (dir string, in packInputs) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gable.yaml"), []byte("app: {name: demo}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dslDir := filepath.Join(dir, "resources")
	if err := os.MkdirAll(dslDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dslDir, "task.model.yaml"), []byte("apiVersion: gable/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	assets := filepath.Join(dir, "public")
	if err := os.MkdirAll(filepath.Join(assets, "css"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "css", "app.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir, packInputs{
		Config: filepath.Join(dir, "gable.yaml"),
		DSL:    dslDir,
		Assets: []string{assets},
		App:    "demo",
		Now:    func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}
}

// Pack then restore reproduces the layout a running application expects, and every file is
// verified on the way in.
func TestPackRestoreRoundTrip(t *testing.T) {
	dir, in := packFixture(t)
	archive := filepath.Join(dir, "bundle.tar.gz")

	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := writeBundle(file, in)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()

	if len(manifest.Files) != 3 || manifest.Version == "" || manifest.App != "demo" {
		t.Fatalf("manifest = %+v", manifest)
	}
	paths := make([]string, 0, len(manifest.Files))
	for _, entry := range manifest.Files {
		paths = append(paths, entry.Path)
		if entry.SHA256 == "" {
			t.Fatalf("entry has no checksum: %+v", entry)
		}
	}
	if strings.Join(paths, ",") != "dsl/task.model.yaml,gable.yaml,public/css/app.css" {
		t.Fatalf("paths = %v", paths)
	}

	out := t.TempDir()
	restored, err := restoreBundle(archive, out, false)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != manifest.Version {
		t.Fatalf("restored version = %q, want %q", restored.Version, manifest.Version)
	}
	for _, entry := range manifest.Files {
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatalf("%s: %v", entry.Path, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != entry.SHA256 {
			t.Fatalf("%s checksum = %s, want %s", entry.Path, got, entry.SHA256)
		}
	}
}

// The content hash is deterministic: same inputs, same version, so two bundles are comparable
// even when they were packed at different times.
func TestPackVersionIsContentDeterministic(t *testing.T) {
	_, in := packFixture(t)
	var first, second bytes.Buffer
	if _, err := writeBundle(&first, in); err != nil {
		t.Fatal(err)
	}
	if _, err := writeBundle(&second, in); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("two packs of the same inputs differ")
	}
	// The timestamp lives in the archive; the version must not depend on it.
	later := in
	later.Now = func() time.Time { return time.Unix(1_900_000_000, 0).UTC() }
	var shifted bytes.Buffer
	manifest, err := writeBundle(&shifted, later)
	if err != nil {
		t.Fatal(err)
	}
	var original bytes.Buffer
	firstManifest, err := writeBundle(&original, in)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != firstManifest.Version {
		t.Fatalf("version changed with the clock: %q vs %q", manifest.Version, firstManifest.Version)
	}
	if bytes.Equal(shifted.Bytes(), original.Bytes()) {
		t.Fatal("the archive should still record when it was packed")
	}

	// A single changed byte changes the version.
	dir := t.TempDir()
	path := filepath.Join(dir, "gable.yaml")
	if err := os.WriteFile(path, []byte("app: {name: other}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in.Config = path
	var third bytes.Buffer
	changed, err := writeBundle(&third, in)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Version == "" || bytes.Equal(first.Bytes(), third.Bytes()) {
		t.Fatal("changing the config must change the bundle")
	}
}

// A corrupted archive is refused before anything is written: a half-restored application is
// harder to diagnose than a refusal.
func TestRestoreRefusesCorruption(t *testing.T) {
	dir, in := packFixture(t)
	archive := filepath.Join(dir, "bundle.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeBundle(file, in); err != nil {
		t.Fatal(err)
	}
	file.Close()

	// Flip a byte inside the compressed stream: the checksum check must catch it, and the
	// gzip/CRC failure is an acceptable way to catch it too — either way nothing is written.
	raw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xff
	corrupt := filepath.Join(dir, "corrupt.tar.gz")
	if err := os.WriteFile(corrupt, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if _, err := restoreBundle(corrupt, out, false); err == nil {
		t.Fatal("a corrupted bundle must be refused")
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("restore wrote %d entries from a corrupt bundle", len(entries))
	}
}

func TestRestoreRefusesANonEmptyDirectory(t *testing.T) {
	dir, in := packFixture(t)
	archive := filepath.Join(dir, "bundle.tar.gz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeBundle(file, in); err != nil {
		t.Fatal(err)
	}
	file.Close()

	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreBundle(archive, out, false); err == nil ||
		!strings.Contains(err.Error(), "not empty") {
		t.Fatalf("err = %v", err)
	}
	if _, err := restoreBundle(archive, out, true); err != nil {
		t.Fatalf("--force: %v", err)
	}
}

func TestPackWithNothingToInclude(t *testing.T) {
	_, err := writeBundle(&bytes.Buffer{}, packInputs{Now: time.Now})
	if err == nil || !strings.Contains(err.Error(), "nothing to include") {
		t.Fatalf("err = %v", err)
	}
}

// A token minted by the CLI is the token the application verifies: same secret, same issuer,
// same TTL.
func TestTokenCommandMintsAVerifiableToken(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gable.yaml")
	if err := os.WriteFile(configPath, []byte("app: {name: demo}\ntenant: {mode: single, root_id: 7}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newTokenCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", configPath, "--secret", "test-secret", "--username", "ada", "--ttl", "5m"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(out.String())
	if token == "" || strings.Contains(token, " ") {
		t.Fatalf("token output = %q", out.String())
	}

	service, err := auth.NewTokenService(auth.Config{Issuer: "demo", Secret: "test-secret"}, nil, nil, auth.NewMemoryTokenStore())
	if err != nil {
		t.Fatal(err)
	}
	principal, err := service.Verify(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Username != "ada" || principal.TenantID != 7 {
		t.Fatalf("principal = %+v", principal)
	}
	// --refresh prints a second line.
	cmd = newTokenCommand()
	out.Reset()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--config", configPath, "--secret", "test-secret", "--subject", "1", "--refresh"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Fields(out.String()); len(lines) != 2 {
		t.Fatalf("lines = %v", lines)
	}

	if _, err := newTokenCommandErr("--config", configPath, "--username", "ada"); err == nil ||
		!strings.Contains(err.Error(), "--secret") {
		t.Fatalf("err = %v", err)
	}
}

// newTokenCommandErr runs the token command and returns its error, for the failure cases.
func newTokenCommandErr(args ...string) (string, error) {
	cmd := newTokenCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}
