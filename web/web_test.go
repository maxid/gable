package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	gableerrors "github.com/maxid/gable/errors"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	gin.DefaultWriter = io.Discard
	gin.DefaultErrorWriter = io.Discard
	os.Exit(m.Run())
}

type helloResponse struct {
	Message string `json:"message"`
}

type createUserRequest struct {
	Name string `json:"name" binding:"required"`
	Age  int    `json:"age"`
}

type userResponse struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type testController struct{}

func (testController) Routes(r *Router) {
	r.GET("/hello/:name", func(c *Context) error {
		return c.JSON(http.StatusOK, helloResponse{Message: "hello " + c.Param("name")})
	}).Doc("say hello").Out(helloResponse{})

	r.POST("/users", func(c *Context) error {
		var request createUserRequest
		if err := c.Bind(&request); err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, userResponse{ID: 1, Name: request.Name})
	}).Doc("create user").In(createUserRequest{}).Out(userResponse{}).Status(http.StatusCreated)

	r.GET("/fail", func(*Context) error {
		return gableerrors.New(gableerrors.CodeNotFound, "missing").WithStatus(http.StatusNotFound)
	})

	r.GET("/boom", func(*Context) error {
		panic("boom")
	})

	r.Group("/admin", WithGroup("admin"), WithTags("admin")).GET("/ping", func(c *Context) error {
		return c.JSON(http.StatusOK, map[string]string{"pong": "1"})
	})
}

func newTestServer(opts ...Option) *Server {
	server := New(append([]Option{WithTitle("test"), WithVersion("1.0.0")}, opts...)...)
	server.Register(testController{})
	return server
}

func perform(server *Server, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func decodeEnvelope(t *testing.T, raw []byte) Envelope {
	t.Helper()
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, raw)
	}
	return envelope
}

func TestEnvelopeAndRequestID(t *testing.T) {
	server := newTestServer()
	recorder := perform(server, http.MethodGet, "/hello/world", nil, map[string]string{"X-Request-Id": "req-1"})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	envelope := decodeEnvelope(t, recorder.Body.Bytes())
	if !envelope.Success || envelope.Code != "0" || envelope.RequestID != "req-1" {
		t.Fatalf("envelope = %+v", envelope)
	}
	if recorder.Header().Get("X-Request-Id") != "req-1" {
		t.Fatalf("response request id = %q", recorder.Header().Get("X-Request-Id"))
	}
	data, _ := envelope.Data.(map[string]any)
	if data["message"] != "hello world" {
		t.Fatalf("data = %+v", envelope.Data)
	}
}

func TestNumericEnvelopeConfig(t *testing.T) {
	server := newTestServer(WithEnvelope(EnvelopeConfig{
		SuccessCode:      "2000000",
		InvalidCode:      "4000000",
		UnauthorizedCode: "4010000",
		ForbiddenCode:    "4030000",
		NotFoundCode:     "4040000",
		ConflictCode:     "4090000",
		InternalCode:     "5000000",
	}))
	ok := decodeEnvelope(t, perform(server, http.MethodGet, "/hello/x", nil, nil).Body.Bytes())
	if ok.Code != "2000000" || !ok.Success {
		t.Fatalf("success envelope = %+v", ok)
	}
	missing := decodeEnvelope(t, perform(server, http.MethodGet, "/fail", nil, nil).Body.Bytes())
	if missing.Code != "4040000" || missing.Success {
		t.Fatalf("error envelope = %+v", missing)
	}
}

func TestValidationError(t *testing.T) {
	server := newTestServer()
	recorder := perform(server, http.MethodPost, "/users", strings.NewReader(`{}`), map[string]string{"Content-Type": "application/json"})

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	envelope := decodeEnvelope(t, recorder.Body.Bytes())
	if envelope.Success || envelope.Code != "invalid_argument" {
		t.Fatalf("envelope = %+v", envelope)
	}
	details, _ := envelope.Data.(map[string]any)
	if details["Name"] != "required" {
		t.Fatalf("details = %+v", envelope.Data)
	}
}

func TestFrameworkErrorAndRecovery(t *testing.T) {
	server := newTestServer()

	missing := perform(server, http.MethodGet, "/fail", nil, nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("status = %d", missing.Code)
	}
	if envelope := decodeEnvelope(t, missing.Body.Bytes()); envelope.Code != "not_found" {
		t.Fatalf("envelope = %+v", envelope)
	}

	boom := perform(server, http.MethodGet, "/boom", nil, nil)
	if boom.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", boom.Code, boom.Body)
	}
	if envelope := decodeEnvelope(t, boom.Body.Bytes()); envelope.Code != "internal" {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestNoRoute(t *testing.T) {
	recorder := perform(newTestServer(), http.MethodGet, "/missing", nil, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d", recorder.Code)
	}
	if envelope := decodeEnvelope(t, recorder.Body.Bytes()); envelope.Code != "not_found" {
		t.Fatalf("envelope = %+v", envelope)
	}
}

func TestOpenAPI(t *testing.T) {
	recorder := perform(newTestServer(), http.MethodGet, "/v3/api-docs", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var spec map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.0.1" {
		t.Fatalf("openapi = %v", spec["openapi"])
	}

	paths := spec["paths"].(map[string]any)
	hello, ok := paths["/hello/{name}"]
	if !ok {
		t.Fatalf("missing hello path: %v", paths)
	}
	operation := hello.(map[string]any)["get"].(map[string]any)
	if operation["summary"] != "say hello" {
		t.Fatalf("summary = %v", operation["summary"])
	}

	create := paths["/users"].(map[string]any)["post"].(map[string]any)
	body := create["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
	if ref := body["schema"].(map[string]any)["$ref"]; ref != "#/components/schemas/createUserRequest" {
		t.Fatalf("request ref = %v", ref)
	}
	responses := create["responses"].(map[string]any)
	if _, ok := responses["201"]; !ok {
		t.Fatalf("responses = %v", responses)
	}
	envelope := responses["201"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	data := envelope["properties"].(map[string]any)["data"].(map[string]any)
	if data["$ref"] != "#/components/schemas/userResponse" {
		t.Fatalf("data ref = %v", data)
	}

	components := spec["components"].(map[string]any)["schemas"].(map[string]any)
	request := components["createUserRequest"].(map[string]any)
	required := request["required"].([]any)
	if len(required) != 1 || required[0] != "name" {
		t.Fatalf("required = %v", required)
	}
}

func TestOpenAPIGroups(t *testing.T) {
	server := newTestServer()

	defaultSpec := perform(server, http.MethodGet, "/v3/api-docs", nil, nil)
	var spec map[string]any
	if err := json.Unmarshal(defaultSpec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	if _, ok := paths["/admin/ping"]; ok {
		t.Fatal("default group must not contain admin routes")
	}

	admin := perform(server, http.MethodGet, "/v3/api-docs/admin", nil, nil)
	if admin.Code != http.StatusOK {
		t.Fatalf("status = %d", admin.Code)
	}
	if err := json.Unmarshal(admin.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	paths = spec["paths"].(map[string]any)
	if _, ok := paths["/admin/ping"]; !ok {
		t.Fatalf("admin paths = %v", paths)
	}

	unknown := perform(server, http.MethodGet, "/v3/api-docs/nope", nil, nil)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown group status = %d", unknown.Code)
	}
}

func TestSwaggerUI(t *testing.T) {
	recorder := perform(newTestServer(), http.MethodGet, "/swagger-ui/index.html", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, want := range []string{"SwaggerUIBundle", "/v3/api-docs", "/v3/api-docs/admin"} {
		if !strings.Contains(body, want) {
			t.Fatalf("swagger ui missing %q", want)
		}
	}
}

func TestStartStop(t *testing.T) {
	server := newTestServer()
	if err := server.Start(nil, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := server.Addr()
	if addr == "" {
		t.Fatal("empty listen address")
	}

	response, err := http.Get("http://" + addr + "/hello/live")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	if err := server.Stop(nil); err != nil {
		t.Fatal(err)
	}
}

func TestModuleWithApp(t *testing.T) {
	cfg := config.Default()
	cfg.App.Name = "web-test"
	cfg.Log.Dir = t.TempDir()
	cfg.HTTP.Addr = "127.0.0.1:0"

	app := gable.New(gable.WithConfig(cfg))
	server := newTestServer()
	if err := app.Register(NewModule(server, "")); err != nil {
		t.Fatal(err)
	}
	if err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()

	addr := server.Addr()
	if addr == "" {
		t.Fatal("module did not start the server")
	}
	response, err := http.Get("http://" + addr + "/hello/app")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
