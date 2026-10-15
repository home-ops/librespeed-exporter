package metrics

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/speedtest"
	"github.com/prometheus/client_golang/prometheus"
)

type Store interface {
	RecordAttempt(serverID int, result speedtest.Result, err error, attemptedAt time.Time)
}

type serverState struct {
	downloadBitsPerSecond       float64
	uploadBitsPerSecond         float64
	pingSeconds                 float64
	jitterSeconds               float64
	bytesSent                   float64
	bytesReceived               float64
	durationSeconds             float64
	lastAttemptTimestampSeconds float64
	lastSuccessTimestampSeconds float64
	success                     float64
	failures                    float64
}

type StateStore struct {
	mu     sync.RWMutex
	ids    []int
	states map[int]serverState
	descs  [11]*prometheus.Desc
}

var metricDefinitions = [...]struct {
	name string
	help string
}{
	{"librespeed_download_bits_per_second", "Download rate from latest successful test, in bits per second."},
	{"librespeed_upload_bits_per_second", "Upload rate from latest successful test, in bits per second."},
	{"librespeed_ping_seconds", "Ping from latest successful test, in seconds."},
	{"librespeed_jitter_seconds", "Jitter from latest successful test, in seconds."},
	{"librespeed_bytes_sent", "Bytes sent during latest successful test."},
	{"librespeed_bytes_received", "Bytes received during latest successful test."},
	{"librespeed_test_duration_seconds", "Duration of latest test attempt, in seconds."},
	{"librespeed_test_last_attempt_timestamp_seconds", "Unix timestamp of latest test attempt, in seconds."},
	{"librespeed_test_last_success_timestamp_seconds", "Unix timestamp of latest successful test, in seconds."},
	{"librespeed_test_success", "1 if latest test attempt succeeded, otherwise 0."},
	{"librespeed_test_failures_total", "Cumulative failed test attempts since process start."},
}

func NewStore(serverIDs []int) *StateStore {
	store := &StateStore{states: make(map[int]serverState, len(serverIDs))}
	for i, definition := range metricDefinitions {
		store.descs[i] = prometheus.NewDesc(definition.name, definition.help, []string{"server_id"}, nil)
	}
	for _, id := range serverIDs {
		if _, exists := store.states[id]; !exists {
			store.states[id] = serverState{}
			store.ids = append(store.ids, id)
		}
	}
	sort.Ints(store.ids)
	return store
}

func (s *StateStore) RecordAttempt(serverID int, result speedtest.Result, err error, attemptedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, exists := s.states[serverID]
	if !exists {
		return
	}
	state.durationSeconds = result.Duration.Seconds()
	state.lastAttemptTimestampSeconds = float64(attemptedAt.UnixNano()) / float64(time.Second)
	if err != nil {
		state.success = 0
		state.failures++
		s.states[serverID] = state
		return
	}
	state.downloadBitsPerSecond = result.DownloadBitsPerSecond
	state.uploadBitsPerSecond = result.UploadBitsPerSecond
	state.pingSeconds = result.PingSeconds
	state.jitterSeconds = result.JitterSeconds
	state.bytesSent = float64(result.BytesSent)
	state.bytesReceived = float64(result.BytesReceived)
	state.lastSuccessTimestampSeconds = state.lastAttemptTimestampSeconds
	state.success = 1
	s.states[serverID] = state
}

func (s *StateStore) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range s.descs {
		ch <- desc
	}
}

func (s *StateStore) Collect(ch chan<- prometheus.Metric) {
	s.mu.RLock()
	snapshot := make([]struct {
		id    int
		state serverState
	}, len(s.ids))
	for i, id := range s.ids {
		snapshot[i].id = id
		snapshot[i].state = s.states[id]
	}
	s.mu.RUnlock()
	for _, entry := range snapshot {
		id, state := entry.id, entry.state
		values := [...]float64{
			state.downloadBitsPerSecond,
			state.uploadBitsPerSecond,
			state.pingSeconds,
			state.jitterSeconds,
			state.bytesSent,
			state.bytesReceived,
			state.durationSeconds,
			state.lastAttemptTimestampSeconds,
			state.lastSuccessTimestampSeconds,
			state.success,
			state.failures,
		}
		for i, value := range values {
			metricType := prometheus.GaugeValue
			if i == len(values)-1 {
				metricType = prometheus.CounterValue
			}
			ch <- prometheus.MustNewConstMetric(s.descs[i], metricType, value, strconv.Itoa(id))
		}
	}
}
