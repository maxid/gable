package orm_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/maxid/gable/orm"
)

// The guards are SQL, so sqlmock proves the statements but not that the engine accepts
// them. This test runs the same flow against real databases when they are configured:
//
//	GABLE_TEST_MYSQL_DSN='root:secret@tcp(127.0.0.1:3306)/app?parseTime=true' \
//	GABLE_TEST_POSTGRES_DSN='postgres://postgres:secret@127.0.0.1:5432/app?sslmode=disable' \
//	go test ./orm -run Integration
type integrationDoc struct {
	ID        int64      `gable:"pk,auto"`
	Title     string     `gable:"size=64"`
	DeletedAt *time.Time `gable:"soft_delete"`
}

type integrationOwner struct {
	ID   int64  `gable:"pk,auto"`
	Name string `gable:"size=64"`
}

type integrationItem struct {
	ID      int64 `gable:"pk,auto"`
	OrderID int64
	OwnerID int64
	Owner   *integrationOwner `gable:"belongs_to=gable_it_owners,foreign_key=owner_id"`
	Label   string            `gable:"size=64"`
}

type integrationTarget struct {
	name   string
	driver string
	dsn    string
}

func integrationTargets(t *testing.T) []integrationTarget {
	t.Helper()
	targets := []integrationTarget{
		{name: "mysql", driver: "mysql", dsn: os.Getenv("GABLE_TEST_MYSQL_DSN")},
		{name: "postgres", driver: "postgres", dsn: os.Getenv("GABLE_TEST_POSTGRES_DSN")},
	}
	available := targets[:0]
	for _, target := range targets {
		if target.dsn != "" {
			available = append(available, target)
		}
	}
	if len(available) == 0 {
		t.Skip("set GABLE_TEST_MYSQL_DSN or GABLE_TEST_POSTGRES_DSN to run the database integration test")
	}
	return available
}

func TestIntegrationSoftDeleteAndPreload(t *testing.T) {
	for _, target := range integrationTargets(t) {
		t.Run(target.name, func(t *testing.T) {
			db, err := orm.Open(target.driver, target.dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			runSoftDeleteAndPreload(t, db)
		})
	}
}

func runSoftDeleteAndPreload(t *testing.T, db *orm.DB) {
	t.Helper()
	ctx := context.Background()

	docs, err := orm.NewRepo[integrationDoc](db, "gable_it_docs")
	if err != nil {
		t.Fatal(err)
	}
	items, err := orm.NewRepo[integrationItem](db, "gable_it_items")
	if err != nil {
		t.Fatal(err)
	}
	owners, err := orm.NewRepo[integrationOwner](db, "gable_it_owners")
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range []interface{ Model() *orm.Model }{docs, items, owners} {
		if err := db.AutoMigrate(repo.Model()); err != nil {
			t.Fatalf("migrate %s: %v", repo.Model().Table, err)
		}
	}
	for _, table := range []string{"gable_it_docs", "gable_it_items", "gable_it_owners"} {
		if _, err := db.ExecContext(ctx, "DELETE FROM "+db.Dialect().Quote(table)); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}

	// Soft delete: the row survives, the scoped read stops seeing it, Unscoped finds it.
	doc := &integrationDoc{Title: "lease"}
	if err := docs.Create(ctx, doc); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := docs.Delete(ctx, doc.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, err := docs.Get(ctx, doc.ID); err != orm.ErrNotFound {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
	hidden, err := docs.Unscoped().Where(orm.Eq("id", doc.ID)).All(ctx)
	if err != nil {
		t.Fatalf("unscoped: %v", err)
	}
	if len(hidden) != 1 || hidden[0].DeletedAt == nil {
		t.Fatalf("hidden = %+v, want one row with a timestamp", hidden)
	}
	if err := docs.Restore(ctx, doc.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := docs.Get(ctx, doc.ID); err != nil {
		t.Fatalf("get after restore: %v", err)
	}

	// Relations: one query for the parents, one for the related rows.
	for _, name := range []string{"alice", "bob"} {
		if err := owners.Create(ctx, &integrationOwner{Name: name}); err != nil {
			t.Fatalf("create owner %s: %v", name, err)
		}
	}
	all, err := owners.Query().OrderBy("id", false).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("owners = %+v", all)
	}
	for i, owner := range all {
		for n := 0; n < 2; n++ {
			item := &integrationItem{OrderID: int64(100 + i), OwnerID: owner.ID, Label: fmt.Sprintf("%s-%d", owner.Name, n)}
			if err := items.Create(ctx, item); err != nil {
				t.Fatalf("create item: %v", err)
			}
		}
	}

	loaded, err := items.Query().OrderBy("id", false).Preload("Owner", owners).All(ctx)
	if err != nil {
		t.Fatalf("preload: %v", err)
	}
	if len(loaded) != 4 {
		t.Fatalf("items = %+v", loaded)
	}
	for _, item := range loaded {
		if item.Owner == nil {
			t.Fatalf("item %d has no owner loaded", item.ID)
		}
		if item.Owner.ID != item.OwnerID {
			t.Fatalf("item %d owner = %d, want %d", item.ID, item.Owner.ID, item.OwnerID)
		}
	}
}
