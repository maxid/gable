package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maxid/gcosid"
	"github.com/maxid/gmcp"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/maxid/gable"
	"github.com/maxid/gable/dsl"
	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/tenant"
	"github.com/maxid/gable/web"
)

func newDSLCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dsl",
		Short: "lint, preview and apply gable DSL resources",
	}
	cmd.AddCommand(
		newDSLLintCommand(), newDSLPreviewCommand(), newDSLApplyCommand(),
		newDSLDiffCommand(), newDSLPushCommand(), newDSLHistoryCommand(), newDSLRestoreCommand(),
		newDSLMCPCommand(), newDSLServeCommand(),
	)
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

// dbStoreOptions translates the CLI flags into store options. An empty flag keeps the
// library default instead of blanking the table name.
func dbStoreOptions(table, revisions string) []dsl.DBStoreOption {
	var options []dsl.DBStoreOption
	if table != "" {
		options = append(options, dsl.WithDBTable(table))
	}
	if revisions != "" {
		options = append(options, dsl.WithDBRevisionsTable(revisions))
	}
	return options
}

// openDSLDB opens the database both `push` and `serve` need.
func openDSLDB(driver, dsn string) (*orm.DB, error) {
	db, err := orm.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	db.Configure(20, 5)
	return db, nil
}

// loadDSLSet returns the resources the runtime serves: from the directory (GitOps) or
// from what a previous `gable dsl push` stored in the database. Both paths lint before
// anything is served, so a bad resource never reaches the data API.
func loadDSLSet(ctx context.Context, dir, mode string, db *orm.DB, table, revisions string) (*dsl.Set, error) {
	if mode == "db" {
		store, err := dsl.NewDBStore(db, dbStoreOptions(table, revisions)...)
		if err != nil {
			return nil, err
		}
		set, issues, err := store.Load(ctx)
		if err != nil {
			return nil, err
		}
		if len(issues) > 0 {
			return nil, issues
		}
		return set, nil
	}

	service, err := dsl.NewService(dir)
	if err != nil {
		return nil, err
	}
	if issues := service.Lint(); len(issues) > 0 {
		return nil, issues
	}
	return service.Set(), nil
}

// dslDirStore loads a directory the way every write command does: lint first, because a
// set that does not lint must not reach the database.
func dslDirStore(dir string) (*dsl.Service, error) {
	service, err := dsl.NewService(dir)
	if err != nil {
		return nil, err
	}
	if issues := service.Lint(); len(issues) > 0 {
		return nil, issues
	}
	return service, nil
}

// printDiff writes the resource-level changes, with line detail for the ones that moved.
func printDiff(w io.Writer, diff dsl.Diff, withLines bool) {
	if diff.Empty() {
		fmt.Fprintln(w, "no changes")
		return
	}
	for _, change := range diff.Changes {
		fmt.Fprintf(w, "%-7s %s\n", change.Action, change.Key())
		if !withLines || change.Action != dsl.ChangeChanged {
			continue
		}
		for _, line := range change.Lines {
			if line.Op == " " {
				continue
			}
			fmt.Fprintf(w, "  %s %s\n", line.Op, line.Text)
		}
	}
	fmt.Fprintf(w, "%s\n", diff)
}

func newDSLDiffCommand() *cobra.Command {
	var dir, driver, dsn, table, revisions string
	var lines bool
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "show what `push` would change, without writing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dsn == "" {
				return fmt.Errorf("dsl: --dsn is required (mysql or postgres)")
			}
			service, err := dslDirStore(dir)
			if err != nil {
				return err
			}
			db, err := openDSLDB(driver, dsn)
			if err != nil {
				return err
			}
			defer db.Close()
			store, err := dsl.NewDBStore(db, dbStoreOptions(table, revisions)...)
			if err != nil {
				return err
			}
			diff, err := store.Diff(cmd.Context(), service.Set())
			if err != nil {
				return err
			}
			printDiff(cmd.OutOrStdout(), diff, lines)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory")
	cmd.Flags().StringVar(&driver, "driver", "mysql", "mysql | postgres")
	cmd.Flags().StringVar(&dsn, "dsn", "", "database DSN")
	cmd.Flags().StringVar(&table, "table", dsl.DefaultResourceTable, "resource table name")
	cmd.Flags().StringVar(&revisions, "revisions-table", dsl.DefaultRevisionTable, "revision history table name")
	cmd.Flags().BoolVar(&lines, "lines", false, "print the changed lines, not just the resource keys")
	return cmd
}

func newDSLPushCommand() *cobra.Command {
	var dir, driver, dsn, table, revisions string
	var dryRun, lines bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "write a validated DSL directory to the database store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dsn == "" {
				return fmt.Errorf("dsl: --dsn is required (mysql or postgres)")
			}
			service, err := dslDirStore(dir)
			if err != nil {
				return err
			}

			db, err := openDSLDB(driver, dsn)
			if err != nil {
				return err
			}
			defer db.Close()

			store, err := dsl.NewDBStore(db, dbStoreOptions(table, revisions)...)
			if err != nil {
				return err
			}
			if dryRun {
				diff, err := store.Diff(cmd.Context(), service.Set())
				if err != nil {
					return err
				}
				printDiff(cmd.OutOrStdout(), diff, lines)
				return nil
			}
			if err := store.Migrate(cmd.Context()); err != nil {
				return err
			}
			written, err := store.Save(cmd.Context(), service.Set())
			if err != nil {
				return err
			}
			if len(written) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no changes")
				return nil
			}
			for _, key := range written {
				fmt.Fprintln(cmd.OutOrStdout(), "wrote", key)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory")
	cmd.Flags().StringVar(&driver, "driver", "mysql", "mysql | postgres")
	cmd.Flags().StringVar(&dsn, "dsn", "", "database DSN")
	cmd.Flags().StringVar(&table, "table", dsl.DefaultResourceTable, "resource table name")
	cmd.Flags().StringVar(&revisions, "revisions-table", dsl.DefaultRevisionTable, "revision history table name")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the changes instead of writing them")
	cmd.Flags().BoolVar(&lines, "lines", false, "with --dry-run, print the changed lines too")
	return cmd
}

func newDSLHistoryCommand() *cobra.Command {
	var driver, dsn, table, revisions string
	cmd := &cobra.Command{
		Use:   "history <Kind>/<name>",
		Short: "list the stored revisions of one resource, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				return fmt.Errorf("dsl: --dsn is required (mysql or postgres)")
			}
			kind, name, err := splitResource(args[0])
			if err != nil {
				return err
			}
			db, err := openDSLDB(driver, dsn)
			if err != nil {
				return err
			}
			defer db.Close()
			store, err := dsl.NewDBStore(db, dbStoreOptions(table, revisions)...)
			if err != nil {
				return err
			}
			history, err := store.History(cmd.Context(), kind, name)
			if err != nil {
				return err
			}
			if len(history) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no revisions")
				return nil
			}
			for _, revision := range history {
				fmt.Fprintf(cmd.OutOrStdout(), "r%d  %s  %d bytes\n",
					revision.Revision, revision.CreatedAt.Format(time.RFC3339), len(revision.Document))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&driver, "driver", "mysql", "mysql | postgres")
	cmd.Flags().StringVar(&dsn, "dsn", "", "database DSN")
	cmd.Flags().StringVar(&table, "table", dsl.DefaultResourceTable, "resource table name")
	cmd.Flags().StringVar(&revisions, "revisions-table", dsl.DefaultRevisionTable, "revision history table name")
	return cmd
}

func newDSLRestoreCommand() *cobra.Command {
	var driver, dsn, table, revisions string
	var revision int64
	cmd := &cobra.Command{
		Use:   "restore <Kind>/<name>",
		Short: "write a stored revision back as the next revision",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dsn == "" {
				return fmt.Errorf("dsl: --dsn is required (mysql or postgres)")
			}
			if revision <= 0 {
				return fmt.Errorf("dsl: --revision is required")
			}
			kind, name, err := splitResource(args[0])
			if err != nil {
				return err
			}
			db, err := openDSLDB(driver, dsn)
			if err != nil {
				return err
			}
			defer db.Close()
			store, err := dsl.NewDBStore(db, dbStoreOptions(table, revisions)...)
			if err != nil {
				return err
			}
			key, err := store.Restore(cmd.Context(), kind, name, revision)
			if err != nil {
				return err
			}
			if key == "" {
				fmt.Fprintln(cmd.OutOrStdout(), "no changes")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "restored", key, "from revision", revision)
			return nil
		},
	}
	cmd.Flags().StringVar(&driver, "driver", "mysql", "mysql | postgres")
	cmd.Flags().StringVar(&dsn, "dsn", "", "database DSN")
	cmd.Flags().StringVar(&table, "table", dsl.DefaultResourceTable, "resource table name")
	cmd.Flags().StringVar(&revisions, "revisions-table", dsl.DefaultRevisionTable, "revision history table name")
	cmd.Flags().Int64Var(&revision, "revision", 0, "revision number to restore")
	return cmd
}

func newDSLMCPCommand() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "serve the DSL tools over MCP stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := dsl.NewService(dir)
			if err != nil {
				return err
			}
			// The MCP server must start even with lint issues: dsl.lint is how
			// an agent discovers and repairs them.
			server := gmcp.NewServer("gable-dsl")
			server.SetVersion(gable.Version)
			if err := service.AddTo(server); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			return server.Serve(ctx, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory")
	return cmd
}

func newDSLServeCommand() *cobra.Command {
	var (
		dir       string
		driver    string
		dsn       string
		load      string
		table     string
		revisions string
		addr      string
		rootID    int64
		rootCode  string
		machine   int64
		reload    time.Duration
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "serve the dynamic /dsl/data API for the resources",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dsn == "" {
				return fmt.Errorf("dsl: --dsn is required (mysql or postgres)")
			}
			if load != "file" && load != "db" {
				return fmt.Errorf("dsl: --load must be file or db, got %q", load)
			}

			db, err := openDSLDB(driver, dsn)
			if err != nil {
				return err
			}
			defer db.Close()

			set, err := loadDSLSet(cmd.Context(), dir, load, db, table, revisions)
			if err != nil {
				return err
			}

			ids, err := gcosid.NewSnowflake(gcosid.MillisecondLayout, machine)
			if err != nil {
				return err
			}
			runtime, err := dsl.NewRuntime(db, set, dsl.WithDefaultIDGenerator(ids))
			if err != nil {
				return err
			}
			if err := runtime.Migrate(context.Background()); err != nil {
				return err
			}

			cfg := tenant.DefaultConfig()
			cfg.RootID = rootID
			cfg.RootCode = rootCode

			server := web.New(
				web.WithTitle("gable-dsl"),
				web.WithVersion(gable.Version),
				web.WithDescription("dynamic data API for gable DSL resources"),
			)
			server.UseTenant(tenant.NewSingleResolver(cfg))
			runtime.Routes(server.Router(), "/dsl/data")

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			// A reload keeps the process and its listener: an operator changes the resources and
			// the API follows, which is the whole point of storing them in a database. A broken
			// set is reported and the running snapshot keeps serving.
			if reload > 0 || load == "db" {
				go watchDSLResources(ctx, cmd.ErrOrStderr(), runtime, reload, func(ctx context.Context) (*dsl.Set, error) {
					return loadDSLSet(ctx, dir, load, db, table, revisions)
				})
			}

			if err := server.Start(ctx, addr); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "listening on", server.Addr())
			if reload > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "reloading every %s\n", reload)
			}
			<-ctx.Done()
			shutdown, cancel := context.WithCancel(context.Background())
			defer cancel()
			return server.Stop(shutdown)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "DSL directory (used with --load file)")
	cmd.Flags().StringVar(&driver, "driver", "mysql", "mysql | postgres")
	cmd.Flags().StringVar(&dsn, "dsn", "", "database DSN")
	cmd.Flags().StringVar(&load, "load", "file", "file | db — where the resources come from; db reads what `gable dsl push` stored")
	cmd.Flags().StringVar(&table, "table", dsl.DefaultResourceTable, "resource table name (used with --load db)")
	cmd.Flags().StringVar(&revisions, "revisions-table", dsl.DefaultRevisionTable, "revision history table name (used with --load db)")
	cmd.Flags().StringVar(&addr, "addr", ":8088", "listen address")
	cmd.Flags().Int64Var(&rootID, "root-id", 1, "root tenant id")
	cmd.Flags().StringVar(&rootCode, "root-code", "management", "root tenant code")
	cmd.Flags().Int64Var(&machine, "machine-id", 1, "gcosid snowflake machine id")
	cmd.Flags().DurationVar(&reload, "reload", 0, "poll interval for resource changes (e.g. 30s); SIGHUP always reloads")
	return cmd
}

// watchDSLResources reloads the runtime on SIGHUP and, when an interval is set, on a timer.
//
// A failed load never replaces the snapshot: the log line says what broke, and the API keeps
// answering from the set that last linted. That is the difference between a bad edit and an
// outage.
func watchDSLResources(ctx context.Context, log io.Writer, runtime *dsl.Runtime, interval time.Duration, load func(context.Context) (*dsl.Set, error)) {
	reloadOnce := func(reason string) {
		set, err := load(ctx)
		if err != nil {
			fmt.Fprintf(log, "dsl: reload (%s) failed, keeping the previous resources: %v\n", reason, err)
			return
		}
		if err := runtime.Reload(set); err != nil {
			fmt.Fprintf(log, "dsl: reload (%s) rejected, keeping the previous resources: %v\n", reason, err)
			return
		}
		// The server migrates at startup, so a model added later needs its table too;
		// creating it here keeps reload and restart equivalent, and a failure is reported
		// without undoing the swap — the resource is still the right one to serve.
		if err := runtime.Migrate(ctx); err != nil {
			fmt.Fprintf(log, "dsl: reload (%s): model tables not created: %v\n", reason, err)
		}
		fmt.Fprintf(log, "dsl: reloaded (%s): %d models\n", reason, len(set.Models))
	}

	hangups := make(chan os.Signal, 1)
	signal.Notify(hangups, syscall.SIGHUP)
	defer signal.Stop(hangups)

	var ticker *time.Ticker
	var ticks <-chan time.Time
	if interval > 0 {
		ticker = time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-hangups:
			reloadOnce("SIGHUP")
		case <-ticks:
			reloadOnce("interval")
		}
	}
}
