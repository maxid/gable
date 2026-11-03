package gable

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/maxid/glogs"
	"github.com/maxid/gplugin"

	"github.com/maxid/gable/config"
	"github.com/maxid/gable/health"
	gablelog "github.com/maxid/gable/log"
	"github.com/maxid/gable/metrics"
)

// DefaultStopTimeout bounds Run's shutdown when the caller's context ends.
const DefaultStopTimeout = 30 * time.Second

// Option configures an App at construction time.
type Option func(*App)

// App owns the container, the module lifecycle and the configuration.
type App struct {
	mu sync.Mutex

	cfg       *config.Config
	namespace config.Namespace

	container *Container
	logger    *glogs.Logger
	ownsLog   bool

	// set holds the capabilities this binary declared. Assembly is the ordered,
	// started subset, nil before Start and after Stop.
	set      *gplugin.Set[*Container]
	assembly *gplugin.Assembly[*Container]
	running  bool

	// health and metrics are process-wide: one probe list, one scrape target. Modules add
	// to them during Register, so the wiring decides what an orchestrator can see.
	health  *health.Registry
	metrics *metrics.Registry
}

// New returns an application with development defaults.
func New(opts ...Option) *App {
	app := &App{
		cfg:       config.Default(),
		container: NewContainer(),
		set:       gplugin.NewSet[*Container](),
		health:    health.New(),
		metrics:   metrics.New(),
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
	if err := a.set.Add(module); err != nil {
		return fmt.Errorf("gable: %w", err)
	}
	return nil
}

// Namespace returns the derived namespace configuration. It is populated by
// Start.
func (a *App) Namespace() config.Namespace { return a.namespace }

// Manifest reports what Start wired, in start order: the capability id, its version
// and the ids it depends on. It is empty before Start and after Stop, which is what
// makes "print what this process enabled" a one-liner.
func (a *App) Manifest() []gplugin.Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.assembly == nil {
		return nil
	}
	return a.assembly.Manifest()
}

// Container returns the application container.
func (a *App) Container() *Container { return a.container }

// Health returns the process-wide probe registry. Modules add checks to it; an HTTP
// server mounts it with web.WithHealth.
func (a *App) Health() *health.Registry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.health == nil {
		a.health = health.New()
	}
	return a.health
}

// Metrics returns the process-wide metric registry, mounted with web.WithMetrics.
func (a *App) Metrics() *metrics.Registry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.metrics == nil {
		a.metrics = metrics.New()
	}
	return a.metrics
}

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
	a.namespace = a.cfg.Namespace.Normalize()
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
	a.container.Set("gable.health", a.health)
	a.container.Set("gable.metrics", a.metrics)

	// Ordering, registration and lifecycle are gplugin's assembly layer: it resolves
	// the dependency closure, sorts it deterministically and rolls back the modules it
	// already started when one fails. gable only owns the container, the configuration
	// and the logger.
	if ids := a.set.IDs(); len(ids) > 0 {
		assembly, err := a.set.Select(ids...)
		if err != nil {
			a.closeLogger()
			return fmt.Errorf("gable: %w", err)
		}
		if err := assembly.Start(ctx, a.container); err != nil {
			a.closeLogger()
			return fmt.Errorf("gable: %w", err)
		}
		a.assembly = assembly
	}

	a.running = true
	// Only now may the process take traffic: a load balancer that probes /readyz during
	// wiring must not see a half-registered application.
	a.health.SetStarted(true)
	// Start holds a.mu, so the manifest is read from the assembly directly: re-entering
	// the accessor would deadlock.
	enabled := 0
	if a.assembly != nil {
		enabled = len(a.assembly.Manifest())
	}
	a.metrics.Gauge("gable_capabilities", "Capabilities enabled in this binary.").Set(float64(enabled))
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

	// Stop accepting traffic before anything is torn down, so a probe during shutdown
	// fails fast instead of hitting a half-closed server.
	a.health.SetStarted(false)
	var err error
	if a.assembly != nil {
		if stopErr := a.assembly.Stop(ctx); stopErr != nil {
			err = fmt.Errorf("gable: %w", stopErr)
		}
		a.assembly = nil
	}
	a.running = false
	a.metrics.Gauge("gable_capabilities", "Capabilities enabled in this binary.").Set(0)
	a.closeLogger()
	return err
}

func (a *App) closeLogger() {
	if a.ownsLog && a.logger != nil {
		_ = a.logger.Close()
		a.logger = nil
		a.ownsLog = false
	}
}
