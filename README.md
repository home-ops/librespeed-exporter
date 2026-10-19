# LibreSpeed Exporter

LibreSpeed Exporter runs scheduled LibreSpeed tests and exports the latest measurements to Prometheus. Scraping `/metrics` never starts a test: tests run immediately at process start and then sequentially, with the configured delay after each full cycle. Scrapes remain independent while a test is running.

This project is maintained as a security-conscious alternative to
[`brendonmatheson/prometheus-librespeed-exporter`](https://github.com/brendonmatheson/prometheus-librespeed-exporter).
That project provides functional LibreSpeed-to-Prometheus coverage. This
exporter's focus is current maintenance, scrape-independent scheduling,
bounded and predictable server selection, hardened deployment examples, and
verifiable release artifacts. Evaluate both against your operational needs.

## Metrics

All exporter metrics have one bounded label, `server_id`, containing configured
server IDs. Rates are bits/second, ping and jitter are seconds, transfer values
are bytes, durations are seconds, and timestamps are Unix seconds.

| Metric | Type | Meaning |
| --- | --- | --- |
| `librespeed_download_bits_per_second` | Gauge | Download rate from latest successful test |
| `librespeed_upload_bits_per_second` | Gauge | Upload rate from latest successful test |
| `librespeed_ping_seconds` | Gauge | Ping from latest successful test |
| `librespeed_jitter_seconds` | Gauge | Jitter from latest successful test |
| `librespeed_bytes_sent` | Gauge | Bytes sent in latest successful test |
| `librespeed_bytes_received` | Gauge | Bytes received in latest successful test |
| `librespeed_test_duration_seconds` | Gauge | Duration of latest attempt, successful or failed |
| `librespeed_test_last_attempt_timestamp_seconds` | Gauge | Time of latest attempt |
| `librespeed_test_last_success_timestamp_seconds` | Gauge | Time of latest success; zero before first success |
| `librespeed_test_success` | Gauge | One for latest successful attempt, otherwise zero |
| `librespeed_test_failures_total` | Counter | Failed attempts since process start |

Failed tests preserve last-good measurement gauges and last-success time, while
setting latest-attempt status to failure. Metric state and failure counts reset
when process restarts. `/healthz` reports process health; `/readyz` reports
configuration/scheduler readiness and does not require successful Internet
tests.

## Configuration

`LIBRESPEED_SERVERS_FILE` selects a LibreSpeed server-list JSON file. File must
contain non-empty array. Each server needs unique non-negative `id`, `name`,
`server`, `dlURL`, `ulURL`, `pingURL`, and `getIpURL` fields. `server` must be an
absolute HTTP(S) URL with a host. Endpoint fields must be relative paths, without
query strings or fragments. Additional JSON fields are accepted. See
[`examples/servers.json`](examples/servers.json) for a working single-server
example. It uses a public test server; review its operator's privacy policy
before use. For another region or self-hosted backend, replace the entry with
server details you trust.

| Environment variable | Default | Constraint |
| --- | --- | --- |
| `LIBRESPEED_SERVERS_FILE` | `/etc/librespeed/servers.json` | Readable valid JSON file |
| `LISTEN_ADDRESS` | `:9469` | Go TCP listen address |
| `TEST_INTERVAL` | `1h` | Go duration, minimum `5m`; delay after complete cycle |
| `TEST_TIMEOUT` | `2m` | Positive Go duration per server test |
| `CLI_PATH` | `/usr/local/bin/librespeed-cli` | LibreSpeed CLI executable |
| `WEB_CONFIG_FILE` | unset | Optional exporter-toolkit TLS/basic-auth YAML |

TLS certificate/key paths referenced from web config must be mounted into the
container. Protect web config, private keys, and bcrypt hashes as secrets. TLS
and authentication are commented out in
[`examples/web-config.yaml`](examples/web-config.yaml); uncomment and configure
them with real mounted files and hashes to enable either feature.

## Privacy And Egress

The exporter does not send telemetry or exporter measurements to this project.
It invokes bundled LibreSpeed CLI with local server-list selection and sharing
disabled; it does not enable LibreSpeed sharing. Speed tests still send test
traffic to each configured server. The repository's sample server list pins one
public server and does not update automatically. LibreSpeed.org
publishes a live list at
[`https://librespeed.org/backend-servers/servers.php`](https://librespeed.org/backend-servers/servers.php).
Use that list to choose a server near your cluster, then copy only that server's
entry into your configuration. Review the operator and privacy policy first; the
remote operator sees your cluster's source IP and test traffic. The CLI necessarily
contacts configured test-server URLs and their endpoint paths to perform tests.
Only configure servers you trust, and review their privacy policies: the remote
operator can observe your source IP and test traffic. Server configuration may
include private hostnames; keep it out of public repositories and support logs.
Metrics expose only configured numeric server IDs, not names, IP addresses, ISP
details, URLs, or CLI output.

Tests run immediately at startup, in configuration order, one server at a time.
After a full cycle, exporter waits `TEST_INTERVAL` before next cycle. Each CLI
run is limited by `TEST_TIMEOUT`; scrape interval has no effect on test cadence.
The exporter does not discover arbitrary public test servers. Standard
Kubernetes NetworkPolicy cannot portably allow egress by FQDN. Apply
CNI-specific FQDN controls or maintain static test-server IP/CIDR rules if
egress restrictions are required. DNS and server-address changes can make
static rules brittle. NetworkPolicy/CNI rules may also need to allow kubelet or
node probe traffic to the health-only pod port described below.

## Docker Compose

Use immutable image reference by digest, not mutable tag:

```sh
export IMAGE='ghcr.io/home-ops/librespeed-exporter@sha256:<verified-digest>'
docker compose -f deploy/docker-compose.yaml up -d
```

Compose requires a non-empty `IMAGE` value only; it does not validate whether
reference is immutable or trusted. Operators MUST set `IMAGE` to a verified
immutable GHCR digest reference (`ghcr.io/...@sha256:<digest>`), not a mutable
tag.

The example mounts server and web configuration read-only, binds host port to
loopback, and applies read-only root, non-root UID, dropped capabilities,
no-new-privileges, and CPU/memory/PID limits. Change the bind address only when
metrics are intentionally exposed on a trusted private interface. The provided
image is distroless and has no shell-based healthcheck. Use Kubernetes HTTP
probes or an external container health monitor.

## Kubernetes

The Kubernetes bundle deploys a single exporter Pod, a ClusterIP Service, a
servers ConfigMap, and an ingress-only NetworkPolicy. It starts with one pinned
public test server: Amsterdam, Netherlands, operated by Clouvider. This is a
working example, not a location recommendation. The exporter tests it
immediately at startup and every hour. Each test transfers data and exposes your
cluster's outbound source IP to the operator. Review [Clouvider's privacy
policy](https://www.clouvider.com/privacy-and-cookies-policy/) and confirm this is
acceptable before deploying.

For a closer test point, use the
[LibreSpeed.org server list](https://librespeed.org/backend-servers/servers.php)
to choose a server, then replace the sample entry. You can also use a
self-hosted LibreSpeed server. Replace the sample in
`deploy/kubernetes/servers-configmap.yaml` before deploying. The copy in
`examples/servers.json` documents the same server and does not update
automatically.

Replace the placeholder with a verified immutable GHCR digest in
`deploy/kubernetes/deployment.yaml`. The bundle defaults to namespace `default`;
change `namespace` in its `kustomization.yaml` for another namespace.

For a local deployment with a different reviewed server list, create a ConfigMap
from your local file and apply the remaining manifests:

```sh
kubectl create configmap librespeed-exporter-servers \
  --from-file=servers.json=/path/to/servers.json \
  --dry-run=client -o yaml > servers-configmap.yaml
kubectl apply -f deploy/kubernetes/deployment.yaml \
  -f deploy/kubernetes/service.yaml \
  -f deploy/kubernetes/networkpolicy.yaml \
  -f servers-configmap.yaml
```

For GitOps, create a Kustomize overlay with a `configMapGenerator` using your
reviewed server list. Include the resources from `deploy/kubernetes/`, exclude
`servers-configmap.yaml`, and generate a ConfigMap named
`librespeed-exporter-servers`. Keep private server URLs out of public Git.
Never include both the sample ConfigMap and a generated ConfigMap with the same
name.

The example Service is ClusterIP. The pod runs non-root with read-only root,
RuntimeDefault seccomp, no added capabilities, resource bounds, and HTTP probes.
Kubernetes probes use dedicated port `9468`, which serves only `/healthz` and
`/readyz`; it never serves metrics or carries sensitive data. This fixed listener
is bound inside the pod, is not declared in the container ports or published by
the Service. The example NetworkPolicy permits inbound TCP/9468 for health
probes and TCP/9469 only from Prometheus Pods labeled
`app.kubernetes.io/name: prometheus` in namespace `monitoring`; adapt labels and
namespace to your cluster. Verify that policy matches your Prometheus Pod labels.
The policy does not restrict egress. If your cluster enforces egress policies,
allow DNS and the configured LibreSpeed server endpoints. Standard Kubernetes
NetworkPolicy cannot portably allow egress by FQDN. Probe traffic can appear to
come from node addresses; allow kubelet/node-originated traffic to Pod TCP/9468
using CNI-specific policy if needed.

The default deployment does not enable `WEB_CONFIG_FILE` and requires no web
configuration Secret. To enable exporter-toolkit TLS or basic authentication,
add the `WEB_CONFIG_FILE` environment variable and mount a Secret containing
`web-config.yaml`; mount any certificate and key files referenced by that
configuration as Secrets too. The metrics Service then still listens on port
9469, using the configured TLS/auth behavior.

For Prometheus, configure a scrape target using the Service DNS name and port:

```yaml
- job_name: librespeed-exporter
  static_configs:
    - targets: [librespeed-exporter.<namespace>.svc.cluster.local:9469]
```

Replace `<namespace>` with the namespace where you deploy the bundle; the
default is `default`. Scraping reads the latest completed test; it does not
start a test. Test cadence is
controlled by `TEST_INTERVAL`, which defaults to `1h` and must be at least `5m`.
Each test has a `2m` timeout by default. The exporter tests servers immediately
at startup, sequentially, then waits the configured interval after each full
cycle.

## Prometheus And Grafana

`deploy/prometheus/prometheus.yaml` shows a 30-second scrape interval and
10-second timeout against private cluster DNS target. Scraping does not trigger
a test and can finish during a test.

Import [`grafana/librespeed-dashboard.json`](grafana/librespeed-dashboard.json)
in Grafana, choose Prometheus datasource, and select server IDs. Dashboard shows
latest throughput, ping, last-success age, attempt status, throughput/latency
history, failures, transfer volumes, duration, and freshness history. Age uses
`time() - librespeed_test_last_success_timestamp_seconds`; zero timestamp before
first success appears very stale, as intended.

## Release Verification

Pin deployment to immutable image digest. Before trusting an image, obtain its
digest and verify release signature and provenance against expected repository
and workflow identity. For releases published with Cosign keyless signing and
GitHub artifact attestations, example checks are:

```sh
cosign verify \
  --certificate-identity-regexp='^https://github\.com/home-ops/librespeed-exporter/\.github/workflows/release\.yaml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+)$' \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  ghcr.io/home-ops/librespeed-exporter@sha256:<digest>
gh attestation verify oci://ghcr.io/home-ops/librespeed-exporter@sha256:<digest> \
  --repo home-ops/librespeed-exporter
```

Use actual release identity/workflow and inspect verifier output; signature
verification alone does not establish that image is safe. Confirm source commit,
SBOM, scan report, and corresponding-source CLI archive for release. These
commands require `cosign` and `gh`; artifact availability depends on release
publication configuration. A digest makes reference immutable, not trustworthy
by itself.

## Security And Limitations

See [`SECURITY.md`](SECURITY.md) for vulnerability reporting. Exporter uses
Apache-2.0; bundled LibreSpeed CLI has separate LGPL-3.0/GPL-3.0 obligations and
corresponding source notice described in [`THIRD_PARTY.md`](THIRD_PARTY.md).
Pinned `go-licenses` generates Go dependency license and copyright notices in
`LICENSES/GO-MODULES`; regenerate with:

```sh
go install github.com/google/go-licenses/v2@v2.0.1
PATH="$(go env GOPATH)/bin:$PATH" sh scripts/update-go-license-notices.sh
```

CI checks generated notices against pinned `go-licenses` v2.0.1. The exporter
module is excluded because its Apache-2.0 `LICENSE` ships separately; existing
CLI license files remain separate and are not duplicated in generated notices.

This is monitoring software, not a speed-test service or network diagnostic
suite. Results depend on selected remote server, route, host load, and test
conditions; they are not guaranteed ISP throughput measurements. No packet-loss
metric is available. Last-good values can be stale, failures are process-local,
server IDs should stay stable, and measurements have no persistence across
restarts. Web TLS/auth protects the metrics listener when configured; the
health-only probe listener remains plain HTTP. Web TLS/auth does not encrypt
outbound tests unless their configured URLs use HTTPS. Container and Kubernetes
examples require operator review and platform policy validation.

## Validating Examples

Run `sh scripts/check-deploy-assets.sh` to parse JSON/YAML and check dashboard
queries against exporter metric descriptors. Run
`sh scripts/check-deploy-assets.sh --self-test` to confirm malformed JSON and
YAML are rejected. For deployment schema checks, also run `yamllint -s` over
the YAML examples, `promtool check config` and `promtool check web-config`, and
`kubeconform -strict -summary deploy/kubernetes/` with those tools installed.
