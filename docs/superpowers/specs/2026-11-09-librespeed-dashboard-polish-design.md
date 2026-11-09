# LibreSpeed Operations Dashboard Polish Design

## Intent

Make the LibreSpeed Operations dashboard easier to scan and less confusing in Grafana light and dark themes. Show one current snapshot per server, distinguish selected-window totals from latest-test measurements, and make missing or incomplete data explicit.

## User Goals

- Scan current download, upload, ping, latest attempt status, and freshness for each server in one compact table.
- Read test and transfer totals as selected-window estimates, not current measurements.
- Recognize incomplete counter history without mistaking missing data for zero or failure.
- Keep useful performance trends and operational health information below the current snapshot.

## Scope

- Redesign `grafana/librespeed-dashboard.json` in `home-ops/librespeed-exporter`.
- Update dashboard assertions in `scripts/check-deploy-assets.sh` and dashboard guidance in `README.md`.
- After the upstream dashboard commit exists, update the GitOps `GrafanaDashboard.spec.url` pin in `ebCrypto/flux-kustomize-helm` to that immutable commit.
- Preserve dashboard variables, exporter metric meanings, and server selection behavior.

## Non-Goals

- Do not change exporter metrics, test scheduling, or measurement semantics.
- Do not add a mockup, Grafana plugin, custom palette, or persistent event history.
- Do not claim runtime Grafana rendering unless tested against the target Grafana version.
- Do not merge either pull request; leave merge decisions to a human.

## Dashboard Behavior

- Keep the window KPI row, but label it as a selected-window summary. Keep its tested/configured server context, attempts, successes, failures, success rate, and successful-test transfer estimates.
- Show `Waiting for counter samples` while selected configured servers lack at least two samples for every required counter. Explain counter rollout history, first-scrape startup loss, `increase()` range-edge extrapolation, and rounded estimates.
- Show success rate as a neutral em dash when the selected window has no attempts. Do not color missing data as a failure.
- Replace the duplicated large current download, upload, ping, latest-status, and age panels with one compact table containing one row per `server_id`.
- Aggregate current metrics by `server_id` so overlapping exporter replicas or scrape-target labels do not duplicate rows. Label rows `Server <server_id>` and hide pod/instance labels; exporter metrics do not expose server names.
- Keep scrape health, performance trends, failure history, and freshness information where useful. Reduce chart fill and label latest-test transfer history distinctly from window totals.
- Use Orchid/Blue-Purple theme-aware gradients for neutral performance and volume values. Reserve green/red for actual status, health, and failure semantics.

## Documentation and GitOps

- README identifies selected-window totals as estimates and latest-test transfer charts as sampled gauge history.
- Keep upstream dashboard JSON canonical. Pin GitOps to the final upstream commit SHA; do not copy dashboard JSON into GitOps.
- Create upstream and dependent GitOps PRs for human review. Keep all merge actions human-only.

## Verification

- Asset checks assert dashboard structure, one-row-per-server query aggregation, selected-window labels and caveats, semantic colors, preserved variables, and known metric names.
- Run `go test ./...` and `sh scripts/check-deploy-assets.sh` in the upstream repository.
- Render the GitOps Kustomize target and verify its URL points to the exact final upstream commit.
- Runtime Grafana rendering remains unverified unless imported into the target Grafana version.
