package dsl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/maxid/gable/orm"
)

// Table names used by the database store unless they are overridden.
const (
	DefaultResourceTable = "gable_dsl_resource"
	DefaultRevisionTable = "gable_dsl_revision"
)

// dbSourcePrefix marks a resource that came from a database. Set.Sources records where a
// resource was loaded from, and not every store is a directory.
const dbSourcePrefix = "db:"

// tableNameRE keeps table names to plain identifiers: the dialect only quotes an
// identifier, so anything else would end up in the DDL as written.
var tableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// DBStore keeps DSL resources in a database so several instances can share one
// definition, and appends every written document to a revision history table.
//
// It follows the file store's rules: documents are the same normalized YAML
// Set.ApplyDir writes, a save only touches documents that actually changed, and nothing
// is deleted implicitly — a resource dropped from the set stays in the database until
// deleting becomes an explicit operation.
type DBStore struct {
	db        *orm.DB
	table     string
	revisions string
	now       func() time.Time
}

// DBStoreOption configures a DBStore.
type DBStoreOption func(*DBStore)

// WithDBTable overrides the resource table name. The default is DefaultResourceTable.
func WithDBTable(name string) DBStoreOption {
	return func(s *DBStore) { s.table = strings.TrimSpace(name) }
}

// WithDBRevisionsTable overrides the revision history table name. The default is
// DefaultRevisionTable.
func WithDBRevisionsTable(name string) DBStoreOption {
	return func(s *DBStore) { s.revisions = strings.TrimSpace(name) }
}

// WithDBClock overrides the clock used for updated_at/created_at.
func WithDBClock(now func() time.Time) DBStoreOption {
	return func(s *DBStore) {
		if now != nil {
			s.now = now
		}
	}
}

// NewDBStore returns a store for the database. Unlike the file store there is nothing to
// fall back to, so a nil database or an unusable table name is a construction error.
func NewDBStore(db *orm.DB, opts ...DBStoreOption) (*DBStore, error) {
	if db == nil {
		return nil, fmt.Errorf("dsl: nil database")
	}
	store := &DBStore{db: db, table: DefaultResourceTable, revisions: DefaultRevisionTable, now: time.Now}
	for _, opt := range opts {
		opt(store)
	}
	switch {
	case !tableNameRE.MatchString(store.table):
		return nil, fmt.Errorf("dsl: resource table %q must be a plain identifier", store.table)
	case !tableNameRE.MatchString(store.revisions):
		return nil, fmt.Errorf("dsl: revision table %q must be a plain identifier", store.revisions)
	case store.table == store.revisions:
		return nil, fmt.Errorf("dsl: resource and revision tables must differ, both are %q", store.table)
	}
	return store, nil
}

// Tables reports the resource and revision table names in use.
func (s *DBStore) Tables() (resources, revisions string) { return s.table, s.revisions }

// Migrate creates the resource and revision tables when they are missing. Like
// AutoMigrate it only adds tables; changing an existing one stays an explicit migration.
func (s *DBStore) Migrate(ctx context.Context) error {
	dialect := s.db.Dialect()
	for _, statement := range []string{s.resourceDDL(dialect), s.revisionDDL(dialect)} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("dsl: migrate: %w", err)
		}
	}
	return nil
}

// Load reads every stored resource and reports the documents that do not parse.
//
// A broken document is an issue, not a failed load: one bad resource must not hide the
// rest of the set from lint, preview or a runtime that is still serving the others.
func (s *DBStore) Load(ctx context.Context) (*Set, Issues, error) {
	rows, err := s.db.QueryContext(ctx, s.selectSQL(s.db.Dialect()))
	if err != nil {
		return nil, nil, fmt.Errorf("dsl: load: %w", err)
	}
	defer func() { _ = rows.Close() }()

	set := NewSet()
	var issues Issues
	for rows.Next() {
		var kind, name, document string
		if err := rows.Scan(&kind, &name, &document); err != nil {
			return nil, issues, fmt.Errorf("dsl: load: %w", err)
		}
		loaded, loadIssues := LoadBytes(resourceKey(Kind(kind), name), []byte(document))
		if len(loadIssues) > 0 {
			issues = append(issues, loadIssues...)
			continue
		}
		issues = append(issues, set.merge(Kind(kind), name, loaded)...)
	}
	if err := rows.Err(); err != nil {
		return nil, issues, fmt.Errorf("dsl: load: %w", err)
	}
	return set, issues, nil
}

// Save writes the set's resources and returns the keys it wrote, in the same order the
// file store applies them.
//
// The whole set is linted first, so an invalid set never reaches the database. Documents
// that are already stored unchanged are skipped — saving an unchanged set writes
// nothing — and each written resource gets the next revision plus a history row, all
// inside one transaction.
func (s *DBStore) Save(ctx context.Context, set *Set) ([]string, error) {
	if set == nil {
		return nil, fmt.Errorf("dsl: nil set")
	}
	if issues := set.Lint(); len(issues) > 0 {
		return nil, issues
	}
	stored, err := s.documents(ctx)
	if err != nil {
		return nil, err
	}

	pending, err := changedResources(set, stored)
	if err != nil {
		return nil, err
	}
	if issues := readonlyConflicts(pending, stored); len(issues) > 0 {
		return nil, issues
	}
	return s.apply(ctx, pending)
}

// readonlyConflicts refuses to overwrite a stored resource that declared itself readonly.
//
// Creating such a resource is allowed — that is how the flag gets there — but changing it
// afterwards is not, which is what makes it usable as a guard rail for hand-written resources
// living next to generated ones. The check is on the *stored* document: a file cannot lift its
// own readonly flag and overwrite the database.
func readonlyConflicts(pending []pending, stored map[string]string) Issues {
	var issues Issues
	for _, item := range pending {
		key := resourceKey(item.Kind, item.Name)
		current, ok := stored[key]
		if !ok {
			continue
		}
		if storedReadonly(current) {
			issues = append(issues, Issue{
				Resource: key,
				Message:  "metadata.readonly is set on the stored resource; change it there first",
			})
		}
	}
	return issues
}

// storedReadonly reports whether a stored document declared metadata.readonly. A document that
// no longer parses cannot protect itself, and Load reports that separately.
func storedReadonly(document string) bool {
	var head header
	if err := yaml.Unmarshal([]byte(document), &head); err != nil {
		return false
	}
	return head.Metadata.Readonly
}

// apply writes the pending documents in one transaction. Save and Restore share it, so a
// restored revision gets the same revision numbering and history row as an ordinary save.
func (s *DBStore) apply(ctx context.Context, pending []pending) ([]string, error) {
	if len(pending) == 0 {
		return nil, nil
	}

	dialect := s.db.Dialect()
	savedAt := s.now().UTC()
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("dsl: save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	written := make([]string, 0, len(pending))
	for _, item := range pending {
		key := resourceKey(item.Kind, item.Name)
		if _, err := tx.ExecContext(ctx, s.upsertSQL(dialect), string(item.Kind), item.Name, item.Document, 1, savedAt); err != nil {
			return nil, fmt.Errorf("dsl: save %s: %w", key, err)
		}
		// The revision is computed by the database (revision + 1 on conflict), so it is
		// read back inside the transaction instead of guessed by the caller.
		var revision int64
		if err := tx.QueryRowxContext(ctx, s.revisionSQL(dialect), string(item.Kind), item.Name).Scan(&revision); err != nil {
			return nil, fmt.Errorf("dsl: save %s: read revision: %w", key, err)
		}
		if _, err := tx.ExecContext(ctx, s.historySQL(dialect), string(item.Kind), item.Name, revision, item.Document, savedAt); err != nil {
			return nil, fmt.Errorf("dsl: save %s: revision %d: %w", key, revision, err)
		}
		written = append(written, key)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("dsl: save: %w", err)
	}
	return written, nil
}

// merge files a loaded document under the key it is stored with. The key stays
// authoritative: a document whose envelope names a different kind or resource is
// reported instead of being filed somewhere the database does not say it is.
func (s *Set) merge(kind Kind, name string, loaded *Set) Issues {
	value, ok := loaded.Resource(kind, name)
	if !ok {
		return Issues{{
			Resource: resourceKey(kind, name),
			Message:  fmt.Sprintf("document is stored as %s but declares %s", resourceKey(kind, name), declaredResource(loaded)),
		}}
	}
	switch kind {
	case KindModel:
		s.Models[name] = value.(*ModelDoc)
	case KindTable:
		s.Tables[name] = value.(*TableDoc)
	case KindForm:
		s.Forms[name] = value.(*FormDoc)
	case KindPage:
		s.Pages[name] = value.(*PageDoc)
	}
	s.Sources[name] = dbSourcePrefix + resourceKey(kind, name)
	return nil
}

// declaredResource names the single resource a loaded set holds, for diagnostics.
func declaredResource(set *Set) string {
	for _, item := range set.resources() {
		return resourceKey(item.Kind, item.Name)
	}
	return "nothing"
}

// pending is a resource whose document differs from the stored one.
type pending struct {
	resource
	Document string
}

// changedResources renders every resource and keeps the ones the store does not have yet
// or holds in another form.
func changedResources(set *Set, stored map[string]string) ([]pending, error) {
	var out []pending
	for _, item := range set.resources() {
		document, err := marshalResource(item.Doc)
		if err != nil {
			return nil, fmt.Errorf("dsl: %s: %w", resourceKey(item.Kind, item.Name), err)
		}
		if current, ok := stored[resourceKey(item.Kind, item.Name)]; ok && current == string(document) {
			continue
		}
		out = append(out, pending{resource: item, Document: string(document)})
	}
	return out, nil
}

// documents reads the stored documents, keyed like Set.Sources.
func (s *DBStore) documents(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, s.selectSQL(s.db.Dialect()))
	if err != nil {
		return nil, fmt.Errorf("dsl: read stored documents: %w", err)
	}
	defer func() { _ = rows.Close() }()

	stored := map[string]string{}
	for rows.Next() {
		var kind, name, document string
		if err := rows.Scan(&kind, &name, &document); err != nil {
			return nil, fmt.Errorf("dsl: read stored documents: %w", err)
		}
		stored[resourceKey(Kind(kind), name)] = document
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dsl: read stored documents: %w", err)
	}
	return stored, nil
}

func (s *DBStore) resourceDDL(dialect orm.Dialect) string {
	quote := dialect.Quote
	return "CREATE TABLE IF NOT EXISTS " + quote(s.table) + " (" +
		quote("kind") + " VARCHAR(32) NOT NULL, " +
		quote("name") + " VARCHAR(191) NOT NULL, " +
		quote("document") + " TEXT NOT NULL, " +
		quote("revision") + " BIGINT NOT NULL, " +
		quote("updated_at") + " " + timestampType(dialect) + " NOT NULL, " +
		"PRIMARY KEY (" + quote("kind") + ", " + quote("name") + "))"
}

func (s *DBStore) revisionDDL(dialect orm.Dialect) string {
	quote := dialect.Quote
	return "CREATE TABLE IF NOT EXISTS " + quote(s.revisions) + " (" +
		quote("kind") + " VARCHAR(32) NOT NULL, " +
		quote("name") + " VARCHAR(191) NOT NULL, " +
		quote("revision") + " BIGINT NOT NULL, " +
		quote("document") + " TEXT NOT NULL, " +
		quote("created_at") + " " + timestampType(dialect) + " NOT NULL, " +
		"PRIMARY KEY (" + quote("kind") + ", " + quote("name") + ", " + quote("revision") + "))"
}

// timestampType keeps the store's columns on the same mapping orm uses for time fields.
func timestampType(dialect orm.Dialect) string {
	if dialect.Name() == "postgres" {
		return "TIMESTAMP"
	}
	return "DATETIME"
}

func (s *DBStore) selectSQL(dialect orm.Dialect) string {
	quote := dialect.Quote
	return "SELECT " + quote("kind") + ", " + quote("name") + ", " + quote("document") +
		" FROM " + quote(s.table) +
		" ORDER BY " + quote("kind") + ", " + quote("name")
}

// upsertSQL writes one document. The revision is assigned by the database: 1 for a new
// row, one more than the stored revision on conflict, so two instances saving at the same
// time cannot hand out the same revision number.
func (s *DBStore) upsertSQL(dialect orm.Dialect) string {
	quote := dialect.Quote
	columns := quote("kind") + ", " + quote("name") + ", " + quote("document") + ", " +
		quote("revision") + ", " + quote("updated_at")
	head := "INSERT INTO " + quote(s.table) + " (" + columns + ") VALUES (" + placeholders(dialect, 5) + ")"
	if dialect.Name() == "postgres" {
		// The target row must be qualified: an unqualified "revision" is ambiguous between
		// the existing row and EXCLUDED.
		return head + " ON CONFLICT (" + quote("kind") + ", " + quote("name") + ") DO UPDATE SET " +
			quote("document") + " = EXCLUDED." + quote("document") + ", " +
			quote("revision") + " = " + quote(s.table) + "." + quote("revision") + " + 1, " +
			quote("updated_at") + " = EXCLUDED." + quote("updated_at")
	}
	return head + " ON DUPLICATE KEY UPDATE " +
		quote("document") + " = VALUES(" + quote("document") + "), " +
		quote("revision") + " = " + quote("revision") + " + 1, " +
		quote("updated_at") + " = VALUES(" + quote("updated_at") + ")"
}

func (s *DBStore) revisionSQL(dialect orm.Dialect) string {
	quote := dialect.Quote
	return "SELECT " + quote("revision") + " FROM " + quote(s.table) +
		" WHERE " + quote("kind") + " = " + dialect.Placeholder(1) +
		" AND " + quote("name") + " = " + dialect.Placeholder(2)
}

func (s *DBStore) historySQL(dialect orm.Dialect) string {
	quote := dialect.Quote
	columns := quote("kind") + ", " + quote("name") + ", " + quote("revision") + ", " +
		quote("document") + ", " + quote("created_at")
	return "INSERT INTO " + quote(s.revisions) + " (" + columns + ") VALUES (" + placeholders(dialect, 5) + ")"
}

func placeholders(dialect orm.Dialect, count int) string {
	parts := make([]string, 0, count)
	for index := 1; index <= count; index++ {
		parts = append(parts, dialect.Placeholder(index))
	}
	return strings.Join(parts, ", ")
}

// Diff compares a desired set with what the database currently stores, without writing
// anything. It is what `gable dsl push --dry-run` prints.
//
// A store that was never migrated holds nothing, which is exactly what a first diff must
// report — so a missing table reads as an empty store instead of an error. The check is
// an information_schema lookup rather than error sniffing, so a real failure still
// surfaces as one.
func (s *DBStore) Diff(ctx context.Context, set *Set) (Diff, error) {
	exists, err := s.resourceTableExists(ctx)
	if err != nil {
		return Diff{}, err
	}
	stored := map[string]string{}
	if exists {
		stored, err = s.documents(ctx)
		if err != nil {
			return Diff{}, err
		}
	}
	return DiffResources(set, stored)
}

// resourceTableExists asks information_schema, which both supported dialects implement;
// current_schema() is the PostgreSQL spelling of MySQL's DATABASE().
func (s *DBStore) resourceTableExists(ctx context.Context) (bool, error) {
	dialect := s.db.Dialect()
	current := "DATABASE()"
	if dialect.Name() == "postgres" {
		current = "current_schema()"
	}
	statement := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = " + current +
		" AND table_name = " + dialect.Placeholder(1)
	var count int64
	if err := s.db.QueryRowContext(ctx, statement, s.table).Scan(&count); err != nil {
		return false, fmt.Errorf("dsl: check table %s: %w", s.table, err)
	}
	return count > 0, nil
}

// Revision is one historical version of a resource.
type Revision struct {
	Kind      Kind      `json:"kind"`
	Name      string    `json:"name"`
	Revision  int64     `json:"revision"`
	Document  string    `json:"document"`
	CreatedAt time.Time `json:"createdAt"`
}

// History reads the stored revisions of one resource, newest first.
//
// History is append-only: restoring an old revision writes a new one instead of deleting
// anything, so "what did it look like yesterday" stays answerable after a rollback.
func (s *DBStore) History(ctx context.Context, kind Kind, name string) ([]Revision, error) {
	quote := s.db.Dialect().Quote
	statement := "SELECT " + quote("revision") + ", " + quote("document") + ", " + quote("created_at") +
		" FROM " + quote(s.revisions) +
		" WHERE " + quote("kind") + " = " + s.db.Dialect().Placeholder(1) +
		" AND " + quote("name") + " = " + s.db.Dialect().Placeholder(2) +
		" ORDER BY " + quote("revision") + " DESC"
	rows, err := s.db.QueryContext(ctx, statement, string(kind), name)
	if err != nil {
		return nil, fmt.Errorf("dsl: history %s: %w", resourceKey(kind, name), err)
	}
	defer func() { _ = rows.Close() }()

	var revisions []Revision
	for rows.Next() {
		var revision Revision
		if err := rows.Scan(&revision.Revision, &revision.Document, &revision.CreatedAt); err != nil {
			return nil, fmt.Errorf("dsl: history %s: %w", resourceKey(kind, name), err)
		}
		revision.Kind = Kind(kind)
		revision.Name = name
		revisions = append(revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("dsl: history %s: %w", resourceKey(kind, name), err)
	}
	return revisions, nil
}

// Restore writes a historical revision back as the current document.
//
// The stored document was accepted when it was written, and re-linting a single
// historical resource out of its set could reject a table whose model lives elsewhere, so
// the document is restored as-is. The restore itself becomes the next revision.
func (s *DBStore) Restore(ctx context.Context, kind Kind, name string, revision int64) (string, error) {
	quote := s.db.Dialect().Quote
	statement := "SELECT " + quote("document") + " FROM " + quote(s.revisions) +
		" WHERE " + quote("kind") + " = " + s.db.Dialect().Placeholder(1) +
		" AND " + quote("name") + " = " + s.db.Dialect().Placeholder(2) +
		" AND " + quote("revision") + " = " + s.db.Dialect().Placeholder(3)
	var document string
	if err := s.db.QueryRowContext(ctx, statement, string(kind), name, revision).Scan(&document); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("dsl: %s has no revision %d", resourceKey(kind, name), revision)
		}
		return "", fmt.Errorf("dsl: restore %s: %w", resourceKey(kind, name), err)
	}
	loaded, issues := LoadBytes(resourceKey(kind, name), []byte(document))
	if len(issues) > 0 {
		return "", fmt.Errorf("dsl: restore %s: stored revision %d does not parse: %w", resourceKey(kind, name), revision, issues)
	}
	if _, ok := loaded.Resource(kind, name); !ok {
		return "", fmt.Errorf("dsl: restore %s: revision %d declares %s", resourceKey(kind, name), revision, declaredResource(loaded))
	}

	// Restoring the revision that is already current writes nothing, the same rule Save
	// follows: an action that changes no document must not add a revision row.
	stored, err := s.documents(ctx)
	if err != nil {
		return "", err
	}
	if current, ok := stored[resourceKey(kind, name)]; ok {
		if current == document {
			return "", nil
		}
		if storedReadonly(current) {
			return "", fmt.Errorf("dsl: %s: metadata.readonly is set on the stored resource; restore would overwrite it",
				resourceKey(kind, name))
		}
	}

	written, err := s.apply(ctx, []pending{{
		resource: resource{Kind: kind, Name: name},
		Document: document,
	}})
	if err != nil {
		return "", err
	}
	if len(written) == 0 {
		return "", nil
	}
	return written[0], nil
}
