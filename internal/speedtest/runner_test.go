package speedtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/config"
)

const validOutput = `[{"server":{"name":"ignored server","host":"ignored.example"},"client":{"ip":"203.0.113.8","isp":"Sensitive ISP"},"bytes_sent":1234,"bytes_received":5678,"ping":12.5,"jitter":0.75,"download":42.25,"upload":8.5}]`

func TestRunnerRun(t *testing.T) {
	tests := []struct {
		name      string
		stdout    string
		stderr    string
		exitCode  int
		timeout   bool
		want      Result
		wantError FailureCategory
	}{
		{
			name:   "parses metrics and converts units",
			stdout: validOutput,
			want: Result{
				DownloadBitsPerSecond: 42_250_000,
				UploadBitsPerSecond:   8_500_000,
				PingSeconds:           0.0125,
				JitterSeconds:         0.00075,
				BytesSent:             1234,
				BytesReceived:         5678,
			},
		},
		{name: "malformed JSON", stdout: `[`, wantError: FailureParse},
		{name: "nonzero exit hides stderr", stdout: validOutput, stderr: "client=203.0.113.8 isp=Sensitive ISP", exitCode: 7, wantError: FailureExit},
		{name: "timeout", timeout: true, wantError: FailureTimeout},
		{name: "missing metric field", stdout: strings.Replace(validOutput, `,"jitter":0.75`, "", 1), wantError: FailureInvalidResult},
		{name: "missing server name", stdout: strings.Replace(validOutput, `"name":"ignored server",`, "", 1), wantError: FailureInvalidResult},
		{name: "null server name", stdout: strings.Replace(validOutput, `"name":"ignored server"`, `"name":null`, 1), wantError: FailureInvalidResult},
		{name: "null metric", stdout: strings.Replace(validOutput, `"download":42.25`, `"download":null`, 1), wantError: FailureInvalidResult},
		{name: "negative metric", stdout: strings.Replace(validOutput, `"ping":12.5`, `"ping":-12.5`, 1), wantError: FailureInvalidResult},
		{name: "non-finite metric", stdout: strings.Replace(validOutput, `"upload":8.5`, `"upload":1e999`, 1), wantError: FailureInvalidResult},
		{name: "multiple selected servers", stdout: strings.Replace(validOutput, `}]`, `},{"server":{"name":"second"}}]`, 1), wantError: FailureInvalidResult},
		{name: "oversized stdout", stdout: strings.Repeat("x", maxStdoutBytes+1), wantError: FailureOutputLimit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			argsPath := filepath.Join(dir, "args.txt")
			cliPath := filepath.Join(dir, "fake-cli")
			timeout := 5 * time.Second
			if tt.timeout {
				timeout = 50 * time.Millisecond
			}
			writeFakeCLI(t, cliPath, argsPath, tt.stdout, tt.stderr, tt.exitCode, tt.timeout)

			result, err := NewRunner(cliPath, filepath.Join(dir, "servers.json"), timeout).Run(context.Background(), config.Server{ID: 327})
			if tt.wantError != "" {
				if err == nil {
					t.Fatalf("Run() error = nil, want category %q", tt.wantError)
				}
				var failure *Failure
				if !errors.As(err, &failure) || failure.Category != tt.wantError {
					t.Fatalf("Run() error = %T %v, want category %q", err, err, tt.wantError)
				}
				if strings.Contains(err.Error(), "203.0.113.8") || strings.Contains(err.Error(), "Sensitive ISP") {
					t.Fatalf("Run() leaked sensitive output: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("Run() error = %v", err)
				}
				if result.DownloadBitsPerSecond != tt.want.DownloadBitsPerSecond || result.UploadBitsPerSecond != tt.want.UploadBitsPerSecond || result.PingSeconds != tt.want.PingSeconds || result.JitterSeconds != tt.want.JitterSeconds || result.BytesSent != tt.want.BytesSent || result.BytesReceived != tt.want.BytesReceived {
					t.Fatalf("Run() result = %#v, want %#v", result, tt.want)
				}
				if result.Duration <= 0 {
					t.Fatalf("Run() duration = %s, want positive duration", result.Duration)
				}
			}

			if tt.timeout {
				return
			}
			gotArgs, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("read recorded arguments: %v", err)
			}
			wantArgs := fmt.Sprintf("--local-json\n%s\n--server\n327\n--json\n--no-icmp\n", filepath.Join(dir, "servers.json"))
			if string(gotArgs) != wantArgs {
				t.Fatalf("CLI argv = %q, want %q", gotArgs, wantArgs)
			}
			for _, forbidden := range []string{"--share", "--telemetry-", "--server-json", "--skip-cert-verify"} {
				if strings.Contains(string(gotArgs), forbidden) {
					t.Errorf("CLI argv contains forbidden flag %q", forbidden)
				}
			}
		})
	}
}

func TestRunnerBoundsStderrWithoutLeakingIt(t *testing.T) {
	dir := t.TempDir()
	cliPath := filepath.Join(dir, "fake-cli")
	sensitiveStderr := "203.0.113.8 Sensitive ISP " + strings.Repeat("x", maxStderrBytes)
	writeFakeCLI(t, cliPath, filepath.Join(dir, "args.txt"), validOutput, sensitiveStderr, 0, false)

	_, err := NewRunner(cliPath, filepath.Join(dir, "servers.json"), time.Second).Run(context.Background(), config.Server{ID: 2})
	var failure *Failure
	if !errors.As(err, &failure) || failure.Category != FailureOutputLimit {
		t.Fatalf("Run() error = %v, want output-limit failure", err)
	}
	if strings.Contains(err.Error(), "203.0.113.8") || strings.Contains(err.Error(), "Sensitive ISP") {
		t.Fatalf("Run() leaked stderr: %v", err)
	}
}

func TestRunnerRejectsInvalidBytes(t *testing.T) {
	for _, value := range []string{"-1", "1.5", "18446744073709551616"} {
		t.Run(value, func(t *testing.T) {
			dir := t.TempDir()
			cliPath := filepath.Join(dir, "fake-cli")
			output := strings.Replace(validOutput, `"bytes_sent":1234`, `"bytes_sent":`+value, 1)
			writeFakeCLI(t, cliPath, filepath.Join(dir, "args.txt"), output, "", 0, false)
			_, err := NewRunner(cliPath, filepath.Join(dir, "servers.json"), 5*time.Second).Run(context.Background(), config.Server{ID: 1})
			var failure *Failure
			if !errors.As(err, &failure) || failure.Category != FailureInvalidResult {
				t.Fatalf("Run() error = %v, want invalid-result failure", err)
			}
		})
	}
}

func writeFakeCLI(t *testing.T, cliPath, argsPath, stdout, stderr string, exitCode int, timeout bool) {
	t.Helper()
	stdoutPath := filepath.Join(filepath.Dir(cliPath), "stdout")
	stderrPath := filepath.Join(filepath.Dir(cliPath), "stderr")
	if err := os.WriteFile(stdoutPath, []byte(stdout), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stderrPath, []byte(stderr), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBRESPEED_FAKE_CLI", "1")
	t.Setenv("LIBRESPEED_FAKE_ARGS", argsPath)
	t.Setenv("LIBRESPEED_FAKE_STDOUT", stdoutPath)
	t.Setenv("LIBRESPEED_FAKE_STDERR", stderrPath)
	t.Setenv("LIBRESPEED_FAKE_EXIT", strconv.Itoa(exitCode))
	t.Setenv("LIBRESPEED_FAKE_TIMEOUT", strconv.FormatBool(timeout))
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(argsPath) + "\nexec " + shellQuote(os.Args[0]) + " -test.run='^TestFakeCLIProcess$' -- \"$@\"\n"
	if err := os.WriteFile(cliPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestFakeCLIProcess(t *testing.T) {
	if os.Getenv("LIBRESPEED_FAKE_CLI") != "1" {
		return
	}
	if os.Getenv("LIBRESPEED_FAKE_TIMEOUT") == "true" {
		select {}
	}
	stdout, _ := os.ReadFile(os.Getenv("LIBRESPEED_FAKE_STDOUT"))
	stderr, _ := os.ReadFile(os.Getenv("LIBRESPEED_FAKE_STDERR"))
	_, _ = os.Stdout.Write(stdout)
	_, _ = os.Stderr.Write(stderr)
	if code := os.Getenv("LIBRESPEED_FAKE_EXIT"); code != "0" {
		os.Exit(7)
	}
	os.Exit(0)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
