package main

import (
	"context"
	"fmt"

	"github.com/maxid/glogs"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
)

type helloModule struct {
	container *gable.Container
}

func (helloModule) ID() string { return "hello" }

func (m *helloModule) Register(c *gable.Container) error {
	m.container = c
	c.Set("hello.greeting", "world")
	return nil
}

func (m *helloModule) Start(context.Context) error {
	logger := gable.MustResolve[*glogs.Logger](m.container, "gable.logger")
	greeting := gable.MustResolve[string](m.container, "hello.greeting")
	logger.Channel(glogs.ChannelApp).Info("hello", "greeting", greeting)
	fmt.Println("hello", greeting)
	return nil
}

func (helloModule) Stop(context.Context) error { return nil }

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		panic(err)
	}
	app := gable.New(gable.WithConfig(cfg))
	if err := app.Register(&helloModule{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Run(ctx); err != nil {
		panic(err)
	}
}
