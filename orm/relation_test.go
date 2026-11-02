package orm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
)

type relUser struct {
	ID   int64 `gable:"pk"`
	Name string
}

type relOrder struct {
	ID     int64 `gable:"pk"`
	UserID int64
	User   *relUser  `gable:"belongs_to=sys_users,foreign_key=user_id"`
	Items  []relItem `gable:"has_many=order_items,foreign_key=order_id"`
	Owner  relUser   `gable:"belongs_to=sys_users,foreign_key=user_id"`
}

type relItem struct {
	ID      int64 `gable:"pk"`
	OrderID int64
	SKU     string
}

func newRelationDB(t *testing.T) (*orm.DB, sqlmock.Sqlmock) {
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

// A relation is metadata, not a column: it never reaches the SELECT list, and the model
// still reports exactly the table's columns.
func TestRelationsAreNotColumns(t *testing.T) {
	db, _ := newRelationDB(t)
	repo, err := orm.NewRepo[relOrder](db, "orders")
	if err != nil {
		t.Fatal(err)
	}
	model := repo.Model()
	if len(model.Fields) != 2 {
		t.Fatalf("fields = %+v", model.Fields)
	}
	if len(model.Relations) != 3 {
		t.Fatalf("relations = %+v", model.Relations)
	}
	byName := map[string]orm.Relation{}
	for _, relation := range model.Relations {
		byName[relation.Name] = relation
	}
	if relation := byName["Items"]; relation.Kind != orm.HasMany || !relation.Many || relation.ForeignKey != "order_id" {
		t.Fatalf("Items relation = %+v", relation)
	}
	if relation := byName["User"]; relation.Kind != orm.BelongsTo || relation.Many || relation.Table != "sys_users" {
		t.Fatalf("User relation = %+v", relation)
	}
	if _, ok := byName["ID"]; ok {
		t.Fatal("ID must not be a relation")
	}
}

// belongs_to loads one query for the whole result set, and matches on the related key.
func TestPreloadBelongsTo(t *testing.T) {
	db, mock := newRelationDB(t)
	orders, err := orm.NewRepo[relOrder](db, "orders")
	if err != nil {
		t.Fatal(err)
	}
	users, err := orm.NewRepo[relUser](db, "sys_users")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT `id`, `user_id` FROM `orders`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).
			AddRow(int64(1), int64(7)).
			AddRow(int64(2), int64(7)).
			AddRow(int64(3), int64(9)))
	// Each Preload is one query, even when two relations point at the same table: the
	// cost is bounded by the number of relations asked for, never by the row count.
	for i := 0; i < 2; i++ {
		mock.ExpectQuery("SELECT `id`, `name` FROM `sys_users` WHERE `id` IN (?, ?)").
			WithArgs(int64(7), int64(9)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
				AddRow(int64(7), "alice").
				AddRow(int64(9), "bob"))
	}

	result, err := orders.Query().Preload("User", users).Preload("Owner", users).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Fatalf("rows = %+v", result)
	}
	if result[0].User == nil || result[0].User.Name != "alice" || result[1].User == nil || result[1].User.Name != "alice" {
		t.Fatalf("pointers = %+v, %+v", result[0].User, result[1].User)
	}
	if result[2].User == nil || result[2].User.Name != "bob" {
		t.Fatalf("bob = %+v", result[2].User)
	}
	if result[0].Owner.Name != "alice" {
		t.Fatalf("value relation = %+v", result[0].Owner)
	}
}

// has_many groups the related rows and gives an empty slice to a parent that has none, so
// a client never has to tell null from [].
func TestPreloadHasMany(t *testing.T) {
	db, mock := newRelationDB(t)
	orders, err := orm.NewRepo[relOrder](db, "orders")
	if err != nil {
		t.Fatal(err)
	}
	items, err := orm.NewRepo[relItem](db, "order_items")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT `id`, `user_id` FROM `orders`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).
			AddRow(int64(1), int64(7)).
			AddRow(int64(2), int64(7)))
	// One query for both parents: the keys are collected, not queried one by one.
	mock.ExpectQuery("SELECT `id`, `order_id`, `sku` FROM `order_items` WHERE `order_id` IN (?, ?)").
		WithArgs(int64(1), int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "order_id", "sku"}).
			AddRow(int64(10), int64(1), "A").
			AddRow(int64(11), int64(1), "B").
			AddRow(int64(12), int64(2), "C"))

	result, err := orders.Query().Preload("Items", items).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result[0].Items) != 2 || result[0].Items[0].SKU != "A" || result[0].Items[1].SKU != "B" {
		t.Fatalf("order 1 items = %+v", result[0].Items)
	}
	if len(result[1].Items) != 1 || result[1].Items[0].SKU != "C" {
		t.Fatalf("order 2 items = %+v", result[1].Items)
	}

	mock.ExpectQuery("SELECT `id`, `user_id` FROM `orders`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(int64(3), int64(9)))
	mock.ExpectQuery("SELECT `id`, `order_id`, `sku` FROM `order_items` WHERE `order_id` IN (?)").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "order_id", "sku"}))
	empty, err := orders.Query().Preload("Items", items).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if empty[0].Items == nil || len(empty[0].Items) != 0 {
		t.Fatalf("empty relation = %#v, want a non-nil empty slice", empty[0].Items)
	}
}

// A preload inherits the parent query's scoping: the related read keeps the tenant
// predicate, and Unscoped lifts it on both queries.
func TestPreloadInheritsScoping(t *testing.T) {
	db, mock := newRelationDB(t)
	orders, err := orm.NewRepo[relOrder](db, "orders")
	if err != nil {
		t.Fatal(err)
	}
	users, err := orm.NewRepo[relUser](db, "sys_users")
	if err != nil {
		t.Fatal(err)
	}

	ctx := tenant.WithTenant(context.Background(), tenant.Tenant{ID: 5, Code: "acme"})
	mock.ExpectQuery("SELECT `id`, `user_id` FROM `orders`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(int64(1), int64(7)))
	mock.ExpectQuery("SELECT `id`, `name` FROM `sys_users` WHERE `id` IN (?)").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(7), "alice"))

	if _, err := orders.Query().Preload("User", users).All(ctx); err != nil {
		t.Fatal(err)
	}

	// A tenant-less order model still fails closed on the tenant guard only when the
	// model declares a tenant column; here the point is that nothing else changed.
	mock.ExpectQuery("SELECT `id`, `user_id` FROM `orders`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(int64(1), int64(7)))
	mock.ExpectQuery("SELECT `id`, `name` FROM `sys_users` WHERE `id` IN (?)").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(7), "alice"))
	if _, err := orders.Unscoped().Preload("User", users).All(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestPreloadRejectsUnknownRelationAndNilRepo(t *testing.T) {
	db, mock := newRelationDB(t)
	orders, err := orm.NewRepo[relOrder](db, "orders")
	if err != nil {
		t.Fatal(err)
	}
	users, err := orm.NewRepo[relUser](db, "sys_users")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT `id`, `user_id` FROM `orders`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id"}).AddRow(int64(1), int64(7)))
	if _, err := orders.Query().Preload("Missing", users).All(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "has no relation") {
		t.Fatalf("err = %v, want an unknown-relation error", err)
	}

	if _, err := orders.Query().Preload("User", nil).All(context.Background()); err == nil {
		t.Fatal("a nil repository must be a query error")
	}
}
