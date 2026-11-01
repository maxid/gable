package orm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/maxid/gable/tenant"
)

type testUser struct {
	ID        int64  `gable:"pk"`
	TenantID  int64  `gable:"tenant"`
	Name      string `gable:"size=64,unique"`
	Age       int
	CreatedAt time.Time `gable:"auto_create"`
	UpdatedAt time.Time `gable:"auto_update"`
}

type counter struct {
	ID   int64 `gable:"pk,auto"`
	Name string
}

type fixedIDs struct{ id int64 }

func (f fixedIDs) Next(context.Context) (int64, error) { return f.id, nil }

func newMock(t *testing.T, dialect Dialect) (*DB, sqlmock.Sqlmock) {
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
	return &DB{DB: sqlx.NewDb(sqlDB, dialect.Name()), dialect: dialect}, mock
}

func tenantContext(id int64) context.Context {
	return tenant.WithTenant(context.Background(), tenant.Tenant{ID: id, Code: "acme"})
}

func TestModelMetadataRejectsInvalidTable(t *testing.T) {
	if _, err := ModelOf[testUser]("sys-user"); err == nil {
		t.Fatal("expected invalid table error")
	}
}

func TestModelOf(t *testing.T) {
	model, err := ModelOf[testUser]("sys_user")
	if err != nil {
		t.Fatal(err)
	}
	if model.PK == nil || model.PK.Column != "id" {
		t.Fatalf("pk = %+v", model.PK)
	}
	if model.Tenant == nil || model.Tenant.Column != "tenant_id" {
		t.Fatalf("tenant = %+v", model.Tenant)
	}
	columns := []string{"id", "tenant_id", "name", "age", "created_at", "updated_at"}
	if len(model.Fields) != len(columns) {
		t.Fatalf("fields = %+v", model.Fields)
	}
	for i, column := range columns {
		if model.Fields[i].Column != column {
			t.Fatalf("field %d = %q, want %q", i, model.Fields[i].Column, column)
		}
	}

	if _, err := ModelOf[testUser]("bad table"); err == nil {
		t.Fatal("expected invalid table error")
	}
}

func TestSnakeCase(t *testing.T) {
	cases := map[string]string{
		"ID":        "id",
		"TenantID":  "tenant_id",
		"URLPath":   "url_path",
		"CreatedAt": "created_at",
	}
	for input, want := range cases {
		if got := snakeCase(input); got != want {
			t.Fatalf("snakeCase(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTenantScopedSelect(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[testUser](db, "sys_user")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT `id`, `tenant_id`, `name`, `age`, `created_at`, `updated_at` FROM `sys_user` WHERE `tenant_id` = ?").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "name", "age", "created_at", "updated_at"}).
			AddRow(int64(1), int64(7), "alice", 30, time.Now(), time.Now()))

	rows, err := repo.Query().All(tenantContext(7))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "alice" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestTenantRequiredAndUnscoped(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[testUser](db, "sys_user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Query().All(context.Background()); !errors.Is(err, tenant.ErrRequired) {
		t.Fatalf("err = %v", err)
	}

	mock.ExpectQuery("SELECT `id`, `tenant_id`, `name`, `age`, `created_at`, `updated_at` FROM `sys_user`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "name", "age", "created_at", "updated_at"}))
	if _, err := repo.Unscoped().All(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFillsTenantAndID(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[testUser](db, "sys_user", WithIDGenerator(fixedIDs{100}))
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("INSERT INTO `sys_user` (`id`, `tenant_id`, `name`, `age`, `created_at`, `updated_at`) VALUES (?, ?, ?, ?, ?, ?)").
		WithArgs(int64(100), int64(7), "alice", 0, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	user := &testUser{Name: "alice"}
	if err := repo.Create(tenantContext(7), user); err != nil {
		t.Fatal(err)
	}
	if user.ID != 100 || user.TenantID != 7 {
		t.Fatalf("user = %+v", user)
	}
	if user.CreatedAt.IsZero() || user.UpdatedAt.IsZero() {
		t.Fatalf("timestamps = %+v", user)
	}
}

func TestCreateRejectsOtherTenant(t *testing.T) {
	db, _ := newMock(t, MySQL())
	repo, err := NewRepo[testUser](db, "sys_user")
	if err != nil {
		t.Fatal(err)
	}
	user := &testUser{TenantID: 9, Name: "alice"}
	if err := repo.Create(tenantContext(7), user); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateAutoIncrementMySQL(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[counter](db, "sys_counter")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("INSERT INTO `sys_counter` (`name`) VALUES (?)").
		WithArgs("x").
		WillReturnResult(sqlmock.NewResult(42, 1))

	entity := &counter{Name: "x"}
	if err := repo.Create(context.Background(), entity); err != nil {
		t.Fatal(err)
	}
	if entity.ID != 42 {
		t.Fatalf("id = %d", entity.ID)
	}
}

func TestGetUpdateDelete(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[testUser](db, "sys_user")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT `id`, `tenant_id`, `name`, `age`, `created_at`, `updated_at` FROM `sys_user` WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs(int64(7), int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "name", "age", "created_at", "updated_at"}).
			AddRow(int64(1), int64(7), "alice", 30, time.Now(), time.Now()))
	user, err := repo.Get(tenantContext(7), 1)
	if err != nil {
		t.Fatal(err)
	}
	if user.Name != "alice" {
		t.Fatalf("user = %+v", user)
	}

	user.Name = "bob"
	mock.ExpectExec("UPDATE `sys_user` SET `name` = ?, `age` = ?, `updated_at` = ? WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs("bob", 30, sqlmock.AnyArg(), int64(7), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Update(tenantContext(7), user); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("DELETE FROM `sys_user` WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs(int64(7), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Delete(tenantContext(7), 1); err != nil {
		t.Fatal(err)
	}
}

func TestCountAndPostgresPlaceholders(t *testing.T) {
	db, mock := newMock(t, Postgres())
	repo, err := NewRepo[testUser](db, "sys_user")
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery(`SELECT COUNT(*) FROM "sys_user" WHERE "tenant_id" = $1 AND "name" = $2`).
		WithArgs(int64(7), "alice").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
	count, err := repo.Query().Where(Eq("name", "alice")).Count(tenantContext(7))
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("count = %d", count)
	}
}

func TestSchemaSQL(t *testing.T) {
	model, err := ModelOf[testUser]("sys_user")
	if err != nil {
		t.Fatal(err)
	}
	mysql, err := SchemaSQL(model, MySQL())
	if err != nil {
		t.Fatal(err)
	}
	wantMySQL := "CREATE TABLE IF NOT EXISTS `sys_user` (`id` BIGINT PRIMARY KEY, `tenant_id` BIGINT NOT NULL, `name` VARCHAR(64) UNIQUE, `age` BIGINT, `created_at` DATETIME, `updated_at` DATETIME)"
	if mysql != wantMySQL {
		t.Fatalf("mysql schema:\n%s\nwant:\n%s", mysql, wantMySQL)
	}

	postgres, err := SchemaSQL(model, Postgres())
	if err != nil {
		t.Fatal(err)
	}
	wantPostgres := `CREATE TABLE IF NOT EXISTS "sys_user" ("id" BIGINT PRIMARY KEY, "tenant_id" BIGINT NOT NULL, "name" VARCHAR(64) UNIQUE, "age" BIGINT, "created_at" TIMESTAMP, "updated_at" TIMESTAMP)`
	if postgres != wantPostgres {
		t.Fatalf("postgres schema:\n%s\nwant:\n%s", postgres, wantPostgres)
	}
}

func TestAutoIncrementSchema(t *testing.T) {
	model, err := ModelOf[counter]("sys_counter")
	if err != nil {
		t.Fatal(err)
	}
	mysql, _ := SchemaSQL(model, MySQL())
	if mysql != "CREATE TABLE IF NOT EXISTS `sys_counter` (`id` BIGINT AUTO_INCREMENT PRIMARY KEY, `name` VARCHAR(255))" {
		t.Fatalf("mysql = %s", mysql)
	}
	postgres, _ := SchemaSQL(model, Postgres())
	if postgres != `CREATE TABLE IF NOT EXISTS "sys_counter" ("id" BIGSERIAL PRIMARY KEY, "name" VARCHAR(255))` {
		t.Fatalf("postgres = %s", postgres)
	}
}

// Every dialect alias resolves to a driver name the database/sql package actually has
// registered: "postgres" is a dialect, pgx's stdlib adapter is the driver.
func TestDriverName(t *testing.T) {
	cases := map[string]string{
		"mysql":      "mysql",
		"postgres":   "pgx",
		"postgresql": "pgx",
		"pg":         "pgx",
	}
	for alias, want := range cases {
		dialect, err := DialectFor(alias)
		if err != nil {
			t.Fatalf("%s: %v", alias, err)
		}
		if got := driverName(dialect); got != want {
			t.Fatalf("%s: driver = %q, want %q", alias, got, want)
		}
	}
	if _, err := DialectFor("sqlite"); err == nil {
		t.Fatal("an unsupported driver must be rejected")
	}
}
