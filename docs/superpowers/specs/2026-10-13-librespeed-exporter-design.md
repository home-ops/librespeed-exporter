# LibreSpeed Exporter Design

## Goal and Positioning

Create a public, actively maintained Prometheus exporter for LibreSpeed tests. The project must differentiate itself from existing `prometheus-librespeed-exporter` primarily through operational reliability and supply-chain security, not by claiming novel speed-test functionality.

The reference project already packages `librespeed-cli`, exposes LibreSpeed results, supports custom servers, and documents Prometheus and Docker usage. Its published image and source have not been updated for years. It runs tests on demand through `/probe`, tying test duration to Prometheus scrape behavior. Its documented implementation uses a shell script and `script_exporter`, an old Alpine base, unverified binary downloads, and `curl -k` for one dependency download.

This project will instead provide:

- Scheduled tests independent of Prometheus scrapes.
- Explicitly configured LibreSpeed test servers; no implicit public-server discovery.
- Stable, bounded metric labels and observable failures/stale results.
- A maintained, hardened distroless image and verifiable release artifacts.
- Ready-to-use Docker, Kubernetes, Prometheus, and Grafana configuration.

## Scope

V1 repository: `home-ops/librespeed-exporter`.

V1 includes a Go exporter, LibreSpeed CLI backend, one OCI image, amd64 publishing, Docker and Kubernetes deployment examples, Prometheus scrape configuration, and a combined Grafana dashboard. It does not implement a speed-test protocol, fork LibreSpeed CLI, host LibreSpeed test servers, or claim Ookla Speedtest compatibility. No Helm chart or additional architecture is required for V1.

The exporter repository is licensed under Apache-2.0. The bundled, unmodified LibreSpeed CLI remains a separately licensed LGPL-3.0 component; ship required notices, provide corresponding CLI source as required by the license, and document exact source/version references. Verify redistribution obligations before the first release.

## Architecture and Data Flow

The image contains two independent executables: the Go exporter and an unmodified, version-pinned `librespeed-cli` binary. The exporter supervises the CLI as a subprocess without invoking a shell. Both binaries and all build inputs are pinned and verified during the build. The runtime image is distroless and contains only required executables, certificates, and license notices.

At startup, the exporter validates its configuration, starts its HTTP server, and immediately begins a test cycle. It invokes LibreSpeed CLI once per configured server, sequentially, with telemetry and ICMP disabled. After the cycle completes, it waits the configured interval before beginning another cycle. Tests never run in response to a scrape, and cycles never overlap. Each invocation has a bounded timeout.

LibreSpeed CLI JSON is parsed into an internal result. Only required numeric measurements and configured server identity are retained. Do not persist, log, or export client IP, ISP, or other unnecessary CLI response fields. Prometheus scrapes read the latest in-memory state while tests continue independently.

## Configuration

Server definitions come from a read-only JSON file mounted into the container. The file uses LibreSpeed CLI's server-list format and must contain at least one server. Each server must have an explicit stable numeric ID, IDs must be unique, and endpoint fields required by the CLI must be valid. Test every configured server once per cycle in file order. HTTP and HTTPS endpoints are supported.

The test interval is configurable, defaults to one hour, and has a five-minute minimum. The per-test timeout is configurable and must be finite. Invalid configuration prevents startup with a concise, actionable error. Configuration is read at startup; changing it requires restarting the process/container.

CLI telemetry and ICMP are disabled. Do not enable result sharing, automatic public-server discovery, or unspecified server selection. Do not expose configuration URLs or CLI output in metric labels or routine logs.

## HTTP and Metrics Contract

Expose:

- `/metrics`: Prometheus metrics and latest completed measurements.
- `/healthz`: process liveness, independent of test-server availability.
- `/readyz`: exporter readiness after valid configuration is loaded and the test loop is initialized. Readiness must not require a successful Internet test, so transient backend/network faults do not trigger restart loops.

Expose this stable metric contract, each labeled only with `server_id`:

| Metric | Type | Unit and meaning |
| --- | --- | --- |
| `librespeed_download_bits_per_second` | Gauge | Download rate from latest successful test. |
| `librespeed_upload_bits_per_second` | Gauge | Upload rate from latest successful test. |
| `librespeed_ping_seconds` | Gauge | Ping from latest successful test, converted from CLI milliseconds. |
| `librespeed_jitter_seconds` | Gauge | Jitter from latest successful test, converted from CLI milliseconds. |
| `librespeed_bytes_sent` | Gauge | Bytes sent during latest successful test; not a process-lifetime counter. |
| `librespeed_bytes_received` | Gauge | Bytes received during latest successful test; not a process-lifetime counter. |
| `librespeed_test_duration_seconds` | Gauge | Duration of latest test attempt. |
| `librespeed_test_last_attempt_timestamp_seconds` | Gauge | Unix timestamp of latest attempt. |
| `librespeed_test_last_success_timestamp_seconds` | Gauge | Unix timestamp of latest successful test; zero before first success. |
| `librespeed_test_success` | Gauge | 1 if latest attempt succeeded, otherwise 0. |
| `librespeed_test_failures_total` | Counter | Cumulative failed attempts since process start. |

All metric names, types, and units are part of the documented API. Latest measurement values remain unchanged when an attempt fails; attempt status and failure count update independently.

Keep the last successful measurement visible after a failed test. Set current test status to failure, increment a failure counter, and retain last-success time so consumers can identify stale readings. A failure for one server does not prevent later configured servers from being tested in the same cycle.

Labels use only the configured server ID, whose set is bounded by the mounted configuration. Never use endpoint URLs, IP addresses, ISP names, arbitrary CLI text, or timestamps as labels. Do not add a label for raw failure messages.

The HTTP server supports Prometheus web configuration for optional TLS and basic authentication. The deployment must not expose the metrics port publicly by default; examples bind or publish it only on a trusted monitoring network and document network-policy/firewall restrictions. Authentication credentials and private keys are mounted as secrets and never baked into the image.

## Container and Deployment Security

The final image runs as a fixed non-root UID, has no shell or package manager, uses a read-only root filesystem, and requires no Linux capabilities. Docker and Kubernetes examples set `allowPrivilegeEscalation: false`, drop all capabilities, use a read-only root filesystem, and provide writable temporary storage only if required by measured CLI behavior. The container must not require privileged mode, host networking, or ICMP permissions.

Build and release controls:

- Pin base images, Go toolchain, LibreSpeed CLI version/source commit, and GitHub Actions by immutable digest or full commit SHA.
- Verify downloaded source/artifacts with checksums or build from pinned source; never disable TLS verification.
- Track updates through reviewable automated dependency/base-image pull requests.
- Run scheduled rebuilds every six hours to refresh build/runtime inputs.
- Produce an SBOM and build provenance, sign published immutable image references, and publish traceable version/revision tags.
- Publish `latest` only after all required build, test, scan, SBOM, provenance, and signing steps succeed.
- Scan source and images and publish reports. Vulnerability findings are informational and do not block publication in V1; scanner execution or report-generation failures do block publication.
- Include LibreSpeed CLI LGPL-3.0 notices and exact source/version references, and provide corresponding CLI source as required by the license. Do not modify CLI source. Verify redistribution obligations for the final distribution before first release.

No credentials, private test-server configuration, or deployment-specific data are included in the repository or image.

## Grafana Dashboard

Provide an importable dashboard JSON with a Prometheus datasource variable and server selector. Combine operations overview and link diagnostics in one dashboard:

- Top row: current download, upload, ping, last-test freshness, and current test status.
- Middle: download/upload trends and test success/failure history.
- Lower section: ping and jitter trends, bytes transferred, test duration, and per-server diagnostics.
- Show stale data explicitly using last-success age/status; do not imply retained last-good values are current.
- Do not include packet loss because LibreSpeed CLI does not provide that measurement.

Dashboard uses only metrics defined by this exporter, has useful default time ranges and units, and includes no bundled Grafana plugins.

## Failure and Shutdown Behavior

A CLI timeout, non-zero exit, invalid JSON, or missing/invalid required measurement fails that server's attempt. Preserve prior successful values, expose failure status, and continue with remaining servers. Log a concise error with configured server ID and failure category; avoid dumping raw CLI output that could contain sensitive endpoint or client data.

On SIGTERM/SIGINT, stop scheduling new cycles, allow the current bounded CLI invocation to terminate within its timeout/grace period, then exit cleanly. Do not start a replacement cycle if shutdown begins during the wait interval.

## Verification

Tests cover configuration validation, scheduling semantics, interval minimum, sequential multi-server execution, no overlap, subprocess timeout and shutdown, CLI JSON parsing, invalid/missing fields, last-good retention, per-server isolation, and metric label bounds. Use a fake CLI executable for deterministic integration tests; normal tests must not contact public speed-test servers.

CI validates formatting, static analysis, unit/integration tests, dependency and image scans, Docker/Kubernetes examples, dashboard JSON, amd64 image build, non-root/read-only runtime smoke test, health endpoints, TLS/basic-auth web configuration, SBOM, provenance, and image signature verification. Release tests verify immutable tags and metadata map back to source revision and pinned LibreSpeed CLI version.

## Success Criteria

- Prometheus scrapes remain fast and do not launch or block on tests.
- Test cycles run on configured cadence, sequentially, without overlap.
- Network/test failures remain observable without erasing last successful readings or restarting the exporter.
- Image works as non-root with read-only filesystem and no added capabilities.
- Published artifacts identify source revisions and CLI version and include verifiable SBOM, provenance, and signature.
- Deployment examples and dashboard import cleanly and use only documented metrics.
- README clearly explains how this project differs from `brendonmatheson/prometheus-librespeed-exporter` and makes no unsupported “enterprise-grade” or vulnerability-free claims.
