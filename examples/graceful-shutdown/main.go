// Package main demonstrates the production-recommended pattern for serving a
// MuxMaster router behind http.Server with:
//
//  1. The full set of Server-side timeouts that close the slowloris and
//     read-stall vectors (SECURITY.md "Slowloris / Server Timeouts").
//  2. Signal-driven graceful shutdown (SIGINT, SIGTERM) that drains
//     in-flight requests up to a bounded deadline.
//  3. A cooperative handler example that observes ctx.Done() so the
//     Timeout middleware can preempt long-running work
//     (SECURITY.md MM-2026-0019).
//
// Run:
//
//	go run .
//
// Smoke-test (separate terminal):
//
//	curl http://localhost:8080/             # instant
//	curl http://localhost:8080/slow         # ~3 s
//	curl -m 1 http://localhost:8080/slow    # client timeout — handler observes ctx.Done()
//
// Trigger graceful shutdown (Ctrl-C, or `kill -TERM <pid>`). In-flight
// requests get up to gracefulTimeout to finish; new connections are
// rejected immediately. The process exits 0 on a clean drain or 1 on
// timeout.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	mm "github.com/FlavioCFOliveira/MuxMaster"
	mw "github.com/FlavioCFOliveira/MuxMaster/middleware"
)

const (
	listenAddr       = ":8080"
	gracefulTimeout  = 30 * time.Second
	readHeader       = 10 * time.Second
	readTimeout      = 30 * time.Second
	writeTimeout     = 30 * time.Second
	idleTimeout      = 90 * time.Second
	maxHeaderBytes   = 1 << 20 // 1 MiB
	cooperativeSleep = 3 * time.Second
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	r := mm.New()

	// Pre middleware wraps BOTH stdlib and FastHandler routes.
	r.Pre(mw.RecovererWithLogger(logger))
	r.Pre(mw.RequestID())

	// Per-request deadline. Handlers MUST observe ctx.Done() to be preempted.
	r.Use(mw.Timeout(5 * time.Second))
	r.Use(mw.Logger(os.Stdout))

	r.GET("/", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, "ready")
	})

	// /slow demonstrates the cooperative pattern: the handler does a long
	// piece of work but checks ctx.Done() so the Timeout middleware (or a
	// shutdown cancellation) can preempt it.
	r.GET("/slow", slowHandler)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           r,
		ReadHeaderTimeout: readHeader,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	// Start the server in a goroutine so we can wait on signals.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", listenAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	// Block until a termination signal arrives or the server fails.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		if err != nil {
			logger.Error("server failed before shutdown", "err", err)
			os.Exit(1)
		}
		return
	case sig := <-stop:
		logger.Info("shutdown signal received — draining in-flight requests",
			"signal", sig.String(), "deadline", gracefulTimeout)
	}

	// Bounded grace period for in-flight requests. After this deadline,
	// remaining connections are forcibly closed.
	ctx, cancel := context.WithTimeout(context.Background(), gracefulTimeout)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		// Shutdown(ctx) returns the ctx error if the deadline expires before
		// every connection drained — surface it as a non-zero exit so an
		// orchestrator (systemd, k8s) can record the unclean shutdown.
		logger.Error("graceful shutdown timed out — connections were closed mid-flight",
			"err", err)
		os.Exit(1)
	}

	logger.Info("clean shutdown complete")
}

// slowHandler simulates expensive work that must yield to context
// cancellation. The Timeout middleware (or graceful shutdown) cancels
// req.Context(); a handler that ignores it would hold a goroutine
// past the timeout. SECURITY.md MM-2026-0019.
func slowHandler(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	select {
	case <-time.After(cooperativeSleep):
		fmt.Fprintln(w, "slow work done")
	case <-ctx.Done():
		// ctx.Err() is context.Canceled (client gone or shutdown)
		// or context.DeadlineExceeded (Timeout middleware fired).
		http.Error(w, "request cancelled: "+ctx.Err().Error(), http.StatusGatewayTimeout)
	}
}
