package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const validServer = `{"id":1,"name":"Example","server":"https://speed.invalid","dlURL":"garbage.php","ulURL":"empty.php","pingURL":"empty.php","getIpURL":"getIP.php"}`

func TestLoadServers(t *testing.T) {
	tests := []struct {
		name    string
		content string
		missing bool
		want    int
		wantErr string
	}{
		{name: "missing file", missing: true, wantErr: "read"},
		{name: "malformed JSON", content: "[", wantErr: "decode"},
		{name: "empty list", content: "[]", wantErr: "at least one"},
		{name: "missing ID", content: "[" + strings.Replace(validServer, `"id":1,`, "", 1) + "]", wantErr: "id"},
		{name: "null ID", content: "[" + strings.Replace(validServer, `"id":1`, `"id":null`, 1) + "]", wantErr: "id"},
		{name: "negative ID", content: "[" + strings.Replace(validServer, `"id":1`, `"id":-1`, 1) + "]", wantErr: "non-negative"},
		{name: "duplicate IDs", content: "[" + validServer + "," + strings.Replace(validServer, `"id":1`, `"id":1,"dlURL":"other.php"`, 1) + "]", wantErr: "duplicate"},
		{name: "invalid server scheme", content: "[" + strings.Replace(validServer, "https://speed.invalid", "ftp://speed.invalid", 1) + "]", wantErr: "server"},
		{name: "server URL without host", content: "[" + strings.Replace(validServer, "https://speed.invalid", "https:///path", 1) + "]", wantErr: "server"},
		{name: "server URL with empty hostname", content: "[" + strings.Replace(validServer, "https://speed.invalid", "http://:80/path", 1) + "]", wantErr: "server"},
		{name: "malformed server URL", content: "[" + strings.Replace(validServer, "https://speed.invalid", "https://[::1", 1) + "]", wantErr: "server"},
		{name: "missing server endpoint", content: "[" + strings.Replace(validServer, `"server":"https://speed.invalid"`, `"server":""`, 1) + "]", wantErr: "server"},
		{name: "missing download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":""`, 1) + "]", wantErr: "dlURL"},
		{name: "absolute download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"https://other.invalid/garbage.php"`, 1) + "]", wantErr: "server at index 0 has invalid dlURL"},
		{name: "rooted download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"/garbage.php"`, 1) + "]", wantErr: "dlURL"},
		{name: "traversal download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"../garbage.php"`, 1) + "]", wantErr: "dlURL"},
		{name: "double-encoded traversal download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"%252e%252e/backend/empty.php"`, 1) + "]", wantErr: "dlURL"},
		{name: "percent-encoded download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"backend/%65mpty.php"`, 1) + "]", wantErr: "dlURL"},
		{name: "query in download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"garbage.php?x=1"`, 1) + "]", wantErr: "dlURL"},
		{name: "control character in download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"garbage\n.php"`, 1) + "]", wantErr: "dlURL"},
		{name: "backslash in download endpoint", content: "[" + strings.Replace(validServer, `"dlURL":"garbage.php"`, `"dlURL":"..\\\\garbage.php"`, 1) + "]", wantErr: "dlURL"},
		{name: "single server", content: "[" + validServer + `]`, want: 1},
		{name: "zero ID", content: "[" + strings.Replace(validServer, `"id":1`, `"id":0`, 1) + `]`, want: 1},
		{name: "multiple servers with nested relative paths and extra fields", content: "[" + validServer + "," + strings.Replace(strings.Replace(validServer, `"id":1`, `"id":2,"futureField":true`, 1), `"dlURL":"garbage.php"`, `"dlURL":"backend/garbage.php"`, 1) + `]`, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "servers.json")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0600); err != nil {
					t.Fatal(err)
				}
			}

			servers, err := LoadServers(path)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("LoadServers() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadServers() error = %v", err)
			}
			if len(servers) != tt.want {
				t.Fatalf("LoadServers() returned %d servers, want %d", len(servers), tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantErr   string
		wantValue Config
	}{
		{
			name: "documented defaults",
			wantValue: Config{
				Servers:       []Server{{ID: 1, Name: "Example", URL: "https://speed.invalid", Download: "garbage.php", Upload: "empty.php", Ping: "empty.php", GetIP: "getIP.php"}},
				ServerFile:    "/etc/librespeed/servers.json",
				ListenAddress: ":9469",
				Interval:      time.Hour,
				TestTimeout:   2 * time.Minute,
				CLIPath:       "/usr/local/bin/librespeed-cli",
			},
		},
		{name: "invalid interval", env: map[string]string{"TEST_INTERVAL": "soon"}, wantErr: "TEST_INTERVAL"},
		{name: "interval below minimum", env: map[string]string{"TEST_INTERVAL": "4m"}, wantErr: "5m"},
		{name: "invalid timeout", env: map[string]string{"TEST_TIMEOUT": "nope"}, wantErr: "TEST_TIMEOUT"},
		{name: "zero timeout", env: map[string]string{"TEST_TIMEOUT": "0s"}, wantErr: "positive"},
		{name: "negative timeout", env: map[string]string{"TEST_TIMEOUT": "-1s"}, wantErr: "positive"},
		{
			name: "environment overrides",
			env: map[string]string{
				"LISTEN_ADDRESS": ":9555", "TEST_INTERVAL": "5m", "TEST_TIMEOUT": "30s", "CLI_PATH": "/opt/cli",
				"WEB_CONFIG_FILE": "/etc/prometheus/web.yaml",
			},
			wantValue: Config{ListenAddress: ":9555", Interval: 5 * time.Minute, TestTimeout: 30 * time.Second, CLIPath: "/opt/cli", WebConfigFile: "/etc/prometheus/web.yaml"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverPath := filepath.Join(t.TempDir(), "servers.json")
			if err := os.WriteFile(serverPath, []byte("["+validServer+"]"), 0600); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{"LIBRESPEED_SERVERS_FILE": serverPath}
			for key, value := range tt.env {
				env[key] = value
			}

			got, err := Load(func(key string) string { return env[key] })
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if tt.name == "documented defaults" {
				tt.wantValue.ServerFile = serverPath
				if !reflect.DeepEqual(got, tt.wantValue) {
					t.Fatalf("Load() = %#v, want %#v", got, tt.wantValue)
				}
				return
			}
			if got.ListenAddress != tt.wantValue.ListenAddress || got.Interval != tt.wantValue.Interval || got.TestTimeout != tt.wantValue.TestTimeout || got.CLIPath != tt.wantValue.CLIPath || got.WebConfigFile != tt.wantValue.WebConfigFile {
				t.Fatalf("Load() overrides = %#v, want %#v", got, tt.wantValue)
			}
		})
	}
}

func TestLoadUsesDefaultServerFile(t *testing.T) {
	_, err := Load(func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "/etc/librespeed/servers.json") {
		t.Fatalf("Load() error = %v, want default server file path", err)
	}
}

func TestLoadUsesConfiguredServerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom-servers.json")
	if err := os.WriteFile(path, []byte("["+validServer+"]"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(func(key string) string {
		if key == "LIBRESPEED_SERVERS_FILE" {
			return path
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ServerFile != path || len(got.Servers) != 1 {
		t.Fatalf("Load() server file = %q with %d servers, want %q with 1 server", got.ServerFile, len(got.Servers), path)
	}
}
