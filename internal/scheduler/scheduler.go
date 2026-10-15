package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/config"
	"github.com/home-ops/librespeed-exporter/internal/metrics"
	"github.com/home-ops/librespeed-exporter/internal/speedtest"
)

type Clock interface {
	Now() time.Time
}

type Waiter interface {
	Wait(context.Context, time.Duration) error
}

type Scheduler struct {
	servers  []config.Server
	interval time.Duration
	runner   speedtest.Runner
	store    metrics.Store
	clock    Clock
	waiter   Waiter
	logger   *slog.Logger
}

func New(servers []config.Server, interval time.Duration, runner speedtest.Runner, store metrics.Store, clock Clock, waiter Waiter, logger *slog.Logger) *Scheduler {
	if clock == nil {
		clock = realClock{}
	}
	if waiter == nil {
		waiter = timerWaiter{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		servers:  append([]config.Server(nil), servers...),
		interval: interval,
		runner:   runner,
		store:    store,
		clock:    clock,
		waiter:   waiter,
		logger:   logger,
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	return s.RunWithReady(ctx, nil)
}

func (s *Scheduler) RunWithReady(ctx context.Context, onStarted func()) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	if onStarted != nil {
		onStarted()
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		for _, server := range s.servers {
			if err := ctx.Err(); err != nil {
				return nil
			}
			attemptedAt := s.clock.Now()
			result, err := s.runner.Run(ctx, server)
			result.Duration = s.clock.Now().Sub(attemptedAt)
			s.store.RecordAttempt(server.ID, result, err, attemptedAt)
			if err != nil && !errors.Is(err, context.Canceled) {
				category := "other"
				var failure *speedtest.Failure
				if errors.As(err, &failure) {
					switch failure.Category {
					case speedtest.FailureTimeout, speedtest.FailureCancelled, speedtest.FailureExit,
						speedtest.FailureParse, speedtest.FailureInvalidResult, speedtest.FailureOutputLimit:
						category = string(failure.Category)
					}
				}
				s.logger.Error("speed test failed", "server_id", server.ID, "category", category)
			}
			if ctx.Err() != nil {
				return nil
			}
		}
		if err := s.waiter.Wait(ctx, s.interval); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type timerWaiter struct{}

func (timerWaiter) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
