package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/dsl"
)

// inspectReport is what `gable inspect` prints: the effective configuration, the names the
// framework derives from it, and what the DSL directory holds.
//
// It exists because "which config did this binary actually pick, and what will it call its
// cache keys" is the first question of every deployment debug session, and answering it by
// reading YAML resolution code is how wrong answers happen.
type inspectReport struct {
	Version   string           `json:"version"`
	App       inspectApp       `json:"app"`
	HTTP      inspectHTTP      `json:"http"`
	Log       inspectLog       `json:"log"`
	DB        inspectDB        `json:"db"`
	Tenant    inspectTenant    `json:"tenant"`
	Namespace inspectNamespace `json:"namespace"`
	DSL       *inspectDSL      `json:"dsl,omitempty"`
}

// The report has its own shapes rather than reusing config structs: JSON naming and
// human-readable durations are part of the output contract, and the config types are
// free to change without breaking a script.
type inspectApp struct {
	Name string `json:"name"`
	Env  string `json:"env"`
}

type inspectHTTP struct {
	Addr         string `json:"addr"`
	ReadTimeout  string `json:"readTimeout"`
	WriteTimeout string `json:"writeTimeout"`
}

type inspectLog struct {
	Dir    string `json:"dir"`
	Level  string `json:"level"`
	Format string `json:"format"`
}

type inspectDB struct {
	Driver  string `json:"driver"`
	DSN     string `json:"dsn,omitempty"`
	MaxOpen int    `json:"maxOpen"`
	MaxIdle int    `json:"maxIdle"`
}

type inspectTenant struct {
	Mode     string `json:"mode"`
	RootID   int64  `json:"rootId"`
	RootCode string `json:"rootCode"`
}

// inspectNamespace lists the derived names, not the raw prefix config: a deployment
// argument is about the cache key and the Flyway table, not about the root string.
type inspectNamespace struct {
	Root       string `json:"root"`
	Cache      string `json:"cache"`
	Flyway     string `json:"flyway"`
	CosID      string `json:"cosid"`
	Cluster    string `json:"cluster"`
	TimeSeries string `json:"timeSeries"`
}

type inspectDSL struct {
	Dir    string      `json:"dir"`
	Models int         `json:"models"`
	Tables int         `json:"tables"`
	Forms  int         `json:"forms"`
	Pages  int         `json:"pages"`
	Valid  bool        `json:"valid"`
	Issues []dsl.Issue `json:"issues,omitempty"`
}

func newInspectCommand() *cobra.Command {
	var configPath, dir, format string
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "print the effective configuration, derived names and DSL summary",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadInspectConfig(cmd, configPath)
			if err != nil {
				return err
			}
			report := newInspectReport(cfg)
			if dir != "" {
				summary, err := inspectDSLDir(dir)
				if err != nil {
					return err
				}
				report.DSL = summary
			}

			out := cmd.OutOrStdout()
			if strings.EqualFold(format, "json") {
				raw, err := json.MarshalIndent(report, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(raw))
				return nil
			}
			printInspect(out, report)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "gable.yaml", "configuration file")
	cmd.Flags().StringVar(&dir, "dir", "", "DSL directory to summarize (optional)")
	cmd.Flags().StringVar(&format, "format", "text", "text | json")
	return cmd
}

func newInspectReport(cfg *config.Config) inspectReport {
	namespace := cfg.Namespace.Normalize()
	report := inspectReport{
		Version: gable.Version,
		App:     inspectApp{Name: cfg.App.Name, Env: cfg.App.Env},
		HTTP: inspectHTTP{
			Addr:         cfg.HTTP.Addr,
			ReadTimeout:  cfg.HTTP.ReadTimeout.Std().String(),
			WriteTimeout: cfg.HTTP.WriteTimeout.Std().String(),
		},
		Log: inspectLog{Dir: cfg.Log.Dir, Level: cfg.Log.Level, Format: cfg.Log.Format},
		DB: inspectDB{
			Driver:  cfg.DB.Driver,
			DSN:     redactDSN(cfg.DB.DSN),
			MaxOpen: cfg.DB.MaxOpen,
			MaxIdle: cfg.DB.MaxIdle,
		},
		Tenant: inspectTenant{
			Mode:     string(cfg.Tenant.Mode),
			RootID:   cfg.Tenant.RootID,
			RootCode: cfg.Tenant.RootCode,
		},
		Namespace: inspectNamespace{
			Root:       namespace.Root,
			Cache:      namespace.CacheName(),
			Flyway:     namespace.FlywayTable("schema_history"),
			CosID:      namespace.CosIDName(),
			Cluster:    namespace.ClusterName(),
			TimeSeries: namespace.TimeSeriesName(),
		},
	}
	return report
}

// loadInspectConfig reads the configuration and falls back to the defaults when the file
// is absent and the path was not given explicitly: "what are the defaults" is one of the
// questions inspect answers.
func loadInspectConfig(cmd *cobra.Command, path string) (*config.Config, error) {
	explicit := cmd.Flags().Changed("config")
	cfg, err := config.Load(path)
	if err == nil {
		return cfg, nil
	}
	if !explicit && os.IsNotExist(unwrapPathError(err)) {
		return config.Default(), nil
	}
	return nil, err
}

// unwrapPathError digs the underlying file error out of the config wrapper.
func unwrapPathError(err error) error {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr
	}
	return err
}

// redactDSN keeps the shape of a DSN and hides the password: inspect output ends up in
// tickets and chat logs, which is exactly where a credential must not go.
func redactDSN(dsn string) string {
	if dsn == "" {
		return ""
	}
	// URL-shaped DSNs (postgres://user:password@host/db) are parsed, not string-split:
	// the scheme and slashes are part of the value.
	if strings.Contains(dsn, "://") {
		if parsed, err := url.Parse(dsn); err == nil && parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword {
				// String surgery rather than round-tripping the URL: url.String() escapes
				// the mask into %2A%2A%2A, which reads like a real password.
				if at := strings.LastIndex(dsn, "@"); at > 0 {
					prefix, suffix := dsn[:at], dsn[at:]
					if colon := strings.LastIndex(prefix, ":"); colon > 0 {
						return prefix[:colon] + ":***" + suffix
					}
				}
			}
		}
		return dsn
	}
	// Go driver DSNs: user:password@tcp(host:port)/db
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		credentials := dsn[:at]
		if colon := strings.Index(credentials, ":"); colon > 0 {
			return credentials[:colon] + ":***" + dsn[at:]
		}
	}
	return dsn
}

func inspectDSLDir(dir string) (*inspectDSL, error) {
	set, loadIssues, err := dsl.LoadDir(dir)
	if err != nil {
		return nil, err
	}
	issues := append(loadIssues, set.Lint()...)
	return &inspectDSL{
		Dir:    dir,
		Models: len(set.Models),
		Tables: len(set.Tables),
		Forms:  len(set.Forms),
		Pages:  len(set.Pages),
		Valid:  len(issues) == 0,
		Issues: issues,
	}, nil
}

func printInspect(out interface{ Write([]byte) (int, error) }, report inspectReport) {
	write := func(format string, args ...any) { fmt.Fprintf(out, format, args...) }
	write("gable %s\n\n", report.Version)
	write("app        %s (%s)\n", report.App.Name, report.App.Env)
	write("http       %s (read %s, write %s)\n", report.HTTP.Addr, report.HTTP.ReadTimeout, report.HTTP.WriteTimeout)
	write("log        %s, %s, %s\n", report.Log.Dir, report.Log.Level, report.Log.Format)
	if report.DB.Driver == "" {
		write("db         not configured\n")
	} else {
		write("db         %s (max open %d, idle %d) %s\n", report.DB.Driver, report.DB.MaxOpen, report.DB.MaxIdle, report.DB.DSN)
	}
	write("tenant     %s, root %d/%s\n\n", report.Tenant.Mode, report.Tenant.RootID, report.Tenant.RootCode)
	write("namespace  root      %s\n", report.Namespace.Root)
	write("           cache     %s\n", report.Namespace.Cache)
	write("           flyway    %s\n", report.Namespace.Flyway)
	write("           cosid     %s\n", report.Namespace.CosID)
	write("           cluster   %s\n", report.Namespace.Cluster)
	write("           timeseries %s\n", report.Namespace.TimeSeries)

	if report.DSL == nil {
		return
	}
	write("\ndsl        %s: %d models, %d tables, %d forms, %d pages\n",
		report.DSL.Dir, report.DSL.Models, report.DSL.Tables, report.DSL.Forms, report.DSL.Pages)
	if report.DSL.Valid {
		write("           valid\n")
		return
	}
	for _, issue := range report.DSL.Issues {
		write("           %s\n", issue.Error())
	}
}
