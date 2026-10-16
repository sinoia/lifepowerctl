package packaging

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type uninstallFixture struct {
	root, install, config, state, jobs, metadata, runtime, powerRuntime, systemd, tmpfiles, sudoers, log string
}

func TestUninstallerRendersExactMetadataAndSafetyContract(t *testing.T) {
	script := renderedUninstaller(t)
	path := filepath.Join(t.TempDir(), "uninstall.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("shell syntax: %v\n%s", err, output)
	}
	output, err := exec.Command("/bin/sh", path, "--release-info").CombinedOutput()
	if err != nil {
		t.Fatalf("release info: %v\n%s", err, output)
	}
	for _, line := range []string{"format=1", "product=lifepowerctl", "repository=sinoia/lifepowerctl", "version=" + testReleaseVersion, "commit=" + testReleaseCommit} {
		if !strings.Contains(string(output), line+"\n") {
			t.Fatalf("release info missing %q:\n%s", line, output)
		}
	}
	for _, required := range []string{"--purge-data requires an interactive terminal", "uninstall-transaction", "uninstall-previous", "maintenance=safe", "lifecycle=inactive", "jobs=idle", "expected_manifest", "PURGE $CONFIG_ROOT $STATE_ROOT $JOBS_ROOT"} {
		if !strings.Contains(script, required) {
			t.Fatalf("uninstaller missing %q", required)
		}
	}
	for _, forbidden := range []string{"eval ", "--non-interactive", "lifepo4wered-daemon.service"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("uninstaller contains forbidden construction %q", forbidden)
		}
	}
}

func TestUninstallPreservesDataAndLeavesClosedRecord(t *testing.T) {
	fixture := newUninstallFixture(t)
	script := uninstallHarness(t, fixture, `
rollback_authorized=0
uninstall_complete=0
installed_version="`+testReleaseVersion+`"
installed_commit="`+testReleaseCommit+`"
job_user=tester
web_lifecycle=enabled
current_web_active=active
state_schema=1
uninstall_preserving_data
`)
	output, err := runUninstallHarness(t, script)
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, output)
	}
	assertUninstalledPreserved(t, fixture)
	record, err := os.ReadFile(filepath.Join(fixture.metadata, "preserved"))
	if err != nil {
		t.Fatal(err)
	}
	expected := "format=1\nversion=" + testReleaseVersion + "\ncommit=" + testReleaseCommit + "\njob_user=tester\nweb=enabled\nstate_schema=1\n"
	if string(record) != expected {
		t.Fatalf("unexpected preserved record:\n%s", record)
	}
	log := readTestFile(t, fixture.log)
	for _, action := range []string{"stop lifepowerctl.service lifepowerctl-controller.service lifepowerctl-job-runner.service lifepowerctl-poweroff.path", "disable lifepowerctl.service lifepowerctl-controller.service lifepowerctl-job-runner.service lifepowerctl-poweroff.path", "daemon-reload"} {
		if !strings.Contains(log, action+"\n") {
			t.Fatalf("systemctl log missing %q:\n%s", action, log)
		}
	}
}

func TestUninstallCaughtFailuresRestoreAtEveryBoundary(t *testing.T) {
	for _, phase := range []string{"prepared", "stopped", "removed", "reloaded", "committed"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newUninstallFixture(t)
			script := uninstallHarness(t, fixture, `
failure_point() { [ "$1" != "`+phase+`" ]; }
rollback_authorized=0
uninstall_complete=0
installed_version="`+testReleaseVersion+`"
installed_commit="`+testReleaseCommit+`"
job_user=tester
web_lifecycle=enabled
current_web_active=active
state_schema=1
uninstall_preserving_data
`)
			output, err := runUninstallHarness(t, script)
			if err == nil {
				t.Fatalf("injected failure unexpectedly succeeded:\n%s", output)
			}
			assertInstalledRestored(t, fixture)
			if !strings.Contains(string(output), "was restored successfully") {
				t.Fatalf("rollback result missing:\n%s", output)
			}
		})
	}
}

func TestAbruptUninstallRecoveryAndTamperRefusal(t *testing.T) {
	fixture := newUninstallFixture(t)
	interrupt := uninstallHarness(t, fixture, `
failure_point() { [ "$1" != removed ] || kill -KILL "$$"; }
rollback_authorized=0
uninstall_complete=0
installed_version="`+testReleaseVersion+`"
installed_commit="`+testReleaseCommit+`"
job_user=tester
web_lifecycle=enabled
current_web_active=active
state_schema=1
uninstall_preserving_data
`)
	if output, err := runUninstallHarness(t, interrupt); err == nil {
		t.Fatalf("abrupt interruption unexpectedly succeeded:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(fixture.metadata, "uninstall-transaction")); err != nil {
		t.Fatalf("interruption did not retain transaction: %v", err)
	}

	foreign := filepath.Join(fixture.metadata, "uninstall-previous", "foreign")
	if err := os.WriteFile(foreign, []byte("do not trust\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recoverScript := uninstallHarness(t, fixture, "load_recovery\n")
	output, err := runUninstallHarness(t, recoverScript)
	if err == nil || !strings.Contains(string(output), "unexpected entries") {
		t.Fatalf("tampered recovery did not fail closed: %v\n%s", err, output)
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	output, err = runUninstallHarness(t, recoverScript)
	if err != nil {
		t.Fatalf("recovery: %v\n%s", err, output)
	}
	assertInstalledRestored(t, fixture)
}

func TestPurgeValidationAndExactRemoval(t *testing.T) {
	t.Run("nested symlink refused", func(t *testing.T) {
		fixture := newUninstallFixture(t)
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(fixture.state, "hostile")); err != nil {
			t.Fatal(err)
		}
		script := uninstallHarness(t, fixture, "validate_purge_paths\n")
		output, err := runUninstallHarness(t, script)
		if err == nil || !strings.Contains(string(output), "purge refused a symlink") {
			t.Fatalf("symlinked purge did not fail closed: %v\n%s", err, output)
		}
		if readTestFile(t, outside) != "keep\n" {
			t.Fatal("refused purge changed outside file")
		}
	})

	t.Run("confirmed purge removes only fixture scope", func(t *testing.T) {
		fixture := newUninstallFixture(t)
		outside := filepath.Join(fixture.root, "unrelated")
		if err := os.WriteFile(outside, []byte("keep\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		script := uninstallHarness(t, fixture, `
rollback_authorized=0
uninstall_complete=0
installed_version="`+testReleaseVersion+`"
installed_commit="`+testReleaseCommit+`"
job_user=tester
web_lifecycle=enabled
current_web_active=active
state_schema=1
uninstall_preserving_data
purge_preserved_data
`)
		output, err := runUninstallHarness(t, script)
		if err != nil {
			t.Fatalf("purge: %v\n%s", err, output)
		}
		for _, path := range []string{fixture.config, fixture.state, fixture.jobs, fixture.metadata} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("purge retained %s: %v", path, err)
			}
		}
		if readTestFile(t, outside) != "keep\n" {
			t.Fatal("purge changed unrelated file")
		}
		accounts := readTestFile(t, filepath.Join(fixture.root, "accounts.log"))
		for _, expected := range []string{"gpasswd -d tester lifepowerctl-jobs", "userdel lifepowerctl", "groupdel lifepowerctl-jobs", "groupdel lifepowerctl"} {
			if !strings.Contains(accounts, expected+"\n") {
				t.Fatalf("account log missing %q:\n%s", expected, accounts)
			}
		}
	})
}

func TestUninstallerRefusesUnsafeRuntimeStateBeforeMutation(t *testing.T) {
	safeStatus := `format=1
product=lifepowerctl
controller=ready
running_version=v0.1.0-rc.1
running_commit=0123456789abcdef0123456789abcdef01234567
running_source_state=clean
running_target_os=linux
running_target_arch=arm64
state_schema_current=1
state_schema_min_readable=1
state_schema=1
state_revision=7
automation=disabled
lifecycle=inactive
jobs=idle
runner=ready
board=ready
board_product=fixture
register_set=7
maintenance=safe
`
	tests := []struct{ name, old, replacement, required string }{
		{name: "automation", old: "automation=disabled", replacement: "automation=enabled", required: "automation must be absent or disabled"},
		{name: "lifecycle", old: "lifecycle=inactive", replacement: "lifecycle=shutdown-intended", required: "lifecycle=inactive"},
		{name: "job", old: "jobs=idle", replacement: "jobs=running", required: "jobs=idle"},
		{name: "maintenance", old: "maintenance=safe", replacement: "maintenance=unsafe", required: "maintenance=safe"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			if err := os.Mkdir(tools, 0o700); err != nil {
				t.Fatal(err)
			}
			runuser := "#!/bin/sh\n[ \"$1\" = -u ]; shift 2; [ \"$1\" = -- ]; shift; exec \"$@\"\n"
			if err := os.WriteFile(filepath.Join(tools, "runuser"), []byte(runuser), 0o755); err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(root, "lifepowerctl")
			status := strings.Replace(safeStatus, test.old, test.replacement, 1)
			body := `#!/bin/sh
if [ "${1-}:${2-}" = version:--machine ]; then
cat <<'EOF'
format=1
product=lifepowerctl
version=v0.1.0-rc.1
commit=0123456789abcdef0123456789abcdef01234567
source_state=clean
target_os=linux
target_arch=arm64
go=go1.23.2
state_schema_current=1
state_schema_min_readable=1
EOF
exit 0
fi
cat <<'EOF'
` + status + `EOF
`
			if err := os.WriteFile(binary, []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
			script := uninstallerPrefix(t) + "\nINSTALL_ROOT='" + root + "'\nPATH='" + tools + `:/usr/bin:/bin'
export PATH
installed_version=v0.1.0-rc.1
installed_commit=0123456789abcdef0123456789abcdef01234567
web_lifecycle=enabled
SERVICE_USER=lifepowerctl
CONFIG_ROOT=/fixture/config
systemctl() {
    case "$1" in
        is-enabled) printf 'enabled\n' ;;
        is-active) if [ "${2-}" = --quiet ]; then return 0; else printf 'active\n'; fi ;;
    esac
}
wget() { return 0; }
validate_running_state
`
			output, err := runUninstallHarness(t, script)
			if err == nil || !strings.Contains(string(output), test.required) {
				t.Fatalf("unsafe state did not fail with %q: %v\n%s", test.required, err, output)
			}
		})
	}
}

func TestUninstallerRejectsTamperedOwnedManifest(t *testing.T) {
	fixture := newUninstallFixture(t)
	manifest := filepath.Join(fixture.install, "install-manifest")
	if err := os.WriteFile(manifest, []byte(fixtureInstallManifest("tester")+"file|remove|/foreign|root|root|0644\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := uninstallHarness(t, fixture, "job_user=tester\nvalidate_layout\n")
	output, err := runUninstallHarness(t, script)
	if err == nil || !strings.Contains(string(output), "owned-file manifest is not exact") {
		t.Fatalf("tampered manifest did not fail closed: %v\n%s", err, output)
	}
	if readTestFile(t, filepath.Join(fixture.config, "config.json")) != "config\n" {
		t.Fatal("manifest refusal changed preserved data")
	}
}

func TestPurgeRequiresExactTypedConfirmation(t *testing.T) {
	fixture := newUninstallFixture(t)
	for _, test := range []struct {
		name, input string
		ok          bool
	}{
		{name: "mismatch", input: "PURGE something else\n"},
		{name: "exact", input: "PURGE " + fixture.config + " " + fixture.state + " " + fixture.jobs + "\n", ok: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := uninstallHarness(t, fixture, "interactive_input() { :; }\nconfirm_purge\n")
			path := filepath.Join(t.TempDir(), "confirm.sh")
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Stdin = strings.NewReader(test.input)
			output, err := command.CombinedOutput()
			if test.ok && err != nil {
				t.Fatalf("exact confirmation failed: %v\n%s", err, output)
			}
			if !test.ok && (err == nil || !strings.Contains(string(output), "confirmation did not match")) {
				t.Fatalf("mismatched confirmation did not fail closed: %v\n%s", err, output)
			}
		})
	}
}

func renderedUninstaller(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "packaging", "release", "uninstall.sh.in"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.NewReplacer("@RELEASE_VERSION@", testReleaseVersion, "@RELEASE_COMMIT@", testReleaseCommit).Replace(string(source))
}

func uninstallerPrefix(t *testing.T) string {
	t.Helper()
	rendered := renderedUninstaller(t)
	index := strings.LastIndex(rendered, "\nrollback_authorized=0\n")
	if index < 0 {
		t.Fatal("could not isolate uninstaller functions")
	}
	return rendered[:index] + "\n"
}

func newUninstallFixture(t *testing.T) uninstallFixture {
	t.Helper()
	root := t.TempDir()
	f := uninstallFixture{
		root: root, install: filepath.Join(root, "install"), config: filepath.Join(root, "config"), state: filepath.Join(root, "state"),
		jobs: filepath.Join(root, "jobs"), metadata: filepath.Join(root, "metadata"), runtime: filepath.Join(root, "runtime"), powerRuntime: filepath.Join(root, "power-runtime"),
		systemd: filepath.Join(root, "systemd"), tmpfiles: filepath.Join(root, "tmpfiles.conf"), sudoers: filepath.Join(root, "sudoers"), log: filepath.Join(root, "systemctl.log"),
	}
	for _, path := range []string{f.install, f.config, filepath.Join(f.state, "activity"), filepath.Join(f.jobs, "output"), f.metadata, f.runtime, f.powerRuntime, f.systemd} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tools := filepath.Join(root, "tools")
	if err := os.Mkdir(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"systemd-tmpfiles": "#!/bin/sh\n[ \"${1-}\" != --create ] || mkdir -p \"$TEST_RUNTIME\" \"$TEST_POWER_RUNTIME\"\n",
		"mountpoint":       "#!/bin/sh\nexit 1\n",
	} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manifest := fixtureInstallManifest("tester")
	files := map[string]string{
		filepath.Join(f.install, "lifepowerctl"): "binary\n", filepath.Join(f.install, "uninstall.sh"): renderedUninstaller(t), filepath.Join(f.install, "install-manifest"): manifest,
		filepath.Join(f.systemd, "lifepowerctl-controller.service"): "controller\n", filepath.Join(f.systemd, "lifepowerctl-job-runner.service"): "runner\n",
		filepath.Join(f.systemd, "lifepowerctl-poweroff.service"): "poweroff\n", filepath.Join(f.systemd, "lifepowerctl-poweroff.path"): "poweroff-path\n", filepath.Join(f.systemd, "lifepowerctl.service"): "web\n",
		f.tmpfiles: "tmpfiles\n", f.sudoers: "sudoers\n", filepath.Join(f.config, "config.json"): "config\n",
		filepath.Join(f.state, "state.json"): "state\n", filepath.Join(f.state, "activity", "activity.jsonl"): "activity\n",
		filepath.Join(f.jobs, "jobs.json"): "jobs\n", filepath.Join(f.jobs, "output", "run.log"): "output\n",
		filepath.Join(f.metadata, "installed"): "format=1\nversion=" + testReleaseVersion + "\ncommit=" + testReleaseCommit + "\njob_user=tester\nweb=enabled\n",
	}
	for path, value := range files {
		mode := os.FileMode(0o644)
		if strings.HasSuffix(path, "lifepowerctl") || strings.HasSuffix(path, "uninstall.sh") {
			mode = 0o755
		}
		if strings.HasSuffix(path, "installed") {
			mode = 0o600
		}
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func uninstallHarness(t *testing.T, f uninstallFixture, body string) string {
	t.Helper()
	q := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	overrides := fmt.Sprintf(`
INSTALL_ROOT=%s
CONFIG_ROOT=%s
STATE_ROOT=%s
JOBS_ROOT=%s
METADATA_ROOT=%s
RUNTIME_ROOT=%s
POWER_RUNTIME_ROOT=%s
SYSTEMD_ROOT=%s
TMPFILES_POLICY=%s
SUDOERS_POLICY=%s
INSTALLED_RECORD="$METADATA_ROOT/installed"
PRESERVED_RECORD="$METADATA_ROOT/preserved"
TRANSACTION_FILE="$METADATA_ROOT/uninstall-transaction"
RECOVERY_ROOT="$METADATA_ROOT/uninstall-previous"
TEST_SYSTEMCTL_LOG=%s
TEST_ACCOUNTS_LOG=%s
TEST_RUNTIME=%s
TEST_POWER_RUNTIME=%s
PATH=%s:"$PATH"
export TEST_RUNTIME TEST_POWER_RUNTIME PATH
install() {
    mode=0755; directory=0
    while [ "$#" -gt 0 ]; do
        case "$1" in
            -o|-g) shift 2 ;;
            -m) mode=$2; shift 2 ;;
            -d) directory=1; shift ;;
            *) break ;;
        esac
    done
    if [ "$directory" -eq 1 ]; then /usr/bin/install -d -m "$mode" "$@"; else /usr/bin/install -m "$mode" "$@"; fi
}
require_owned_file() { [ -f "$1" ] && [ ! -L "$1" ] || fail "fixture file is unsafe: $1"; }
require_owned_directory() { [ -d "$1" ] && [ ! -L "$1" ] || fail "fixture directory is unsafe: $1"; }
validate_accounts() { :; }
validate_running_state() { :; }
id() { if [ "${1-}" = -u ]; then printf '0\n'; else /usr/bin/id "$@"; fi; }
stat() {
    if [ "${1-}" = -c ] && [ "${2-}" = '%%U:%%G:%%a' ]; then printf 'root:root:%%s\n' "$(/usr/bin/stat -c %%a "$3")"; else /usr/bin/stat "$@"; fi
}
systemctl() {
    printf '%%s\n' "$*" >>"$TEST_SYSTEMCTL_LOG"
    case "${1-}" in is-active) printf 'inactive\n' ;; is-enabled) printf 'disabled\n' ;; esac
    return 0
}
gpasswd() { printf 'gpasswd %%s\n' "$*" >>"$TEST_ACCOUNTS_LOG"; }
userdel() { printf 'userdel %%s\n' "$*" >>"$TEST_ACCOUNTS_LOG"; }
groupdel() { printf 'groupdel %%s\n' "$*" >>"$TEST_ACCOUNTS_LOG"; }
`, q(f.install), q(f.config), q(f.state), q(f.jobs), q(f.metadata), q(f.runtime), q(f.powerRuntime), q(f.systemd), q(f.tmpfiles), q(f.sudoers), q(f.log), q(filepath.Join(f.root, "accounts.log")), q(f.runtime), q(f.powerRuntime), q(filepath.Join(f.root, "tools")))
	return uninstallerPrefix(t) + overrides + body
}

func runUninstallHarness(t *testing.T, script string) ([]byte, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return exec.Command("/bin/sh", path).CombinedOutput()
}

func fixtureInstallManifest(jobUser string) string {
	return fmt.Sprintf(`format=1
file|remove|/usr/lib/lifepowerctl/lifepowerctl|root|root|0755
file|remove|/usr/lib/lifepowerctl/uninstall.sh|root|root|0755
file|remove|/usr/lib/lifepowerctl/install-manifest|root|root|0644
file|remove|/etc/systemd/system/lifepowerctl-controller.service|root|root|0644
file|remove|/etc/systemd/system/lifepowerctl-job-runner.service|root|root|0644
file|remove|/etc/systemd/system/lifepowerctl-poweroff.service|root|root|0644
file|remove|/etc/systemd/system/lifepowerctl-poweroff.path|root|root|0644
file|remove|/etc/systemd/system/lifepowerctl.service|root|root|0644
file|remove|/etc/tmpfiles.d/lifepowerctl.conf|root|root|0644
file|remove|/etc/sudoers.d/lifepowerctl|root|root|0440
file|preserve|/etc/lifepowerctl/config.json|root|lifepowerctl|0640
directory|remove-if-empty|/usr/lib/lifepowerctl|root|root|0755
directory|preserve|/etc/lifepowerctl|root|lifepowerctl|0750
directory|preserve|/var/lib/lifepowerctl|lifepowerctl|lifepowerctl|0700
directory|preserve|/var/lib/lifepowerctl/activity|lifepowerctl|lifepowerctl|0700
directory|preserve|/var/lib/lifepowerctl-jobs|%s|lifepowerctl-jobs|2750
directory|preserve|/var/lib/lifepowerctl-jobs/output|%s|lifepowerctl-jobs|2750
directory|runtime|/run/lifepowerctl|%s|lifepowerctl-jobs|2770
directory|runtime|/run/lifepowerctl-power|root|lifepowerctl|0730
directory|metadata|/var/lib/lifepowerctl-installer|root|root|0700
`, jobUser, jobUser, jobUser)
}

func assertUninstalledPreserved(t *testing.T, f uninstallFixture) {
	t.Helper()
	for _, path := range []string{f.install, f.runtime, f.powerRuntime, f.tmpfiles, f.sudoers, filepath.Join(f.systemd, "lifepowerctl.service"), filepath.Join(f.systemd, "lifepowerctl-controller.service"), filepath.Join(f.systemd, "lifepowerctl-job-runner.service"), filepath.Join(f.systemd, "lifepowerctl-poweroff.service"), filepath.Join(f.systemd, "lifepowerctl-poweroff.path")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("package path remains %s: %v", path, err)
		}
	}
	for path, expected := range map[string]string{filepath.Join(f.config, "config.json"): "config\n", filepath.Join(f.state, "state.json"): "state\n", filepath.Join(f.state, "activity", "activity.jsonl"): "activity\n", filepath.Join(f.jobs, "jobs.json"): "jobs\n", filepath.Join(f.jobs, "output", "run.log"): "output\n"} {
		if value := readTestFile(t, path); value != expected {
			t.Fatalf("preserved file %s = %q", path, value)
		}
	}
	entries, err := os.ReadDir(f.metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "preserved" {
		t.Fatalf("unexpected metadata entries: %v", entries)
	}
}

func assertInstalledRestored(t *testing.T, f uninstallFixture) {
	t.Helper()
	for _, path := range []string{filepath.Join(f.install, "lifepowerctl"), filepath.Join(f.install, "uninstall.sh"), filepath.Join(f.install, "install-manifest"), f.runtime, f.powerRuntime, f.tmpfiles, f.sudoers, filepath.Join(f.systemd, "lifepowerctl.service"), filepath.Join(f.systemd, "lifepowerctl-controller.service"), filepath.Join(f.systemd, "lifepowerctl-job-runner.service"), filepath.Join(f.systemd, "lifepowerctl-poweroff.service"), filepath.Join(f.metadata, "installed")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("restored path missing %s: %v", path, err)
		}
	}
	entries, err := os.ReadDir(f.metadata)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "installed" {
		t.Fatalf("restoration left metadata: %v", entries)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
