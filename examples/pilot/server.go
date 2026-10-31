package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	gableerrors "github.com/maxid/gable/errors"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

type createTaskRequest struct {
	Title  string `json:"title" binding:"required" doc:"task title" example:"write the docs"`
	Status string `json:"status" doc:"todo | doing | done" example:"todo"`
}

type updateTaskRequest struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

type taskResponse struct {
	ID        int64     `json:"id"`
	TenantID  int64     `json:"tenantId"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type taskListResponse struct {
	Items []taskResponse `json:"items"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
}

func toTaskResponse(task Task) taskResponse {
	return taskResponse{
		ID:        task.ID,
		TenantID:  task.TenantID,
		Title:     task.Title,
		Status:    task.Status,
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
	}
}

type taskController struct {
	store TaskStore
}

func (c *taskController) Routes(r *web.Router) {
	r.POST("/tasks", c.create).Doc("create a task").In(createTaskRequest{}).Out(taskResponse{}).Status(201)
	r.GET("/tasks", c.list).Doc("list tasks").
		Query("status", "todo | doing | done", false).
		Query("page", "1-based page", false).
		Query("size", "page size, 0 defaults to 20", false).
		Out(taskListResponse{})
	r.GET("/tasks/:id", c.get).Doc("get a task").Out(taskResponse{})
	r.PUT("/tasks/:id", c.update).Doc("update a task").In(updateTaskRequest{}).Out(taskResponse{})
	r.DELETE("/tasks/:id", c.delete).Doc("delete a task").Out(map[string]bool{})
	r.GET("/whoami", c.whoami).Doc("current tenant")
}

func (c *taskController) create(ctx *web.Context) error {
	var request createTaskRequest
	if err := ctx.Bind(&request); err != nil {
		return err
	}
	status := request.Status
	if status == "" {
		status = "todo"
	}
	task := &Task{Title: request.Title, Status: status}
	if err := c.store.Create(ctx.Request().Context(), task); err != nil {
		return err
	}
	return ctx.JSON(201, toTaskResponse(*task))
}

func (c *taskController) list(ctx *web.Context) error {
	page := ctx.QueryInt("page", 1)
	size := ctx.QueryInt("size", 20)
	items, total, err := c.store.List(ctx.Request().Context(), ctx.Query("status"), page, size)
	if err != nil {
		return err
	}
	response := taskListResponse{Items: make([]taskResponse, 0, len(items)), Total: total, Page: page, Size: size}
	for _, task := range items {
		response.Items = append(response.Items, toTaskResponse(task))
	}
	return ctx.JSON(200, response)
}

func (c *taskController) get(ctx *web.Context) error {
	id, err := taskID(ctx)
	if err != nil {
		return err
	}
	task, err := c.store.Get(ctx.Request().Context(), id)
	if err != nil {
		return err
	}
	return ctx.JSON(200, toTaskResponse(*task))
}

func (c *taskController) update(ctx *web.Context) error {
	id, err := taskID(ctx)
	if err != nil {
		return err
	}
	var request updateTaskRequest
	if err := ctx.Bind(&request); err != nil {
		return err
	}

	task, err := c.store.Get(ctx.Request().Context(), id)
	if err != nil {
		return err
	}
	if request.Title != "" {
		task.Title = request.Title
	}
	if request.Status != "" {
		task.Status = request.Status
	}
	if err := c.store.Update(ctx.Request().Context(), task); err != nil {
		return err
	}
	return ctx.JSON(200, toTaskResponse(*task))
}

func (c *taskController) delete(ctx *web.Context) error {
	id, err := taskID(ctx)
	if err != nil {
		return err
	}
	if err := c.store.Delete(ctx.Request().Context(), id); err != nil {
		return err
	}
	return ctx.JSON(200, map[string]bool{"deleted": true})
}

func (c *taskController) whoami(ctx *web.Context) error {
	resolved, ok := ctx.Tenant()
	if !ok {
		return tenant.ErrRequired
	}
	return ctx.JSON(200, map[string]any{"tenantId": resolved.ID, "tenantCode": resolved.Code})
}

func taskID(ctx *web.Context) (int64, error) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, gableerrors.New(gableerrors.CodeInvalid, "invalid task id").WithStatus(400)
	}
	return id, nil
}

type loginController struct {
	resolver tenant.LoginResolver
}

func (c *loginController) Routes(r *web.Router) {
	r.GET("/login/options", func(ctx *web.Context) error {
		resolved, err := c.resolver.ResolveLogin(ctx.Request().Context(), ctx.Request().Host, ctx.Query("tenantCode"))
		if err != nil {
			return err
		}
		return ctx.JSON(200, map[string]any{"tenantId": resolved.ID, "tenantCode": resolved.Code})
	}).Doc("resolve the login tenant from the host")
}

func pilotSource() tenant.Source {
	// Demo-only source. A real app reads the principal from its auth middleware.
	return tenant.SourceFunc(func(_ context.Context, request *http.Request) (tenant.Principal, bool, error) {
		raw := request.Header.Get("X-User-Tenant")
		if raw == "" {
			return tenant.Principal{}, false, nil
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return tenant.Principal{}, false, nil
		}
		return tenant.Principal{TenantID: id, Operator: request.Header.Get("X-Operator") == "1"}, true, nil
	})
}

func pilotTenants() *tenant.MemoryStore {
	return tenant.NewMemoryStore(
		tenant.Tenant{ID: 1, Code: "management"},
		tenant.Tenant{ID: 2, Code: "acme"},
		tenant.Tenant{ID: 3, Code: "beta"},
	)
}

func newServer(cfg *config.Config, tasks TaskStore, tenants tenant.Store) (*web.Server, error) {
	server := web.New(
		web.WithTitle("gable-pilot"),
		web.WithVersion(gable.Version),
		web.WithDescription("tenant-scoped task board on gable"),
	)

	var login tenant.LoginResolver
	var resolver tenant.Resolver
	switch cfg.Tenant.Mode {
	case tenant.ModeSaaS:
		if tenants == nil {
			return nil, fmt.Errorf("pilot: saas mode requires a tenant store")
		}
		saasLogin, err := tenant.NewSaaSLoginResolver(cfg.Tenant, tenants)
		if err != nil {
			return nil, err
		}
		saasResolver, err := tenant.NewSaaSResolver(cfg.Tenant, tenants, pilotSource())
		if err != nil {
			return nil, err
		}
		login = saasLogin
		resolver = saasResolver
	default:
		login = tenant.NewSingleLoginResolver(cfg.Tenant)
		resolver = tenant.NewSingleResolver(cfg.Tenant)
	}

	server.Register(&loginController{resolver: login})
	server.UseTenant(resolver)
	server.Register(&taskController{store: tasks})
	return server, nil
}
