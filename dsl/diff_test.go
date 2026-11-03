package dsl

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/maxid/gable/orm"
)

func TestDiffResources(t *testing.T) {
	documents, set := validDocuments(t)

	// Nothing stored: everything is an addition.
	fresh, err := DiffResources(set, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Added() != 4 || fresh.Changed() != 0 || fresh.Removed() != 0 {
		t.Fatalf("fresh diff = %s", fresh)
	}

	// Exactly what is stored: no changes, and diff agrees with Save's definition.
	same, err := DiffResources(set, documents)
	if err != nil {
		t.Fatal(err)
	}
	if !same.Empty() {
		t.Fatalf("identical diff = %s", same)
	}

	// One document drifted and one stored resource is an orphan.
	stored := map[string]string{}
	for key, document := range documents {
		stored[key] = document
	}
	stored["Model/task"] = strings.Replace(stored["Model/task"], "title: Task", "title: Old", 1)
	stored["Model/retired"] = strings.Replace(documents["Model/task"], "name: task", "name: retired", 1)

	changed, err := DiffResources(set, stored)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Changed() != 1 || changed.Removed() != 1 || changed.Added() != 0 {
		t.Fatalf("diff = %s (%+v)", changed, changed.Changes)
	}
	byKey := map[string]Change{}
	for _, change := range changed.Changes {
		byKey[change.Key()] = change
	}
	if change := byKey["Model/task"]; change.Action != ChangeChanged {
		t.Fatalf("Model/task = %+v", change)
	}
	if change := byKey["Model/retired"]; change.Action != ChangeRemoved {
		t.Fatalf("Model/retired = %+v", change)
	}

	// The line diff explains the change: the old line goes, the new one arrives.
	lines := byKey["Model/task"].Lines
	var removedTitle, addedTitle bool
	for _, line := range lines {
		if line.Op == "-" && strings.Contains(line.Text, "title: Old") {
			removedTitle = true
		}
		if line.Op == "+" && strings.Contains(line.Text, "title: Task") {
			addedTitle = true
		}
	}
	if !removedTitle || !addedTitle {
		t.Fatalf("lines = %+v, want the title line replaced", lines)
	}
}

func TestDiffLinesKeepsUnchangedLinesAsContext(t *testing.T) {
	lines := diffLines("a\nb\nc\n", "a\nB\nc\n")
	want := []DiffLine{
		{Op: " ", Text: "a"},
		{Op: "-", Text: "b"},
		{Op: "+", Text: "B"},
		{Op: " ", Text: "c"},
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %+v", lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines = %+v, want %+v", lines, want)
		}
	}
	// A whole-file addition has no context and no removals.
	added := diffLines("", "a\nb\n")
	if len(added) != 2 || added[0].Op != "+" || added[1].Op != "+" {
		t.Fatalf("added = %+v", added)
	}
	if none := diffLines("", ""); len(none) != 0 {
		t.Fatalf("empty diff = %+v", none)
	}
}

// The dry-run reads what is stored and writes nothing.
func TestDBStoreDiffReadsWithoutWriting(t *testing.T) {
	documents, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.MySQL())

	rows := sqlmock.NewRows([]string{"kind", "name", "document"})
	stored := map[string]string{
		"Model/task":       documents["Model/task"],
		"Table/task.table": documents["Table/task.table"],
		"Form/task.form":   strings.Replace(documents["Form/task.form"], "title: Task form", "title: Old form", 1),
	}
	for key, document := range stored {
		kind, name, _ := strings.Cut(key, "/")
		rows.AddRow(kind, name, document)
	}
	mock.ExpectQuery("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?").
		WithArgs("gable_dsl_resource").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(rows)

	diff, err := store.Diff(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Added() != 1 || diff.Changed() != 1 {
		t.Fatalf("diff = %s (%+v)", diff, diff.Changes)
	}
	// No ExpectBegin/ExpectExec: sqlmock fails the test if anything was written.
}

// A database that was never migrated holds nothing, so a first dry-run reports every
// resource as an addition instead of failing on a missing table.
func TestDBStoreDiffOnAnUnmigratedDatabase(t *testing.T) {
	_, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.Postgres())

	mock.ExpectQuery("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1").
		WithArgs("gable_dsl_resource").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))

	diff, err := store.Diff(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Added() != 4 || diff.Changed() != 0 || diff.Removed() != 0 {
		t.Fatalf("diff = %s (%+v)", diff, diff.Changes)
	}
}

func TestDBStoreHistory(t *testing.T) {
	store, mock := newFixedStore(t, orm.MySQL())
	mock.ExpectQuery("SELECT `revision`, `document`, `created_at` FROM `gable_dsl_revision` "+
		"WHERE `kind` = ? AND `name` = ? ORDER BY `revision` DESC").
		WithArgs("Model", "task").
		WillReturnRows(sqlmock.NewRows([]string{"revision", "document", "created_at"}).
			AddRow(int64(2), "second", fixedClock).
			AddRow(int64(1), "first", fixedClock))

	revisions, err := store.History(context.Background(), KindModel, "task")
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0].Revision != 2 || revisions[0].Document != "second" {
		t.Fatalf("revisions = %+v", revisions)
	}
	if revisions[0].Kind != KindModel || revisions[0].Name != "task" {
		t.Fatalf("revisions = %+v", revisions)
	}
}

// Restoring writes the historical document as a new revision: history is append-only.
func TestDBStoreRestore(t *testing.T) {
	documents, _ := validDocuments(t)
	store, mock := newFixedStore(t, orm.MySQL())
	old := strings.Replace(documents["Model/task"], "title: Task", "title: Older", 1)

	mock.ExpectQuery("SELECT `document` FROM `gable_dsl_revision` WHERE `kind` = ? AND `name` = ? AND `revision` = ?").
		WithArgs("Model", "task", int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(old))
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}).AddRow("Model", "task", documents["Model/task"]))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `gable_dsl_resource` (`kind`, `name`, `document`, `revision`, `updated_at`) VALUES (?, ?, ?, ?, ?) "+
		"ON DUPLICATE KEY UPDATE `document` = VALUES(`document`), `revision` = `revision` + 1, `updated_at` = VALUES(`updated_at`)").
		WithArgs("Model", "task", old, int64(1), fixedClock).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT `revision` FROM `gable_dsl_resource` WHERE `kind` = ? AND `name` = ?").
		WithArgs("Model", "task").
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(8)))
	mock.ExpectExec("INSERT INTO `gable_dsl_revision` (`kind`, `name`, `revision`, `document`, `created_at`) VALUES (?, ?, ?, ?, ?)").
		WithArgs("Model", "task", int64(8), old, fixedClock).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	key, err := store.Restore(context.Background(), KindModel, "task", 3)
	if err != nil {
		t.Fatal(err)
	}
	if key != "Model/task" {
		t.Fatalf("restored = %q", key)
	}

	// Restoring what is already current is a no-op, not another revision.
	mock.ExpectQuery("SELECT `document` FROM `gable_dsl_revision` WHERE `kind` = ? AND `name` = ? AND `revision` = ?").
		WithArgs("Model", "task", int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(old))
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}).AddRow("Model", "task", old))
	if key, err := store.Restore(context.Background(), KindModel, "task", 3); err != nil || key != "" {
		t.Fatalf("no-op restore = %q, %v", key, err)
	}

	// A revision that is not there, and a stored document that does not parse, are both
	// errors rather than partial writes.
	mock.ExpectQuery("SELECT `document` FROM `gable_dsl_revision` WHERE `kind` = ? AND `name` = ? AND `revision` = ?").
		WithArgs("Model", "task", int64(99)).
		WillReturnRows(sqlmock.NewRows([]string{"document"}))
	if _, err := store.Restore(context.Background(), KindModel, "task", 99); err == nil ||
		!strings.Contains(err.Error(), "no revision 99") {
		t.Fatalf("missing revision = %v", err)
	}

	mock.ExpectQuery("SELECT `document` FROM `gable_dsl_revision` WHERE `kind` = ? AND `name` = ? AND `revision` = ?").
		WithArgs("Model", "task.table", int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(tableYAML))
	if _, err := store.Restore(context.Background(), KindModel, "task.table", 1); err == nil ||
		!strings.Contains(err.Error(), "declares Table/task.table") {
		t.Fatalf("misfiled revision = %v", err)
	}
}
