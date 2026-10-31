package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/maxid/gable/dsl"
	"github.com/maxid/gable/orm"
)

func newDSLCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dsl",
		Short: "lint, preview and apply gable DSL resources",
	}
	cmd.AddCommand(newDSLLintCommand(), newDSLPreviewCommand(), newDSLApplyCommand())
	return cmd
}

func newDSLLintCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "validate a DSL directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			set, issues, err := dsl.LoadDir(dir)
			if err != nil {
				return err
			}
			issues = append(issues, set.Lint()...)
			if len(issues) > 0 {
				for _, issue := range issues {
					fmt.Fprintln(cmd.ErrOrStderr(), issue.Error())
				}
				return fmt.Errorf("dsl: %d issue(s)", len(issues))
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "OK: %d models, %d tables, %d forms, %d pages\n",
				len(set.Models), len(set.Tables), len(set.Forms), len(set.Pages))
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory")
	return cmd
}

func newDSLPreviewCommand() *cobra.Command {
	var dir, format, dialectName string
	cmd := &cobra.Command{
		Use:   "preview <Kind>/<name>",
		Short: "render a resource preview (IR for pages, DDL for models)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind, name, err := splitResource(args[0])
			if err != nil {
				return err
			}
			set, issues, err := dsl.LoadDir(dir)
			if err != nil {
				return err
			}
			issues = append(issues, set.Lint()...)
			if len(issues) > 0 {
				return issues
			}
			dialect, err := orm.DialectFor(dialectName)
			if err != nil {
				return err
			}
			payload, err := set.Preview(kind, name, dialect)
			if err != nil {
				return err
			}

			var raw []byte
			switch strings.ToLower(format) {
			case "yaml", "yml":
				raw, err = yaml.Marshal(payload)
			case "json", "":
				raw, err = json.MarshalIndent(payload, "", "  ")
				raw = append(raw, '\n')
			default:
				return fmt.Errorf("dsl: unsupported format %q", format)
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(raw)
			return err
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory")
	cmd.Flags().StringVar(&format, "format", "json", "json | yaml")
	cmd.Flags().StringVar(&dialectName, "dialect", "mysql", "mysql | postgres")
	return cmd
}

func newDSLApplyCommand() *cobra.Command {
	var dir, out string
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "validate and write normalized resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			set, issues, err := dsl.LoadDir(dir)
			if err != nil {
				return err
			}
			issues = append(issues, set.Lint()...)
			if len(issues) > 0 {
				return issues
			}
			written, err := set.ApplyDir(out)
			if err != nil {
				return err
			}
			if len(written) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no changes")
				return nil
			}
			for _, path := range written {
				fmt.Fprintln(cmd.OutOrStdout(), "wrote", path)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory")
	cmd.Flags().StringVar(&out, "out", ".gable/dsl", "normalized output directory")
	return cmd
}

func splitResource(value string) (dsl.Kind, string, error) {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", fmt.Errorf("dsl: expected <Kind>/<name>, got %q", value)
	}
	kind := dsl.Kind(parts[0])
	switch kind {
	case dsl.KindModel, dsl.KindTable, dsl.KindForm, dsl.KindPage:
		return kind, parts[1], nil
	default:
		return "", "", fmt.Errorf("dsl: unsupported kind %q", parts[0])
	}
}
