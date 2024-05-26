package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmanero/nimby"
	"github.com/jmanero/nimby/logging"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGTERM)

	ctx, logger, err := logging.Create(ctx, os.Stdout, nimby.EnvString("NIMBY_LOG_LEVEL", "info"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to create logger", err)
		os.Exit(1)
	}

	controller, err := nimby.New(nimby.Options{
		WatchServices: nimby.EnvStrings("NIMBY_SERVICES", ",", []string{"*"}),

		TokenPath:   nimby.EnvString("NOMAD_TOKEN_PATH", "/secrets/nomad_token"),
		TokenReload: []os.Signal{syscall.SIGINT},
	})

	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to create controller", err)
		os.Exit(1)
	}

	server := http.Server{
		Handler: controller,
		Addr:    nimby.EnvString("NIMBY_ADDR", "0.0.0.0:9876"),
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			// Inject an annotated logger into request contexts
			ctx, logger := logging.WithLogger(ctx, logger, zap.Stringer("peer.addr", conn.RemoteAddr()))
			logger.Info("http.connection")

			return ctx
		},
	}

	server.ErrorLog, _ = zap.NewStdLogAt(logger, zap.ErrorLevel)

	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error { return nimby.NotError(server.ListenAndServe(), http.ErrServerClosed) })
	group.Go(func() error { return controller.TokenReloader(ctx) })
	group.Go(func() error { return controller.Updater(ctx) })

	<-ctx.Done()

	if err := nimby.Shutdown(&server, time.Minute); err != nil {
		logger.Error("http.shutdown", zap.Error(err))
	}

	if err := group.Wait(); err != nil {
		logger.Error("service", zap.Error(err))
		os.Exit(1)
	}
}
