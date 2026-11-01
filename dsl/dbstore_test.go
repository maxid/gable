package dsl

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/maxid/gable/orm"
)

var fixedClock = time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)

func newStoreDB(t *testing.T, dialect orm.Dialect) (*orm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = sqlDB.Close()
	})
	db, err := orm.New(sqlx.NewDb(sqlDB, dialect.Name()), dialect)
	if err != nil {
		t.Fatal(err)
	}
	return db, mock
}

func newFixedStore(t *testing.T, dialect orm.Dialect, opts ...DBStoreOption) (*DBStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock := newStoreDB(t, dialect)
	opts = append(opts, WithDBClock(func() time.Time { return fixedClock }))
	store, err := NewDBStore(db, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return store, mock
}

// validDocuments renders the fixture set the way both stores persist it.
func validDocuments(t *testing.T) (map[string]string, *Set) {
	t.Helper()
	set, issues, err := LoadDir(validSet(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatal(issues)
	}
	documents := map[string]string{}
	for _, item := range set.resources() {
		raw, err := marshalResource(item.Doc)
		if err != nil {
			t.Fatal(err)
		}
		documents[resourceKey(item.Kind, item.Name)] = string(raw)
	}
	return documents, set
}

func TestNewDBStoreValidatesConfiguration(t *testing.T) {
	db, _ := newStoreDB(t, orm.MySQL())

	if _, err := NewDBStore(nil); err == nil {
		t.Fatal("nil database must be rejected")
	}
	if _, err := NewDBStore(db, WithDBTable("bad table")); err == nil {
		t.Fatal("a table name with a space must be rejected")
	}
	if _, err := NewDBStore(db, WithDBTable("same"), WithDBRevisionsTable("same")); err == nil {
		t.Fatal("using one table for both roles must be rejected")
	}

	store, err := NewDBStore(db)
	if err != nil {
		t.Fatal(err)
	}
	resources, revisions := store.Tables()
	if resources != DefaultResourceTable || revisions != DefaultRevisionTable {
		t.Fatalf("tables = %q, %q", resources, revisions)
	}
	if _, err := NewDBStore(db, WithDBTable("app_dsl"), WithDBRevisionsTable("app_dsl_rev")); err != nil {
		t.Fatal(err)
	}
}

// The DDL is the contract a DBA reads: two tables, composite keys, dialect quoting.
func TestDBStoreMigrate(t *testing.T) {
	cases := []struct {
		name     string
		dialect  orm.Dialect
		resource string
		revision string
	}{
		{
			name:    "mysql",
			dialect: orm.MySQL(),
			resource: "CREATE TABLE IF NOT EXISTS `gable_dsl_resource` (`kind` VARCHAR(32) NOT NULL, `name` VARCHAR(191) NOT NULL, " +
				"`document` TEXT NOT NULL, `revision` BIGINT NOT NULL, `updated_at` DATETIME NOT NULL, PRIMARY KEY (`kind`, `name`))",
			revision: "CREATE TABLE IF NOT EXISTS `gable_dsl_revision` (`kind` VARCHAR(32) NOT NULL, `name` VARCHAR(191) NOT NULL, " +
				"`revision` BIGINT NOT NULL, `document` TEXT NOT NULL, `created_at` DATETIME NOT NULL, " +
				"PRIMARY KEY (`kind`, `name`, `revision`))",
		},
		{
			name:    "postgres",
			dialect: orm.Postgres(),
			resource: `CREATE TABLE IF NOT EXISTS "gable_dsl_resource" ("kind" VARCHAR(32) NOT NULL, "name" VARCHAR(191) NOT NULL, ` +
				`"document" TEXT NOT NULL, "revision" BIGINT NOT NULL, "updated_at" TIMESTAMP NOT NULL, PRIMARY KEY ("kind", "name"))`,
			revision: `CREATE TABLE IF NOT EXISTS "gable_dsl_revision" ("kind" VARCHAR(32) NOT NULL, "name" VARCHAR(191) NOT NULL, ` +
				`"revision" BIGINT NOT NULL, "document" TEXT NOT NULL, "created_at" TIMESTAMP NOT NULL, ` +
				`PRIMARY KEY ("kind", "name", "revision"))`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, mock := newFixedStore(t, tc.dialect)
			mock.ExpectExec(tc.resource).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectExec(tc.revision).WillReturnResult(sqlmock.NewResult(0, 0))
			if err := store.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// An empty database is a valid set, not an error: a first deployment starts there.
func TestDBStoreLoadEmpty(t *testing.T) {
	store, mock := newFixedStore(t, orm.MySQL())
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}))

	set, issues, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(set.Models) != 0 {
		t.Fatalf("set = %+v, issues = %v", set, issues)
	}
	if lint := set.Lint(); len(lint) != 0 {
		t.Fatalf("lint = %v", lint)
	}
}

// A broken or mis-filed document is reported and skipped; the rest of the table loads.
func TestDBStoreLoadReportsBadDocuments(t *testing.T) {
	store, mock := newFixedStore(t, orm.MySQL())
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}).
			AddRow("Model", "task", modelYAML).
			AddRow("Table", "task.table", tableYAML).
			AddRow("Model", "wrong-version", strings.Replace(modelYAML, "gable/v1", "nope/v1", 1)).
			AddRow("Model", "misfiled", tableYAML))

	set, issues, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Models) != 1 || len(set.Tables) != 1 {
		t.Fatalf("set = %+v", set)
	}
	if set.Models["task"] == nil || set.Tables["task.table"] == nil {
		t.Fatalf("set = %+v", set)
	}
	if source := set.Sources["task"]; source != "db:Model/task" {
		t.Fatalf("source = %q", source)
	}

	text := issues.Error()
	if !strings.Contains(text, "Model/wrong-version") || !strings.Contains(text, "apiVersion") {
		t.Fatalf("issues = %v, want the bad envelope reported against its key", issues)
	}
	if !strings.Contains(text, "Model/misfiled") || !strings.Contains(text, "declares Table/task.table") {
		t.Fatalf("issues = %v, want the kind mismatch reported", issues)
	}
}

// Saving an empty store writes every resource and records revision 1 of each.
func TestDBStoreSaveWritesEveryResourceOnce(t *testing.T) {
	documents, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.MySQL())

	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}))
	mock.ExpectBegin()
	for _, item := range []struct {
		kind Kind
		name string
	}{
		{KindModel, "task"},
		{KindTable, "task.table"},
		{KindForm, "task.form"},
		{KindPage, "task.page"},
	} {
		key := resourceKey(item.kind, item.name)
		mock.ExpectExec("INSERT INTO `gable_dsl_resource` (`kind`, `name`, `document`, `revision`, `updated_at`) VALUES (?, ?, ?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE `document` = VALUES(`document`), `revision` = `revision` + 1, `updated_at` = VALUES(`updated_at`)").
			WithArgs(string(item.kind), item.name, documents[key], int64(1), fixedClock).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT `revision` FROM `gable_dsl_resource` WHERE `kind` = ? AND `name` = ?").
			WithArgs(string(item.kind), item.name).
			WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(1)))
		mock.ExpectExec("INSERT INTO `gable_dsl_revision` (`kind`, `name`, `revision`, `document`, `created_at`) VALUES (?, ?, ?, ?, ?)").
			WithArgs(string(item.kind), item.name, int64(1), documents[key], fixedClock).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	written, err := store.Save(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Model/task", "Table/task.table", "Form/task.form", "Page/task.page"}
	if len(written) != len(want) {
		t.Fatalf("written = %v", written)
	}
	for i := range want {
		if written[i] != want[i] {
			t.Fatalf("written = %v, want %v", written, want)
		}
	}
}

// An unchanged set writes nothing: no transaction, no revision, no history.
func TestDBStoreSaveIsIdempotent(t *testing.T) {
	documents, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.MySQL())

	rows := sqlmock.NewRows([]string{"kind", "name", "document"})
	for key, document := range documents {
		kind, name, _ := strings.Cut(key, "/")
		rows.AddRow(kind, name, document)
	}
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(rows)

	written, err := store.Save(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v, want nothing", written)
	}
}

// Only the changed resource is written, and the database decides the next revision.
func TestDBStoreSaveWritesOnlyTheChangedResource(t *testing.T) {
	documents, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.MySQL())

	// The stored model drifts from the set; the other three are current.
	stale := strings.Replace(documents["Model/task"], "title: Task", "title: Old", 1)
	rows := sqlmock.NewRows([]string{"kind", "name", "document"}).
		AddRow("Model", "task", stale).
		AddRow("Table", "task.table", documents["Table/task.table"]).
		AddRow("Form", "task.form", documents["Form/task.form"]).
		AddRow("Page", "task.page", documents["Page/task.page"])
	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(rows)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `gable_dsl_resource` (`kind`, `name`, `document`, `revision`, `updated_at`) VALUES (?, ?, ?, ?, ?) "+
		"ON DUPLICATE KEY UPDATE `document` = VALUES(`document`), `revision` = `revision` + 1, `updated_at` = VALUES(`updated_at`)").
		WithArgs("Model", "task", documents["Model/task"], int64(1), fixedClock).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT `revision` FROM `gable_dsl_resource` WHERE `kind` = ? AND `name` = ?").
		WithArgs("Model", "task").
		WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(4)))
	mock.ExpectExec("INSERT INTO `gable_dsl_revision` (`kind`, `name`, `revision`, `document`, `created_at`) VALUES (?, ?, ?, ?, ?)").
		WithArgs("Model", "task", int64(4), documents["Model/task"], fixedClock).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	written, err := store.Save(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != "Model/task" {
		t.Fatalf("written = %v", written)
	}
}

// PostgreSQL upserts through ON CONFLICT and positional placeholders.
func TestDBStoreSaveOnPostgres(t *testing.T) {
	documents, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.Postgres())

	mock.ExpectQuery(`SELECT "kind", "name", "document" FROM "gable_dsl_resource" ORDER BY "kind", "name"`).
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}))
	mock.ExpectBegin()
	for _, item := range []struct {
		kind Kind
		name string
	}{
		{KindModel, "task"},
		{KindTable, "task.table"},
		{KindForm, "task.form"},
		{KindPage, "task.page"},
	} {
		key := resourceKey(item.kind, item.name)
		mock.ExpectExec(`INSERT INTO "gable_dsl_resource" ("kind", "name", "document", "revision", "updated_at") VALUES ($1, $2, $3, $4, $5) `+
			`ON CONFLICT ("kind", "name") DO UPDATE SET "document" = EXCLUDED."document", "revision" = "gable_dsl_resource"."revision" + 1, "updated_at" = EXCLUDED."updated_at"`).
			WithArgs(string(item.kind), item.name, documents[key], int64(1), fixedClock).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT "revision" FROM "gable_dsl_resource" WHERE "kind" = $1 AND "name" = $2`).
			WithArgs(string(item.kind), item.name).
			WillReturnRows(sqlmock.NewRows([]string{"revision"}).AddRow(int64(1)))
		mock.ExpectExec(`INSERT INTO "gable_dsl_revision" ("kind", "name", "revision", "document", "created_at") VALUES ($1, $2, $3, $4, $5)`).
			WithArgs(string(item.kind), item.name, int64(1), documents[key], fixedClock).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()

	written, err := store.Save(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 4 {
		t.Fatalf("written = %v", written)
	}
}

// A failure part-way through leaves no half-written revision behind.
func TestDBStoreSaveRollsBackOnFailure(t *testing.T) {
	_, set := validDocuments(t)
	store, mock := newFixedStore(t, orm.MySQL())

	mock.ExpectQuery("SELECT `kind`, `name`, `document` FROM `gable_dsl_resource` ORDER BY `kind`, `name`").
		WillReturnRows(sqlmock.NewRows([]string{"kind", "name", "document"}))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `gable_dsl_resource` (`kind`, `name`, `document`, `revision`, `updated_at`) VALUES (?, ?, ?, ?, ?) " +
		"ON DUPLICATE KEY UPDATE `document` = VALUES(`document`), `revision` = `revision` + 1, `updated_at` = VALUES(`updated_at`)").
		WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	written, err := store.Save(context.Background(), set)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v", written)
	}
}

// An invalid set never reaches the database.
func TestDBStoreSaveRejectsInvalidSet(t *testing.T) {
	store, _ := newFixedStore(t, orm.MySQL())
	set := NewSet()
	set.Models["broken"] = &ModelDoc{
		APIVersion: APIVersion,
		Kind:       KindModel,
		Metadata:   Metadata{Name: "broken"},
		Spec:       ModelSpec{Fields: []FieldSpec{{Name: "id", Type: "int64"}}},
	}

	written, err := store.Save(context.Background(), set)
	if err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("err = %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("written = %v", written)
	}
	if _, err := store.Save(context.Background(), nil); err == nil {
		t.Fatal("nil set must be rejected")
	}
}
