package main

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/maxid/gcosid"

	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
)

// Task is the pilot's tenant-scoped record. The gable tags drive both the
// schema and the tenant guard.
type Task struct {
	ID        int64     `gable:"pk"`
	TenantID  int64     `gable:"tenant"`
	Title     string    `gable:"size=128"`
	Status    string    `gable:"size=16"`
	CreatedAt time.Time `gable:"auto_create"`
	UpdatedAt time.Time `gable:"auto_update"`
}

const taskTable = "pilot_task"

// TaskStore is the use-case boundary the HTTP layer depends on.
type TaskStore interface {
	Create(ctx context.Context, task *Task) error
	Get(ctx context.Context, id int64) (*Task, error)
	List(ctx context.Context, status string, page, size int) ([]Task, int64, error)
	Update(ctx context.Context, task *Task) error
	Delete(ctx context.Context, id int64) error
}

type memoryTaskStore struct {
	mu    sync.RWMutex
	next  int64
	tasks map[int64]Task
}

func newMemoryTaskStore() *memoryTaskStore {
	return &memoryTaskStore{tasks: map[int64]Task{}}
}

func (s *memoryTaskStore) Create(ctx context.Context, task *Task) error {
	current, err := tenant.MustFromContext(ctx)
	if err != nil {
		return err
	}
	if task.TenantID == 0 {
		task.TenantID = current.ID
	} else if task.TenantID != current.ID {
		return orm.ErrTenantMismatch
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	now := time.Now()
	task.ID = s.next
	task.CreatedAt = now
	task.UpdatedAt = now
	s.tasks[task.ID] = *task
	return nil
}

func (s *memoryTaskStore) Get(ctx context.Context, id int64) (*Task, error) {
	current, err := tenant.MustFromContext(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok || task.TenantID != current.ID {
		return nil, orm.ErrNotFound
	}
	copy := task
	return &copy, nil
}

func (s *memoryTaskStore) List(ctx context.Context, status string, page, size int) ([]Task, int64, error) {
	current, err := tenant.MustFromContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	switch {
	case size < 0:
		size = 0
	case size == 0:
		size = 20
	}

	s.mu.RLock()
	items := make([]Task, 0, len(s.tasks))
	for _, task := range s.tasks {
		if task.TenantID != current.ID {
			continue
		}
		if status != "" && task.Status != status {
			continue
		}
		items = append(items, task)
	}
	s.mu.RUnlock()

	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	total := int64(len(items))
	if size == 0 {
		return items, total, nil
	}
	start := (page - 1) * size
	if start >= len(items) {
		return []Task{}, total, nil
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], total, nil
}

func (s *memoryTaskStore) Update(ctx context.Context, task *Task) error {
	current, err := tenant.MustFromContext(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tasks[task.ID]
	if !ok || existing.TenantID != current.ID {
		return orm.ErrNotFound
	}
	existing.Title = task.Title
	existing.Status = task.Status
	existing.UpdatedAt = time.Now()
	s.tasks[existing.ID] = existing
	*task = existing
	return nil
}

func (s *memoryTaskStore) Delete(ctx context.Context, id int64) error {
	current, err := tenant.MustFromContext(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok || task.TenantID != current.ID {
		return orm.ErrNotFound
	}
	delete(s.tasks, id)
	return nil
}

type ormTaskStore struct {
	repo *orm.Repo[Task]
}

func newORMStore(db *orm.DB, ids gcosid.Generator) (*ormTaskStore, error) {
	model, err := orm.ModelOf[Task](taskTable)
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(model); err != nil {
		return nil, err
	}
	repo, err := orm.NewRepo[Task](db, taskTable, orm.WithIDGenerator(ids))
	if err != nil {
		return nil, err
	}
	return &ormTaskStore{repo: repo}, nil
}

func (s *ormTaskStore) Create(ctx context.Context, task *Task) error {
	return s.repo.Create(ctx, task)
}

func (s *ormTaskStore) Get(ctx context.Context, id int64) (*Task, error) {
	return s.repo.Get(ctx, id)
}

func (s *ormTaskStore) List(ctx context.Context, status string, page, size int) ([]Task, int64, error) {
	query := s.repo.Query()
	if status != "" {
		query = query.Where(orm.Eq("status", status))
	}
	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	items, err := query.OrderBy("id", false).Page(page, size).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *ormTaskStore) Update(ctx context.Context, task *Task) error {
	return s.repo.Update(ctx, task)
}

func (s *ormTaskStore) Delete(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}
