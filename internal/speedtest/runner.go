package speedtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/config"
)

const (
	maxStdoutBytes = 1 << 20
	maxStderrBytes = 16 << 10
)

type Result struct {
	DownloadBitsPerSecond, UploadBitsPerSecond float64
	PingSeconds, JitterSeconds                 float64
	BytesSent, BytesReceived                   uint64
	Duration                                   time.Duration
}

type Runner interface {
	Run(context.Context, config.Server) (Result, error)
}

type FailureCategory string

const (
	FailureTimeout       FailureCategory = "timeout"
	FailureCancelled     FailureCategory = "cancelled"
	FailureExit          FailureCategory = "exit"
	FailureParse         FailureCategory = "parse"
	FailureInvalidResult FailureCategory = "invalid-result"
	FailureOutputLimit   FailureCategory = "output-limit"
)

type Failure struct {
	Category FailureCategory
}

func (f *Failure) Error() string {
	return fmt.Sprintf("LibreSpeed CLI %s failure", f.Category)
}

type CLIRunner struct {
	cliPath    string
	serverFile string
	timeout    time.Duration
}

func NewRunner(cliPath, serverFile string, timeout time.Duration) *CLIRunner {
	return &CLIRunner{cliPath: cliPath, serverFile: serverFile, timeout: timeout}
}

func (r *CLIRunner) Run(ctx context.Context, server config.Server) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	started := time.Now()
	stdout := &boundedBuffer{limit: maxStdoutBytes}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd := exec.CommandContext(ctx, r.cliPath,
		"--local-json", r.serverFile,
		"--server", strconv.Itoa(server.ID),
		"--json", "--no-icmp",
	)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	duration := time.Since(started)

	if stdout.overflow || stderr.overflow {
		return Result{}, &Failure{Category: FailureOutputLimit}
	}
	if ctx.Err() != nil {
		category := FailureCancelled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			category = FailureTimeout
		}
		return Result{}, &Failure{Category: category}
	}
	if err != nil {
		return Result{}, &Failure{Category: FailureExit}
	}
	result, err := parseResult(stdout.Bytes())
	if err != nil {
		return Result{}, err
	}
	result.Duration = duration
	return result, nil
}

type boundedBuffer struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func parseResult(data []byte) (Result, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var records []map[string]json.RawMessage
	if err := decoder.Decode(&records); err != nil {
		return Result{}, &Failure{Category: FailureParse}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Result{}, &Failure{Category: FailureParse}
	}
	if len(records) != 1 {
		return Result{}, &Failure{Category: FailureInvalidResult}
	}

	var server struct {
		Name json.RawMessage `json:"name"`
	}
	if raw, ok := records[0]["server"]; !ok || json.Unmarshal(raw, &server) != nil || len(server.Name) == 0 {
		return Result{}, &Failure{Category: FailureInvalidResult}
	}
	var serverName string
	if json.Unmarshal(server.Name, &serverName) != nil || serverName == "" {
		return Result{}, &Failure{Category: FailureInvalidResult}
	}

	values := make(map[string]float64, 4)
	for _, key := range []string{"ping", "jitter", "download", "upload"} {
		raw, ok := records[0][key]
		if !ok {
			return Result{}, &Failure{Category: FailureInvalidResult}
		}
		value, err := finiteNumber(raw)
		if err != nil || value < 0 {
			return Result{}, &Failure{Category: FailureInvalidResult}
		}
		values[key] = value
	}

	bytesSent, err := unsignedInteger(records[0]["bytes_sent"])
	if err != nil {
		return Result{}, &Failure{Category: FailureInvalidResult}
	}
	bytesReceived, err := unsignedInteger(records[0]["bytes_received"])
	if err != nil {
		return Result{}, &Failure{Category: FailureInvalidResult}
	}

	result := Result{
		DownloadBitsPerSecond: values["download"] * 1_000_000,
		UploadBitsPerSecond:   values["upload"] * 1_000_000,
		PingSeconds:           values["ping"] / 1_000,
		JitterSeconds:         values["jitter"] / 1_000,
		BytesSent:             bytesSent,
		BytesReceived:         bytesReceived,
	}
	if math.IsInf(result.DownloadBitsPerSecond, 0) || math.IsNaN(result.DownloadBitsPerSecond) || math.IsInf(result.UploadBitsPerSecond, 0) || math.IsNaN(result.UploadBitsPerSecond) {
		return Result{}, &Failure{Category: FailureInvalidResult}
	}
	return result, nil
}

func finiteNumber(raw json.RawMessage) (float64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	value, err := number.Float64()
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("invalid number")
	}
	return value, nil
}

func unsignedInteger(raw json.RawMessage) (uint64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(number.String(), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}
