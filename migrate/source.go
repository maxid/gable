package migrate

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// LoadDir loads migrations from a directory on disk.
func LoadDir(dir string) ([]Migration, error) { return LoadFS(os.DirFS(dir)) }

var filenameRE = regexp.MustCompile(`^V([0-9][0-9.]*)__(.+)\.sql$`)

// LoadFS walks fsys, parses V<version>__<description>.sql files and returns
// them ordered by version then script. Versions are unique across the tree,
// matching Flyway's one-history-table-per-schema model.
func LoadFS(fsys fs.FS) ([]Migration, error) {
	var out []Migration
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".sql") {
			return nil
		}
		script := path.Clean(p)
		name := path.Base(script)
		m := filenameRE.FindStringSubmatch(name)
		if m == nil {
			return fmt.Errorf("migration %s: invalid name (want V<version>__<description>.sql)", script)
		}
		version, description := m[1], m[2]
		if err := validateVersion(version); err != nil {
			return fmt.Errorf("migration %s: %w", script, err)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		out = append(out, Migration{
			Version:     version,
			Description: description,
			Script:      script,
			Type:        "SQL",
			Checksum:    Checksum(data),
			SQL:         data,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if c := VersionCompare(out[i].Version, out[j].Version); c != 0 {
			return c < 0
		}
		return out[i].Script < out[j].Script
	})
	seen := map[string]string{}
	for _, m := range out {
		if prev, ok := seen[m.Version]; ok {
			return nil, fmt.Errorf("duplicate version %s: %s and %s", m.Version, prev, m.Script)
		}
		seen[m.Version] = m.Script
	}
	return out, nil
}

// Checksum computes Flyway's checksum: CRC32 over each UTF-8 line without its
// line ending. The result is cast to int32, exactly as Flyway stores it, so an
// existing Flyway history row keeps the same checksum.
func Checksum(data []byte) int32 {
	h := crc32.NewIEEE()
	reader := bufio.NewReader(bytes.NewReader(data))
	first := true
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")
			if first {
				line = strings.TrimPrefix(line, "\uFEFF")
				first = false
			}
			_, _ = h.Write([]byte(line))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return int32(h.Sum32())
		}
	}
	return int32(h.Sum32())
}
