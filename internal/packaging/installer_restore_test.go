package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerClassifiesClosedPreservedRecord(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	root := t.TempDir()
	paths := recoveryHarnessPaths(root)
	if err := os.Mkdir(paths.metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	oldCommit := strings.Repeat("1", 40)
	record := "format=1\nversion=v0.0.9\ncommit=" + oldCommit + "\njob_user=tester\nweb=on-demand\nstate_schema=1\n"
	if err := os.WriteFile(filepath.Join(paths.metadata, "preserved"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	script := prefix + recoveryHarnessOverrides(paths) + `
allow_downgrade=0
load_installation_record
printf '%s\n' "$installation_action|$installed_version|$installed_commit|$installed_job_user|$installed_web|$preserved_state_schema"
`
	path := filepath.Join(t.TempDir(), "classify-preserved.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", path)
	command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, "", writeRecoveryTools(t), "")...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("classification: %v\n%s", err, output)
	}
	want := "restore|v0.0.9|" + oldCommit + "|tester|on-demand|1\n"
	if string(output) != want {
		t.Fatalf("classification = %q, want %q", output, want)
	}
}

func TestPreservedDataReinstallSuccessAndCaughtRollback(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name, failure string
	}{
		{name: "success"},
		{name: "prepared", failure: "prepared"},
		{name: "payload", failure: "payload"},
		{name: "units", failure: "units"},
		{name: "started", failure: "started"},
		{name: "healthy", failure: "healthy"},
		{name: "committed", failure: "committed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPreservedFixture(t)
			hook := `failure_point() { :; }`
			if test.failure != "" {
				hook = `failure_point() { [ "$1" != "` + test.failure + `" ]; }`
			}
			harness := strings.Replace(prefix, "failure_point() { :; }", hook, 1) + restoreHarnessBody(fixture) + `
trap 'cleanup $?' EXIT
trap 'cleanup 1' HUP INT TERM
mutate_restore
`
			output, err := runRestoreHarness(t, fixture, harness, test.failure)
			if test.failure == "" {
				if err != nil {
					t.Fatalf("restore: %v\n%s", err, output)
				}
				assertRestoredInstall(t, fixture)
			} else {
				if err == nil || !strings.Contains(string(output), "Preserved-data reinstall rollback completed") {
					t.Fatalf("caught failure did not roll back: %v\n%s", err, output)
				}
				assertPreservedUninstalled(t, fixture)
			}
		})
	}
}

func TestPreservedDataReinstallAbruptRecoveryAtEveryBoundary(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	for _, phase := range []string{"prepared", "payload", "units", "started", "healthy", "committed"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newPreservedFixture(t)
			hook := `failure_point() { [ "$1" != "$TEST_PHASE" ] || kill -KILL "$$"; }`
			interrupt := strings.Replace(prefix, "failure_point() { :; }", hook, 1) + restoreHarnessBody(fixture) + `
trap 'cleanup $?' EXIT
trap 'cleanup 1' HUP INT TERM
mutate_restore
`
			if output, err := runRestoreHarness(t, fixture, interrupt, phase); err == nil {
				t.Fatalf("interruption unexpectedly succeeded:\n%s", output)
			}
			marker, err := os.ReadFile(filepath.Join(fixture.paths.metadata, "transaction"))
			if err != nil || !strings.Contains(string(marker), "phase="+phase+"\n") {
				t.Fatalf("transaction after interruption: %v\n%s", err, marker)
			}
			recoverScript := prefix + recoveryHarnessOverrides(fixture.paths) + restoreValidationOverrides() + "\nrecover_install\n"
			output, err := runRestoreHarness(t, fixture, recoverScript, phase)
			if err != nil || !strings.Contains(string(output), "Interrupted preserved-data reinstall recovered") {
				t.Fatalf("explicit recovery: %v\n%s", err, output)
			}
			assertPreservedUninstalled(t, fixture)
		})
	}
}

func newPreservedFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	fixture := newLifecycleFixture(t)
	for _, path := range []string{fixture.paths.install, fixture.paths.runtime, fixture.paths.tmpfiles, fixture.paths.sudoers,
		filepath.Join(fixture.paths.systemd, "lifepowerctl-controller.service"), filepath.Join(fixture.paths.systemd, "lifepowerctl-job-runner.service"),
		filepath.Join(fixture.paths.systemd, "lifepowerctl-poweroff.service"), filepath.Join(fixture.paths.systemd, "lifepowerctl.service")} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(fixture.paths.metadata, "installed")); err != nil {
		t.Fatal(err)
	}
	oldCommit := strings.Repeat("1", 40)
	record := "format=1\nversion=v0.0.9\ncommit=" + oldCommit + "\njob_user=tester\nweb=enabled\nstate_schema=1\n"
	if err := os.WriteFile(filepath.Join(fixture.paths.metadata, "preserved"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func restoreHarnessBody(fixture lifecycleFixture) string {
	return recoveryHarnessOverrides(fixture.paths) + restoreValidationOverrides() + `
job_user=tester
job_uid=1000
job_gid=1000
job_group=tester
job_home=/tmp
controller_uid="$TEST_UID"
web_lifecycle=enabled
installed_version=v0.0.9
installed_commit=1111111111111111111111111111111111111111
preserved_state_schema=1
minimum_schema=1
state_schema=1
work_directory="$TEST_WORK"
payload="$TEST_PAYLOAD"
install_complete=0
rollback_authorized=0
rollback_kind=''
lifecycle_backup_started=0
`
}

func restoreValidationOverrides() string {
	return `
require_owned_directory() { [ -d "$1" ] && [ ! -L "$1" ] || fail "fixture directory is unsafe: $1"; }
require_owned_file() { [ -f "$1" ] && [ ! -L "$1" ] || fail "fixture file is unsafe: $1"; }
`
}

func runRestoreHarness(t *testing.T, fixture lifecycleFixture, script, phase string) ([]byte, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "restore.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", path)
	command.Env = append(os.Environ(), fixture.environment(phase)...)
	return command.CombinedOutput()
}

func assertRestoredInstall(t *testing.T, fixture lifecycleFixture) {
	t.Helper()
	assertLifecycleFixture(t, fixture, testReleaseVersion, testReleaseCommit)
	entries, err := os.ReadDir(fixture.paths.metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "installed" {
		t.Fatalf("restored install metadata: %v", entries)
	}
}

func assertPreservedUninstalled(t *testing.T, fixture lifecycleFixture) {
	t.Helper()
	for _, path := range []string{fixture.paths.install, fixture.paths.runtime, fixture.paths.tmpfiles, fixture.paths.sudoers,
		filepath.Join(fixture.paths.systemd, "lifepowerctl-controller.service"), filepath.Join(fixture.paths.systemd, "lifepowerctl-job-runner.service"),
		filepath.Join(fixture.paths.systemd, "lifepowerctl-poweroff.service"), filepath.Join(fixture.paths.systemd, "lifepowerctl.service")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("rollback retained package path %s: %v", path, err)
		}
	}
	record := readTestFile(t, filepath.Join(fixture.paths.metadata, "preserved"))
	want := "format=1\nversion=v0.0.9\ncommit=" + strings.Repeat("1", 40) + "\njob_user=tester\nweb=enabled\nstate_schema=1\n"
	if record != want {
		t.Fatalf("preserved record changed:\n%s", record)
	}
	for path, content := range map[string]string{
		filepath.Join(fixture.paths.config, "config.json"):     "preserved-config\n",
		filepath.Join(fixture.paths.state, "controller.json"):  "preserved-state\n",
		filepath.Join(fixture.paths.state, "activity", "one"):  "preserved-activity\n",
		filepath.Join(fixture.paths.jobs, "jobs.json"):         "preserved-jobs\n",
		filepath.Join(fixture.paths.jobs, "output", "run.log"): "preserved-output\n",
	} {
		value, err := os.ReadFile(path)
		if err != nil || string(value) != content {
			t.Fatalf("preserved data changed at %s: %q, %v", path, value, err)
		}
	}
	entries, err := os.ReadDir(fixture.paths.metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "preserved" {
		t.Fatalf("rollback metadata entries: %v", entries)
	}
}
