package dsl

// ModelDoc defines a runtime model: table, fields and the flags the ORM and
// the low-code UI need.
type ModelDoc struct {
	APIVersion string    `yaml:"apiVersion" json:"apiVersion"`
	Kind       Kind      `yaml:"kind" json:"kind"`
	Metadata   Metadata  `yaml:"metadata" json:"metadata"`
	Spec       ModelSpec `yaml:"spec" json:"spec"`
}

// ModelSpec is the model body.
type ModelSpec struct {
	Table  string      `yaml:"table,omitempty" json:"table,omitempty"`
	Fields []FieldSpec `yaml:"fields" json:"fields"`
}

// FieldSpec is one model field.
type FieldSpec struct {
	Name        string   `yaml:"name" json:"name"`
	Type        string   `yaml:"type" json:"type"`
	Size        int      `yaml:"size,omitempty" json:"size,omitempty"`
	Primary     bool     `yaml:"primary,omitempty" json:"primary,omitempty"`
	Tenant      bool     `yaml:"tenant,omitempty" json:"tenant,omitempty"`
	Required    bool     `yaml:"required,omitempty" json:"required,omitempty"`
	Unique      bool     `yaml:"unique,omitempty" json:"unique,omitempty"`
	AutoCreate  bool     `yaml:"autoCreate,omitempty" json:"autoCreate,omitempty"`
	AutoUpdate  bool     `yaml:"autoUpdate,omitempty" json:"autoUpdate,omitempty"`
	Generator   string   `yaml:"generator,omitempty" json:"generator,omitempty"` // snowflake | auto | none
	Default     any      `yaml:"default,omitempty" json:"default,omitempty"`
	Enum        []string `yaml:"enum,omitempty" json:"enum,omitempty"`
	Label       string   `yaml:"label,omitempty" json:"label,omitempty"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
}

// TableName returns the SQL table, defaulting to the resource name.
func (m *ModelDoc) TableName() string {
	if m.Spec.Table != "" {
		return m.Spec.Table
	}
	return m.Metadata.Name
}

// Field returns a field by name.
func (m *ModelDoc) Field(name string) *FieldSpec {
	for i := range m.Spec.Fields {
		if m.Spec.Fields[i].Name == name {
			return &m.Spec.Fields[i]
		}
	}
	return nil
}

// Primary returns the primary key field.
func (m *ModelDoc) Primary() *FieldSpec {
	for i := range m.Spec.Fields {
		if m.Spec.Fields[i].Primary {
			return &m.Spec.Fields[i]
		}
	}
	return nil
}

// TenantField returns the tenant field.
func (m *ModelDoc) TenantField() *FieldSpec {
	for i := range m.Spec.Fields {
		if m.Spec.Fields[i].Tenant {
			return &m.Spec.Fields[i]
		}
	}
	return nil
}

// TableDoc defines a list page over a model.
type TableDoc struct {
	APIVersion string    `yaml:"apiVersion" json:"apiVersion"`
	Kind       Kind      `yaml:"kind" json:"kind"`
	Metadata   Metadata  `yaml:"metadata" json:"metadata"`
	Spec       TableSpec `yaml:"spec" json:"spec"`
}

// TableSpec is the table body.
type TableSpec struct {
	Model      string       `yaml:"model" json:"model"`
	RuntimeAPI string       `yaml:"runtimeApi,omitempty" json:"runtimeApi,omitempty"`
	Columns    []ColumnSpec `yaml:"columns,omitempty" json:"columns,omitempty"`
	Filters    []string     `yaml:"filters,omitempty" json:"filters,omitempty"`
}

// ColumnSpec selects and presents one model field.
type ColumnSpec struct {
	Field    string `yaml:"field" json:"field"`
	Label    string `yaml:"label,omitempty" json:"label,omitempty"`
	Sortable bool   `yaml:"sortable,omitempty" json:"sortable,omitempty"`
	Visible  *bool  `yaml:"visible,omitempty" json:"visible,omitempty"`
}

// FormDoc defines a form over a model.
type FormDoc struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       Kind     `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       FormSpec `yaml:"spec" json:"spec"`
}

// FormSpec is the form body.
type FormSpec struct {
	Model  string          `yaml:"model" json:"model"`
	Fields []FormFieldSpec `yaml:"fields,omitempty" json:"fields,omitempty"`
	Layout *FormLayout     `yaml:"layout,omitempty" json:"layout,omitempty"`
}

// FormFieldSpec selects and presents one form field.
type FormFieldSpec struct {
	Field       string `yaml:"field" json:"field"`
	Label       string `yaml:"label,omitempty" json:"label,omitempty"`
	ControlType string `yaml:"controlType,omitempty" json:"controlType,omitempty"`
	Required    bool   `yaml:"required,omitempty" json:"required,omitempty"`
	Readonly    bool   `yaml:"readonly,omitempty" json:"readonly,omitempty"`
	Visible     *bool  `yaml:"visible,omitempty" json:"visible,omitempty"`
	Multiple    bool   `yaml:"multiple,omitempty" json:"multiple,omitempty"`
}

// FormLayout maps to the IR layout block.
type FormLayout struct {
	LabelPosition string `yaml:"labelPosition,omitempty" json:"labelPosition,omitempty"` // LEFT | TOP
}

// PageDoc composes a table and/or form into a UI page.
type PageDoc struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       Kind     `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       PageSpec `yaml:"spec" json:"spec"`
}

// PageSpec is the page body.
type PageSpec struct {
	Title string `yaml:"title,omitempty" json:"title,omitempty"`
	Table string `yaml:"table,omitempty" json:"table,omitempty"`
	Form  string `yaml:"form,omitempty" json:"form,omitempty"`
}
