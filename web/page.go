package web

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	gableerrors "github.com/maxid/gable/errors"
)

// Page is a validated pagination request.
//
// Handlers used to parse page/size/sort by hand, which is how one endpoint ends up sorting
// by a column that is not in the response and another silently accepts `order=drop table`.
// Page enforces the sort whitelist the caller declares.
type Page struct {
	// Number is the 1-based page.
	Number int `json:"page"`
	// Size is the page size, already clamped to MaxPageSize.
	Size int `json:"size"`
	// Sort is the column to order by, or empty for the handler's default.
	Sort string `json:"sort,omitempty"`
	// Desc reports a descending order.
	Desc bool `json:"desc,omitempty"`
}

// Pagination bounds.
const (
	DefaultPageSize = 20
	MaxPageSize     = 200
)

// Offset is the row offset to pass to a query.
func (p Page) Offset() int {
	if p.Number < 1 {
		return 0
	}
	return (p.Number - 1) * p.Size
}

// Limit is the row count to pass to a query.
func (p Page) Limit() int { return p.Size }

// Page parses the pagination of the request and validates the sort column against the
// allowed list.
//
// It accepts both this framework's spelling (page/size/sort/order) and the vamis aliases the
// existing frontend sends (perPage/orderBy/orderDir), because a frontend migration should not
// need a new query parser. An unknown sort column is an error listing what is allowed: a
// silently ignored sort returns the wrong order and looks like a data bug.
func (c *Context) Page(allowedSorts ...string) (Page, error) {
	query := c.Request().URL.Query()
	page := Page{Number: intParam(query, "page", 1), Size: intParam(query, "size", 0)}
	if page.Size == 0 {
		page.Size = intParam(query, "perPage", 0)
	}
	if page.Size <= 0 {
		page.Size = DefaultPageSize
	}
	if page.Size > MaxPageSize {
		page.Size = MaxPageSize
	}
	if page.Number < 1 {
		page.Number = 1
	}

	page.Sort = firstNonEmpty(query.Get("sort"), query.Get("orderBy"))
	if page.Sort != "" && len(allowedSorts) > 0 && !containsFold(allowedSorts, page.Sort) {
		return Page{}, gableerrors.New(gableerrors.CodeInvalid,
			fmt.Sprintf("invalid sort %q: allowed %s", page.Sort, strings.Join(allowedSorts, ", "))).
			WithStatus(400)
	}

	order := strings.ToLower(firstNonEmpty(query.Get("order"), query.Get("orderDir")))
	switch order {
	case "", "asc", "ascending":
		page.Desc = false
	case "desc", "descending":
		page.Desc = true
	default:
		return Page{}, gableerrors.New(gableerrors.CodeInvalid,
			fmt.Sprintf("invalid order %q: expected asc or desc", order)).WithStatus(400)
	}
	return page, nil
}

func intParam(query url.Values, name string, fallback int) int {
	raw := query.Get(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}
