package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sinoia/lifepowerctl/internal/board"
)

func TestDefaultsAreSafe(t *testing.T) {
	value := Default()
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	if value.ListenAddress != "127.0.0.1:8080" || value.ControllerSocket != "/run/lifepowerctl/controller.sock" || value.VendorExecutable != board.VendorCLIPath {
		t.Fatalf("Default() = %+v", value)
	}
}

func TestLoadOverlaysDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "listen_address": "127.0.0.1:18081",
  "state_directory": "/tmp/lifepowerctl-state",
  "activity_directory": "/tmp/lifepowerctl-activity",
  "external_origin": "http://localhost:18081"
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if value.ListenAddress != "127.0.0.1:18081" || value.VendorExecutable != board.VendorCLIPath {
		t.Fatalf("Load() = %+v", value)
	}
}

func TestLoadRejectsUnsafeConfiguration(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "unknown field", json: `{"debug":true}`},
		{name: "trailing value", json: `{}` + "\n{}"},
		{name: "relative state", json: `{"state_directory":"state"}`},
		{name: "relative controller socket", json: `{"controller_socket":"controller.sock"}`},
		{name: "bad port", json: `{"listen_address":"127.0.0.1:service"}`},
		{name: "unacknowledged LAN", json: `{"listen_address":"0.0.0.0:8080","external_origin":"https://power.example"}`},
		{name: "plaintext LAN", json: `{"listen_address":"0.0.0.0:8080","external_origin":"http://power.example","acknowledge_non_loopback":true}`},
		{name: "untrusted origin", json: `{"external_origin":"http://power.example"}`},
		{name: "proxy without HTTPS", json: `{"trusted_proxy":"127.0.0.1"}`},
		{name: "root job identity", json: `{"job_identity":{"user":"root","uid":0,"home":"/root","path":"/usr/bin:/bin"}}`},
		{name: "relative job home", json: `{"job_identity":{"user":"pi","uid":1000,"home":"home/pi","path":"/usr/bin:/bin"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.json), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load accepted unsafe configuration")
			}
		})
	}
}

func TestLoadRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", MaxFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted oversized configuration")
	}
}

func TestNonLoopbackRequiresExplicitHTTPSAcknowledgement(t *testing.T) {
	value := Default()
	value.ListenAddress = "0.0.0.0:8080"
	value.ExternalOrigin = "https://power.example"
	value.AllowNonLoopback = true
	value.TrustedProxy = "192.0.2.10"
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoopbackListenerAcceptsExplicitHTTPSProxy(t *testing.T) {
	value := Default()
	value.ExternalOrigin = "https://power.example:8443"
	value.TrustedProxy = "127.0.0.1"
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}

	value.TrustedProxy = "192.0.2.10"
	if err := value.Validate(); err == nil || !strings.Contains(err.Error(), "loopback trusted_proxy") {
		t.Fatalf("non-loopback proxy error = %v", err)
	}
}

func TestForRootKeepsFixedVendorExecutable(t *testing.T) {
	root := t.TempDir()
	value := ForRoot(root)
	if !strings.HasPrefix(value.StateDirectory, root) || !strings.HasPrefix(value.ActivityDirectory, root) || value.VendorExecutable != board.VendorCLIPath {
		t.Fatalf("ForRoot() = %+v", value)
	}
}
