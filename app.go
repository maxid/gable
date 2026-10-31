package gable

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/maxid/glogs"
	"github.com/maxid/gns"

	"github.com/maxid/gable/config"
	gablelog "github.com/maxid/gable/log"
)

// DefaultStopTimeout bounds Run's shutdown when the caller's context ends.
const DefaultStopTimeout = 30 * time.Second

// Option configures an App at construction time.
type Option func(*App)

// App owns the container, the module lifecycle and the configuration.
type App struct {
	mu sync.Mutex

	cfg       *config.Config
	namespace gns.Set

	container *Container
	logger    *glogs.Logger
	ownsLog   bool

	modules []Module
	started []Module
	running bool
}

// New returns an application with development defaults.
func New(opts ...Option) *App {
	app := &App{
		cfg:       config.Default(),
		container: NewContainer(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(app)
		}
	}
	return app
}

// WithConfig replaces the configuration. A nil config is ignored.
func WithConfig(cfg *config.Config) Option {
	return func(app *App) {
		if cfg != nil {
			app.cfg = cfg
		}
	}
}

// WithLogger uses an existing glogs logger. The App does not close it.
func WithLogger(logger *glogs.Logger) Option {
	return func(app *App) {
		if logger != nil {
			app.logger = logger
			app.ownsLog = false
		}
	}
}

// Register adds a module. Registration happens before Start, so modules can
// declare dependencies on each other without a global registry.
func (a *App) Register(module Module) error {
	if module == nil {
		return errors.New("gable: nil module")
	}
	for _, existing := range a.modules {
		if existing.ID() == module.ID() {
			return fmt.Errorf("gable: duplicate module %q", module.ID())
		}
	}
	a.modules = append(a.modules, module)
	return nil
}

// Namespace returns the derived namespace set. It is populated by Start.
func (a *App) Namespace() gns.Set { return a.namespace }

// Container returns the application container.
func (a *App) Container() *Container { return a.container }

// Config returns the active configuration.
func (a *App) Config() *config.Config { return a.cfg }

// Logger returns the active logger, or nil before Start.
func (a *App) Logger() *glogs.Logger { return a.logger }

// Start validates the configuration, registers modules in dependency order
// and starts them. It does not block.
func (a *App) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.running {
		return errors.New("gable: app already started")
	}
	if a.cfg == nil {
		a.cfg = config.Default()
	}
	if err := a.cfg.Validate(); err != nil {
		return err
	}
	ns, err := gns.New(a.cfg.Namespace)
	if err != nil {
		return err
	}
	a.namespace = ns
	if a.logger == nil {
		logger, err := gablelog.New(a.cfg.Log, a.cfg.App.Name)
		if err != nil {
			return err
		}
		a.logger = logger
		a.ownsLog = true
	}

	// Framework values live under the reserved "gable." prefix so modules can
	// resolve them without importing App itself.
	a.container.Set("gable.app", a)
	a.container.Set("gable.config", a.cfg)
	a.container.Set("gable.logger", a.logger)
	a.container.Set("gable.namespace", a.namespace)

	ordered, err := orderModules(a.modules)
	if err != nil {
		a.closeLogger()
		return err
	}

	for _, module := range ordered {
		if err := module.Register(a.container); err != nil {
			a.closeLogger()
			return fmt.Errorf("gable: module %s register: %w", module.ID(), err)
		}
	}

	a.started = nil
	for _, module := range ordered {
		if err := module.Start(ctx); err != nil {
			_ = a.stopStarted(ctx)
			a.closeLogger()
			return fmt.Errorf("gable: module %s start: %w", module.ID(), err)
		}
		a.started = append(a.started, module)
	}

	a.running = true
	return nil
}

// Run starts the application, waits for ctx to end and shuts it down.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()

	stopCtx, cancel := context.WithTimeout(context.Background(), DefaultStopTimeout)
	defer cancel()
	return a.Stop(stopCtx)
}

// Stop shuts down started modules in reverse order and closes the logger the
// App created. It is safe to call before Start.
func (a *App) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	err := a.stopStarted(ctx)
	a.running = false
	a.closeLogger()
	return err
}

func (a *App) stopStarted(ctx context.Context) error {
	var errs []error
	for i := len(a.started) - 1; i >= 0; i-- {
		module := a.started[i]
		if err := module.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("gable: module %s stop: %w", module.ID(), err))
		}
	}
	a.started = nil
	return errors.Join(errs...)
}

func (a *App) closeLogger() {
	if a.ownsLog && a.logger != nil {
		_ = a.logger.Close()
		a.logger = nil
		a.ownsLog = false
	}
}
