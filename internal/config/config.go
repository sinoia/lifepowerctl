package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sinoia/lifepowerctl/internal/board"
	"github.com/sinoia/lifepowerctl/internal/jobs"
)

const MaxFileBytes = 64 << 10

type Config struct {
	ListenAddress      string                 `json:"listen_address"`
	StateDirectory     string                 `json:"state_directory"`
	ActivityDirectory  string                 `json:"activity_directory"`
	ExternalOrigin     string                 `json:"external_origin"`
	TrustedProxy       string                 `json:"trusted_proxy,omitempty"`
	AllowNonLoopback   bool                   `json:"acknowledge_non_loopback"`
	JobIdentity        jobs.ExecutionIdentity `json:"job_identity"`
	RunnerSocket       string                 `json:"runner_socket"`
	ControllerSocket   string                 `json:"controller_socket"`
	JobOutputDirectory string                 `json:"job_output_directory"`
	VendorExecutable   string                 `json:"-"`
}

func Default() Config {
	return Config{
		ListenAddress:      "127.0.0.1:8080",
		StateDirectory:     "/var/lib/lifepowerctl",
		ActivityDirectory:  "/var/lib/lifepowerctl/activity",
		ExternalOrigin:     "http://127.0.0.1:8080",
		JobIdentity:        jobs.ExecutionIdentity{User: "pi", UID: 1000, Home: "/home/pi", Path: jobs.DefaultPath},
		RunnerSocket:       "/run/lifepowerctl/jobs.sock",
		ControllerSocket:   "/run/lifepowerctl/controller.sock",
		JobOutputDirectory: "/var/lib/lifepowerctl-jobs/output",
		VendorExecutable:   board.VendorCLIPath,
	}
}

// ForRoot provides safe temporary paths for tests and development tools. It
// does not make the vendor executable configurable.
func ForRoot(root string) Config {
	value := Default()
	value.StateDirectory = filepath.Join(root, "state")
	value.ActivityDirectory = filepath.Join(root, "activity")
	return value
}

// Load overlays a strict bounded JSON file on the safe defaults. An empty path
// means no file. Every field remains validated after overlay.
func Load(path string) (Config, error) {
	value := Default()
	if path == "" {
		return value, value.Validate()
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}
	if len(data) > MaxFileBytes {
		return Config{}, fmt.Errorf("configuration exceeds %d bytes", MaxFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode configuration: trailing data")
	}
	value.VendorExecutable = board.VendorCLIPath
	if err := value.Validate(); err != nil {
		return Config{}, err
	}
	return value, nil
}

func (value Config) Validate() error {
	host, port, err := net.SplitHostPort(value.ListenAddress)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("invalid listen_address %q", value.ListenAddress)
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("listen_address host must be an IP address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("listen_address port must be between 1 and 65535")
	}
	if value.VendorExecutable != board.VendorCLIPath {
		return fmt.Errorf("vendor executable must be %s", board.VendorCLIPath)
	}
	if err := value.JobIdentity.Validate(); err != nil {
		return fmt.Errorf("invalid job_identity: %w", err)
	}
	for name, path := range map[string]string{"state_directory": value.StateDirectory, "activity_directory": value.ActivityDirectory, "runner_socket": value.RunnerSocket, "controller_socket": value.ControllerSocket, "job_output_directory": value.JobOutputDirectory} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%s must be a clean absolute path", name)
		}
	}
	origin, err := url.Parse(value.ExternalOrigin)
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return fmt.Errorf("external_origin must be an origin without path, query, user, or fragment")
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return fmt.Errorf("external_origin must use http or https")
	}
	var trustedProxy netip.Addr
	if value.TrustedProxy != "" {
		trustedProxy, err = netip.ParseAddr(value.TrustedProxy)
		if err != nil || trustedProxy.IsUnspecified() || trustedProxy.IsMulticast() {
			return fmt.Errorf("trusted_proxy must be one specific IP address")
		}
		if origin.Scheme != "https" {
			return fmt.Errorf("trusted_proxy requires an https external_origin")
		}
	}
	if address.IsLoopback() {
		if trustedProxy.IsValid() && !trustedProxy.IsLoopback() {
			return fmt.Errorf("loopback listen requires a loopback trusted_proxy")
		}
		if !originHostIsLoopback(origin.Hostname()) && !trustedProxy.IsValid() {
			return fmt.Errorf("loopback listen with an external origin requires a trusted_proxy")
		}
	} else if !value.AllowNonLoopback || origin.Scheme != "https" {
		return fmt.Errorf("non-loopback listen requires acknowledge_non_loopback and an https external_origin")
	}
	return nil
}

func originHostIsLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}
