package cli

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/maxid/gable/dsl"
)

// genModel is one Model resource the generator produced, with the warnings that belong to
// it so a dry run can explain why a field was left out.
type genModel struct {
	Doc      *dsl.ModelDoc
	Warnings []string
}

func newGenCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "generate resources from Go code",
	}
	cmd.AddCommand(newGenDSLCommand())
	return cmd
}

func newGenDSLCommand() *cobra.Command {
	var dir, out, prefix string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "dsl",
		Short: "generate Model resources from gable-tagged Go structs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			models, err := generateModels(dir, prefix)
			if err != nil {
				return err
			}
			writer := cmd.OutOrStdout()
			for _, model := range models {
				for _, warning := range model.Warnings {
					fmt.Fprintln(cmd.ErrOrStderr(), "warning:", warning)
				}
			}
			if dryRun {
				for _, model := range models {
					raw, err := yaml.Marshal(model.Doc)
					if err != nil {
						return err
					}
					fmt.Fprintf(writer, "--- %s\n%s", model.Doc.Metadata.Name, raw)
				}
				return nil
			}

			if err := os.MkdirAll(outDir(out), 0o755); err != nil {
				return err
			}
			for _, model := range models {
				path, changed, err := writeGenerated(out, model.Doc)
				if err != nil {
					return err
				}
				if changed {
					fmt.Fprintln(writer, "wrote", path)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "pkg", ".", "Go package directory to read")
	cmd.Flags().StringVar(&out, "out", ".", "output directory")
	cmd.Flags().StringVar(&prefix, "table-prefix", "", "prefix for generated table names")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the resources instead of writing them")
	return cmd
}

// outDir keeps the write path and the flag name apart.
func outDir(out string) string {
	if out == "" {
		return "."
	}
	return out
}

// writeGenerated writes one Model resource, leaving an identical file untouched so running
// the generator twice is not a diff.
func writeGenerated(dir string, doc *dsl.ModelDoc) (string, bool, error) {
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, "Model-"+doc.Metadata.Name+".yaml")
	existing, err := os.ReadFile(path)
	if err == nil && string(existing) == string(raw) {
		return path, false, nil
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// generateModels reads the package and turns every gable-tagged struct into a Model.
//
// It parses rather than type-checks: a struct tag is syntax, and requiring a buildable
// package to generate documentation would tie the generator to the build graph for no gain.
func generateModels(dir, tablePrefix string) ([]genModel, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("gen: read %s: %w", dir, err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("gen: no Go files in %s", dir)
	}

	var models []genModel
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("gen: parse %s: %w", file, err)
		}
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					continue
				}
				model, ok := modelFromStruct(typeSpec.Name.Name, structType, tablePrefix)
				if !ok {
					continue
				}
				models = append(models, model)
			}
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("gen: no structs with gable tags in %s", dir)
	}
	return models, nil
}

// structTags are the tag parts that describe a column.
var structTags = map[string]bool{
	"pk": true, "tenant": true, "auto_create": true, "auto_update": true, "unique": true,
	"soft_delete": true, "retain": true, "auto": true,
}

// modelFromStruct converts one struct. It returns false for a struct without any gable tag,
// which is how a package's DTOs stay out of the output.
func modelFromStruct(name string, structType *ast.StructType, tablePrefix string) (genModel, bool) {
	model := genModel{Doc: &dsl.ModelDoc{
		APIVersion: dsl.APIVersion,
		Kind:       dsl.KindModel,
		Metadata:   dsl.Metadata{Name: lowerFirst(name), Title: name},
		Spec:       dsl.ModelSpec{},
	}}
	table := snakeCase(name)
	tagged := false

	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			continue // embedded field: not a column
		}
		tag := ""
		if field.Tag != nil {
			raw, err := strconv.Unquote(field.Tag.Value)
			if err == nil {
				tag = raw
			}
		}
		parts := strings.Split(structTag(tag, "gable"), ",")
		skip := false
		relation := false
		var flags []string
		for _, part := range parts {
			part = strings.TrimSpace(part)
			key, value, hasValue := strings.Cut(part, "=")
			switch {
			case part == "-":
				skip = true
			case key == "has_many" || key == "belongs_to":
				relation = true
			case key == "table" && hasValue:
				table = value
			case structTags[key]:
				flags = append(flags, key)
				tagged = true
			}
		}
		if skip || relation {
			continue
		}
		for _, flag := range flags {
			if flag == "soft_delete" || flag == "retain" {
				tagged = true
			}
		}

		fieldName := columnOf(field.Names[0].Name, parts)
		fieldType, ok := dslTypeOf(field.Type)
		if !ok {
			model.Warnings = append(model.Warnings, fmt.Sprintf("%s.%s: unsupported Go type, field omitted", name, field.Names[0].Name))
			continue
		}

		spec := dsl.FieldSpec{Name: fieldName, Type: fieldType}
		for _, flag := range flags {
			switch flag {
			case "pk":
				spec.Primary = true
			case "tenant":
				spec.Tenant = true
			case "unique":
				spec.Unique = true
			case "auto_create":
				spec.AutoCreate = true
			case "auto_update":
				spec.AutoUpdate = true
			case "auto":
				spec.Generator = "auto"
			case "soft_delete", "retain":
				model.Warnings = append(model.Warnings, fmt.Sprintf(
					"%s.%s: %s markers are not part of the DSL yet; the field is generated without it",
					name, field.Names[0].Name, flag))
			}
		}
		for _, part := range parts {
			key, value, hasValue := strings.Cut(strings.TrimSpace(part), "=")
			if !hasValue {
				continue
			}
			switch key {
			case "size":
				if size, err := strconv.Atoi(value); err == nil {
					spec.Size = size
				}
			case "column":
				spec.Name = value
			}
		}
		model.Doc.Spec.Fields = append(model.Doc.Spec.Fields, spec)
	}

	if !tagged || len(model.Doc.Spec.Fields) == 0 {
		return genModel{}, false
	}
	if model.Doc.Primary() == nil {
		model.Warnings = append(model.Warnings, fmt.Sprintf("%s: no pk field; the DSL requires exactly one", name))
	}
	model.Doc.Spec.Table = tablePrefix + table
	return model, true
}

// structTag reads one key of a raw struct tag without depending on reflect, which would need
// the type to exist at run time.
func structTag(tag, key string) string {
	marker := key + `:"`
	index := strings.Index(tag, marker)
	if index < 0 {
		return ""
	}
	rest := tag[index+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// columnOf resolves the column name the same way the ORM does: the gable column, the db tag,
// the json tag, then the snake-cased Go name.
func columnOf(goName string, parts []string) string {
	for _, part := range parts {
		key, value, hasValue := strings.Cut(strings.TrimSpace(part), "=")
		if hasValue && key == "column" {
			return value
		}
	}
	return snakeCase(goName)
}

// dslTypeOf maps a Go type expression onto a DSL field type.
func dslTypeOf(expr ast.Expr) (string, bool) {
	switch typed := expr.(type) {
	case *ast.Ident:
		switch typed.Name {
		case "int64", "int":
			return "int64", true
		case "int32":
			return "int32", true
		case "string":
			return "string", true
		case "bool":
			return "bool", true
		case "float64":
			return "float64", true
		case "float32":
			return "float64", true
		}
	case *ast.SelectorExpr:
		pkg, ok := typed.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		switch pkg.Name + "." + typed.Sel.Name {
		case "time.Time":
			return "time", true
		case "json.RawMessage":
			return "json", true
		}
	case *ast.StarExpr:
		// A pointer is a nullable column; the DSL type is the pointee's.
		return dslTypeOf(typed.X)
	case *ast.MapType:
		return "json", true
	case *ast.ArrayType:
		// A slice is an ArrayType with no length in go/ast.
		if ident, ok := typed.Elt.(*ast.Ident); ok && ident.Name == "byte" {
			return "text", true
		}
		if _, ok := typed.Elt.(*ast.StarExpr); ok {
			return "json", true
		}
	}
	return "", false
}

// snakeCase is the ORM's Go-name convention, duplicated here because the generator must not
// depend on the ORM's internal helpers to describe a struct it never instantiates.
func snakeCase(value string) string {
	var builder strings.Builder
	runes := []rune(value)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || nextLower {
					builder.WriteByte('_')
				}
			}
			builder.WriteRune(unicode.ToLower(r))
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func lowerFirst(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}
