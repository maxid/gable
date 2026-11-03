package main

import (
	"context"
	"net/http"

	"github.com/maxid/gable/web"
)

// usersModule publishes the user service. It is written against this pilot's platform
// contract only — no gable import, no gable type in its signature.
type usersModule struct{ service *UserService }

// NewUsersModule returns the module with an empty service.
func NewUsersModule() *usersModule {
	return &usersModule{service: &UserService{users: map[int64]*User{}}}
}

func (m *usersModule) ID() string      { return "users" }
func (m *usersModule) Version() string { return "1.4.0" }

func (m *usersModule) DependsOn() []string { return nil }

func (m *usersModule) Register(c *Container) error {
	c.Set("admin.users", m.service)
	return nil
}

func (m *usersModule) Start(context.Context) error {
	m.service.Create(User{ID: 1, Username: "admin", Roles: []string{"admin"}})
	return nil
}

func (m *usersModule) Stop(context.Context) error { return nil }

// rolesModule publishes the role service.
type rolesModule struct{ service *RoleService }

// NewRolesModule returns the module with an empty service.
func NewRolesModule() *rolesModule {
	return &rolesModule{service: &RoleService{roles: map[string]string{}}}
}

func (m *rolesModule) ID() string          { return "roles" }
func (m *rolesModule) Version() string     { return "1.2.0" }
func (m *rolesModule) DependsOn() []string { return nil }

func (m *rolesModule) Register(c *Container) error {
	c.Set("admin.roles", m.service)
	return nil
}

func (m *rolesModule) Start(context.Context) error {
	m.service.Add("admin", "Administrator")
	m.service.Add("member", "Member")
	return nil
}

func (m *rolesModule) Stop(context.Context) error { return nil }

// adminModule wires the HTTP surface.
//
// It depends on the two services by id, so the assembly starts them first, and it reads them
// out of the container exactly as a platform module would. A missing service is a Register
// error, not a nil dereference on the first request.
type adminModule struct{ server *web.Server }

// NewAdminModule returns the module bound to a server.
func NewAdminModule(server *web.Server) *adminModule { return &adminModule{server: server} }

func (m *adminModule) ID() string                  { return "admin" }
func (m *adminModule) Version() string             { return "0.9.0" }
func (m *adminModule) DependsOn() []string         { return []string{"roles", "users"} }
func (m *adminModule) Start(context.Context) error { return nil }
func (m *adminModule) Stop(context.Context) error  { return nil }

func (m *adminModule) Register(c *Container) error {
	users, err := Require[*UserService](c, "admin.users")
	if err != nil {
		return err
	}
	roles, err := Require[*RoleService](c, "admin.roles")
	if err != nil {
		return err
	}
	m.server.Router().GET("/admin/users", func(ctx *web.Context) error {
		return ctx.JSON(http.StatusOK, users.List())
	}).Doc("list users").Tags("admin")

	m.server.Router().GET("/admin/roles", func(ctx *web.Context) error {
		return ctx.JSON(http.StatusOK, roles.All())
	}).Doc("list roles").Tags("admin")

	return nil
}
