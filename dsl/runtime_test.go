package dsl

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

type fixedGenerator struct{ id int64 }

func (f fixedGenerator) Next(context.Context) (int64, error) { return f.id, nil }

func newRuntimeMock(t *testing.T) (*Runtime, sqlmock.Sqlmock) {
	t.Helper()
	set, issues, err := LoadDir(validSet(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if issues := set.Lint(); len(issues) > 0 {
		t.Fatal(issues)
	}
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
	db, err := orm.New(sqlx.NewDb(sqlDB, "mysql"), orm.MySQL())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(db, set, WithDefaultIDGenerator(fixedGenerator{100}))
	if err != nil {
		t.Fatal(err)
	}
	return runtime, mock
}

func runtimeContext(id int64) context.Context {
	return tenant.WithTenant(context.Background(), tenant.Tenant{ID: id, Code: "acme"})
}

const runtimeColumns = "`id`, `tenant_id`, `title`, `status`, `created_at`, `updated_at`"

func runtimeRow(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "tenant_id", "title", "status", "created_at", "updated_at"}).
		AddRow(int64(100), int64(7), "write docs", "todo", now, now)
}

func TestRuntimeCRUD(t *testing.T) {
	runtime, mock := newRuntimeMock(t)
	ctx := runtimeContext(7)
	now := time.Now()

	mock.ExpectExec("INSERT INTO `dsl_task` (`id`, `tenant_id`, `title`, `status`, `created_at`, `updated_at`) VALUES (?, ?, ?, ?, ?, ?)").
		WithArgs(int64(100), int64(7), "write docs", "todo", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT "+runtimeColumns+" FROM `dsl_task` WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs(int64(7), int64(100)).
		WillReturnRows(runtimeRow(now))

	item, err := runtime.Create(ctx, "task", map[string]any{"title": "write docs"})
	if err != nil {
		t.Fatal(err)
	}
	if item["title"] != "write docs" || item["tenant_id"] != int64(7) {
		t.Fatalf("item = %+v", item)
	}

	mock.ExpectQuery("SELECT COUNT(*) FROM `dsl_task` WHERE `tenant_id` = ?").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT " + runtimeColumns + " FROM `dsl_task` WHERE `tenant_id` = ? ORDER BY `id` ASC LIMIT 20 OFFSET 0").
		WithArgs(int64(7)).
		WillReturnRows(runtimeRow(now))
	page, err := runtime.List(ctx, "task", ListOptions{Page: 1, Size: 20})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %+v", page)
	}

	mock.ExpectExec("UPDATE `dsl_task` SET `title` = ?, `updated_at` = ? WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs("updated", sqlmock.AnyArg(), int64(7), int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT "+runtimeColumns+" FROM `dsl_task` WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs(int64(7), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "status", "created_at", "updated_at"}).
			AddRow(int64(100), int64(7), "updated", "todo", now, now))
	if _, err := runtime.Update(ctx, "task", 100, map[string]any{"title": "updated"}); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("DELETE FROM `dsl_task` WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs(int64(7), int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := runtime.Delete(ctx, "task", 100); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRequiresTenant(t *testing.T) {
	runtime, _ := newRuntimeMock(t)
	if _, err := runtime.List(context.Background(), "task", ListOptions{}); !errors.Is(err, tenant.ErrRequired) {
		t.Fatalf("err = %v", err)
	}
	if _, err := runtime.Create(context.Background(), "task", map[string]any{"title": "x"}); !errors.Is(err, tenant.ErrRequired) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuntimeValidatesPayload(t *testing.T) {
	runtime, _ := newRuntimeMock(t)
	ctx := runtimeContext(7)
	if _, err := runtime.Create(ctx, "task", map[string]any{}); err == nil {
		t.Fatal("expected required title error")
	}
	if _, err := runtime.Create(ctx, "task", map[string]any{"title": "x", "tenant_id": 9}); err == nil {
		t.Fatal("expected managed field error")
	}
	if _, err := runtime.Create(ctx, "task", map[string]any{"title": "x", "status": "nope"}); err == nil {
		t.Fatal("expected enum error")
	}
	if _, err := runtime.List(ctx, "task", ListOptions{Filters: map[string]string{"nope": "x"}}); err == nil {
		t.Fatal("expected unknown filter error")
	}
}

func TestRuntimeHTTPAndVamisAliases(t *testing.T) {
	runtime, mock := newRuntimeMock(t)
	server := web.New(web.WithTitle("dsl"))
	server.UseTenant(tenant.NewSingleResolver(tenant.DefaultConfig()))
	runtime.Routes(server.Router(), "/dsl/data")

	now := time.Now()
	mock.ExpectExec("INSERT INTO `dsl_task` (`id`, `tenant_id`, `title`, `status`, `created_at`, `updated_at`) VALUES (?, ?, ?, ?, ?, ?)").
		WithArgs(int64(100), int64(1), "write docs", "todo", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT "+runtimeColumns+" FROM `dsl_task` WHERE `tenant_id` = ? AND `id` = ?").
		WithArgs(int64(1), int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "status", "created_at", "updated_at"}).
			AddRow(int64(100), int64(1), "write docs", "todo", now, now))

	body := bytes.NewBufferString(`{"title":"write docs"}`)
	request := httptest.NewRequest(http.MethodPost, "/dsl/data/task", body)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", recorder.Code, recorder.Body)
	}

	mock.ExpectQuery("SELECT COUNT(*) FROM `dsl_task` WHERE `tenant_id` = ?").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT " + runtimeColumns + " FROM `dsl_task` WHERE `tenant_id` = ? ORDER BY `title` DESC LIMIT 10 OFFSET 0").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "status", "created_at", "updated_at"}).
			AddRow(int64(100), int64(1), "write docs", "todo", now, now))
	list := httptest.NewRequest(http.MethodGet, "/dsl/data/task?perPage=10&orderBy=title&orderDir=desc", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, list)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", recorder.Code, recorder.Body)
	}
}

// A sort column outside the model is refused instead of silently ignored: the earlier
// behaviour returned 200 with the wrong order, which reads as a data bug.
func TestRuntimeRejectsUnknownSortColumn(t *testing.T) {
	runtime, _ := newRuntimeMock(t)
	server := web.New(web.WithTitle("dsl"))
	server.UseTenant(tenant.NewSingleResolver(tenant.DefaultConfig()))
	runtime.Routes(server.Router(), "/dsl/data")

	request := httptest.NewRequest(http.MethodGet, "/dsl/data/task?sort=password_hash", nil)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "invalid sort") {
		t.Fatalf("body = %s", body)
	}

	// An unknown model is a client error too, not a nil-pointer panic on the whitelist.
	request = httptest.NewRequest(http.MethodGet, "/dsl/data/missing", nil)
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown model status = %d, body = %s", recorder.Code, recorder.Body)
	}
}

func TestRuntimeMigrate(t *testing.T) {
	runtime, mock := newRuntimeMock(t)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS `dsl_task` (`id` BIGINT PRIMARY KEY, `tenant_id` BIGINT NOT NULL, `title` VARCHAR(128), `status` VARCHAR(16), `created_at` DATETIME, `updated_at` DATETIME)").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := runtime.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}
