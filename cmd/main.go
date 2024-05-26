package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmanero/nimby"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGTERM)

	controller := nimby.New()

	server := http.Server{
		Handler: controller,
		Addr:    nimby.EnvString("NIMBY_ADDR", "0.0.0.0:9876"),
	}

	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error { return server.ListenAndServe() })
	group.Go(func() error { return controller.Run(ctx, nimby.EnvStrings("NIMBY_SERVICES", ",", []string{"*"})) })

	<-ctx.Done()
	nimby.Shutdown(&server, time.Minute)
}
