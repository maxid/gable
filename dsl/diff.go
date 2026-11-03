package dsl

import (
	"fmt"
	"sort"
	"strings"
)

// ChangeAction is what happened to one resource between two revisions of a set.
type ChangeAction string

const (
	// ChangeAdded is a resource the target does not have.
	ChangeAdded ChangeAction = "added"
	// ChangeRemoved is a resource the target has and the source does not.
	ChangeRemoved ChangeAction = "removed"
	// ChangeChanged is a resource both sides have with different documents.
	ChangeChanged ChangeAction = "changed"
)

// DiffLine is one line of a document diff. Op is " " for context, "-" for a line the
// target has and "+" for a line it would gain.
type DiffLine struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

// Change is one resource-level difference, with the line diff that explains it.
type Change struct {
	Kind   Kind         `json:"kind"`
	Name   string       `json:"name"`
	Action ChangeAction `json:"action"`
	Lines  []DiffLine   `json:"lines,omitempty"`
}

// Key names the resource the way issues and CLI output do.
func (c Change) Key() string { return resourceKey(c.Kind, c.Name) }

// Diff is the difference between a desired set and what is stored.
type Diff struct {
	Changes []Change `json:"changes"`
}

// Empty reports whether the two sides are identical.
func (d Diff) Empty() bool { return len(d.Changes) == 0 }

// Added, Removed and Changed count the changes by action.
func (d Diff) Added() int   { return d.count(ChangeAdded) }
func (d Diff) Removed() int { return d.count(ChangeRemoved) }
func (d Diff) Changed() int { return d.count(ChangeChanged) }

func (d Diff) count(action ChangeAction) int {
	total := 0
	for _, change := range d.Changes {
		if change.Action == action {
			total++
		}
	}
	return total
}

// String renders a short summary, for CLI output and log lines.
func (d Diff) String() string {
	if d.Empty() {
		return "no changes"
	}
	return fmt.Sprintf("%d added, %d changed, %d removed", d.Added(), d.Changed(), d.Removed())
}

// DiffResources compares a desired set with documents already stored, keyed like
// Set.Sources.
//
// It is the read-only half of Save and shares its notion of "changed": the same normalized
// YAML, the same key order. A resource the desired set no longer has is reported, because
// Save never removes anything — a diff is where an operator finds the orphans.
func DiffResources(desired *Set, stored map[string]string) (Diff, error) {
	if desired == nil {
		return Diff{}, fmt.Errorf("dsl: nil set")
	}
	var changes []Change

	seen := map[string]bool{}
	for _, item := range desired.resources() {
		key := resourceKey(item.Kind, item.Name)
		seen[key] = true
		document, err := marshalResource(item.Doc)
		if err != nil {
			return Diff{}, fmt.Errorf("dsl: %s: %w", key, err)
		}
		current, ok := stored[key]
		switch {
		case !ok:
			changes = append(changes, Change{
				Kind: item.Kind, Name: item.Name, Action: ChangeAdded,
				Lines: diffLines("", string(document)),
			})
		case current != string(document):
			changes = append(changes, Change{
				Kind: item.Kind, Name: item.Name, Action: ChangeChanged,
				Lines: diffLines(current, string(document)),
			})
		}
	}

	// Orphans first, by key, so the output is stable regardless of map order.
	var removed []string
	for key := range stored {
		if !seen[key] {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	for _, key := range removed {
		kind, name, ok := splitStoredKey(key)
		if !ok {
			// A row whose key is not Kind/name cannot be described; it is still an
			// orphan, so it is reported with an empty name rather than dropped.
			changes = append(changes, Change{Action: ChangeRemoved, Name: key})
			continue
		}
		changes = append(changes, Change{
			Kind: kind, Name: name, Action: ChangeRemoved,
			Lines: diffLines(stored[key], ""),
		})
	}
	return Diff{Changes: changes}, nil
}

func splitStoredKey(key string) (Kind, string, bool) {
	kind, name, ok := strings.Cut(key, "/")
	if !ok || name == "" {
		return "", "", false
	}
	return Kind(kind), name, true
}

// diffLines renders a line diff between two documents. Documents are small YAML, so the
// straightforward longest-common-subsequence table is both fast enough and easy to read;
// nothing here is in a request path.
func diffLines(current, desired string) []DiffLine {
	left := splitLines(current)
	right := splitLines(desired)

	table := make([][]int, len(left)+1)
	for i := range table {
		table[i] = make([]int, len(right)+1)
	}
	for i := len(left) - 1; i >= 0; i-- {
		for j := len(right) - 1; j >= 0; j-- {
			if left[i] == right[j] {
				table[i][j] = table[i+1][j+1] + 1
				continue
			}
			if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
				continue
			}
			table[i][j] = table[i][j+1]
		}
	}

	lines := make([]DiffLine, 0, len(left)+len(right))
	for i, j := 0, 0; i < len(left) || j < len(right); {
		switch {
		case i < len(left) && j < len(right) && left[i] == right[j]:
			lines = append(lines, DiffLine{Op: " ", Text: left[i]})
			i++
			j++
		// Removals come before additions when the two choices are equally good, which is
		// what readers expect from a unified diff.
		case j < len(right) && (i >= len(left) || table[i][j+1] > table[i+1][j]):
			lines = append(lines, DiffLine{Op: "+", Text: right[j]})
			j++
		default:
			lines = append(lines, DiffLine{Op: "-", Text: left[i]})
			i++
		}
	}
	return lines
}

// splitLines keeps a trailing newline from inventing an empty last line.
func splitLines(document string) []string {
	if document == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(document, "\n"), "\n")
}
