package migrate

import (
	"context"
	"errors"
	"testing"
)

type memoryStore struct {
	history []History
	applied []Migration
	failed  int
}

func (s *memoryStore) Ensure(context.Context) error { return nil }
func (s *memoryStore) List(context.Context) ([]History, error) {
	return append([]History(nil), s.history...), nil
}
func (s *memoryStore) Apply(_ context.Context, m Migration) error {
	s.applied = append(s.applied, m)
	checksum := m.Checksum
	s.history = append(s.history, History{Version: m.Version, Script: m.Script, Type: "SQL", Checksum: &checksum, Success: true})
	return nil
}
func (s *memoryStore) Record(_ context.Context, m Migration) error {
	s.history = append(s.history, History{Version: m.Version, Script: m.Script, Type: m.migrationType(), Success: true})
	return nil
}
func (s *memoryStore) DeleteFailed(context.Context) error {
	var kept []History
	for _, h := range s.history {
		if h.Success {
			kept = append(kept, h)
		} else {
			s.failed++
		}
	}
	s.history = kept
	return nil
}
func (s *memoryStore) Lock(context.Context, string) (func(), error) { return func() {}, nil }
func (s *memoryStore) Close() error                                 { return nil }

func migration(version, script string, checksum int32) Migration {
	return Migration{Version: version, Description: "m" + version, Script: script, Type: "SQL", Checksum: checksum}
}

func TestEngineUpIsIdempotent(t *testing.T) {
	store := &memoryStore{}
	engine, _ := NewEngine(store)
	ms := []Migration{migration("1", "V1__init.sql", 10), migration("2", "V2__add.sql", 20)}
	done, err := engine.Up(context.Background(), ms)
	if err != nil || len(done) != 2 {
		t.Fatalf("Up = %d, %v", len(done), err)
	}
	done, err = engine.Up(context.Background(), ms)
	if err != nil || len(done) != 0 {
		t.Fatalf("second Up = %d, %v", len(done), err)
	}
}

func TestEngineRejectsChecksumDrift(t *testing.T) {
	store := &memoryStore{}
	engine, _ := NewEngine(store)
	if _, err := engine.Up(context.Background(), []Migration{migration("1", "V1__init.sql", 10)}); err != nil {
		t.Fatal(err)
	}
	err := engine.Validate(context.Background(), []Migration{migration("1", "V1__init.sql", 11)})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("Validate = %v, want ErrValidation", err)
	}
}

func TestEngineBaselineSkipsLowerVersions(t *testing.T) {
	store := &memoryStore{}
	engine, _ := NewEngine(store)
	if err := engine.Baseline(context.Background(), "5"); err != nil {
		t.Fatal(err)
	}
	done, err := engine.Up(context.Background(), []Migration{
		migration("1", "V1__old.sql", 1),
		migration("6", "V6__new.sql", 6),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].Version != "6" {
		t.Fatalf("done = %+v", done)
	}
}

func TestEngineOutOfOrder(t *testing.T) {
	store := &memoryStore{}
	engine, _ := NewEngine(store)
	if _, err := engine.Up(context.Background(), []Migration{migration("2", "V2__two.sql", 2)}); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Up(context.Background(), []Migration{
		migration("2", "V2__two.sql", 2),
		migration("1", "V1__one.sql", 1),
	})
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("Up = %v, want ErrOutOfOrder", err)
	}
}

func TestEngineRepairRemovesFailedRows(t *testing.T) {
	store := &memoryStore{history: []History{{Version: "1", Script: "V1__init.sql", Type: "SQL", Success: false}}}
	engine, _ := NewEngine(store)
	if err := engine.Repair(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.failed != 1 || len(store.history) != 0 {
		t.Fatalf("failed = %d, history = %+v", store.failed, store.history)
	}
}

func TestEngineBaselineRequiresEmptyHistory(t *testing.T) {
	store := &memoryStore{history: []History{{Version: "1", Type: "SQL", Success: true}}}
	engine, _ := NewEngine(store)
	if err := engine.Baseline(context.Background(), "1"); !errors.Is(err, ErrBaselineExists) {
		t.Fatalf("Baseline = %v, want ErrBaselineExists", err)
	}
}
