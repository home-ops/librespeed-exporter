package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
)

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

func Load(getenv func(string) string) (Config, error) {
	if getenv == nil {
		return Config{}, fmt.Errorf("environment lookup function is nil")
	}

	config := Config{
		ServerFile:    envOrDefault(getenv, "LIBRESPEED_SERVERS_FILE", "/etc/librespeed/servers.json"),
		ListenAddress: envOrDefault(getenv, "LISTEN_ADDRESS", ":9469"),
		CLIPath:       envOrDefault(getenv, "CLI_PATH", "/usr/local/bin/librespeed-cli"),
		WebConfigFile: strings.TrimSpace(getenv("WEB_CONFIG_FILE")),
	}
	interval, err := durationFromEnv(getenv, "TEST_INTERVAL", "1h")
	if err != nil {
		return Config{}, err
	}
	if interval < 5*time.Minute {
		return Config{}, fmt.Errorf("TEST_INTERVAL must be at least 5m")
	}
	config.Interval = interval

	timeout, err := durationFromEnv(getenv, "TEST_TIMEOUT", "2m")
	if err != nil {
		return Config{}, err
	}
	if timeout <= 0 {
		return Config{}, fmt.Errorf("TEST_TIMEOUT must be positive")
	}
	config.TestTimeout = timeout

	config.Servers, err = LoadServers(config.ServerFile)
	if err != nil {
		return Config{}, err
	}
	return config, nil
}

func LoadServers(path string) ([]Server, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read server file: %w", err)
	}
	var servers []Server
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("decode server file: %w", err)
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("server file must contain at least one server")
	}

	var rawServers []map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawServers); err != nil {
		return nil, fmt.Errorf("decode server file: %w", err)
	}
	ids := make(map[int]struct{}, len(servers))
	for i, server := range servers {
		if rawID, exists := rawServers[i]["id"]; !exists || string(rawID) == "null" {
			return nil, fmt.Errorf("server at index %d is missing id", i)
		}
		if server.ID < 0 {
			return nil, fmt.Errorf("server at index %d has id %d; id must be non-negative", i, server.ID)
		}
		if _, exists := ids[server.ID]; exists {
			return nil, fmt.Errorf("server at index %d has duplicate id %d", i, server.ID)
		}
		ids[server.ID] = struct{}{}
		if err := validateServerURL(server.URL); err != nil {
			return nil, fmt.Errorf("server at index %d has invalid server: %w", i, err)
		}
		for _, endpoint := range []struct {
			name  string
			value string
		}{
			{name: "dlURL", value: server.Download},
			{name: "ulURL", value: server.Upload},
			{name: "pingURL", value: server.Ping},
			{name: "getIpURL", value: server.GetIP},
		} {
			if err := validateEndpointPath(endpoint.value); err != nil {
				return nil, fmt.Errorf("server at index %d has invalid %s: %w", i, endpoint.name, err)
			}
		}
	}
	return servers, nil
}

func validateServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("must be an absolute http or https URL with host")
	}
	return nil
}

func validateEndpointPath(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("must not be empty")
	}
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || u.Path == "" || strings.HasPrefix(raw, "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, `%\`) {
		return fmt.Errorf("must be a relative endpoint path without query or fragment")
	}
	for _, r := range u.Path {
		if unicode.IsControl(r) {
			return fmt.Errorf("must not contain control characters")
		}
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("must not contain path traversal segments")
		}
	}
	return nil
}

func envOrDefault(getenv func(string) string, key, fallback string) string {
	if value := strings.TrimSpace(getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationFromEnv(getenv func(string) string, key, fallback string) (time.Duration, error) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		value = fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}
	return duration, nil
}
