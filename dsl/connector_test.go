package dsl

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

// twoConnectors builds a default database and a named one, each with its own expectations so a
// query sent to the wrong one fails loudly.
func twoConnectors(t *testing.T) (*Connectors, sqlmock.Sqlmock, sqlmock.Sqlmock) {
	t.Helper()
	defaultDB, defaultMock := newMockDB(t)
	reportingDB, reportingMock := newMockDB(t)
	connectors := NewConnectors(defaultDB)
	if err := connectors.Add("reporting", reportingDB); err != nil {
		t.Fatal(err)
	}
	return connectors, defaultMock, reportingMock
}

func newMockDB(t *testing.T) (*orm.DB, sqlmock.Sqlmock) {
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
	db, err := orm.New(sqlx.NewDb(sqlDB, "mysql"), orm.MySQL())
	if err != nil {
		t.Fatal(err)
	}
	return db, mock
}

const reportingModelYAML = `apiVersion: gable/v1
kind: Model
metadata: {name: revenue, title: Revenue}
spec:
  table: dsl_revenue
  connector: reporting
  fields:
    - {name: id, type: int64, primary: true}
    - {name: tenant_id, type: int64, tenant: true}
    - {name: amount, type: int64}
`

// The same definition, sent to a different database, is the whole point of a connector.
func TestRuntimeRoutesAModelToItsConnector(t *testing.T) {
	connectors, defaultMock, reportingMock := twoConnectors(t)
	set, issues := LoadBytes("revenue.yaml", []byte(reportingModelYAML))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if lint := set.Lint(); len(lint) > 0 {
		t.Fatal(lint)
	}

	runtime, err := NewRuntime(connectors.Default(), set, WithConnectors(connectors), WithDefaultIDGenerator(fixedGenerator{7}))
	if err != nil {
		t.Fatal(err)
	}
	server := web.New(web.WithTitle("connector"))
	server.UseTenant(tenant.NewSingleResolver(tenant.DefaultConfig()))
	runtime.Routes(server.Router(), "/dsl/data")

	// The count and select go to the reporting connector; the default one has no expectation,
	// so sending anything there would fail the test.
	reportingMock.ExpectQuery("SELECT COUNT(*) FROM `dsl_revenue` WHERE `tenant_id` = ?").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
	reportingMock.ExpectQuery("SELECT `id`, `tenant_id`, `amount` FROM `dsl_revenue` WHERE `tenant_id` = ? ORDER BY `id` ASC LIMIT 20 OFFSET 0").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "amount"}).AddRow(int64(5), int64(1), int64(900)))

	recorder := perform(server, "GET", "/dsl/data/revenue", nil)
	if recorder.Code != 200 {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), `"amount":900`) {
		t.Fatalf("body = %s", recorder.Body)
	}
	_ = defaultMock

	// Migration follows the same routing.
	reportingMock.ExpectExec("CREATE TABLE IF NOT EXISTS `dsl_revenue` (`id` BIGINT PRIMARY KEY, `tenant_id` BIGINT NOT NULL, `amount` BIGINT)").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := runtime.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A model naming a connector nobody registered is refused when the set is bound, not on the
// first request that happens to touch it.
func TestUnknownConnectorIsABindTimeError(t *testing.T) {
	db, _ := newMockDB(t)
	set, issues := LoadBytes("revenue.yaml", []byte(reportingModelYAML))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	_, err := NewRuntime(db, set)
	if err == nil || !strings.Contains(err.Error(), "connector \"reporting\"") {
		t.Fatalf("err = %v", err)
	}

	connectors := NewConnectors(db)
	if err := connectors.Add("reporting", db); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntime(db, set, WithConnectors(connectors)); err != nil {
		t.Fatalf("registered connector rejected: %v", err)
	}
}

func TestConnectorRegistration(t *testing.T) {
	db, _ := newMockDB(t)
	connectors := NewConnectors(db)

	if err := connectors.Add("reporting", db); err != nil {
		t.Fatal(err)
	}
	if err := connectors.Add("reporting", db); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("duplicate = %v", err)
	}
	if err := connectors.Add("Bad Name", db); err == nil {
		t.Fatal("an invalid connector name must be refused")
	}
	if err := connectors.Add("empty", nil); err == nil {
		t.Fatal("a nil database must be refused")
	}
	if names := connectors.Names(); len(names) != 2 || names[0] != "default" || names[1] != "reporting" {
		t.Fatalf("names = %v", names)
	}
	if got, ok := connectors.Get(""); !ok || got != db {
		t.Fatal("an empty name must resolve to the default connector")
	}
	if _, ok := connectors.Get("missing"); ok {
		t.Fatal("an unknown connector must not resolve")
	}
}

// Lint rejects a connector name the runtime could never resolve.
func TestLintRejectsAnInvalidConnectorName(t *testing.T) {
	broken := strings.Replace(reportingModelYAML, "connector: reporting", "connector: Reporting DB", 1)
	set, issues := LoadBytes("revenue.yaml", []byte(broken))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if lint := set.Lint(); len(lint) == 0 || !strings.Contains(lint.Error(), "connector") {
		t.Fatalf("lint = %v", lint)
	}
}
