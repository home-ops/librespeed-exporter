package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/config"
	"github.com/home-ops/librespeed-exporter/internal/httpserver"
	"github.com/home-ops/librespeed-exporter/internal/metrics"
	"github.com/home-ops/librespeed-exporter/internal/scheduler"
	"github.com/home-ops/librespeed-exporter/internal/speedtest"
	"github.com/prometheus/exporter-toolkit/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("exporter stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	return runWithSignals(os.Getenv, signal.NotifyContext)
}

func runWithSignals(getenv func(string) string, notifySignals func(context.Context, ...os.Signal) (context.Context, context.CancelFunc)) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	if err := web.Validate(cfg.WebConfigFile); err != nil {
		return fmt.Errorf("invalid web config: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return err
	}
	probeListener, err := net.Listen("tcp", ":9468")
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("listen on probe port: %w", err)
	}

	serverIDs := make([]int, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		serverIDs = append(serverIDs, server.ID)
	}
	store := metrics.NewStore(serverIDs)
	registry := httpserver.NewRegistry(store)
	var ready atomic.Bool
	httpSrv := &http.Server{
		Handler:           httpserver.NewHandler(registry, ready.Load),
		ReadHeaderTimeout: 5 * time.Second,
	}
	probeSrv := &http.Server{
		Handler:           httpserver.NewProbeHandler(ready.Load),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := notifySignals(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	schedulerCtx, cancelScheduler := context.WithCancel(ctx)
	defer cancelScheduler()
	loop := scheduler.New(cfg.Servers, cfg.Interval, speedtest.NewRunner(cfg.CLIPath, cfg.ServerFile, cfg.TestTimeout), store, nil, nil, slog.Default())
	schedulerDone := make(chan struct{})
	var schedulerErr error
	schedulerStarted := make(chan struct{})
	go func() {
		schedulerErr = loop.RunWithReady(schedulerCtx, func() {
			ready.Store(schedulerCtx.Err() == nil)
			close(schedulerStarted)
		})
		close(schedulerDone)
	}()
	select {
	case <-schedulerStarted:
	case <-ctx.Done():
	}

	serverDone := make(chan struct{})
	var serveErr error
	probeDone := make(chan struct{})
	var probeErr error
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	go func() {
		serveErr = httpserver.Serve(listener, httpSrv, cfg.WebConfigFile, logger)
		close(serverDone)
	}()
	go func() {
		probeErr = probeSrv.Serve(probeListener)
		close(probeDone)
	}()

	shutdownErr := waitAndShutdown(ctx, cancelScheduler, &ready, httpSrv, listener, probeSrv, probeListener, schedulerDone, serverDone, probeDone, 10*time.Second)

	serveErr = normalizeServeError(ctx, serveErr)
	probeErr = normalizeServeError(ctx, probeErr)
	if errors.Is(schedulerErr, context.Canceled) {
		schedulerErr = nil
	}
	return errors.Join(serveErr, probeErr, schedulerErr, shutdownErr)
}

func normalizeServeError(ctx context.Context, err error) error {
	if errors.Is(err, http.ErrServerClosed) || (ctx.Err() != nil && errors.Is(err, net.ErrClosed)) {
		return nil
	}
	return err
}

func waitAndShutdown(ctx context.Context, cancelScheduler context.CancelFunc, ready *atomic.Bool, server *http.Server, listener net.Listener, probeServer *http.Server, probeListener net.Listener, schedulerDone, serverDone, probeDone <-chan struct{}, timeout time.Duration) error {
	select {
	case <-ctx.Done():
	case <-serverDone:
	case <-probeDone:
	case <-schedulerDone:
	}
	return shutdown(cancelScheduler, ready, server, listener, probeServer, probeListener, schedulerDone, serverDone, probeDone, timeout)
}

func shutdown(cancelScheduler context.CancelFunc, ready *atomic.Bool, server *http.Server, listener net.Listener, probeServer *http.Server, probeListener net.Listener, schedulerDone, serverDone, probeDone <-chan struct{}, timeout time.Duration) error {
	ready.Store(false)
	cancelScheduler()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	probeShutdownErr := probeServer.Shutdown(shutdownCtx)
	if probeShutdownErr != nil {
		probeShutdownErr = errors.Join(probeShutdownErr, probeServer.Close())
	}
	closeErr := listener.Close()
	probeCloseErr := probeListener.Close()
	<-schedulerDone
	<-serverDone
	<-probeDone
	if errors.Is(closeErr, net.ErrClosed) {
		closeErr = nil
	}
	if errors.Is(probeCloseErr, net.ErrClosed) {
		probeCloseErr = nil
	}
	return errors.Join(shutdownErr, probeShutdownErr, closeErr, probeCloseErr)
}
