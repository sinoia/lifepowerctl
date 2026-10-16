package board

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/sinoia/lifepowerctl/internal/limits"
)

type recordingRunner struct {
	path      string
	arguments []string
	result    commandResult
	err       error
}

func (runner *recordingRunner) Run(_ context.Context, path string, arguments []string, _ limits.Command) (commandResult, error) {
	runner.path = path
	runner.arguments = append([]string(nil), arguments...)
	return runner.result, runner.err
}

func TestVendorDetectorUsesOnlyFixedIdentityCommand(t *testing.T) {
	runner := &recordingRunner{result: commandResult{stdout: []byte("7\n")}}
	detector := newVendorDetector(runner, limits.Default().Vendor)
	identity, err := detector.Detect(context.Background())
	if err != nil || identity.RegisterSet != 7 {
		t.Fatalf("Detect() = %+v, %v", identity, err)
	}
	if runner.path != VendorCLIPath || !reflect.DeepEqual(runner.arguments, []string{"GET", "I2C_REG_VER"}) {
		t.Fatalf("command = %q %q", runner.path, runner.arguments)
	}
}

func TestVendorDetectorPreservesTypedFailures(t *testing.T) {
	runner := &recordingRunner{result: commandResult{stdout: []byte("8\n")}}
	detector := newVendorDetector(runner, limits.Default().Vendor)
	identity, err := detector.Detect(context.Background())
	if !errors.Is(err, ErrUnsupportedRegisterSet) || identity.RegisterSet != 8 {
		t.Fatalf("unsupported Detect() = %+v, %v", identity, err)
	}
	runner.err = ErrVendorTimeout
	if _, err := detector.Detect(context.Background()); !errors.Is(err, ErrVendorTimeout) {
		t.Fatalf("timeout Detect() error = %v", err)
	}
}

func TestExecRunnerBoundsFailureModes(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner := execRunner{}
	base := limits.Command{Timeout: 2 * time.Second, MaxStdoutBytes: 4, MaxStderrBytes: 4}
	tests := []struct {
		mode   string
		limits limits.Command
		want   error
	}{
		{mode: "output", limits: base, want: ErrVendorOutputLimit},
		{mode: "stderr", limits: base, want: ErrVendorOutputLimit},
		{mode: "failure", limits: base, want: ErrVendorCommand},
		{mode: "sleep", limits: limits.Command{Timeout: 50 * time.Millisecond, MaxStdoutBytes: 4, MaxStderrBytes: 4}, want: ErrVendorTimeout},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			_, err := runner.Run(context.Background(), executable, []string{"-test.run=TestVendorExecHelper", "--", test.mode}, test.limits)
			if !errors.Is(err, test.want) {
				t.Fatalf("Run() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestVendorExecHelper(t *testing.T) {
	mode := ""
	for index, argument := range os.Args {
		if argument == "--" && index+1 < len(os.Args) {
			mode = os.Args[index+1]
			break
		}
	}
	switch mode {
	case "output":
		fmt.Print("123456789")
	case "stderr":
		_, _ = fmt.Fprint(os.Stderr, "123456789")
	case "failure":
		os.Exit(6)
	case "sleep":
		time.Sleep(2 * time.Second)
	}
}

func TestVendorDetectorLive(t *testing.T) {
	if os.Getenv("LIFEPOWERCTL_TEST_LIVE_VENDOR") != "1" {
		t.Skip("set LIFEPOWERCTL_TEST_LIVE_VENDOR=1 on a reviewed read-only hardware target")
	}
	detector, err := NewVendorDetector(limits.Default().Vendor)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if identity.RegisterSet != 7 {
		t.Fatalf("live identity = %+v", identity)
	}
}
