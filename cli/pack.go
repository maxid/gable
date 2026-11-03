package cli

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/maxid/gable"
)

// packManifest is the index inside a bundle.
//
// It carries a checksum per file so restore can refuse a corrupted archive instead of writing
// half an application, and a version so an operator can tell two bundles apart without opening
// them.
type packManifest struct {
	Version   string      `json:"version"`
	App       string      `json:"app,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	Files     []packEntry `json:"files"`
}

type packEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// packManifestPath is the fixed name of the index inside the archive.
const packManifestPath = "bundle.json"

func newPackCommand() *cobra.Command {
	var configPath, dir, out, app string
	var assets []string
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "bundle configuration, DSL resources and static assets into one archive",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bundle := packInputs{
				Config: configPath,
				DSL:    dir,
				Assets: assets,
				App:    app,
				Now:    time.Now,
			}
			if out == "" {
				out = "bundle.tar.gz"
			}
			file, err := os.Create(out)
			if err != nil {
				return err
			}
			defer file.Close()

			manifest, err := writeBundle(file, bundle)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s: %d files, sha256:%s\n", out, len(manifest.Files), shortSHA(manifest.Version))
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "gable.yaml", "configuration file to include")
	cmd.Flags().StringVar(&dir, "dir", "", "DSL resource directory to include")
	cmd.Flags().StringArrayVar(&assets, "assets", nil, "additional directory to include, repeatable")
	cmd.Flags().StringVar(&out, "out", "bundle.tar.gz", "archive to write")
	cmd.Flags().StringVar(&app, "app", "", "application name recorded in the bundle (defaults to the config's)")
	return cmd
}

// packInputs is what goes into a bundle.
type packInputs struct {
	Config string
	DSL    string
	Assets []string
	App    string
	Now    func() time.Time
}

// writeBundle writes the archive and returns its manifest.
//
// The archive is content-deterministic: entries are sorted, mtimes are zero and ownership is
// dropped, so the same inputs produce the same file order and the same version hash. The
// archive itself also records when it was made, which is why two packs an hour apart are not
// byte-identical — the version is, and that is what a deployment compares.
func writeBundle(w io.Writer, in packInputs) (packManifest, error) {
	files, err := collectBundleFiles(in)
	if err != nil {
		return packManifest{}, err
	}

	manifest := packManifest{
		CreatedAt: in.Now().UTC(),
		App:       in.App,
		Files:     make([]packEntry, 0, len(files)),
	}
	for _, file := range files {
		manifest.Files = append(manifest.Files, packEntry{Path: file.path, Size: int64(len(file.data)), SHA256: sha256Hex(file.data)})
	}
	// The bundle version is a hash of its contents: two packs of the same inputs share it, and
	// any change to a file changes it.
	version, err := bundleVersion(manifest.Files)
	if err != nil {
		return packManifest{}, err
	}
	manifest.Version = version

	index, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return packManifest{}, err
	}

	gzipWriter := gzip.NewWriter(w)
	tarWriter := tar.NewWriter(gzipWriter)
	write := func(name string, data []byte) error {
		header := &tar.Header{
			Name:    name,
			Mode:    0o644,
			Size:    int64(len(data)),
			ModTime: time.Time{},
			// Ownership and the user/group names are machine-specific; dropping them keeps
			// the archive identical across machines.
			Uid: 0, Gid: 0, Uname: "", Gname: "",
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		_, err := tarWriter.Write(data)
		return err
	}
	if err := write(packManifestPath, index); err != nil {
		return packManifest{}, err
	}
	for _, file := range files {
		if err := write(file.path, file.data); err != nil {
			return packManifest{}, err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return packManifest{}, err
	}
	if err := gzipWriter.Close(); err != nil {
		return packManifest{}, err
	}
	return manifest, nil
}

type bundleFile struct {
	path string
	data []byte
}

// collectBundleFiles reads the inputs and maps them to archive paths.
//
// The config goes to its own name, DSL resources keep their directory, and assets land under
// their base name, so restoring into a directory reproduces the layout a running application
// expects.
func collectBundleFiles(in packInputs) ([]bundleFile, error) {
	var files []bundleFile
	add := func(path, prefix string) error {
		walkErr := filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			data, err := os.ReadFile(current)
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(path, current)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(filepath.Join(prefix, relative))
			files = append(files, bundleFile{path: name, data: data})
			return nil
		})
		return walkErr
	}

	if in.Config != "" {
		data, err := os.ReadFile(in.Config)
		if err != nil {
			return nil, fmt.Errorf("pack: read config: %w", err)
		}
		files = append(files, bundleFile{path: filepath.Base(in.Config), data: data})
	}
	if in.DSL != "" {
		if err := add(in.DSL, "dsl"); err != nil {
			return nil, fmt.Errorf("pack: dsl: %w", err)
		}
	}
	for _, asset := range in.Assets {
		if err := add(asset, filepath.Base(asset)); err != nil {
			return nil, fmt.Errorf("pack: assets %s: %w", asset, err)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("pack: nothing to include (pass --config, --dir or --assets)")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

// bundleVersion hashes the file list, so the version answers "are these two bundles the same".
func bundleVersion(entries []packEntry) (string, error) {
	hasher := sha256.New()
	for _, entry := range entries {
		if _, err := fmt.Fprintf(hasher, "%s\x00%d\x00%s\n", entry.Path, entry.Size, entry.SHA256); err != nil {
			return "", err
		}
	}
	hasher.Write([]byte("gable:" + gable.Version))
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func shortSHA(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func newRestoreCommand() *cobra.Command {
	var out string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <bundle>",
		Short: "verify a bundle and extract it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				out = "."
			}
			manifest, err := restoreBundle(args[0], out, force)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "restored sha256:%s (%s): %d files into %s\n",
				shortSHA(manifest.Version), manifest.App, len(manifest.Files), out)
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", ".", "directory to extract into")
	cmd.Flags().BoolVar(&force, "force", false, "extract even when the target directory is not empty")
	return cmd
}

// restoreBundle verifies every file before writing any of them.
//
// Verification first is the point: an archive with one corrupted file must fail without leaving
// a half-restored application behind, because that state is harder to diagnose than a refusal.
func restoreBundle(path, out string, force bool) (packManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return packManifest{}, err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return packManifest{}, fmt.Errorf("restore: %w", err)
	}
	defer gzipReader.Close()

	reader := tar.NewReader(gzipReader)
	contents := map[string][]byte{}
	var manifest packManifest
	found := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return packManifest{}, fmt.Errorf("restore: %w", err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			return packManifest{}, fmt.Errorf("restore: read %s: %w", header.Name, err)
		}
		if header.Name == packManifestPath {
			if err := json.Unmarshal(data, &manifest); err != nil {
				return packManifest{}, fmt.Errorf("restore: bundle index: %w", err)
			}
			found = true
			continue
		}
		contents[header.Name] = data
	}
	if !found {
		return packManifest{}, fmt.Errorf("restore: %s has no %s; not a gable bundle", path, packManifestPath)
	}

	var problems []string
	for _, entry := range manifest.Files {
		data, ok := contents[entry.Path]
		if !ok {
			problems = append(problems, entry.Path+": missing from the archive")
			continue
		}
		if actual := sha256Hex(data); actual != entry.SHA256 {
			problems = append(problems, fmt.Sprintf("%s: checksum %s, want %s", entry.Path, shortSHA(actual), shortSHA(entry.SHA256)))
		}
	}
	if len(problems) > 0 {
		return packManifest{}, fmt.Errorf("restore: %s is corrupt:\n  %s", path, strings.Join(problems, "\n  "))
	}

	if !force {
		entries, err := os.ReadDir(out)
		if err != nil && !os.IsNotExist(err) {
			return packManifest{}, err
		}
		if len(entries) > 0 {
			return packManifest{}, fmt.Errorf("restore: %s is not empty (use --force to extract anyway)", out)
		}
	}
	for _, entry := range manifest.Files {
		target := filepath.Join(out, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return packManifest{}, err
		}
		if err := os.WriteFile(target, contents[entry.Path], 0o644); err != nil {
			return packManifest{}, err
		}
	}
	return manifest, nil
}
