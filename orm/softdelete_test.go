package orm

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type document struct {
	ID        int64 `gable:"pk"`
	Title     string
	DeletedAt *time.Time `gable:"soft_delete"`
}

type flagged struct {
	ID     int64 `gable:"pk"`
	Name   string
	Retain bool `gable:"retain"`
}

// A soft_deleted row is invisible to every scoped read, in one place rather than in each
// caller's WHERE clause.
func TestSoftDeleteHidesRowsFromScopedReads(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[document](db, "documents")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT `id`, `title`, `deleted_at` FROM `documents` WHERE `deleted_at` IS NULL").
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "deleted_at"}).AddRow(int64(1), "kept", nil))
	rows, err := repo.Query().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Title != "kept" {
		t.Fatalf("rows = %+v", rows)
	}

	mock.ExpectQuery("SELECT `id`, `title`, `deleted_at` FROM `documents` WHERE `deleted_at` IS NULL AND `id` = ?").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "deleted_at"}))
	if _, err := repo.Get(context.Background(), 1); err != ErrNotFound {
		t.Fatalf("get deleted row = %v, want ErrNotFound", err)
	}

	// Unscoped is the one way to read a hidden row, for cleanup and for audits.
	mock.ExpectQuery("SELECT `id`, `title`, `deleted_at` FROM `documents`").
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "deleted_at"}).
			AddRow(int64(2), "gone", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)))
	all, err := repo.Unscoped().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].DeletedAt == nil {
		t.Fatalf("unscoped rows = %+v", all)
	}
}

// Delete becomes a timestamp write, not a DELETE, and stays inside the guard so a second
// delete is a not-found rather than a silent success.
func TestSoftDeleteWritesTheTimestamp(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[document](db, "documents")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("UPDATE `documents` SET `deleted_at` = ? WHERE `deleted_at` IS NULL AND `id` = ?").
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Delete(context.Background(), 7); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec("UPDATE `documents` SET `deleted_at` = ? WHERE `deleted_at` IS NULL AND `id` = ?").
		WithArgs(sqlmock.AnyArg(), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.Delete(context.Background(), 7); err != ErrNotFound {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}

	// Restore reaches the hidden row: the guard would otherwise select nothing.
	mock.ExpectExec("UPDATE `documents` SET `deleted_at` = NULL WHERE `id` = ?").
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Restore(context.Background(), 7); err != nil {
		t.Fatal(err)
	}

	// Purge is the only path that removes the row, and it also reaches hidden rows.
	mock.ExpectExec("DELETE FROM `documents` WHERE `id` = ?").
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Purge(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
}

// The retain flag is the other spelling of the same rule, with false meaning visible.
func TestRetainFlagIsTheGuard(t *testing.T) {
	db, mock := newMock(t, Postgres())
	repo, err := NewRepo[flagged](db, "flags")
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery(`SELECT "id", "name", "retain" FROM "flags" WHERE "retain" = $1`).
		WithArgs(false).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "retain"}).AddRow(int64(1), "a", false))
	if _, err := repo.Query().All(context.Background()); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec(`UPDATE "flags" SET "retain" = $1 WHERE "retain" = $2 AND "id" = $3`).
		WithArgs(true, false, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Delete(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec(`UPDATE "flags" SET "retain" = $1 WHERE "id" = $2`).
		WithArgs(false, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Restore(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	mock.ExpectExec(`DELETE FROM "flags" WHERE "id" = $1`).
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Purge(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
}

// Update owns every mutable column but not the delete markers: a stale entity must not
// resurrect a deleted row.
func TestUpdateLeavesTheDeleteMarkerAlone(t *testing.T) {
	db, mock := newMock(t, MySQL())
	repo, err := NewRepo[document](db, "documents")
	if err != nil {
		t.Fatal(err)
	}

	deleted := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectExec("UPDATE `documents` SET `title` = ? WHERE `deleted_at` IS NULL AND `id` = ?").
		WithArgs("renamed", int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Update(context.Background(), &document{ID: 3, Title: "renamed", DeletedAt: &deleted}); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteMarkerValidation(t *testing.T) {
	cases := []struct {
		name   string
		entity any
		want   string
	}{
		{
			name: "soft_delete needs a pointer",
			entity: struct {
				ID        int64     `gable:"pk"`
				DeletedAt time.Time `gable:"soft_delete"`
			}{},
			want: "soft_delete",
		},
		{
			name: "retain needs a bool",
			entity: struct {
				ID     int64 `gable:"pk"`
				Retain int   `gable:"retain"`
			}{},
			want: "retain",
		},
		{
			name: "two soft_delete fields",
			entity: struct {
				ID     int64      `gable:"pk"`
				First  *time.Time `gable:"soft_delete"`
				Second *time.Time `gable:"soft_delete"`
			}{},
			want: "more than one soft_delete",
		},
		{
			name: "both spellings",
			entity: struct {
				ID        int64      `gable:"pk"`
				DeletedAt *time.Time `gable:"soft_delete"`
				Retain    bool       `gable:"retain"`
			}{},
			want: "both soft_delete and retain",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typ := reflect.TypeOf(tc.entity)
			if _, err := buildModel(typ, "markers"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
