package metrics

import (
	"errors"
	"testing"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/speedtest"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestStoreSeedsMetricContract(t *testing.T) {
	store := NewStore([]int{7, 42})
	registry := prometheus.NewRegistry()
	if err := registry.Register(store); err != nil {
		t.Fatal(err)
	}

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]dto.MetricType{
		"librespeed_download_bits_per_second":            dto.MetricType_GAUGE,
		"librespeed_upload_bits_per_second":              dto.MetricType_GAUGE,
		"librespeed_ping_seconds":                        dto.MetricType_GAUGE,
		"librespeed_jitter_seconds":                      dto.MetricType_GAUGE,
		"librespeed_bytes_sent":                          dto.MetricType_GAUGE,
		"librespeed_bytes_received":                      dto.MetricType_GAUGE,
		"librespeed_test_duration_seconds":               dto.MetricType_GAUGE,
		"librespeed_test_last_attempt_timestamp_seconds": dto.MetricType_GAUGE,
		"librespeed_test_last_success_timestamp_seconds": dto.MetricType_GAUGE,
		"librespeed_test_success":                        dto.MetricType_GAUGE,
		"librespeed_test_failures_total":                 dto.MetricType_COUNTER,
	}
	if len(families) != len(want) {
		t.Fatalf("got %d metric families, want %d", len(families), len(want))
	}
	for _, family := range families {
		name := family.GetName()
		metricType, ok := want[name]
		if !ok {
			t.Errorf("unexpected metric family %q", name)
			continue
		}
		if family.GetType() != metricType {
			t.Errorf("%s type = %s, want %s", name, family.GetType(), metricType)
		}
		if len(family.Metric) != 2 {
			t.Errorf("%s has %d series, want 2", name, len(family.Metric))
		}
		for _, metric := range family.Metric {
			if len(metric.Label) != 1 || metric.Label[0].GetName() != "server_id" {
				t.Errorf("%s labels = %v, want only server_id", name, metric.Label)
				continue
			}
			if id := metric.Label[0].GetValue(); id != "7" && id != "42" {
				t.Errorf("%s server_id = %q, want configured decimal ID", name, id)
			}
			if value := metricValue(metric, metricType); value != 0 {
				t.Errorf("%s initial value = %v, want 0", name, value)
			}
		}
	}

}

func TestStoreRecordsSuccessfulAttempt(t *testing.T) {
	store := NewStore([]int{5})
	attemptedAt := time.Unix(1_700_000_000, 0)
	store.RecordAttempt(5, speedtest.Result{
		DownloadBitsPerSecond: 10_000_000,
		UploadBitsPerSecond:   2_000_000,
		PingSeconds:           0.02,
		JitterSeconds:         0.003,
		BytesSent:             123,
		BytesReceived:         456,
		Duration:              4 * time.Second,
	}, nil, attemptedAt)

	families := gather(t, store)
	want := map[string]float64{
		"librespeed_download_bits_per_second":            10_000_000,
		"librespeed_upload_bits_per_second":              2_000_000,
		"librespeed_ping_seconds":                        0.02,
		"librespeed_jitter_seconds":                      0.003,
		"librespeed_bytes_sent":                          123,
		"librespeed_bytes_received":                      456,
		"librespeed_test_duration_seconds":               4,
		"librespeed_test_last_attempt_timestamp_seconds": float64(attemptedAt.Unix()),
		"librespeed_test_last_success_timestamp_seconds": float64(attemptedAt.Unix()),
		"librespeed_test_success":                        1,
		"librespeed_test_failures_total":                 0,
	}
	for _, family := range families {
		if got := metricValue(family.Metric[0], family.GetType()); got != want[family.GetName()] {
			t.Errorf("%s = %v, want %v", family.GetName(), got, want[family.GetName()])
		}
	}
}

func TestStoreRetainsLastGoodValuesAfterFailure(t *testing.T) {
	store := NewStore([]int{3})
	firstAttempt := time.Unix(1_700_000_000, 0)
	result := speedtest.Result{
		DownloadBitsPerSecond: 25_000_000,
		UploadBitsPerSecond:   4_000_000,
		PingSeconds:           0.012,
		JitterSeconds:         0.002,
		BytesSent:             100,
		BytesReceived:         200,
		Duration:              5 * time.Second,
	}
	store.RecordAttempt(3, result, nil, firstAttempt)
	store.RecordAttempt(3, speedtest.Result{Duration: 2 * time.Second}, errors.New("private endpoint failure"), firstAttempt.Add(time.Minute))

	families := gather(t, store)
	wantValues := map[string]float64{
		"librespeed_download_bits_per_second":            25_000_000,
		"librespeed_upload_bits_per_second":              4_000_000,
		"librespeed_ping_seconds":                        0.012,
		"librespeed_jitter_seconds":                      0.002,
		"librespeed_bytes_sent":                          100,
		"librespeed_bytes_received":                      200,
		"librespeed_test_duration_seconds":               2,
		"librespeed_test_last_attempt_timestamp_seconds": float64(firstAttempt.Add(time.Minute).Unix()),
		"librespeed_test_last_success_timestamp_seconds": float64(firstAttempt.Unix()),
		"librespeed_test_success":                        0,
		"librespeed_test_failures_total":                 1,
	}
	for _, family := range families {
		if got := metricValue(family.Metric[0], family.GetType()); got != wantValues[family.GetName()] {
			t.Errorf("%s = %v, want %v", family.GetName(), got, wantValues[family.GetName()])
		}
	}
}

func TestCollectDoesNotHoldStoreLockWhileSending(t *testing.T) {
	store := NewStore([]int{3})
	collected := make(chan prometheus.Metric)
	collectDone := make(chan struct{})
	go func() {
		store.Collect(collected)
		close(collectDone)
	}()

	<-collected
	recorded := make(chan struct{})
	go func() {
		store.RecordAttempt(3, speedtest.Result{DownloadBitsPerSecond: 10}, nil, time.Unix(1, 0))
		close(recorded)
	}()

	select {
	case <-recorded:
	case <-time.After(time.Second):
		t.Fatal("RecordAttempt blocked while Collect was blocked sending")
	}

	go func() {
		for range 10 {
			<-collected
		}
	}()
	select {
	case <-collectDone:
	case <-time.After(time.Second):
		t.Fatal("Collect did not finish after channel was drained")
	}
}

func gather(t *testing.T, collector prometheus.Collector) []*dto.MetricFamily {
	t.Helper()
	registry := prometheus.NewRegistry()
	if err := registry.Register(collector); err != nil {
		t.Fatal(err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	return families
}

func metricValue(metric *dto.Metric, metricType dto.MetricType) float64 {
	if metricType == dto.MetricType_COUNTER {
		return metric.GetCounter().GetValue()
	}
	return metric.GetGauge().GetValue()
}
