package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHardwareGateVendorProxyFaultsAreOneShot(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	proxy := filepath.Join(filepath.Dir(file), "..", "..", "tools", "hardware-gate", "vendor-cli-proxy.sh")
	root := t.TempDir()
	state := filepath.Join(root, "state")
	if err := os.Mkdir(state, 0o700); err != nil {
		t.Fatal(err)
	}
	realCLI := filepath.Join(root, "real-cli")
	fake := `#!/bin/sh
set -eu
value_file=$LIFEPOWERCTL_FAKE_VALUE
case "$1:$2" in
SET:RTC_WAKE_TIME) echo "$3" >"$value_file" ;;
GET:RTC_WAKE_TIME) cat "$value_file" ;;
*) echo 0 ;;
esac
`
	if err := os.WriteFile(realCLI, []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	valueFile := filepath.Join(root, "value")
	if err := os.WriteFile(valueFile, []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(arguments ...string) (string, error) {
		command := exec.Command("/bin/sh", append([]string{proxy}, arguments...)...)
		command.Env = append(os.Environ(), "LIFEPOWERCTL_GATE_REAL_CLI="+realCLI, "LIFEPOWERCTL_GATE_STATE_DIR="+state, "LIFEPOWERCTL_FAKE_VALUE="+valueFile)
		output, err := command.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	setMode := func(mode string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(state, "mode"), []byte(mode+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	setMode("fail-absolute-write")
	if output, err := run("SET", "RTC_WAKE_TIME", "123"); err == nil || !strings.Contains(output, "injected") {
		t.Fatalf("write failure = %q, %v", output, err)
	}
	if output, err := run("SET", "RTC_WAKE_TIME", "123"); err != nil || output != "" {
		t.Fatalf("one-shot write = %q, %v", output, err)
	}

	setMode("mismatch-absolute-read")
	if output, err := run("SET", "RTC_WAKE_TIME", "456"); err != nil || output != "" {
		t.Fatalf("mismatch setup = %q, %v", output, err)
	}
	if output, err := run("GET", "RTC_WAKE_TIME"); err != nil || output != "457" {
		t.Fatalf("mismatch read = %q, %v", output, err)
	}
	if output, err := run("GET", "RTC_WAKE_TIME"); err != nil || output != "456" {
		t.Fatalf("one-shot read = %q, %v", output, err)
	}
	if _, err := run("SET", "RTC_WAKE_TIME", "0"); err != nil {
		t.Fatal(err)
	}
}
