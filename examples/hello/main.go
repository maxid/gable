package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"github.com/maxid/gable"
	"github.com/maxid/gable/config"
	"github.com/maxid/gable/web"
)

type helloResponse struct {
	Message string `json:"message"`
}

type helloController struct{}

func (helloController) Routes(r *web.Router) {
	r.GET("/hello/:name", func(c *web.Context) error {
		return c.JSON(200, helloResponse{Message: "hello " + c.Param("name")})
	}).Doc("say hello").Out(helloResponse{})
}

func main() {
	cfg, err := config.Load("gable.yaml")
	if err != nil {
		log.Fatal(err)
	}

	server := web.New(
		web.WithTitle("hello"),
		web.WithVersion(gable.Version),
		web.WithDescription("gable example application"),
	)
	server.Register(helloController{})

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
