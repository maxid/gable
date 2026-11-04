package dialect

import (
	"errors"
	"testing"
)

func TestPlaceholdersAndQuoting(t *testing.T) {
	cases := []struct {
		d        Dialect
		ph       []string
		quote    string
		quotedOK bool
	}{
		{MySQL(), []string{"?", "?"}, "`user`", true},
		{Postgres(), []string{"$1", "$2"}, `"user"`, true},
		{ClickHouse(), []string{"?", "?"}, "`user`", true},
		{DM(), []string{"?", "?"}, `"user"`, true},
	}
	for _, c := range cases {
		if got := c.d.Placeholder(1); got != c.ph[0] {
			t.Fatalf("%s placeholder1 = %q", c.d.Name(), got)
		}
		if got := c.d.Placeholder(2); got != c.ph[1] {
			t.Fatalf("%s placeholder2 = %q", c.d.Name(), got)
		}
		got, err := c.d.Quote("user")
		if err != nil || got != c.quote {
			t.Fatalf("%s quote = %q err=%v", c.d.Name(), got, err)
		}
	}
}

func TestQuoteRejectsUnsafeIdentifiers(t *testing.T) {
	for _, bad := range []string{"", "user;drop", "a b", "1abc", "a-b"} {
		if got, err := MySQL().Quote(bad); err == nil {
			t.Fatalf("%q accepted -> %q", bad, got)
		}
	}
	if got, err := Postgres().Quote("public.users"); err != nil || got != `"public"."users"` {
		t.Fatalf("dotted quote = %q err=%v", got, err)
	}
}

func TestPagination(t *testing.T) {
	if got := MySQL().Pagination(10, 20, 0); got != "LIMIT ? OFFSET ?" {
		t.Fatalf("mysql pagination = %q", got)
	}
	if got := Postgres().Pagination(10, 20, 3); got != "LIMIT $4 OFFSET $5" {
		t.Fatalf("pg pagination = %q", got)
	}
	if got := DM().Pagination(10, 20, 0); got != "OFFSET ? ROWS FETCH NEXT ? ROWS ONLY" {
		t.Fatalf("dm pagination = %q", got)
	}
	// Negative values clamp to zero; DM order is offset then limit.
	if args := MySQL().PaginationArgs(-5, -7); args[0] != 0 || args[1] != 0 {
		t.Fatalf("mysql args = %v", args)
	}
	if args := DM().PaginationArgs(10, 20); args[0] != 20 || args[1] != 10 {
		t.Fatalf("dm args = %v", args)
	}
}

func TestUpsertMySQL(t *testing.T) {
	sql, err := MySQL().Upsert("demo_user", []string{"tenant_id", "username"}, []string{"tenant_id", "username", "password", "salt"})
	if err != nil {
		t.Fatal(err)
	}
	want := "INSERT INTO `demo_user` (`tenant_id`, `username`, `password`, `salt`) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE `password` = VALUES(`password`), `salt` = VALUES(`salt`)"
	if sql != want {
		t.Fatalf("sql =\n%s\nwant\n%s", sql, want)
	}
}

func TestUpsertPostgres(t *testing.T) {
	sql, err := Postgres().Upsert("demo_user", []string{"tenant_id", "username"}, []string{"tenant_id", "username", "password", "salt"})
	if err != nil {
		t.Fatal(err)
	}
	want := `INSERT INTO "demo_user" ("tenant_id", "username", "password", "salt") VALUES ($1, $2, $3, $4) ON CONFLICT ("tenant_id", "username") DO UPDATE SET "password" = EXCLUDED."password", "salt" = EXCLUDED."salt"`
	if sql != want {
		t.Fatalf("sql =\n%s\nwant\n%s", sql, want)
	}
}

func TestUpsertKeyOnlyAndNoKeys(t *testing.T) {
	sql, err := MySQL().Upsert("t", []string{"k"}, []string{"k"})
	if err != nil {
		t.Fatal(err)
	}
	if sql != "INSERT INTO `t` (`k`) VALUES (?) ON DUPLICATE KEY UPDATE `k` = `k`" {
		t.Fatalf("sql = %s", sql)
	}
	sql, err = Postgres().Upsert("t", []string{"k"}, []string{"k"})
	if err != nil {
		t.Fatal(err)
	}
	if sql != `INSERT INTO "t" ("k") VALUES ($1) ON CONFLICT ("k") DO NOTHING` {
		t.Fatalf("sql = %s", sql)
	}
}

func TestUpsertRejectsBadInput(t *testing.T) {
	if _, err := MySQL().Upsert("t", []string{"missing"}, []string{"k"}); err == nil {
		t.Fatal("expected key subset error")
	}
	if _, err := MySQL().Upsert("t", []string{"k"}, []string{"k", "bad-name"}); err == nil {
		t.Fatal("expected unsafe identifier error")
	}
	if _, err := ClickHouse().Upsert("t", []string{"k"}, []string{"k"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("clickhouse err = %v", err)
	}
	if _, err := DM().Upsert("t", []string{"k"}, []string{"k"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("dm err = %v", err)
	}
}

func TestDialectFor(t *testing.T) {
	for in, want := range map[string]string{
		"mysql": "mysql", "tidb": "mysql", "oceanbase": "mysql",
		"postgres": "postgres", "kingbase": "postgres", "gaussdb": "postgres",
		"clickhouse": "clickhouse", "dm": "dm", "dameng": "dm",
	} {
		d, err := DialectFor(in)
		if err != nil || d.Name() != want {
			t.Fatalf("%q -> %v err=%v", in, d, err)
		}
	}
	if _, err := DialectFor("oracle"); err == nil {
		t.Fatal("expected unknown dialect error")
	}
}
