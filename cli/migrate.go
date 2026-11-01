package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/dsl"
	"github.com/maxid/gable/orm"
)

// newMigrateCommand applies the DSL models to the configured database.
//
// The command exists because a gable application's schema source of truth is code
// (model tags) or DSL models, not a hand-maintained migration folder for new tables.
// It only *creates* missing tables (`AutoMigrate`): altering an existing table is a
// decision with data-loss risk and stays with explicit migrations.
func newMigrateCommand() *cobra.Command {
	var (
		configPath string
		dir        string
		dryRun     bool
	)

	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "create missing tables from DSL models",
		Long: "Loads the DSL models and creates the tables the database does not have.\n" +
			"With --dry-run it prints the statements instead of executing them, which is\n" +
			"the way to review a schema change before it runs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}

			set, issues, err := dsl.LoadDir(dir)
			if err != nil {
				return err
			}
			// LoadDir reports parse/kind problems; Lint reports semantic ones (a model
			// with no primary key parses fine but would produce a table nobody designed).
			issues = append(issues, set.Lint()...)
			if len(issues) > 0 {
				// A model with issues would produce a table nobody designed.
				for _, issue := range issues {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), issue.Error())
				}
				return fmt.Errorf("migrate: %d DSL issue(s)", len(issues))
			}

			dialect, err := orm.DialectFor(cfg.DB.Driver)
			if err != nil {
				return err
			}

			names := make([]string, 0, len(set.Models))
			for name := range set.Models {
				names = append(names, name)
			}
			sort.Strings(names)

			out := cmd.OutOrStdout()
			if dryRun {
				for _, name := range names {
					statement, err := set.ModelDDL(name, dialect)
					if err != nil {
						return err
					}
					_, _ = fmt.Fprintln(out, strings.TrimSpace(statement)+";")
				}
				return nil
			}

			if strings.TrimSpace(cfg.DB.DSN) == "" {
				return fmt.Errorf("migrate: database DSN is not configured (gable.db.dsn or GABLE_DB_DSN)")
			}
			db, err := orm.Open(cfg.DB.Driver, cfg.DB.DSN)
			if err != nil {
				return err
			}
			defer db.Close()

			applied := 0
			for _, name := range names {
				model, err := set.Models[name].ORMModel()
				if err != nil {
					return err
				}
				if err := db.AutoMigrate(model); err != nil {
					return fmt.Errorf("migrate: %s: %w", name, err)
				}
				applied++
				_, _ = fmt.Fprintf(out, "migrated %s\n", model.Table)
			}
			_, _ = fmt.Fprintf(out, "%d model(s) applied\n", applied)
			return nil
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "gable.yaml", "configuration file")
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the statements instead of executing them")
	return cmd
}

var _ = gable.Version
