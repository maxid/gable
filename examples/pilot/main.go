package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"

	"github.com/maxid/gcosid"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/orm"
	"github.com/maxid/gable/web"
)

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		log.Fatal(err)
	}

	tasks, cleanup, err := buildTaskStore(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer cleanup()

	server, err := newServer(cfg, tasks, pilotTenants())
	if err != nil {
		log.Fatal(err)
	}

	app := gable.New(gable.WithConfig(cfg))
	if err := app.Register(web.NewModule(server, "")); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

// buildTaskStore uses an in-memory store by default and switches to the ORM
// when a DSN is configured. The memory store keeps the example runnable without
// a database; the ORM path is what production uses.
func buildTaskStore(cfg *config.Config) (TaskStore, func(), error) {
	dsn := os.Getenv("GABLE_DB_DSN")
	if dsn == "" {
		dsn = cfg.DB.DSN
	}
	if dsn == "" {
		return newMemoryTaskStore(), func() {}, nil
	}

	driver := os.Getenv("GABLE_DB_DRIVER")
	if driver == "" {
		driver = cfg.DB.Driver
	}
	if driver == "" {
		driver = "mysql"
	}

	db, err := orm.Open(driver, dsn)
	if err != nil {
		return nil, nil, err
	}
	db.Configure(cfg.DB.MaxOpen, cfg.DB.MaxIdle)

	machineID := int64(1)
	if raw := os.Getenv("GABLE_MACHINE_ID"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			machineID = parsed
		}
	}
	ids, err := gcosid.NewSnowflake(gcosid.MillisecondLayout, machineID)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}

	store, err := newORMStore(db, ids)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	return store, func() { _ = db.Close() }, nil
}
