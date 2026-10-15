package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestRunWithSignalsHandlesInterruptAndTermination(t *testing.T) {
	for _, test := range []struct {
		name string
		sig  os.Signal
	}{
		{name: "SIGINT", sig: os.Interrupt},
		{name: "SIGTERM", sig: syscall.SIGTERM},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			serverFile := filepath.Join(dir, "servers.json")
			servers := `[{"id":1,"name":"local","server":"https://localhost","dlURL":"down","ulURL":"up","pingURL":"ping","getIpURL":"ip"}]`
			if err := os.WriteFile(serverFile, []byte(servers), 0600); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(dir, "cli.pid")
			cliPath := filepath.Join(dir, "fake-cli")
			script := "#!/bin/sh\nexec " + shellQuote(os.Args[0]) + " -test.run='^TestSignalCLIHelper$' --\n"
			if err := os.WriteFile(cliPath, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LIBRESPEED_SERVERS_FILE", serverFile)
			t.Setenv("LISTEN_ADDRESS", address)
			t.Setenv("TEST_INTERVAL", "5m")
			t.Setenv("CLI_PATH", cliPath)
			t.Setenv("SIGNAL_TEST_CLI_PID_FILE", pidFile)
			t.Setenv("SIGNAL_TEST_CLI_HELPER", "1")

			runResult := make(chan error, 1)
			go func() { runResult <- runWithSignals(os.Getenv, signal.NotifyContext) }()
			waitForFile(t, pidFile)
			waitForHTTP(t, "http://"+address+"/readyz", http.StatusOK)
			waitForHTTP(t, "http://127.0.0.1:9468/readyz", http.StatusOK)
			pidBytes, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			if err != nil {
				t.Fatalf("parse CLI pid: %v", err)
			}

			self, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			if err := self.Signal(test.sig); err != nil {
				t.Fatalf("send %s: %v", test.name, err)
			}
			select {
			case err := <-runResult:
				if err != nil {
					t.Fatalf("runWithSignals() after %s: %v", test.name, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("runWithSignals() did not return after %s", test.name)
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("scheduler CLI process %d still exists after %s; kill error = %v", pid, test.name, err)
			}
			if conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond); err == nil {
				conn.Close()
				t.Fatalf("listener still accepted connections after %s", test.name)
			}
			if conn, err := net.DialTimeout("tcp", "127.0.0.1:9468", 100*time.Millisecond); err == nil {
				conn.Close()
				t.Fatalf("probe listener still accepted connections after %s", test.name)
			}
		})
	}
}

func TestRunFailsAndClosesMetricsListenerWhenProbePortCannotBind(t *testing.T) {
	probeListener, err := net.Listen("tcp", ":9468")
	if err != nil {
		t.Fatalf("reserve probe port: %v", err)
	}
	defer probeListener.Close()

	dir := t.TempDir()
	serverFile := filepath.Join(dir, "servers.json")
	servers := `[{"id":1,"name":"local","server":"https://localhost","dlURL":"down","ulURL":"up","pingURL":"ping","getIpURL":"ip"}]`
	if err := os.WriteFile(serverFile, []byte(servers), 0600); err != nil {
		t.Fatal(err)
	}
	metricsListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	metricsAddress := metricsListener.Addr().String()
	if err := metricsListener.Close(); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"LIBRESPEED_SERVERS_FILE": serverFile,
		"LISTEN_ADDRESS":          metricsAddress,
		"TEST_INTERVAL":           "5m",
	}
	err = runWithSignals(func(key string) string { return env[key] }, signal.NotifyContext)
	if err == nil || !strings.Contains(err.Error(), "listen on probe port") {
		t.Fatalf("runWithSignals() error = %v, want probe-listener startup error", err)
	}
	if conn, err := net.DialTimeout("tcp", metricsAddress, 50*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("metrics listener remained open after probe bind failed")
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForHTTP(t *testing.T, url string, status int) {
	t.Helper()
	client := &http.Client{Timeout: 100 * time.Millisecond}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(url)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == status {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for HTTP %d at %s", status, url)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestSignalCLIHelper(t *testing.T) {
	if os.Getenv("SIGNAL_TEST_CLI_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("SIGNAL_TEST_CLI_PID_FILE"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
		os.Exit(2)
	}
	select {}
}

func TestNormalizeServeErrorForListenerClosedDuringShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	serveErr := (&http.Server{}).Serve(listener)
	if !errors.Is(serveErr, net.ErrClosed) {
		t.Fatalf("Serve() error = %v, want net.ErrClosed", serveErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := normalizeServeError(ctx, serveErr); got != nil {
		t.Fatalf("normalizeServeError() = %v, want nil for canceled shutdown", got)
	}
}

func TestNormalizeServeErrorPreservesUnexpectedFailureDuringShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := errors.New("listener failure")
	if got := normalizeServeError(ctx, want); !errors.Is(got, want) {
		t.Fatalf("normalizeServeError() = %v, want original error %v", got, want)
	}
}

func TestShutdownBoundsHTTPDrainAndClosesActiveConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	block := make(chan struct{})
	defer close(block)
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-block
		w.WriteHeader(http.StatusOK)
	})}
	serverDone := make(chan struct{})
	go func() {
		_ = httpServer.Serve(listener)
		close(serverDone)
	}()
	clientResult := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		clientResult <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request handler did not start")
	}

	schedulerDone := make(chan struct{})
	close(schedulerDone)
	probeListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	probeServer := &http.Server{}
	probeDone := make(chan struct{})
	go func() {
		_ = probeServer.Serve(probeListener)
		close(probeDone)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ready atomic.Bool
	startedShutdown := time.Now()
	err = waitAndShutdown(ctx, func() {}, &ready, httpServer, listener, probeServer, probeListener, schedulerDone, serverDone, probeDone, 30*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(startedShutdown); elapsed > time.Second {
		t.Fatalf("shutdown took %s, exceeded 1s bound", elapsed)
	}
	select {
	case clientErr := <-clientResult:
		if clientErr == nil {
			t.Fatal("active request completed successfully after forced connection close")
		}
	case <-time.After(time.Second):
		t.Fatal("active HTTP connection remained open after forced close")
	}
	if _, err := net.DialTimeout("tcp", listener.Addr().String(), 50*time.Millisecond); err == nil {
		t.Fatal("listener still accepted connections after forced close")
	}
	if _, err := net.DialTimeout("tcp", probeListener.Addr().String(), 50*time.Millisecond); err == nil {
		t.Fatal("probe listener still accepted connections after forced close")
	}
}
