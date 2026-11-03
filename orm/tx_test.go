package orm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
)

type txOrder struct {
	ID       int64 `gable:"pk"`
	TenantID int64 `gable:"tenant"`
	Name     string
}

// Statements written inside the callback land on the transaction, and a clean return
// commits once.
func TestWithTxCommits(t *testing.T) {
	db, mock := newRelationDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `orders` (`id`, `tenant_id`, `name`) VALUES (?, ?, ?)").
		WithArgs(int64(1), int64(7), "first").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `orders` (`id`, `tenant_id`, `name`) VALUES (?, ?, ?)").
		WithArgs(int64(2), int64(7), "second").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// txOrder carries a tenant column, so the guard needs a tenant in the context — the
	// same rule as outside a transaction.
	ctx := tenant.WithTenant(context.Background(), tenant.Tenant{ID: 7, Code: "acme"})
	err := db.WithTx(ctx, func(ctx context.Context, tx *orm.Tx) error {
		repo, err := tx.Repo[txOrder]("orders")
		if err != nil {
			return err
		}
		if repo.Model().Table != "orders" {
			t.Fatalf("table = %q", repo.Model().Table)
		}
		if err := repo.Create(ctx, &txOrder{ID: 1, TenantID: 7, Name: "first"}); err != nil {
			return err
		}
		return repo.Create(ctx, &txOrder{ID: 2, TenantID: 7, Name: "second"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An error from the callback rolls everything back, and the caller sees the original
// error rather than a database detail.
func TestWithTxRollsBackOnError(t *testing.T) {
	db, mock := newRelationDB(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `orders` (`id`, `tenant_id`, `name`) VALUES (?, ?, ?)").
		WithArgs(int64(1), int64(7), "first").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectRollback()

	boom := errors.New("business rule failed")
	ctx := tenant.WithTenant(context.Background(), tenant.Tenant{ID: 7, Code: "acme"})
	err := db.WithTx(ctx, func(ctx context.Context, tx *orm.Tx) error {
		repo, err := tx.Repo[txOrder]("orders")
		if err != nil {
			return err
		}
		if err := repo.Create(ctx, &txOrder{ID: 1, TenantID: 7, Name: "first"}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the callback error", err)
	}
}

// A panic must roll back and propagate: swallowing it would let deferred cleanup run as
// if the work had succeeded.
func TestWithTxRollsBackOnPanic(t *testing.T) {
	db, mock := newRelationDB(t)
	mock.ExpectBegin()
	mock.ExpectRollback()

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic must propagate")
			}
		}()
		_ = db.WithTx(context.Background(), func(context.Context, *orm.Tx) error {
			panic("handler exploded")
		})
	}()
}

// The guards are repository behaviour, not connection behaviour: inside a transaction the
// tenant predicate is still injected and a missing tenant still fails closed.
func TestTxKeepsTheTenantGuard(t *testing.T) {
	db, mock := newRelationDB(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT `id`, `tenant_id`, `name` FROM `orders` WHERE `tenant_id` = ?").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "name"}).AddRow(int64(1), int64(7), "first"))
	mock.ExpectCommit()

	ctx := tenant.WithTenant(context.Background(), tenant.Tenant{ID: 7, Code: "acme"})
	if err := db.WithTx(ctx, func(ctx context.Context, tx *orm.Tx) error {
		repo, err := tx.Repo[txOrder]("orders")
		if err != nil {
			return err
		}
		rows, err := repo.Query().All(ctx)
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %+v", rows)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	mock.ExpectBegin()
	mock.ExpectRollback()
	err := db.WithTx(context.Background(), func(ctx context.Context, tx *orm.Tx) error {
		repo, err := tx.Repo[txOrder]("orders")
		if err != nil {
			return err
		}
		// No tenant in the context: the guard must refuse, as it does outside a tx.
		_, err = repo.Query().All(ctx)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Fatalf("err = %v, want a tenant error", err)
	}
}
