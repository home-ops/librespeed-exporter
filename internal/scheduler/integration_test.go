package scheduler_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/config"
	"github.com/home-ops/librespeed-exporter/internal/httpserver"
	"github.com/home-ops/librespeed-exporter/internal/metrics"
	"github.com/home-ops/librespeed-exporter/internal/scheduler"
	"github.com/home-ops/librespeed-exporter/internal/speedtest"
)

func TestSchedulerFailureRetainsLastGoodMetricsAndScrapesDuringNextRun(t *testing.T) {
	servers := []config.Server{{ID: 12}, {ID: 34}}
	store := metrics.NewStore([]int{12, 34})
	registry := httpserver.NewRegistry(store)
	ready := false
	handler := httpserver.NewHandler(registry, func() bool { return ready })
	runner := &cycleRunner{
		started: make(chan int, 4),
		blockID: 34,
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
	waiter := &cycleWaiter{calls: make(chan chan struct{}, 2)}
	clock := &fixedClock{now: time.Unix(1_700_000_000, 0)}
	loop := scheduler.New(servers, time.Hour, runner, store, clock, waiter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- loop.RunWithReady(ctx, func() { ready = true })
	}()
	defer func() {
		cancel()
		close(runner.release)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("scheduler did not stop")
		}
	}()

	for _, id := range []int{12, 34} {
		if got := receiveID(t, runner.started); got != id {
			t.Fatalf("first cycle server ID = %d, want %d", got, id)
		}
	}
	firstCycle := receiveRelease(t, waiter.calls)
	clock.Advance(10 * time.Second)
	close(firstCycle)

	if got := receiveID(t, runner.started); got != 12 {
		t.Fatalf("second cycle first server ID = %d, want 12", got)
	}
	if got := receiveID(t, runner.started); got != 34 {
		t.Fatalf("server after failure = %d, want 34", got)
	}
	select {
	case <-runner.blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("later server did not run and block after earlier server failure")
	}

	scrape := make(chan struct {
		status int
		body   string
	}, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		body, _ := io.ReadAll(response.Body)
		scrape <- struct {
			status int
			body   string
		}{response.Code, string(body)}
	}()
	var result struct {
		status int
		body   string
	}
	select {
	case result = <-scrape:
	case <-time.After(time.Second):
		t.Fatal("metrics scrape blocked while runner was blocked")
	}
	if result.status != http.StatusOK {
		t.Fatalf("scrape status = %d, want 200", result.status)
	}
	assertMetricValue(t, result.body, "librespeed_download_bits_per_second", 12, 12000000)
	assertMetricValue(t, result.body, "librespeed_test_success", 12, 0)
	assertMetricValue(t, result.body, "librespeed_test_failures_total", 12, 1)
	assertMetricValue(t, result.body, "librespeed_test_last_attempt_timestamp_seconds", 12, 1700000010)
	assertMetricValue(t, result.body, "librespeed_test_last_success_timestamp_seconds", 12, 1700000000)
	assertMetricValue(t, result.body, "librespeed_test_success", 34, 1)
}

func assertMetricValue(t *testing.T, body, name string, serverID int, want float64) {
	t.Helper()
	label := fmt.Sprintf("%s{server_id=\"%d\"} ", name, serverID)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, label) {
			got, err := strconv.ParseFloat(strings.TrimPrefix(line, label), 64)
			if err != nil || got != want {
				t.Fatalf("%s value = %q, want %v", label, strings.TrimPrefix(line, label), want)
			}
			return
		}
	}
	t.Fatalf("metric %s for server %d missing", name, serverID)
}

type cycleRunner struct {
	mu      sync.Mutex
	counts  map[int]int
	started chan int
	blockID int
	blocked chan struct{}
	release chan struct{}
}

func (r *cycleRunner) Run(ctx context.Context, server config.Server) (speedtest.Result, error) {
	r.mu.Lock()
	if r.counts == nil {
		r.counts = make(map[int]int)
	}
	r.counts[server.ID]++
	count := r.counts[server.ID]
	r.started <- server.ID
	r.mu.Unlock()
	if server.ID == r.blockID && count == 2 {
		close(r.blocked)
		select {
		case <-r.release:
		case <-ctx.Done():
			return speedtest.Result{}, ctx.Err()
		}
	}
	if server.ID == 12 && count == 2 {
		return speedtest.Result{}, errors.New("injected failure")
	}
	return speedtest.Result{DownloadBitsPerSecond: float64(server.ID) * 1_000_000}, nil
}

type cycleWaiter struct {
	calls chan chan struct{}
}

func (w *cycleWaiter) Wait(ctx context.Context, _ time.Duration) error {
	release := make(chan struct{})
	w.calls <- release
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fixedClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func receiveID(t *testing.T, ids <-chan int) int {
	t.Helper()
	select {
	case id := <-ids:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server runner")
		return 0
	}
}

func receiveRelease(t *testing.T, releases <-chan chan struct{}) chan struct{} {
	t.Helper()
	select {
	case release := <-releases:
		return release
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cycle completion")
		return nil
	}
}
