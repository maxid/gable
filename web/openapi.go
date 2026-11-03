package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type oaSpec struct {
	OpenAPI    string                             `json:"openapi"`
	Info       oaInfo                             `json:"info"`
	Servers    []oaServer                         `json:"servers,omitempty"`
	Tags       []oaTag                            `json:"tags,omitempty"`
	Paths      map[string]map[string]*oaOperation `json:"paths"`
	Components oaComponents                       `json:"components"`
}

type oaInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

type oaServer struct {
	URL string `json:"url"`
}

type oaTag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type oaOperation struct {
	OperationID string                `json:"operationId,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Parameters  []oaParameter         `json:"parameters,omitempty"`
	RequestBody *oaRequestBody        `json:"requestBody,omitempty"`
	Responses   map[string]oaResponse `json:"responses"`
	// Extensions are the route's x-... vendor extensions, merged into the operation
	// document at build time.
	Extensions map[string]any `json:"-"`
}

// MarshalJSON folds the vendor extensions into the operation object.
//
// A struct field cannot be "inline or absent" in encoding/json, and an operation with
// `"extensions": {...}` instead of top-level `x-...` keys is exactly the mistake this type
// exists to prevent, so the merge happens here.
func (o *oaOperation) MarshalJSON() ([]byte, error) {
	type alias oaOperation
	if len(o.Extensions) == 0 {
		return json.Marshal((*alias)(o))
	}
	raw, err := json.Marshal((*alias)(o))
	if err != nil {
		return nil, err
	}
	merged := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &merged); err != nil {
		return nil, err
	}
	for name, value := range o.Extensions {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("web: extension %s: %w", name, err)
		}
		merged[name] = encoded
	}
	return json.Marshal(merged)
}

type oaParameter struct {
	Name        string    `json:"name"`
	In          string    `json:"in"`
	Description string    `json:"description,omitempty"`
	Required    bool      `json:"required,omitempty"`
	Schema      *oaSchema `json:"schema"`
}

type oaRequestBody struct {
	Required bool               `json:"required,omitempty"`
	Content  map[string]oaMedia `json:"content"`
}

type oaMedia struct {
	Schema *oaSchema `json:"schema"`
}

type oaResponse struct {
	Description string             `json:"description"`
	Content     map[string]oaMedia `json:"content,omitempty"`
}

type oaComponents struct {
	Schemas map[string]*oaSchema `json:"schemas"`
}

type oaSchema struct {
	Ref                  string               `json:"$ref,omitempty"`
	Type                 string               `json:"type,omitempty"`
	Format               string               `json:"format,omitempty"`
	Description          string               `json:"description,omitempty"`
	Properties           map[string]*oaSchema `json:"properties,omitempty"`
	Required             []string             `json:"required,omitempty"`
	Items                *oaSchema            `json:"items,omitempty"`
	AdditionalProperties *oaSchema            `json:"additionalProperties,omitempty"`
	AllOf                []*oaSchema          `json:"allOf,omitempty"`
	Example              any                  `json:"example,omitempty"`
	Nullable             bool                 `json:"nullable,omitempty"`
}

type openAPIBuilder struct {
	opts       options
	components map[string]*oaSchema
	names      map[reflect.Type]string
	owners     map[string]reflect.Type
}

func (s *Server) buildSpec(group string) *oaSpec {
	builder := &openAPIBuilder{
		opts:       s.opts,
		components: make(map[string]*oaSchema),
		names:      make(map[reflect.Type]string),
		owners:     make(map[string]reflect.Type),
	}

	spec := &oaSpec{
		OpenAPI: s.opts.openapiVersion,
		Info: oaInfo{
			Title:       s.opts.title,
			Version:     s.opts.version,
			Description: s.opts.description,
		},
		Paths:      make(map[string]map[string]*oaOperation),
		Components: oaComponents{Schemas: make(map[string]*oaSchema)},
	}
	for _, server := range s.opts.servers {
		spec.Servers = append(spec.Servers, oaServer{URL: server})
	}

	tags := make(map[string]bool)
	for _, route := range s.routeSnapshot() {
		routeGroup := route.group
		if routeGroup == "" {
			routeGroup = "default"
		}
		if routeGroup != group {
			continue
		}

		path := openAPIPath(route.path)
		if spec.Paths[path] == nil {
			spec.Paths[path] = make(map[string]*oaOperation)
		}
		spec.Paths[path][strings.ToLower(route.method)] = builder.operation(route)
		for _, tag := range route.tags {
			tags[tag] = true
		}
	}

	for tag := range tags {
		spec.Tags = append(spec.Tags, oaTag{Name: tag})
	}
	sort.Slice(spec.Tags, func(i, j int) bool { return spec.Tags[i].Name < spec.Tags[j].Name })
	spec.Components.Schemas = builder.components
	return spec
}

func (s *Server) docGroups() []string {
	seen := map[string]bool{"default": true}
	for _, route := range s.routeSnapshot() {
		if route.group != "" {
			seen[route.group] = true
		}
	}
	groups := make([]string, 0, len(seen))
	for group := range seen {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	return groups
}

func (s *Server) hasGroup(group string) bool {
	for _, candidate := range s.docGroups() {
		if candidate == group {
			return true
		}
	}
	return false
}

func (b *openAPIBuilder) operation(route *Route) *oaOperation {
	op := &oaOperation{
		OperationID: route.operationID,
		Summary:     route.summary,
		Description: route.description,
		Tags:        route.tags,
		Parameters:  append(pathParameters(route.path), route.params...),
		Responses:   make(map[string]oaResponse),
		Extensions:  route.extensions,
	}
	if op.OperationID == "" {
		op.OperationID = defaultOperationID(route.method, route.path)
	}

	if route.request != nil {
		op.RequestBody = &oaRequestBody{
			Required: true,
			Content: map[string]oaMedia{
				"application/json": {Schema: b.schemaForType(reflect.TypeOf(route.request))},
			},
		}
	}

	var data *oaSchema
	if route.response != nil {
		data = b.schemaForType(reflect.TypeOf(route.response))
	}
	status := route.status
	if status == 0 {
		status = http.StatusOK
	}
	op.Responses[fmt.Sprintf("%d", status)] = oaResponse{
		Description: "OK",
		Content: map[string]oaMedia{
			"application/json": {Schema: b.envelopeSchema(data)},
		},
	}
	return op
}

func (b *openAPIBuilder) envelopeSchema(data *oaSchema) *oaSchema {
	if data == nil {
		data = &oaSchema{Type: "object"}
	}
	return &oaSchema{
		Type: "object",
		Properties: map[string]*oaSchema{
			"code":    {Type: "string"},
			"message": {Type: "string"},
			"data":    data,
			"reqId":   {Type: "string"},
			"success": {Type: "boolean"},
			"tm":      {Type: "integer", Format: "int64"},
		},
	}
}

func defaultOperationID(method, path string) string {
	cleaned := strings.NewReplacer("/", "_", ":", "", "*", "", "{", "", "}", "").Replace(path)
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		cleaned = "root"
	}
	return strings.ToLower(method) + "_" + cleaned
}

var (
	timeType       = reflect.TypeOf(time.Time{})
	rawMessageType = reflect.TypeOf(json.RawMessage{})
)

func (b *openAPIBuilder) schemaForType(t reflect.Type) *oaSchema {
	if t == nil {
		return &oaSchema{Type: "object"}
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t == timeType {
		return &oaSchema{Type: "string", Format: "date-time"}
	}
	if t == rawMessageType {
		return &oaSchema{Type: "object"}
	}

	switch t.Kind() {
	case reflect.Struct:
		return b.refFor(t)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return &oaSchema{Type: "string", Format: "byte"}
		}
		return &oaSchema{Type: "array", Items: b.schemaForType(t.Elem())}
	case reflect.Map:
		return &oaSchema{Type: "object", AdditionalProperties: b.schemaForType(t.Elem())}
	case reflect.String:
		return &oaSchema{Type: "string"}
	case reflect.Bool:
		return &oaSchema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		format := "int64"
		switch t.Kind() {
		case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Uint8, reflect.Uint16, reflect.Uint32:
			format = "int32"
		}
		return &oaSchema{Type: "integer", Format: format}
	case reflect.Float32:
		return &oaSchema{Type: "number", Format: "float"}
	case reflect.Float64:
		return &oaSchema{Type: "number", Format: "double"}
	default:
		return &oaSchema{Type: "object"}
	}
}

func (b *openAPIBuilder) refFor(t reflect.Type) *oaSchema {
	if name, ok := b.names[t]; ok {
		return &oaSchema{Ref: "#/components/schemas/" + name}
	}

	name := b.componentName(t)
	b.names[t] = name
	b.owners[name] = t
	component := &oaSchema{Type: "object", Properties: make(map[string]*oaSchema)}
	b.components[name] = component

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		if field.Anonymous && jsonTag == "" {
			b.flatten(component, field.Type)
			continue
		}

		name, _ := parseJSONTag(jsonTag, field.Name)
		if name == "" || name == "-" {
			continue
		}

		schema := b.schemaForType(field.Type)
		doc := field.Tag.Get("doc")
		format := field.Tag.Get("format")
		if schema.Ref != "" {
			if doc != "" {
				schema = &oaSchema{AllOf: []*oaSchema{schema}, Description: doc}
			}
		} else {
			if doc != "" {
				schema.Description = doc
			}
			if format != "" {
				schema.Format = format
			}
			if example := field.Tag.Get("example"); example != "" {
				schema.Example = example
			}
		}
		component.Properties[name] = schema
		if isRequired(field) {
			component.Required = append(component.Required, name)
		}
	}
	sort.Strings(component.Required)
	return &oaSchema{Ref: "#/components/schemas/" + name}
}

func (b *openAPIBuilder) flatten(component *oaSchema, t reflect.Type) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	nested := b.refFor(t)
	if nested.Ref == "" {
		return
	}
	name := strings.TrimPrefix(nested.Ref, "#/components/schemas/")
	source := b.components[name]
	if source == nil {
		return
	}
	for key, schema := range source.Properties {
		component.Properties[key] = schema
	}
	component.Required = append(component.Required, source.Required...)
}

func (b *openAPIBuilder) componentName(t reflect.Type) string {
	base := t.Name()
	if base == "" {
		base = "Anonymous"
	}
	if owner, exists := b.owners[base]; !exists || owner == t {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s_%d", base, i)
		if _, exists := b.owners[candidate]; !exists {
			return candidate
		}
	}
}

func parseJSONTag(tag, fallback string) (string, []string) {
	if tag == "" {
		return fallback, nil
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = fallback
	}
	return name, parts[1:]
}

func isRequired(field reflect.StructField) bool {
	for _, key := range []string{"binding", "validate"} {
		for _, part := range strings.Split(field.Tag.Get(key), ",") {
			if part == "required" {
				return true
			}
		}
	}
	return false
}

func (s *Server) registerDocs() {
	if !s.opts.docs {
		return
	}
	docsPath := s.opts.docsPath
	s.engine.GET(docsPath, func(c *gin.Context) {
		c.JSON(http.StatusOK, s.buildSpec("default"))
	})
	s.engine.GET(docsPath+"/:group", func(c *gin.Context) {
		group := c.Param("group")
		if !s.hasGroup(group) {
			c.JSON(http.StatusNotFound, gin.H{"error": "openapi group not found"})
			return
		}
		c.JSON(http.StatusOK, s.buildSpec(group))
	})

	if !s.opts.swagger {
		return
	}
	redirect := func(c *gin.Context) { c.Redirect(http.StatusFound, "/swagger-ui/index.html") }
	s.engine.GET("/swagger-ui", redirect)
	s.engine.GET("/swagger-ui/", redirect)
	s.engine.GET("/swagger-ui/index.html", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, swaggerHTML(docsPath, s.docGroups()))
	})
}
