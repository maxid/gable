package dsl

import (
	"net/http"
	"strconv"

	"github.com/maxid/gable/web"
)

// Routes registers the dynamic data API for every model under prefix
// (default /dsl/data). The model name and row id are path parameters, so a new
// Model resource needs no new Go route.
func (r *Runtime) Routes(router *web.Router, prefix string) {
	if prefix == "" {
		prefix = "/dsl/data"
	}
	group := router.Group(prefix, web.WithTags("dsl-data"))
	group.GET("/:model", r.listHTTP).Doc("list rows").
		Query("page", "1-based page", false).
		Query("size", "page size, max 200", false).
		Query("sort", "sort field, defaults to the primary key", false).
		Query("order", "asc | desc", false).
		Out(PageResult{})
	group.POST("/:model", r.createHTTP).Doc("create a row").In(map[string]any{}).Out(map[string]any{}).Status(http.StatusCreated)
	group.GET("/:model/:id", r.getHTTP).Doc("get a row").Out(map[string]any{})
	group.PUT("/:model/:id", r.updateHTTP).Doc("update a row").In(map[string]any{}).Out(map[string]any{})
	group.DELETE("/:model/:id", r.deleteHTTP).Doc("delete a row").Out(map[string]bool{})
}

func (r *Runtime) listHTTP(ctx *web.Context) error {
	filters := map[string]string{}
	for key, values := range ctx.Request().URL.Query() {
		switch key {
		case "page", "size", "perPage", "sort", "order", "orderBy", "orderDir", "keywords":
			continue
		}
		if len(values) > 0 {
			filters[key] = values[0]
		}
	}
	size := ctx.QueryInt("size", 0)
	if size == 0 {
		size = ctx.QueryInt("perPage", 20)
	}
	sort := ctx.Query("sort")
	if sort == "" {
		sort = ctx.Query("orderBy")
	}
	order := ctx.Query("order")
	if order == "" {
		order = ctx.Query("orderDir")
	}
	result, err := r.List(ctx.Request().Context(), ctx.Param("model"), ListOptions{
		Page:    ctx.QueryInt("page", 1),
		Size:    size,
		Sort:    sort,
		Order:   order,
		Filters: filters,
	})
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, result)
}

func (r *Runtime) getHTTP(ctx *web.Context) error {
	id, err := rowID(ctx)
	if err != nil {
		return err
	}
	item, err := r.Get(ctx.Request().Context(), ctx.Param("model"), id)
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, item)
}

func (r *Runtime) createHTTP(ctx *web.Context) error {
	var payload map[string]any
	if err := ctx.Bind(&payload); err != nil {
		return err
	}
	item, err := r.Create(ctx.Request().Context(), ctx.Param("model"), payload)
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusCreated, item)
}

func (r *Runtime) updateHTTP(ctx *web.Context) error {
	id, err := rowID(ctx)
	if err != nil {
		return err
	}
	var payload map[string]any
	if err := ctx.Bind(&payload); err != nil {
		return err
	}
	item, err := r.Update(ctx.Request().Context(), ctx.Param("model"), id, payload)
	if err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, item)
}

func (r *Runtime) deleteHTTP(ctx *web.Context) error {
	id, err := rowID(ctx)
	if err != nil {
		return err
	}
	if err := r.Delete(ctx.Request().Context(), ctx.Param("model"), id); err != nil {
		return err
	}
	return ctx.JSON(http.StatusOK, map[string]bool{"deleted": true})
}

func rowID(ctx *web.Context) (int64, error) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalid("invalid row id")
	}
	return id, nil
}
