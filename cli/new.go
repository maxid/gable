package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type newOptions struct {
	module    string
	gablePath string
	force     bool
}

func newNewCommand() *cobra.Command {
	opts := &newOptions{}
	cmd := &cobra.Command{
		Use:   "new <dir>",
		Short: "scaffold a new gable application",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNew(cmd, args[0], opts)
		},
	}
	cmd.Flags().StringVar(&opts.module, "module", "", "Go module path (default example.com/<dir>)")
	cmd.Flags().StringVar(&opts.gablePath, "gable-path", "", "path to a gable checkout (defaults to GABLE_PATH or the current module)")
	cmd.Flags().BoolVar(&opts.force, "force", false, "write into a non-empty directory")
	return cmd
}

func runNew(cmd *cobra.Command, dir string, opts *newOptions) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(abs); err == nil && len(entries) > 0 && !opts.force {
		return fmt.Errorf("gable: %s is not empty (use --force to write anyway)", dir)
	}

	name := filepath.Base(abs)
	module := opts.module
	if module == "" {
		module = "example.com/" + name
	}
	gablePath := opts.gablePath
	if gablePath == "" {
		gablePath = os.Getenv("GABLE_PATH")
	}
	if gablePath == "" {
		gablePath = findGablePath()
	}
	if gablePath == "" {
		return fmt.Errorf("gable: cannot find a gable checkout; pass --gable-path or set GABLE_PATH")
	}
	absGable, err := filepath.Abs(gablePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}

	files := map[string]string{
		"go.mod": fmt.Sprintf(`module %s

go 1.27

require github.com/maxid/gable v0.0.0

replace github.com/maxid/gable => %s
`, module, filepath.ToSlash(absGable)),
		"main.go":       mainTemplate(name),
		"gable.yaml":    configTemplate(name),
		"README.md":     readmeTemplate(name),
		".gitignore":    ".gable-dev/\nlogs/\n",
		"app/README.md": "Application packages live here.\n",
	}
	for path, content := range files {
		target := filepath.Join(abs, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "created %s\n\nNext:\n  cd %s\n  go mod tidy\n  go run .\n", abs, dir)
	return nil
}

func findGablePath() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module github.com/maxid/gable") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func mainTemplate(name string) string {
	return fmt.Sprintf(`package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/web"
)

type appModule struct{}

func (appModule) ID() string { return "app" }

func (appModule) Register(c *gable.Container) error {
	c.Set("app.name", %q)
	return nil
}

func (appModule) Start(context.Context) error { return nil }
func (appModule) Stop(context.Context) error  { return nil }

type helloController struct{}

func (helloController) Routes(r *web.Router) {
	r.GET("/hello/:name", func(c *web.Context) error {
		return c.JSON(200, map[string]string{"message": "hello " + c.Param("name")})
	}).Doc("say hello")
}

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		log.Fatal(err)
	}

	server := web.New(
		web.WithTitle(%q),
		web.WithVersion(gable.Version),
	)
	server.Register(helloController{})

	app := gable.New(gable.WithConfig(cfg))
	if err := app.Register(appModule{}); err != nil {
		log.Fatal(err)
	}
	if err := app.Register(web.NewModule(server, "")); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
`, name, name)
}

func configTemplate(name string) string {
	return fmt.Sprintf(`app:
  name: %s
  env: development

log:
  dir: logs
  level: info
  format: text

http:
  addr: ":8080"

db:
  max_open: 20
  max_idle: 5
`, name)
}

func readmeTemplate(name string) string {
	return fmt.Sprintf("# %s\n\nA gable application.\n\n```bash\ngo mod tidy\ngo run .\n```\n\nUse `gable dev` to rebuild and restart on source changes.\n", name)
}
