# LibreSpeed Windowed KPI Dashboard Design

## Intent

Add trustworthy, time-windowed summary numbers to the LibreSpeed Operations
dashboard. Use cumulative exporter metrics for attempts and transferred bytes;
do not estimate totals from scrape-sampled latest-value gauges.

## User Goals

- Show how many distinct configured servers had a test attempt in the selected
  dashboard window.
- Show attempts, success rate, uploaded bytes, and downloaded bytes for that
  window.
- Keep dashboard colors polished in both Grafana themes, using the selected
  Orchid direction.

## Scope

- Update `home-ops/librespeed-exporter` metrics, tests, metric documentation,
  and canonical dashboard JSON.
- Add a compact KPI row above existing panels.
- Update the GitOps `GrafanaDashboard.spec.url` pin to the final upstream
  commit. Keep server configuration local and dashboard JSON upstream-only.
- Preserve existing dashboard panels, `server_id` filtering, and operational
  meanings.

## Non-Goals

- Do not add a per-test event log, persistent database, or new server discovery
  behavior.
- Do not change test scheduling, existing measurement semantics, or panel
  queries beyond KPI additions and color treatment.
- Do not claim exact bytes for failed tests. The CLI result provides no usable
  byte totals on failed attempts.
- Do not add percentile latency claims: current metrics retain latest
  measurements, not per-test histograms.

## KPI Definitions

All KPIs follow the selected Grafana time range and selected `server_id` values.

1. **Servers tested:** distinct server IDs with attempts in the selected range.
   Show tested count against configured count, and show zero when no servers
   were tested. Derive configured count from `librespeed_test_success`, emitted
   for every configured server before its first test. Filter numerator and
   denominator by selected `server_id` values.
2. **Test attempts:** all completed attempts in the selected range, with
   successful and failed counts as supporting values.
3. **Success rate:** successful attempts divided by all attempts; show no value
   when there were no attempts.
4. **Data uploaded:** bytes sent by successful tests in the selected range.
5. **Data downloaded:** bytes received by successful tests in the selected
   range.

Byte cards count successful-test transfer only. Failed attempts may transfer
partial data, but exporter cannot report those bytes. Prometheus `increase()`
extrapolates at range boundaries; rounded totals are estimates. Window-derived
KPIs show no value until every selected configured server has at least two
counter samples; configured-server count remains available. An attempt completed
before the first scrape after startup may be missed. Counter results become
complete only after metrics accumulate data; earlier history cannot be
reconstructed.

## Exporter Metric Contract

Keep existing metrics and add counters, each labeled only by bounded
`server_id`:

- `librespeed_test_attempts_total`: increment once for every completed attempt,
  success or failure.
- `librespeed_test_successes_total`: increment once for each successful attempt.
- `librespeed_bytes_sent_total`: increment by bytes sent during each successful
  test.
- `librespeed_bytes_received_total`: increment by bytes received during each
  successful test.

Continue exposing existing `librespeed_test_failures_total`. Preserve
latest-value `librespeed_bytes_sent` and `librespeed_bytes_received` gauges for
compatibility. Use Prometheus `increase()` over the selected range; it handles
observed counter resets. Round count and byte cards for display. Keep attempt,
success, failure, and transfer updates atomic under the metrics store lock.

## Dashboard Appearance

- Use a concise KPI row above existing performance panels.
- Use Grafana's theme-aware Blue-Purple continuous scheme as native Orchid-like
  gradient for throughput values and history fills. Mockup hex colors are
  illustrative; do not add a plugin or unsupported custom palette mechanism.
- Use the theme-aware Blue-Purple scheme for transfer-volume cards; never use
  threshold colors for volume values.
- Keep surfaces neutral and low-contrast in both light and dark themes.
- Use red below 80%, yellow from 80% to below 95%, and green at 95% or above
  for success rate. Use green at zero failures and red for one or more failures.
- Keep ping thresholds and status/failure colors semantic. Gradients must not
  imply health or reverse lower-is-better latency meaning. Preserve existing
  status mappings and thresholds.
- Preserve existing layout and `server_id` selector except for space needed by
  the KPI row.

## Verification

- Unit tests prove attempts increment on success and failure, successes
  increment only on success, and cumulative bytes increment only on success.
- Metric descriptor tests verify counter types, names, bounded labels, and
  unchanged existing metrics.
- Dashboard JSON checks verify KPI PromQL uses the selected range, selected
  servers, and new counters; semantic thresholds/mappings and existing
  panels/selectors remain.
- Run exporter tests and deploy-asset validation. Render GitOps Kustomize and
  verify the pinned upstream URL after final commit.
- Do not claim runtime Grafana rendering unless tested against the target
  version.

## Delivery

PR #7 and PR #1107 merged during implementation. Create a new upstream PR for
exporter, tests, docs, and dashboard, then a dependent GitOps draft PR with the
final immutable dashboard pin. The current deployment image predates the new
counters. Keep the GitOps PR draft until a counter-enabled image is published
and its immutable digest is pinned. Keep both PRs open for human review; merge
upstream before GitOps.
