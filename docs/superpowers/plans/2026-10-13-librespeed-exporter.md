# LibreSpeed Exporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a secure, actively maintained Prometheus LibreSpeed exporter with scrape-independent scheduled tests, hardened amd64 images, and deployment-ready observability assets.

**Architecture:** A Go service reads a mounted LibreSpeed server-list JSON file, runs pinned `librespeed-cli` as a bounded subprocess once per configured server sequentially, and serves in-memory latest-good measurements plus status metrics. Build the exporter and unmodified CLI from pinned sources into a minimal distroless image; CI publishes verified, signed artifacts and Docker/Kubernetes/Grafana examples.

**Tech Stack:** Go, Prometheus client_golang, Prometheus exporter-toolkit web config, LibreSpeed CLI, Docker Buildx, distroless, GitHub Actions, Trivy, SBOM/provenance/signing tooling, Grafana dashboard JSON.

---

## File Map

- `go.mod`, `go.sum`: exporter module and pinned direct/transitive Go dependencies.
- `cmd/librespeed-exporter/main.go`: flags, configuration startup, signal context, dependency wiring, and graceful shutdown.
- `internal/config/config.go`, `internal/config/config_test.go`: environment/flag parsing and strict server-file validation.
- `internal/speedtest/runner.go`, `internal/speedtest/runner_test.go`: CLI process invocation, timeout, JSON parsing, and output privacy.
- `internal/scheduler/scheduler.go`, `internal/scheduler/scheduler_test.go`: immediate initial cycle, sequential servers, no overlap, interval-after-cycle semantics, and cancellation.
- `internal/metrics/metrics.go`, `internal/metrics/metrics_test.go`: stable metric descriptors, per-server last-good state, status, timestamps, and counters.
- `internal/httpserver/server.go`, `internal/httpserver/server_test.go`: `/metrics`, `/healthz`, `/readyz`, and Prometheus web config TLS/basic auth.
- `Dockerfile`, `.dockerignore`, `scripts/image-smoke-test.sh`: pinned multi-stage amd64 build, non-root distroless runtime, and deterministic container smoke test.
- `LICENSE`, `LICENSES/librespeed-cli-LGPL-3.0.txt`, `THIRD_PARTY.md`: project and bundled CLI notices/source information.
- `deploy/docker-compose.yaml`, `deploy/kubernetes/deployment.yaml`, `deploy/kubernetes/service.yaml`, `deploy/kubernetes/networkpolicy.yaml`, `deploy/prometheus/prometheus.yaml`: secure deployment and scrape examples.
- `examples/servers.json`, `examples/web-config.yaml`: non-secret server and optional TLS/auth examples.
- `grafana/librespeed-dashboard.json`: importable dashboard using documented metric contract.
- `.github/workflows/ci.yaml`, `.github/workflows/release.yaml`: PR validation and signed scheduled/tagged publication.
- `renovate.json`: automated reviewable dependency, action, CLI, and base-image update PRs.
- `README.md`, `SECURITY.md`: positioning, setup, operations, security reporting, and limitations.

## Go Interfaces and State Types

Keep signatures shared across tasks:

```go
// internal/config
type Server struct {
    ID       int    `json:"id"`
    Name     string `json:"name"`
    URL      string `json:"server"`
    Download string `json:"dlURL"`
    Upload   string `json:"ulURL"`
    Ping     string `json:"pingURL"`
    GetIP    string `json:"getIpURL"`
}
type Config struct {
    Servers       []Server
    ServerFile    string
    ListenAddress string
    Interval      time.Duration
    TestTimeout   time.Duration
    CLIPath       string
    WebConfigFile string
}
func Load(getenv func(string) string) (Config, error)
func LoadServers(path string) ([]Server, error)

// internal/speedtest
type Result struct {
    DownloadBitsPerSecond, UploadBitsPerSecond float64
    PingSeconds, JitterSeconds                 float64
    BytesSent, BytesReceived                   uint64
    Duration                                   time.Duration
}
type Runner interface { Run(context.Context, config.Server) (Result, error) }
func NewRunner(cliPath, serverFile string, timeout time.Duration) *CLIRunner

// internal/metrics
type Store interface {
    RecordAttempt(serverID int, result speedtest.Result, err error, attemptedAt time.Time)
}
func NewStore(serverIDs []int) *StateStore // seed bounded series before first test
```

`internal/scheduler.Scheduler` receives immutable `[]config.Server`, `time.Duration`, a `speedtest.Runner`, a `Store`, and injectable `Clock`/`Waiter` interfaces. The production clock uses `time.Now`; the production waiter uses a timer that returns on context cancellation. A test clock/waiter lets schedule tests avoid wall-clock sleeps.

Define `Clock` as `Now() time.Time` and `Waiter` as `Wait(context.Context, time.Duration) error`. `Scheduler.Run(context.Context) error` runs first cycle immediately, then waits only after each configured server finishes. Cancellation during wait ends cleanly; ordinary per-server errors are recorded and do not end cycle.

## Pinned CLI Build Input

- LibreSpeed CLI release: `v1.0.14`.
- Immutable source commit: `42e5f76c90e64e04ab7079fd76d833dfe41805da`.
- Upstream source: `https://github.com/librespeed/speedtest-cli`.
- The release's `go.mod` declares Go `1.25.0`; use a Go `1.25.x` patch toolchain pinned by image digest and update it through Renovate.
- Build CLI from this source commit, not upstream release binary, so provenance ties executable to available source and checksum validation is straightforward.

## External Go APIs

- Register exporter metrics with `prometheus.NewRegistry` and expose them using `promhttp.HandlerFor` from `github.com/prometheus/client_golang`.
- Use `github.com/prometheus/exporter-toolkit/web` `FlagConfig` and `Serve` for Prometheus web config parsing and TLS/basic-auth listener behavior; create the TCP listener with `net.Listen` so shutdown can close it. Do not implement custom TLS/authentication middleware.

## Task 1: Bootstrap Module and Strict Configuration

**Files:**

- Create: `go.mod`, `cmd/librespeed-exporter/main.go`
- Create: `internal/config/config.go`, `internal/config/config_test.go`
- Create: `examples/servers.json`, `LICENSE`, `LICENSES/librespeed-cli-LGPL-3.0.txt`, `THIRD_PARTY.md`

- [ ] **Step 1: Add failing configuration tests** for `LoadServers` missing file, malformed JSON, empty list, duplicate IDs, invalid endpoint schemes/URLs, missing required endpoint paths, and accepted one-/multi-server configuration; add `Load` tests for invalid interval, interval below five minutes, invalid timeout, and documented defaults. Permit additional JSON fields so CLI-supported schema additions do not break this wrapper.
- [ ] **Step 2: Run `go test ./internal/config -run 'TestLoad$|TestLoadServers$' -v`**. Expected: FAIL because package implementation is absent.
- [ ] **Step 3: Implement config types and validation.** Read server JSON using LibreSpeed fields `id`, `name`, `server`, `dlURL`, `ulURL`, `pingURL`, and `getIpURL`; require unique IDs and non-empty endpoint fields. Parse `LIBRESPEED_SERVERS_FILE`, `LISTEN_ADDRESS`, `TEST_INTERVAL`, `TEST_TIMEOUT`, `CLI_PATH`, and `WEB_CONFIG_FILE`; default to `/etc/librespeed/servers.json`, `:9469`, `1h`, `2m`, `/usr/local/bin/librespeed-cli`, and disabled web config. Accept only `http` and `https` URLs with host; reject interval under `5m` and non-positive timeout. Do not parse web config in package; Task 4 validates it using exporter-toolkit `web.Validate` before opening listener.
- [ ] **Step 4: Add example config and licensing files.** Use reserved `.invalid` HTTPS endpoints in `examples/servers.json`; add Apache-2.0 project license; add exact LGPL-3.0 license text and `THIRD_PARTY.md` with LibreSpeed CLI source URL, release `v1.0.14`, commit `42e5f76c90e64e04ab7079fd76d833dfe41805da`, and corresponding-source release-asset URL policy. Do not include private endpoints.
- [ ] **Step 5: Run `gofmt -w internal/config cmd/librespeed-exporter && go test ./internal/config -v`.** Expected: all config tests PASS.
- [ ] **Step 6: Add minimal `cmd/librespeed-exporter/main.go`** that calls config loader, prints startup validation errors to stderr, and exits nonzero; scheduler/HTTP wiring comes in Task 4. Run `go test ./...`; expected: PASS with config package and executable scaffold.

## Task 2: Implement Bounded LibreSpeed CLI Runner

**Files:**

- Create: `internal/speedtest/runner.go`, `internal/speedtest/runner_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`

- [ ] **Step 1: Add fake executable tests** that record argv and return success JSON, malformed JSON, nonzero exit, timeout, missing measurement fields, and stderr containing sensitive-looking client/ISP data. Assert invocation uses `--local-json <path> --server <id> --json --no-icmp` and contains no `--share`, `--telemetry-*`, `--server-json`, or `--skip-cert-verify` arguments. Telemetry remains disabled by never enabling LibreSpeed CLI's sharing option; do not pass `--telemetry-level disabled`, which implies `--share` in CLI.
- [ ] **Step 2: Run `go test ./internal/speedtest -v`.** Expected: FAIL because runner is not implemented.
- [ ] **Step 3: Implement `Runner.Run(ctx, server)` using `exec.CommandContext` directly, no shell.** Invoke CLI with `--local-json <configured-file> --server <id> --json --no-icmp`; omit every telemetry/sharing flag because CLI sends telemetry only when `--share` is enabled. Pass no remote server-list or automatic discovery flag. Apply configured per-test timeout through child context. Bound stdout to 1 MiB and stderr to 16 KiB; classify overflow without returning or logging captured contents. Return typed categories for timeout, exit failure, parse failure, and invalid result without embedding raw output in errors. Use direct `exec.CommandContext`; don't add shell wrappers or child processes.
- [ ] **Step 4: Parse JSON fields** `server.name`, `bytes_sent`, `bytes_received`, `ping`, `jitter`, `download`, and `upload`. Require finite, non-negative numeric values. LibreSpeed CLI `--json` rates are Mbps; convert to bits per second by multiplying by 1,000,000. Convert ping and jitter milliseconds to seconds by dividing by 1,000. Preserve byte counts as bytes. Ignore client IP and ISP fields. Measure wall-clock duration around process execution.
- [ ] **Step 5: Run `go test ./internal/speedtest -v`.** Expected: all cases PASS, argv assertion confirms telemetry and ICMP disabled and configured server ID selected, and sensitive fixture strings never appear in errors.
- [ ] **Step 6: Run `go test ./...`.** Expected: PASS.

## Task 3: Add Scheduler and Last-Good Metric State

**Files:**

- Create: `internal/scheduler/scheduler.go`, `internal/scheduler/scheduler_test.go`
- Create: `internal/metrics/metrics.go`, `internal/metrics/metrics_test.go`

- [ ] **Step 1: Write metrics contract tests** asserting exact metric names/types/units and only `server_id` label, including zero last-success timestamp before first success.
- [ ] **Step 2: Write scheduler tests with a fake runner and controllable clock/waiter** for immediate first run, configured order, sequential execution, wait-after-completion, no overlap, interval cancellation, continue-after-one-server-failure, and shutdown cancellation.
- [ ] **Step 3: Run `go test ./internal/metrics ./internal/scheduler -v`.** Expected: FAIL because packages are absent.
- [ ] **Step 4: Implement a concurrency-safe metric state store and collector** exactly matching spec names: `librespeed_download_bits_per_second`, `librespeed_upload_bits_per_second`, `librespeed_ping_seconds`, `librespeed_jitter_seconds`, `librespeed_bytes_sent`, `librespeed_bytes_received`, `librespeed_test_duration_seconds`, `librespeed_test_last_attempt_timestamp_seconds`, `librespeed_test_last_success_timestamp_seconds`, `librespeed_test_success`, and `librespeed_test_failures_total`. Seed one series per configured server before first attempt; `test_success` and `last_success_timestamp_seconds` start at 0, as do measurement gauges. Each metric is labeled only by decimal `server_id`. Successful attempts atomically replace latest-good readings; failed attempts retain measurement gauges and last-success time while setting success to 0, updating attempt timestamp/duration, and incrementing failure counter.
- [ ] **Step 5: Implement scheduler.** On startup run cycle immediately. Process servers in configured order, update attempt state for every result, continue after per-server failure, then wait configured interval. Context cancellation stops future servers/cycles and no overlapping worker is launched.
- [ ] **Step 6: Run `go test ./internal/metrics ./internal/scheduler -race -v`.** Expected: all tests PASS with race detector.
- [ ] **Step 7: Run `go test ./...`.** Expected: PASS.

## Task 4: Add HTTP Endpoints, Web Config, and Process Lifecycle

**Files:**

- Create: `internal/httpserver/server.go`, `internal/httpserver/server_test.go`
- Modify: `cmd/librespeed-exporter/main.go`

- [ ] **Step 1: Write HTTP tests** for `/metrics`, `/healthz`, `/readyz`, readiness before/after initialization, health remaining successful when test attempts fail, graceful shutdown, and optional web-config TLS/basic-auth behavior. Define `NewHandler(registry, ready func() bool) http.Handler` and test routes with `httptest`; test listener/toolkit lifecycle separately.
- [ ] **Step 2: Run `go test ./internal/httpserver -v`.** Expected: FAIL because server is not implemented.
- [ ] **Step 3: Implement HTTP server** with a private `prometheus.Registry`, exporter metrics, `collectors.NewGoCollector`, `/metrics` handler using `promhttp.HandlerFor`, `/healthz`, and `/readyz`. Liveness reports process health only. Readiness is an HTTP-server atomic flag set after config validation and scheduler startup; it does not depend on Internet test success.
- [ ] **Step 4: Add Prometheus exporter-toolkit web config** with configured file path. Create listener with `net.Listen`. If web config is absent, call `http.Server.Serve(listener)`. If configured, call `web.Serve(listener, server, &web.FlagConfig{WebListenAddresses: &addresses, WebSystemdSocket: &systemdSocket, WebConfigFile: &webConfigPath}, logger)` from `github.com/prometheus/exporter-toolkit/web`; fail startup on invalid/unreadable config. Do not implement custom password or TLS handling. On SIGTERM/SIGINT invoke `http.Server.Shutdown` and close listener; treat `http.ErrServerClosed` as normal shutdown.
- [ ] **Step 5: Wire main lifecycle.** Construct store/collector/registry, seed configured server labels, start scheduler goroutine and set readiness true once scheduler is running, then serve listener. Use `signal.NotifyContext` for SIGTERM/SIGINT; cancel scheduler/subprocess first, call `http.Server.Shutdown` with 10-second context, close listener, wait for scheduler goroutine, and surface startup errors without logging config URLs or raw child output.
- [ ] **Step 6: Run `go test ./internal/httpserver ./... -race -v`.** Expected: PASS, including scrape not waiting for a test cycle.
- [ ] **Step 7: Run `go vet ./...`.** Expected: PASS.

## Task 5: Build and Verify Hardened Image

**Files:**

- Create: `Dockerfile`, `.dockerignore`
- Modify: `THIRD_PARTY.md`; create: `LICENSES/librespeed-cli-LGPL-3.0.txt`, `scripts/image-smoke-test.sh`

- [ ] **Step 1: Verify and record pinned LibreSpeed CLI input.** Check out upstream commit `42e5f76c90e64e04ab7079fd76d833dfe41805da`, verify it corresponds to release `v1.0.14`, inspect its LGPL-3.0 license and Go `1.25.0` module requirement, and confirm its CLI JSON/flags match Task 2. Create a corresponding-source archive from this exact unmodified source; record its SHA-256 and pinned builder/runtime image digests in `THIRD_PARTY.md`. Publish archive at matching GitHub release as `librespeed-cli-v1.0.14-source.tar.gz` and include repository URL/commit in `THIRD_PARTY.md`.
- [ ] **Step 2: Add exact LGPL-3.0 license notice** at `LICENSES/librespeed-cli-LGPL-3.0.txt` and update `THIRD_PARTY.md` with corresponding-source archive generation and release-asset URL convention.
- [ ] **Step 3: Implement multi-stage Dockerfile.** Pin Go `1.25.x` builder and distroless runtime images by digest; compile exporter and LibreSpeed CLI from commit `42e5f76c90e64e04ab7079fd76d833dfe41805da` with `CGO_ENABLED=0`, `-trimpath`, and stripped symbols. Fetch exact Git commit over verified TLS, check out detached, and assert `git rev-parse HEAD` equals pin before compiling; no mutable branch/tag resolution. Copy only exporter, CLI, CA certificates, and project/LGPL notices into distroless nonroot runtime. Do not add CLI source archive to runtime image; distribute corresponding source as GitHub release asset linked beside each image release. Set fixed non-root UID, executable entrypoint, OCI source/version/license labels, and `STOPSIGNAL SIGTERM`.
- [ ] **Step 4: Build amd64 image** with `docker buildx build --platform linux/amd64 --load -t librespeed-exporter:test .`. Expected: successful build and no network access during final stage.
- [ ] **Step 5: Add `scripts/image-smoke-test.sh`.** Accept image ref argument, generate temporary server-list JSON targeting `http://127.0.0.1:9/` so test cannot contact public network, start image with hardened flags, poll `healthz` and `readyz` for up to 20 seconds, scrape metrics from host loopback, assert scrape completes within 2 seconds and seeded labels contain only server IDs, then remove container and temp files using trap. Script exits nonzero and prints container logs on any failed assertion.
- [ ] **Step 6: Build and smoke-test amd64 image.** Run `docker buildx build --platform linux/amd64 --load -t librespeed-exporter:test .` then `scripts/image-smoke-test.sh librespeed-exporter:test`. Expected: image builds, runs non-root/read-only/no-capabilities, health endpoints return 200, metrics scrape completes during failed local test attempt, and no external server is contacted.
- [ ] **Step 7: Scan image and inspect filesystem.** Run Trivy vulnerability/misconfiguration scans and inspect image configuration for user, entrypoint, shell absence, package-manager absence, and unexpected files. Findings are recorded, not gated; tool execution errors fail.

## Task 6: Add Deployment Examples, Dashboard, and Operational Docs

**Files:**

- Create: `deploy/docker-compose.yaml`, `deploy/kubernetes/deployment.yaml`, `deploy/kubernetes/service.yaml`, `deploy/prometheus/prometheus.yaml`
- Create: `examples/web-config.yaml`, `grafana/librespeed-dashboard.json`
- Create: `README.md`, `SECURITY.md`

- [ ] **Step 1: Define example configuration and documentation checks.** Add JSON/YAML/dashboard syntax checks to a local script; confirm malformed examples are rejected.
- [ ] **Step 2: Add Docker Compose example.** Require non-empty image reference through `${IMAGE:?Set IMAGE to a verified immutable GHCR digest}`. Mount server JSON and optional web config read-only, bind service only to private interface or internal network, set read-only root, no-new-privileges, drop all capabilities, non-root user, resource bounds, and restart policy. Do not configure in-container healthcheck: stock distroless image has no shell or HTTP client; use Kubernetes HTTP probes or external monitoring.
- [ ] **Step 3: Add Kubernetes Deployment, Service, and NetworkPolicy.** Set non-root UID/GID, `allowPrivilegeEscalation: false`, read-only root filesystem, drop all capabilities, seccomp RuntimeDefault, resource requests/limits, read-only config/secret mounts, probes, and ClusterIP-only Service. Do not use host network or privileged mode. NetworkPolicy permits metrics ingress only from Prometheus pods. Document that standard Kubernetes NetworkPolicy cannot portably express FQDN egress allowlists; operators requiring egress restrictions must configure CNI-specific FQDN policy or static test-server IP/CIDR rules.
- [ ] **Step 4: Add Prometheus scrape example** with `scrape_interval: 30s`, `scrape_timeout: 10s`, and private service target. This demonstrates scrape does not trigger tests and completes independently of CLI's default per-server HTTP timeout.
- [ ] **Step 5: Add importable Grafana dashboard** with datasource and server ID variables, top-row current download/upload/ping/freshness/status, bandwidth history, success/failure history, ping/jitter trends, transfer volumes and test duration. Use `time() - librespeed_test_last_success_timestamp_seconds` for stale age. Do not add packet-loss panels or metrics absent from contract.
- [ ] **Step 6: Document operations and differentiator.** README explains scheduled-vs-scrape behavior, configuration schema, units, defaults/minimums, backend privacy, server egress, stale readings, security controls, Docker/Kubernetes setup, dashboard import, signed digest verification, license split, and limitations. Compare fairly with `brendonmatheson/prometheus-librespeed-exporter`: its existing functional coverage is acknowledged; differentiation is current maintenance, independent scheduling, hardening, predictable egress, and verifiable supply chain. SECURITY.md gives private vulnerability reporting instructions.
- [ ] **Step 7: Validate deployment assets.** Run `python3 -m json.tool examples/servers.json`, `python3 -m json.tool grafana/librespeed-dashboard.json`, `yamllint -s deploy/docker-compose.yaml deploy/kubernetes/deployment.yaml deploy/kubernetes/service.yaml deploy/kubernetes/networkpolicy.yaml deploy/prometheus/prometheus.yaml examples/web-config.yaml`, `promtool check config deploy/prometheus/prometheus.yaml`, `promtool check web-config examples/web-config.yaml`, and `kubeconform -strict -summary deploy/kubernetes/`. Expected: all examples parse and manifests validate offline against Kubernetes schemas. CI pins yamllint, Prometheus, and kubeconform versions.

## Task 7: Automate CI, Updates, Scanning, and Signed Releases

**Files:**

- Create: `.github/workflows/ci.yaml`, `.github/workflows/release.yaml`, `renovate.json`

- [ ] **Step 1: Add CI workflow.** Trigger on pull request, relevant branch pushes, and manual dispatch. Set least-privilege job permissions, concurrency, pinned action SHAs, Go formatting/vet/tests/race tests, config/dashboard validation, Docker build, amd64 smoke test, and source/image Trivy reports. Pull-request workflow must never publish or sign images.
- [ ] **Step 2: Add scheduled rebuild workflow** at `0 */6 * * *` UTC plus version-tag/manual release triggers. Scheduled build refreshes reports and rebuilds pinned inputs; immutable tags include UTC build timestamp and source revision, while `latest` moves only after full successful release. Dependency/base updates arrive as Renovate PRs and are built when merged. Separate unprivileged test/scan jobs from publication job; use GitHub OIDC for artifact attestation and keyless Cosign; grant package write and `id-token: write` only to publish job. Do not use long-lived signing secrets.
- [ ] **Step 3: Publish traceable references and artifacts in order.** Push immutable version + source-revision tag; generate SPDX JSON SBOM with Syft, GitHub artifact attestation provenance, and keyless Cosign signature using GitHub OIDC; publish scan reports and CLI corresponding-source archive; verify signature and provenance subject digest; only then publish/update `latest`. Attach immutable image digest and artifact links to workflow summary/release. Any failed required step leaves existing `latest` unchanged.
- [ ] **Step 4: Configure Renovate in `renovate.json`** for daily Go module, GitHub Actions, Docker digest, and pinned LibreSpeed CLI tag update PRs. Automerge only low-risk patch updates after CI; require human review for major/toolchain or security-workflow changes.
- [ ] **Step 5: Verify permissions and failure behavior.** Inspect rendered workflow permissions; ensure build/test jobs have read-only token, publish job alone has package write and signing OIDC identity, fork PRs cannot access secrets, vulnerability findings remain report-only, and scanner/SBOM/provenance/signing execution failures prevent publication. Generate third-party notices for exporter Go modules and bundled CLI; verify license metadata appears in SBOM and notice file ships in image/release.
- [ ] **Step 6: Run local validation:** `gofmt -w cmd internal && go test ./... -race && go vet ./... && docker buildx build --platform linux/amd64 --load -t librespeed-exporter:ci .`. Expected: all tests and build pass. Validate workflow YAML and image artifacts before merging.

## Task 8: End-to-End Acceptance and Release Readiness

**Files:**

- Modify only files identified by acceptance failures from Tasks 1-7.

- [ ] **Step 1: Run all unit/integration checks** with fake CLI and race detector: `go test ./... -race -count=1`; expected PASS with no Internet-dependent tests.
- [ ] **Step 2: Run local container acceptance** with an ephemeral local LibreSpeed-compatible test server or a fake CLI wired into a test-only image. Verify one successful cycle then forced failure: last-good values remain, `librespeed_test_success` becomes 0, failure counter increments, last-success timestamp is unchanged, later servers still run, and scrape stays responsive during tests.
- [ ] **Step 3: Verify security posture** by running the container with read-only root, no capabilities, no-new-privileges, non-root UID, and no host networking; verify TLS/basic-auth web config and reject invalid web config. Confirm no raw CLI output, client IP, ISP, or URLs enter logs/metrics.
- [ ] **Step 4: Validate deployment assets** by parsing Docker Compose, Kubernetes resources, Prometheus config, server JSON, web config, and Grafana dashboard; import dashboard into test Grafana or validate JSON/model queries against exported metric names.
- [ ] **Step 5: Verify release artifacts** in a non-production package/tag: inspect image digest and labels, SBOM, provenance, signature, scan artifacts, corresponding-source archive, and read-only nonroot runtime. Confirm all references resolve to exact repository commit and CLI source version.
- [ ] **Step 6: Review final diff** with `git diff --check`, inspect workflow permissions and license files, and ensure README claims match tested guarantees. Do not state enterprise compliance or vulnerability-free status.

## Spec Coverage Review

- Positioning, reference comparison, and explicit non-goals: Tasks 6 and 8.
- Go exporter, pinned CLI subprocess, config validation, scheduling, timeout, signal handling, and scrape independence: Tasks 1-4 and 8.
- Metric names/types/units, bounded labels, stale values, status, and per-server fault isolation: Tasks 3, 4, and 8.
- TLS/basic auth and readiness/liveness: Task 4.
- Distroless/non-root/read-only/no capabilities and license/source obligations: Tasks 1, 5, and 8.
- SBOM, provenance, signing, scan reports, six-hour rebuilds, pinning, and least privilege: Task 7.
- Docker/Kubernetes/Prometheus/Grafana assets: Task 6.
- Test coverage and end-to-end acceptance: Tasks 1-8.

## Commit Checkpoints

After completing and verifying each task, commit only files listed for that task. Suggested messages:

- Task 1: `feat: add validated exporter configuration`
- Task 2: `feat: run pinned LibreSpeed CLI safely`
- Task 3: `feat: schedule tests and export stable metrics`
- Task 4: `feat: serve metrics and health endpoints`
- Task 5: `build: add hardened distroless image`
- Task 6: `docs: add deployment examples and dashboard`
- Task 7: `ci: verify and sign exporter releases`
- Task 8: `test: verify end-to-end release posture`
