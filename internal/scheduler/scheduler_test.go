package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/config"
	"github.com/home-ops/librespeed-exporter/internal/speedtest"
)

func TestRunLogsOnlyConfiguredServerIDAndFailureCategory(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	secret := "raw failure https://secret.invalid/path token=secret"
	runner := &singleResultRunner{errs: map[int]error{
		47: fmt.Errorf("%s: %w", secret, &speedtest.Failure{Category: speedtest.FailureParse}),
		48: errors.New(secret),
	}}
	waiter := newFakeWaiter()
	scheduler := New([]config.Server{{ID: 47}, {ID: 48}}, time.Hour, runner, recordingStore{}, nil, waiter, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, scheduler)
	_ = receive(t, waiter.calls)
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	output := logs.String()
	if !strings.Contains(output, "server_id=47") || !strings.Contains(output, "category=parse") ||
		!strings.Contains(output, "server_id=48") || !strings.Contains(output, "category=other") {
		t.Fatalf("log = %q, want configured IDs and safe categories", output)
	}
	if strings.Contains(output, secret) || strings.Contains(output, "secret.invalid") {
		t.Fatalf("log exposed raw failure: %q", output)
	}
}

func TestRunStartsImmediatelyAndWaitsAfterWholeCycle(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	waiter := newFakeWaiter()
	runner := newRecordingRunner(clock)
	scheduler := New([]config.Server{{ID: 8}, {ID: 2}}, 5*time.Minute, runner, recordingStore{}, clock, waiter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, scheduler)

	if got := receive(t, runner.started); got != 8 {
		t.Fatalf("first server ID = %d, want 8", got)
	}
	runner.releases <- struct{}{}
	if got := receive(t, runner.started); got != 2 {
		t.Fatalf("second server ID = %d, want 2", got)
	}
	runner.releases <- struct{}{}
	wait := receive(t, waiter.calls)
	if wait.duration != 5*time.Minute {
		t.Fatalf("wait duration = %s, want 5m", wait.duration)
	}
	if want := time.Unix(1_700_000_006, 0); clock.Now() != want {
		t.Fatalf("clock at wait = %s, want cycle completion at %s", clock.Now(), want)
	}
	select {
	case id := <-runner.started:
		t.Fatalf("server %d started before interval elapsed", id)
	default:
	}

	wait.release <- nil
	if got := receive(t, runner.started); got != 8 {
		t.Fatalf("next cycle server ID = %d, want 8", got)
	}
	runner.releases <- struct{}{}
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("Run() error = %v, want nil on cancellation", err)
	}
}

func TestRunWithReadySignalsBeforeRunnerStarts(t *testing.T) {
	ready := false
	runner := &readyCheckingRunner{ready: &ready, started: make(chan bool, 1)}
	scheduler := New([]config.Server{{ID: 1}}, time.Hour, runner, recordingStore{}, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- scheduler.RunWithReady(ctx, func() { ready = true })
	}()

	if runnerSawReady := receive(t, runner.started); !runnerSawReady {
		t.Fatal("runner started before readiness callback")
	}
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("RunWithReady() error = %v, want nil on cancellation", err)
	}
}

func TestRunWithReadySkipsCallbackWhenAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	scheduler := New(nil, time.Hour, nil, nil, nil, nil, nil)
	if err := scheduler.RunWithReady(ctx, func() { called = true }); err != nil {
		t.Fatalf("RunWithReady() error = %v, want nil", err)
	}
	if called {
		t.Fatal("readiness callback called after context cancellation")
	}
}

func TestRunIsSequentialAndContinuesAfterServerFailure(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	waiter := newFakeWaiter()
	failed := errors.New("test failed")
	runner := newRecordingRunner(clock)
	runner.failures[4] = failed
	store := &recordingStore{attempts: make(chan recordedAttempt, 3)}
	scheduler := New([]config.Server{{ID: 4}, {ID: 5}, {ID: 6}}, time.Hour, runner, store, clock, waiter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, scheduler)

	for i, id := range []int{4, 5, 6} {
		if got := receive(t, runner.started); got != id {
			t.Fatalf("server order = %d, want %d", got, id)
		}
		select {
		case attempt := <-store.attempts:
			t.Fatalf("server %d recorded before runner completed: %#v", id, attempt)
		default:
		}
		select {
		case next := <-runner.started:
			t.Fatalf("server %d started before server %d completed", next, id)
		default:
		}
		runner.releases <- struct{}{}
		attempt := receive(t, store.attempts)
		if attempt.serverID != id {
			t.Fatalf("recorded server ID = %d, want %d", attempt.serverID, id)
		}
		if attempt.result.Duration != 3*time.Second {
			t.Errorf("server %d duration = %s, want 3s", id, attempt.result.Duration)
		}
		if id == 4 && !errors.Is(attempt.err, failed) {
			t.Fatalf("first server error = %v, want failure", attempt.err)
		}
		if id != 4 && attempt.err != nil {
			t.Fatalf("server %d error = %v, want nil", id, attempt.err)
		}
		if attempt.attemptedAt != time.Unix(1_700_000_000+int64(i*3), 0) {
			t.Errorf("server %d attemptedAt = %s", id, attempt.attemptedAt)
		}
	}
	_ = receive(t, waiter.calls)
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if got := runner.maxActive(); got != 1 {
		t.Fatalf("maximum concurrent runners = %d, want 1", got)
	}
}

func TestRunCancellationDuringWaitStopsNextCycle(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	waiter := newFakeWaiter()
	runner := newRecordingRunner(clock)
	scheduler := New([]config.Server{{ID: 1}}, time.Hour, runner, recordingStore{}, clock, waiter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, scheduler)
	_ = receive(t, runner.started)
	runner.releases <- struct{}{}
	_ = receive(t, waiter.calls)
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	select {
	case id := <-runner.started:
		t.Fatalf("server %d started after cancellation", id)
	default:
	}
}

func TestRunCancellationStopsRemainingServersAndRecordsActiveAttempt(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	runner := &blockingRunner{started: make(chan int, 2)}
	store := &recordingStore{attempts: make(chan recordedAttempt, 2)}
	scheduler := New([]config.Server{{ID: 1}, {ID: 2}}, time.Hour, runner, store, clock, newFakeWaiter(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, scheduler)
	if got := receive(t, runner.started); got != 1 {
		t.Fatalf("active server ID = %d, want 1", got)
	}
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	active := receive(t, store.attempts)
	if active.serverID != 1 || !errors.Is(active.err, context.Canceled) {
		t.Fatalf("active attempt = %#v, want server 1 cancellation", active)
	}
	select {
	case id := <-runner.started:
		t.Fatalf("server %d started after cancellation", id)
	default:
	}
}

func TestNewUsesProductionClockAndWaiterWhenOmitted(t *testing.T) {
	runner := &immediateRunner{started: make(chan struct{}, 1)}
	store := &recordingStore{attempts: make(chan recordedAttempt, 1)}
	scheduler := New([]config.Server{{ID: 1}}, time.Hour, runner, store, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, scheduler)
	attempt := receive(t, store.attempts)
	if attempt.attemptedAt.IsZero() {
		t.Fatal("attemptedAt is zero, want production clock timestamp")
	}
	if attempt.result.Duration <= 0 {
		t.Fatalf("attempt duration = %s, want positive production clock duration", attempt.result.Duration)
	}
	_ = receive(t, runner.started)
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type waitCall struct {
	duration time.Duration
	release  chan error
}

type fakeWaiter struct {
	calls chan waitCall
}

func newFakeWaiter() *fakeWaiter {
	return &fakeWaiter{calls: make(chan waitCall, 8)}
}

func (w *fakeWaiter) Wait(ctx context.Context, duration time.Duration) error {
	release := make(chan error, 1)
	w.calls <- waitCall{duration: duration, release: release}
	select {
	case err := <-release:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type recordingRunner struct {
	clock    *fakeClock
	mu       sync.Mutex
	active   int
	maximum  int
	failures map[int]error
	started  chan int
	releases chan struct{}
}

func newRecordingRunner(clock *fakeClock) *recordingRunner {
	return &recordingRunner{
		clock:    clock,
		failures: make(map[int]error),
		started:  make(chan int, 16),
		releases: make(chan struct{}, 16),
	}
}

func (r *recordingRunner) Run(ctx context.Context, server config.Server) (speedtest.Result, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.maximum {
		r.maximum = r.active
	}
	r.started <- server.ID
	err := r.failures[server.ID]
	r.mu.Unlock()
	select {
	case <-r.releases:
	case <-ctx.Done():
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
		return speedtest.Result{}, ctx.Err()
	}
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	r.clock.Advance(3 * time.Second)
	return speedtest.Result{DownloadBitsPerSecond: float64(server.ID)}, err
}

func (r *recordingRunner) maxActive() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maximum
}

type blockingRunner struct {
	started chan int
}

type immediateRunner struct {
	started chan struct{}
}

type singleResultRunner struct {
	errs map[int]error
}

func (r *singleResultRunner) Run(_ context.Context, server config.Server) (speedtest.Result, error) {
	return speedtest.Result{}, r.errs[server.ID]
}

type readyCheckingRunner struct {
	ready   *bool
	started chan bool
}

func (r *readyCheckingRunner) Run(ctx context.Context, _ config.Server) (speedtest.Result, error) {
	r.started <- *r.ready
	<-ctx.Done()
	return speedtest.Result{}, ctx.Err()
}

func (r *immediateRunner) Run(context.Context, config.Server) (speedtest.Result, error) {
	r.started <- struct{}{}
	return speedtest.Result{}, nil
}

func (r *blockingRunner) Run(ctx context.Context, server config.Server) (speedtest.Result, error) {
	r.started <- server.ID
	<-ctx.Done()
	return speedtest.Result{}, ctx.Err()
}

type recordedAttempt struct {
	serverID    int
	result      speedtest.Result
	err         error
	attemptedAt time.Time
}

type recordingStore struct {
	attempts chan recordedAttempt
}

func (s recordingStore) RecordAttempt(serverID int, result speedtest.Result, err error, attemptedAt time.Time) {
	if s.attempts != nil {
		s.attempts <- recordedAttempt{serverID: serverID, result: result, err: err, attemptedAt: attemptedAt}
	}
}

func runAsync(ctx context.Context, scheduler *Scheduler) <-chan error {
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	return done
}

func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		var zero T
		return zero
	}
}

func receiveErr(t *testing.T, done <-chan error) error {
	t.Helper()
	return receive(t, done)
}

func TestRunReportsConfiguredServerOrder(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	runner := newRecordingRunner(clock)
	store := &recordingStore{attempts: make(chan recordedAttempt, 3)}
	waiter := newFakeWaiter()
	scheduler := New([]config.Server{{ID: 13}, {ID: 1}, {ID: 9}}, time.Minute, runner, store, clock, waiter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(ctx, scheduler)
	var got []int
	for range 3 {
		got = append(got, receive(t, runner.started))
		runner.releases <- struct{}{}
		_ = receive(t, store.attempts)
	}
	for i, want := range []int{13, 1, 9} {
		if got[i] != want {
			t.Fatalf("server order = %v, want [13 1 9]", got)
		}
	}
	_ = receive(t, waiter.calls)
	cancel()
	if err := receiveErr(t, done); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
}
