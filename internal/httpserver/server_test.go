package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-ops/librespeed-exporter/internal/metrics"
	"github.com/home-ops/librespeed-exporter/internal/speedtest"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/exporter-toolkit/web"
	"golang.org/x/crypto/bcrypt"
)

func TestHandlerServesMetricsAndHealthRoutes(t *testing.T) {
	store := metrics.NewStore([]int{7})
	registry := prometheus.NewRegistry()
	registry.MustRegister(store)
	ready := false
	handler := NewHandler(registry, func() bool { return ready })

	for _, test := range []struct {
		path       string
		statusCode int
		body       string
	}{
		{path: "/healthz", statusCode: http.StatusOK, body: "ok"},
		{path: "/readyz", statusCode: http.StatusServiceUnavailable, body: "not ready"},
		{path: "/metrics", statusCode: http.StatusOK, body: "librespeed_test_success{server_id=\"7\"} 0"},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.statusCode {
				t.Fatalf("status = %d, want %d", response.Code, test.statusCode)
			}
			if !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("body %q does not contain %q", response.Body.String(), test.body)
			}
		})
	}

	ready = true
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestProbeHandlerServesOnlyHealthAndReadiness(t *testing.T) {
	ready := false
	handler := NewProbeHandler(func() bool { return ready })

	for _, test := range []struct {
		path       string
		statusCode int
	}{
		{path: "/healthz", statusCode: http.StatusOK},
		{path: "/readyz", statusCode: http.StatusServiceUnavailable},
		{path: "/metrics", statusCode: http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.statusCode {
			t.Errorf("%s status = %d, want %d", test.path, response.Code, test.statusCode)
		}
	}

	ready = true
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestHealthRemainsSuccessfulWhenTestAttemptFails(t *testing.T) {
	store := metrics.NewStore([]int{3})
	store.RecordAttempt(3, speedtest.Result{}, errors.New("backend unavailable"), time.Now())
	registry := prometheus.NewRegistry()
	registry.MustRegister(store)
	ready := true
	handler := NewHandler(registry, func() bool { return ready })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ready status after failed attempt = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestMetricsScrapeDoesNotWaitForConcurrentAttempts(t *testing.T) {
	store := metrics.NewStore([]int{1})
	registry := prometheus.NewRegistry()
	registry.MustRegister(store)
	handler := NewHandler(registry, func() bool { return true })
	updated := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			store.RecordAttempt(1, speedtest.Result{}, nil, time.Now())
		}
		close(updated)
	}()

	type scrapeResult struct {
		status int
		err    error
	}
	scraped := make(chan scrapeResult, 1)
	go func() {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		_, err := io.Copy(io.Discard, response.Body)
		scraped <- scrapeResult{status: response.Code, err: err}
	}()
	select {
	case result := <-scraped:
		if result.status != http.StatusOK {
			t.Fatalf("metrics status = %d, want %d", result.status, http.StatusOK)
		}
		if result.err != nil {
			t.Fatalf("read metrics response: %v", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("metrics scrape blocked on concurrent state updates")
	}
	<-updated
}

func TestServeEnforcesToolkitBasicAuthAndShutsDown(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "web.yml")
	hash, err := bcrypt.GenerateFromPassword([]byte("test"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	config := "basic_auth_users:\n  test: " + string(hash) + "\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := web.Validate(configPath); err != nil {
		t.Fatalf("validate toolkit config: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: NewHandler(prometheus.NewRegistry(), func() bool { return true })}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- Serve(listener, server, configPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	client := &http.Client{Timeout: time.Second}
	url := "http://" + listener.Addr().String() + "/healthz"
	var response *http.Response
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		response, err = client.Get(url)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("request without credentials: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("test", "test")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("authenticated request: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		if closeErr := server.Close(); closeErr != nil {
			t.Fatalf("shutdown HTTP server: %v; close: %v", err, closeErr)
		}
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close listener: %v", err)
	}
	if err := <-serveResult; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve error = %v, want http.ErrServerClosed", err)
	}
}

func TestServeUsesToolkitTLSAndBasicAuth(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeTestCertificate(t, dir)
	hash, err := bcrypt.GenerateFromPassword([]byte("test"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "web.yml")
	config := "tls_server_config:\n  cert_file: " + certPath + "\n  key_file: " + keyPath + "\nbasic_auth_users:\n  test: " + string(hash) + "\n"
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := web.Validate(configPath); err != nil {
		t.Fatalf("validate toolkit TLS config: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: NewHandler(prometheus.NewRegistry(), func() bool { return true })}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- Serve(listener, server, configPath, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	client := &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: true, // Test certificate is self-signed.
		}},
	}
	defer client.CloseIdleConnections()
	url := "https://" + listener.Addr().String() + "/healthz"
	var response *http.Response
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		response, err = client.Get(url)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("TLS request without credentials: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated TLS status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("test", "test")
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("authenticated TLS request: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated TLS status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown TLS server: %v", err)
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatalf("close TLS listener: %v", err)
	}
	if err := <-serveResult; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("TLS serve error = %v, want http.ErrServerClosed", err)
	}
}

func writeTestCertificate(t *testing.T, directory string) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(directory, "cert.pem")
	keyPath := filepath.Join(directory, "key.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(certPath, cert, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, privateKey, 0600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestNewRegistryIncludesStateAndGoCollectors(t *testing.T) {
	registry := NewRegistry(metrics.NewStore([]int{2}))
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	seen := make(map[string]bool, len(families))
	for _, family := range families {
		seen[family.GetName()] = true
	}
	if !seen["librespeed_test_success"] || !seen["go_goroutines"] {
		t.Fatalf("expected exporter and Go metrics, got %v", seen)
	}
}
