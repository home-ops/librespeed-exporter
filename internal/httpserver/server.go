package httpserver

import (
	"log/slog"
	"net"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/exporter-toolkit/web"
)

func NewRegistry(stateStore prometheus.Collector) *prometheus.Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(stateStore, collectors.NewGoCollector())
	return registry
}

func NewHandler(registry *prometheus.Registry, ready func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	registerHealthHandlers(mux, ready)
	return mux
}

func NewProbeHandler(ready func() bool) http.Handler {
	mux := http.NewServeMux()
	registerHealthHandlers(mux, ready)
	return mux
}

func registerHealthHandlers(mux *http.ServeMux, ready func() bool) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if ready == nil || !ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
}

func Serve(listener net.Listener, server *http.Server, webConfigFile string, logger *slog.Logger) error {
	if webConfigFile == "" {
		return server.Serve(listener)
	}
	addresses := []string{listener.Addr().String()}
	systemdSocket := false
	return web.Serve(listener, server, &web.FlagConfig{
		WebListenAddresses: &addresses,
		WebSystemdSocket:   &systemdSocket,
		WebConfigFile:      &webConfigFile,
	}, logger)
}
