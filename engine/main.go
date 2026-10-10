// Command s3bender is a self-hosted object storage server with per-bucket credentials.
// See _docs/ for the protocol; this binary is one implementation of it.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"s3bender/internal/server"
	"s3bender/wwwroot"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg := server.ConfigFromEnv()
	srv, err := server.New(cfg, wwwroot.FS, logger)
	if err != nil {
		return err
	}
	defer srv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Index anything on disk the object index doesn't know about yet, without blocking startup.
	go srv.ReindexAll(ctx)

	// Same pipeline on both ports; only the console's static assets differ (see Server.Handler).
	servers := []*http.Server{
		newHTTPServer(cfg.APIPort, srv.Handler(false)),
		newHTTPServer(cfg.FrontendPort, srv.Handler(true)),
	}
	errs := make(chan error, len(servers))
	for _, hs := range servers {
		go func() {
			logger.Info("listening", "addr", hs.Addr)
			if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errs <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err = <-errs:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, hs := range servers {
		wg.Go(func() { _ = hs.Shutdown(shutdownCtx) })
	}
	wg.Wait()
	return err
}

func newHTTPServer(port int, h http.Handler) *http.Server {
	return &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),
		Handler: h,
		// No read/write timeouts: uploads and downloads are streamed and may legitimately take
		// a long time. Only bound how long a client may dribble the headers.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
}
