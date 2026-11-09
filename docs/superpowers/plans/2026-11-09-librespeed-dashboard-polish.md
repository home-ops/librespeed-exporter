# LibreSpeed Operations Dashboard Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the LibreSpeed Operations dashboard present one clear current snapshot per server and distinguish window estimates from latest-test history.

**Architecture:** Keep the canonical Grafana JSON and metric semantics in the upstream exporter repository. Use instant Prometheus queries aggregated by `server_id` for a compact server table, retain the KPI and history panels with clearer labels, assert the contract in the existing asset checker, and pin the final upstream commit from GitOps.

**Tech Stack:** Grafana dashboard JSON, PromQL, Python asset assertions, Go test suite, Kustomize.

**Spec:** `docs/superpowers/specs/2026-11-09-librespeed-dashboard-polish-design.md`

## Global Constraints

- Preserve exporter metrics, scheduling, metric meanings, and `$server_id` filtering.
- Keep the upstream dashboard JSON canonical; GitOps stores only its immutable URL pin.
- Show incomplete counter windows as `Waiting for counter samples`, not zero.
- Keep latest-test transfer gauges separate from selected-window counter totals.
- Use theme-aware Orchid/Blue-Purple gradients for neutral values; reserve green/red for health and failures.
- Do not claim runtime Grafana rendering unless tested against the target version.
- Leave PR merge decisions to a human; never enable auto-merge.

## Review Focus

- Duplicate scrape targets: current table and history must not duplicate server rows or labels; checker asserts aggregation by `server_id`.
- Incomplete counter coverage: window estimates remain unavailable until each selected configured server has enough samples; checker asserts waiting copy and sample caveat.
- No-attempt windows: success rate remains neutral and non-failing; checker asserts its no-value presentation and semantic color configuration.
- Selected-server filtering: all server-specific current and window queries preserve `$server_id`; checker asserts selectors.
- Latest-test versus window totals: history label and README explicitly identify sampled latest-test values; checker asserts both.

---

## File Map

- Modify `grafana/librespeed-dashboard.json`: KPI wording, compact current-server table, history layout, query aggregation, legends, and colors.
- Modify `scripts/check-deploy-assets.sh`: dashboard structure, query, label, and color assertions.
- Modify `README.md`: explain window estimates, sample coverage, and latest-test history.
- Modify `apps/base/librespeed-exporter/grafanadashboard.yaml` in the GitOps repository: pin final upstream commit SHA.

### Task 1: Clarify KPI Window Summary and Add Per-Server Snapshot Table

**Files:**

- Modify: `grafana/librespeed-dashboard.json`
- Test: `scripts/check-deploy-assets.sh`

**Interfaces:**

- Consumes: Existing Prometheus metrics, `$server_id` variable, Grafana dashboard schema.
- Produces: Window-labeled KPI row and one table row per selected server labeled `Server <server_id>`, with download, upload, ping, latest attempt status, and freshness.

- [ ] **Step 1: Add failing asset assertions** for KPI window wording and waiting behavior, the replacement snapshot table, its five fields, instant `$server_id`-filtered queries, `server_id` aggregation, and absence of pod/instance fields.
- [ ] **Step 2: Run `sh scripts/check-deploy-assets.sh`.** Expected: FAIL on missing snapshot table and revised labels.
- [ ] **Step 3: Update KPI labels and no-value states.** Label the row and its totals as selected-window estimates; retain sample-coverage and first-scrape caveats; show `Waiting for counter samples` for incomplete counters and neutral `—` when a complete window has no attempts.
- [ ] **Step 4: Replace current gauge/status/age panels with one compact table.** Use instant queries reduced to one series per `server_id`; join query results on `server_id`; expose `Server <server_id>` and the five specified values; hide scrape-target labels. Retain the Prometheus scrape-health panel separately.
- [ ] **Step 5: Run `sh scripts/check-deploy-assets.sh`.** Expected: PASS for KPI and snapshot assertions; add history-specific assertions in Task 2.

### Task 2: Simplify and Disambiguate History Panels

**Files:**

- Modify: `grafana/librespeed-dashboard.json`
- Test: `scripts/check-deploy-assets.sh`

**Interfaces:**

- Consumes: Task 1's table and KPI layout.
- Produces: Distinct selected-window KPIs and sampled latest-test trend panels, with duplicate series removed.

- [ ] **Step 1: Add failing asset assertions** for `server_id` aggregation on history queries, a latest-test transfer title/description, less chart fill, retained failure/status semantics, and neutral gradients for non-health values.
- [ ] **Step 2: Run `sh scripts/check-deploy-assets.sh`.** Expected: FAIL on unaggregated queries or ambiguous history labels.
- [ ] **Step 3: Aggregate current gauge histories by `server_id`.** Preserve metric meaning and selectors; use aggregation that prevents overlapping exporter targets from creating duplicate server series.
- [ ] **Step 4: Rename transfer history to identify latest-test sampled gauges.** Simplify legends, reduce fill, and keep green/red only for actual status, health, and failure outcomes; retain neutral Orchid/Blue-Purple gradients elsewhere.
- [ ] **Step 5: Run `sh scripts/check-deploy-assets.sh`.** Expected: PASS for dashboard structure, metric names, and assertions from Tasks 1–2.

### Task 3: Document Data Windows and Validate Upstream Assets

**Files:**

- Modify: `README.md`
- Test: `scripts/check-deploy-assets.sh`, Go test suite.

**Interfaces:**

- Consumes: Final dashboard panel labels and descriptions from Tasks 1–2.
- Produces: README guidance consistent with dashboard labels and limitations.

- [ ] **Step 1: Add failing README assertions** for selected-window estimates, successful-test-only transfer totals, two-sample coverage, first-scrape loss, and latest-test sampled history.
- [ ] **Step 2: Run `sh scripts/check-deploy-assets.sh`.** Expected: FAIL until README explains each distinction.
- [ ] **Step 3: Update the Grafana README section** to separate counter-derived window estimates from latest-test gauge history and explain incomplete coverage and startup baseline loss.
- [ ] **Step 4: Run `sh scripts/check-deploy-assets.sh` and `go test ./...`.** Expected: both PASS.

### Task 4: Pin GitOps to Final Upstream Dashboard Commit

**Files:**

- Modify: `apps/base/librespeed-exporter/grafanadashboard.yaml` in `ebCrypto/flux-kustomize-helm`.

**Test:** Rendered Kustomize output contains the exact URL `https://raw.githubusercontent.com/home-ops/librespeed-exporter/<final-sha>/grafana/librespeed-dashboard.json`.

**Interfaces:**

- Consumes: Final upstream dashboard commit SHA from Task 3.
- Produces: Immutable GitOps `GrafanaDashboard.spec.url` pointing at that exact commit.

- [ ] **Step 1: Record final upstream SHA** and set `UPSTREAM_SHA` to that value for the URL update and rendered-output check.
- [ ] **Step 2: Create an isolated GitOps worktree from current `main`.** Do not reuse the merged counter-image-pin branch.
- [ ] **Step 3: Update only the dashboard URL pin** to `https://raw.githubusercontent.com/home-ops/librespeed-exporter/<final-sha>/grafana/librespeed-dashboard.json`.
- [ ] **Step 4: Render with `kustomize build apps/base/librespeed-exporter`** and verify the output contains `https://raw.githubusercontent.com/home-ops/librespeed-exporter/${UPSTREAM_SHA}/grafana/librespeed-dashboard.json` exactly once.
- [ ] **Step 5: Create or update upstream PR first, then dependent GitOps PR.** Leave both open for human review and do not merge or enable auto-merge.
