package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/maxid/gable/config"
	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

type envelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
	Success bool            `json:"success"`
}

func testConfig(t *testing.T, mode tenant.Mode) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.App.Name = "pilot-test"
	cfg.Log.Dir = t.TempDir()
	cfg.Tenant.Mode = mode
	if mode == tenant.ModeSaaS {
		cfg.Tenant.BaseDomain = "example.com"
	}
	return cfg
}

func testServer(t *testing.T, mode tenant.Mode, store TaskStore) *web.Server {
	t.Helper()
	server, err := newServer(testConfig(t, mode), store, pilotTenants())
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func request(t *testing.T, server *web.Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if strings.EqualFold(key, "Host") {
			req.Host = value
			continue
		}
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, req)
	return recorder
}

func decode[T any](t *testing.T, recorder *httptest.ResponseRecorder) (envelope, T) {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, recorder.Body)
	}
	var value T
	if len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, &value); err != nil {
			t.Fatalf("decode data: %v\n%s", err, env.Data)
		}
	}
	return env, value
}

func TestSingleTenantCRUD(t *testing.T) {
	server := testServer(t, tenant.ModeSingle, newMemoryTaskStore())

	created := request(t, server, http.MethodPost, "/tasks", `{"title":"write docs"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body)
	}
	env, task := decode[taskResponse](t, created)
	if !env.Success || task.TenantID != 1 || task.ID == 0 || task.Status != "todo" {
		t.Fatalf("created = %+v, env = %+v", task, env)
	}

	listed := request(t, server, http.MethodGet, "/tasks", "", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d", listed.Code)
	}
	_, page := decode[taskListResponse](t, listed)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != task.ID {
		t.Fatalf("page = %+v", page)
	}

	updated := request(t, server, http.MethodPut, "/tasks/"+itoa(task.ID), `{"title":"updated","status":"done"}`, nil)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.Code, updated.Body)
	}
	_, changed := decode[taskResponse](t, updated)
	if changed.Title != "updated" || changed.Status != "done" {
		t.Fatalf("changed = %+v", changed)
	}

	deleted := request(t, server, http.MethodDelete, "/tasks/"+itoa(task.ID), "", nil)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete status = %d", deleted.Code)
	}

	missing := request(t, server, http.MethodGet, "/tasks/"+itoa(task.ID), "", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body = %s", missing.Code, missing.Body)
	}
	env, _ = decode[any](t, missing)
	if env.Code != orm.ErrNotFound.Code {
		t.Fatalf("missing code = %q", env.Code)
	}

	who := request(t, server, http.MethodGet, "/whoami", "", nil)
	_, whoData := decode[map[string]any](t, who)
	if whoData["tenantCode"] != "management" {
		t.Fatalf("whoami = %+v", whoData)
	}
}

func TestSaaSTenantIsolation(t *testing.T) {
	server := testServer(t, tenant.ModeSaaS, newMemoryTaskStore())

	login := request(t, server, http.MethodGet, "/login/options", "", map[string]string{"Host": "acme.example.com"})
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body)
	}
	_, loginData := decode[map[string]any](t, login)
	if loginData["tenantCode"] != "acme" {
		t.Fatalf("login = %+v", loginData)
	}

	if missing := request(t, server, http.MethodPost, "/tasks", `{"title":"x"}`, nil); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing principal status = %d", missing.Code)
	}

	acme := map[string]string{"X-User-Tenant": "2"}
	created := request(t, server, http.MethodPost, "/tasks", `{"title":"acme task"}`, acme)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body)
	}
	_, task := decode[taskResponse](t, created)
	if task.TenantID != 2 {
		t.Fatalf("task tenant = %d", task.TenantID)
	}

	_, acmePage := decode[taskListResponse](t, request(t, server, http.MethodGet, "/tasks", "", acme))
	if acmePage.Total != 1 {
		t.Fatalf("acme page = %+v", acmePage)
	}
	beta := map[string]string{"X-User-Tenant": "3"}
	_, betaPage := decode[taskListResponse](t, request(t, server, http.MethodGet, "/tasks", "", beta))
	if betaPage.Total != 0 {
		t.Fatalf("beta page = %+v", betaPage)
	}
	if cross := request(t, server, http.MethodGet, "/tasks/"+itoa(task.ID), "", beta); cross.Code != http.StatusNotFound {
		t.Fatalf("cross tenant status = %d", cross.Code)
	}

	_, who := decode[map[string]any](t, request(t, server, http.MethodGet, "/whoami", "", acme))
	if who["tenantCode"] != "acme" {
		t.Fatalf("whoami = %+v", who)
	}
	operator := map[string]string{"X-User-Tenant": "2", "X-Operator": "1", "X-Tenant-Id": "3"}
	_, who = decode[map[string]any](t, request(t, server, http.MethodGet, "/whoami", "", operator))
	if who["tenantCode"] != "beta" {
		t.Fatalf("impersonated whoami = %+v", who)
	}
	forbidden := map[string]string{"X-User-Tenant": "2", "X-Tenant-Id": "3"}
	if status := request(t, server, http.MethodGet, "/whoami", "", forbidden).Code; status != http.StatusForbidden {
		t.Fatalf("forbidden status = %d", status)
	}
}

func TestOpenAPI(t *testing.T) {
	server := testServer(t, tenant.ModeSingle, newMemoryTaskStore())
	recorder := request(t, server, http.MethodGet, "/v3/api-docs", "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var spec map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	for _, path := range []string{"/tasks", "/tasks/{id}", "/whoami"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("missing path %s", path)
		}
	}
	components := spec["components"].(map[string]any)["schemas"].(map[string]any)
	if _, ok := components["createTaskRequest"]; !ok {
		t.Fatalf("missing createTaskRequest schema")
	}
}

type fixedIDs struct{}

func (fixedIDs) Next(context.Context) (int64, error) { return 100, nil }

func TestORMWiring(t *testing.T) {
	sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = sqlDB.Close()
	}()

	db, err := orm.New(sqlx.NewDb(sqlDB, "mysql"), orm.MySQL())
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS `pilot_task` (`id` BIGINT PRIMARY KEY, `tenant_id` BIGINT NOT NULL, `title` VARCHAR(128), `status` VARCHAR(16), `created_at` DATETIME, `updated_at` DATETIME)").
		WillReturnResult(sqlmock.NewResult(0, 0))
	store, err := newORMStore(db, fixedIDs{})
	if err != nil {
		t.Fatal(err)
	}

	server := testServer(t, tenant.ModeSingle, store)

	mock.ExpectExec("INSERT INTO `pilot_task` (`id`, `tenant_id`, `title`, `status`, `created_at`, `updated_at`) VALUES (?, ?, ?, ?, ?, ?)").
		WithArgs(int64(100), int64(1), "write docs", "todo", sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	created := request(t, server, http.MethodPost, "/tasks", `{"title":"write docs"}`, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body)
	}
	_, task := decode[taskResponse](t, created)
	if task.ID != 100 || task.TenantID != 1 {
		t.Fatalf("task = %+v", task)
	}

	now := time.Now()
	mock.ExpectQuery("SELECT COUNT(*) FROM `pilot_task` WHERE `tenant_id` = ?").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	mock.ExpectQuery("SELECT `id`, `tenant_id`, `title`, `status`, `created_at`, `updated_at` FROM `pilot_task` WHERE `tenant_id` = ? ORDER BY `id` ASC LIMIT 20 OFFSET 0").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "title", "status", "created_at", "updated_at"}).
			AddRow(int64(100), int64(1), "write docs", "todo", now, now))
	listed := request(t, server, http.MethodGet, "/tasks", "", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body)
	}
	_, page := decode[taskListResponse](t, listed)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != 100 {
		t.Fatalf("page = %+v", page)
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
