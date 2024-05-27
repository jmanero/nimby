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

// Main wraps the service to handle errors before calling os.Exit to set a no-zero status in main()
func Main() error {
	ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)

	ctx, logger, err := logging.Create(ctx, os.Stdout, nimby.EnvString("NIMBY_LOG_LEVEL", "info"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Unable to create logger", err)
		return err
	}

	logger.Info("service.starting")

	controller, err := nimby.New(nimby.Options{
		WatchServices: nimby.EnvStrings("NIMBY_SERVICES", ",", []string{"*"}),

		TokenPath:   nimby.EnvString("NOMAD_TOKEN_PATH", "/secrets/nomad_token"),
		TokenReload: []os.Signal{syscall.SIGHUP},
	})

	if err != nil {
		logger.Error("service.error", zap.Error(err))
		return err
	}

	server := http.Server{
		Handler: controller,
		Addr:    nimby.EnvString("NIMBY_ADDR", "0.0.0.0:9876"),
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			// Inject an annotated logger into request contexts
			ctx, logger := logging.WithLogger(ctx, logger, zap.Stringer("peer", conn.RemoteAddr()))
			logger.Info("http.connection")

			return ctx
		},
	}

	server.ErrorLog, _ = zap.NewStdLogAt(logger, zap.ErrorLevel)

	group, ctx := errgroup.WithContext(ctx)

	logger.Info("http.listen", zap.String("http.addr", server.Addr))
	group.Go(func() error { return nimby.NotError(server.ListenAndServe(), http.ErrServerClosed) })

	group.Go(func() error { return controller.TokenReloader(ctx) })
	group.Go(func() error { return controller.Updater(ctx) })

	<-ctx.Done()
	logger.Info("service.stopping")

	err = nimby.Shutdown(&server, time.Minute)
	logger.Error("http.shutdown", zap.Error(err))

	logger.Info("service.stopped")
	return group.Wait()
}

func main() {
	if Main() != nil {
		os.Exit(1)
	}
}
