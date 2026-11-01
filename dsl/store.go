package dsl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Set is a loaded, validated collection of resources.
type Set struct {
	Models  map[string]*ModelDoc
	Tables  map[string]*TableDoc
	Forms   map[string]*FormDoc
	Pages   map[string]*PageDoc
	Sources map[string]string // resource key (Kind/name) -> file path
}

// NewSet returns an empty set.
func NewSet() *Set {
	return &Set{
		Models:  map[string]*ModelDoc{},
		Tables:  map[string]*TableDoc{},
		Forms:   map[string]*FormDoc{},
		Pages:   map[string]*PageDoc{},
		Sources: map[string]string{},
	}
}

// LoadDir walks dir and loads .yaml/.yml/.json resources.
func LoadDir(dir string) (*Set, Issues, error) {
	set := NewSet()
	var issues Issues
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "node_modules" || name == "logs" || name == ".gable" {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".yaml", ".yml", ".json":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, Issue{Resource: path, Message: err.Error()})
			return nil
		}
		// A project keeps its application config next to its DSL files, so "this file
		// is not ours" must not be an error. Only a file that claims a gable envelope
		// and gets it wrong is reported.
		if !isDSLDocument(data) {
			return nil
		}
		loaded, loadIssues := LoadBytes(path, data)
		issues = append(issues, loadIssues...)
		for key, doc := range loaded.Models {
			if _, exists := set.Models[doc.Metadata.Name]; exists {
				issues = append(issues, Issue{Resource: key, Message: "duplicate model name"})
				continue
			}
			set.Models[doc.Metadata.Name] = doc
			set.Sources[key] = path
		}
		for key, doc := range loaded.Tables {
			if _, exists := set.Tables[doc.Metadata.Name]; exists {
				issues = append(issues, Issue{Resource: key, Message: "duplicate table name"})
				continue
			}
			set.Tables[doc.Metadata.Name] = doc
			set.Sources[key] = path
		}
		for key, doc := range loaded.Forms {
			if _, exists := set.Forms[doc.Metadata.Name]; exists {
				issues = append(issues, Issue{Resource: key, Message: "duplicate form name"})
				continue
			}
			set.Forms[doc.Metadata.Name] = doc
			set.Sources[key] = path
		}
		for key, doc := range loaded.Pages {
			if _, exists := set.Pages[doc.Metadata.Name]; exists {
				issues = append(issues, Issue{Resource: key, Message: "duplicate page name"})
				continue
			}
			set.Pages[doc.Metadata.Name] = doc
			set.Sources[key] = path
		}
		return nil
	})
	if err != nil {
		return nil, issues, err
	}
	return set, issues, nil
}

// LoadBytes loads one resource.
func LoadBytes(name string, data []byte) (*Set, Issues) {
	set := NewSet()
	var head header
	if err := yaml.Unmarshal(data, &head); err != nil {
		return set, Issues{{Resource: name, Message: "parse: " + err.Error()}}
	}
	if head.APIVersion != APIVersion {
		return set, Issues{{Resource: name, Message: fmt.Sprintf("apiVersion must be %s", APIVersion)}}
	}
	if strings.TrimSpace(head.Metadata.Name) == "" {
		return set, Issues{{Resource: name, Message: "metadata.name is required"}}
	}

	switch head.Kind {
	case KindModel:
		var doc ModelDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return set, Issues{{Resource: name, Message: "parse model: " + err.Error()}}
		}
		set.Models[doc.Metadata.Name] = &doc
	case KindTable:
		var doc TableDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return set, Issues{{Resource: name, Message: "parse table: " + err.Error()}}
		}
		set.Tables[doc.Metadata.Name] = &doc
	case KindForm:
		var doc FormDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return set, Issues{{Resource: name, Message: "parse form: " + err.Error()}}
		}
		set.Forms[doc.Metadata.Name] = &doc
	case KindPage:
		var doc PageDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return set, Issues{{Resource: name, Message: "parse page: " + err.Error()}}
		}
		set.Pages[doc.Metadata.Name] = &doc
	default:
		return set, Issues{{Resource: name, Message: fmt.Sprintf("unsupported kind %q", head.Kind)}}
	}
	return set, nil
}

// isDSLDocument reports whether the file declares a gable envelope. A wrong version
// still counts as ours: that is a bug the author must see, not a file to skip.
func isDSLDocument(data []byte) bool {
	var head struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &head); err != nil {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(head.APIVersion), "gable/") {
		return true
	}
	// A file that names one of our kinds is ours even with the wrong apiVersion: the
	// author meant a resource, and the error must say so.
	switch Kind(strings.TrimSpace(head.Kind)) {
	case KindModel, KindTable, KindForm, KindPage:
		return true
	default:
		return false
	}
}

// Resource returns one resource by kind and name.
func (s *Set) Resource(kind Kind, name string) (any, bool) {
	switch kind {
	case KindModel:
		doc, ok := s.Models[name]
		return doc, ok
	case KindTable:
		doc, ok := s.Tables[name]
		return doc, ok
	case KindForm:
		doc, ok := s.Forms[name]
		return doc, ok
	case KindPage:
		doc, ok := s.Pages[name]
		return doc, ok
	default:
		return nil, false
	}
}

// ApplyDir writes normalized resources to out. Files whose content is already
// identical are left untouched, so applying twice is idempotent.
func (s *Set) ApplyDir(out string) ([]string, error) {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return nil, err
	}
	var written []string

	models := sortedKeys(s.Models)
	for _, name := range models {
		path, changed, err := writeYAML(out, string(KindModel)+"-"+name+".yaml", s.Models[name])
		if err != nil {
			return written, err
		}
		if changed {
			written = append(written, path)
		}
	}
	tables := sortedKeys(s.Tables)
	for _, name := range tables {
		path, changed, err := writeYAML(out, string(KindTable)+"-"+name+".yaml", s.Tables[name])
		if err != nil {
			return written, err
		}
		if changed {
			written = append(written, path)
		}
	}
	forms := sortedKeys(s.Forms)
	for _, name := range forms {
		path, changed, err := writeYAML(out, string(KindForm)+"-"+name+".yaml", s.Forms[name])
		if err != nil {
			return written, err
		}
		if changed {
			written = append(written, path)
		}
	}
	pages := sortedKeys(s.Pages)
	for _, name := range pages {
		path, changed, err := writeYAML(out, string(KindPage)+"-"+name+".yaml", s.Pages[name])
		if err != nil {
			return written, err
		}
		if changed {
			written = append(written, path)
		}
	}
	return written, nil
}

func writeYAML(dir, file string, value any) (string, bool, error) {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, file)
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(raw) {
		return path, false, nil
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return path, false, err
	}
	return path, true, nil
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
