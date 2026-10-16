package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSystemdTemplatesRenderAndVerify(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd units are Linux packaging")
	}
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze is not installed")
	}
	root := filepath.Join("..", "..", "packaging", "systemd")
	replacements := strings.NewReplacer(
		"@JOB_USER@", "nobody",
		"@JOB_UID@", "65534",
		"@JOB_GROUP@", "nogroup",
		"@JOB_HOME@", "/tmp",
		"@CONTROLLER_UID@", "1000",
		"/usr/lib/lifepowerctl/lifepowerctl", "/bin/true",
		"User=lifepowerctl", "User=nobody",
		"Group=lifepowerctl", "Group=nogroup",
		"SupplementaryGroups=lifepowerctl-jobs i2c", "",
		"SupplementaryGroups=lifepowerctl-jobs", "",
	)
	temporary := t.TempDir()
	var rendered []string
	for _, name := range []string{"lifepowerctl.service.in", "lifepowerctl-controller.service.in", "lifepowerctl-job-runner.service.in", "lifepowerctl-poweroff.service.in", "lifepowerctl-poweroff.path.in"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		value := replacements.Replace(string(data))
		if strings.Contains(value, "@") {
			t.Fatalf("unresolved template token in %s", name)
		}
		path := filepath.Join(temporary, strings.TrimSuffix(name, ".in"))
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		rendered = append(rendered, path)
	}
	command := exec.Command(analyze, append([]string{"verify"}, rendered...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("systemd-analyze verify: %v\n%s", err, output)
	}
	for _, unit := range rendered {
		command := exec.Command(analyze, "security", "--offline=yes", "--no-pager", unit)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("systemd-analyze security %s: %v\n%s", filepath.Base(unit), err, output)
		} else {
			summary := "summary unavailable"
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, "Overall exposure level") {
					summary = strings.TrimSpace(line)
				}
			}
			t.Logf("%s: %s", filepath.Base(unit), summary)
		}
	}
}

func TestPackagingTemplatesKeepPrivilegeBoundaryExplicit(t *testing.T) {
	root := filepath.Join("..", "..", "packaging", "systemd")
	runner, err := os.ReadFile(filepath.Join(root, "lifepowerctl-job-runner.service.in"))
	if err != nil {
		t.Fatal(err)
	}
	web, err := os.ReadFile(filepath.Join(root, "lifepowerctl.service.in"))
	if err != nil {
		t.Fatal(err)
	}
	controller, err := os.ReadFile(filepath.Join(root, "lifepowerctl-controller.service.in"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"User=@JOB_USER@", "Group=@JOB_GROUP@", "--controller-uid @CONTROLLER_UID@", "MemoryMax=256M", "TasksMax=64", "LimitNOFILE=1024", "ProtectHome=no"} {
		if !strings.Contains(string(runner), required) {
			t.Fatalf("runner template missing %q", required)
		}
	}
	for _, required := range []string{"User=lifepowerctl", "NoNewPrivileges=yes", "CapabilityBoundingSet=", "DevicePolicy=closed", "DeviceAllow=/dev/i2c-1 rw", "MemoryMax=128M", "LimitNOFILE=256", "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6", "ReadOnlyPaths=/var/lib/lifepowerctl-jobs/output"} {
		if !strings.Contains(string(web), required) {
			t.Fatalf("web template missing %q", required)
		}
	}
	for _, required := range []string{"lifepowerctl controller", "User=lifepowerctl", "NoNewPrivileges=yes", "AmbientCapabilities=", "CapabilityBoundingSet=", "DevicePolicy=closed", "DeviceAllow=/dev/i2c-1 rw", "MemoryMax=128M", "LimitNOFILE=128", "RestrictAddressFamilies=AF_UNIX", "ReadWritePaths=/run/lifepowerctl /var/lib/lifepowerctl"} {
		if !strings.Contains(string(controller), required) {
			t.Fatalf("controller template missing %q", required)
		}
	}
	poweroff, err := os.ReadFile(filepath.Join(root, "lifepowerctl-poweroff.service.in"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Type=oneshot", "ExecStart=/usr/bin/rm -f /run/lifepowerctl-power/poweroff.request", "ExecStart=/usr/bin/sleep 8", "ExecStart=/usr/bin/systemctl --no-block poweroff", "User=root", "ProtectSystem=strict"} {
		if !strings.Contains(string(poweroff), required) {
			t.Fatalf("poweroff template missing %q", required)
		}
	}
	if strings.Contains(string(poweroff), "CapabilityBoundingSet=") {
		t.Fatal("poweroff helper must retain the capabilities needed by systemctl and PID 1")
	}
	pathUnit, err := os.ReadFile(filepath.Join(root, "lifepowerctl-poweroff.path.in"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"PathExists=/run/lifepowerctl-power/poweroff.request", "Unit=lifepowerctl-poweroff.service", "WantedBy=multi-user.target"} {
		if !strings.Contains(string(pathUnit), required) {
			t.Fatalf("poweroff path template missing %q", required)
		}
	}
	sudoers, err := os.ReadFile(filepath.Join(root, "lifepowerctl-sudoers.in"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sudoers), "NOPASSWD:") || strings.Contains(string(sudoers), "lifepowerctl ALL=") {
		t.Fatalf("compatibility sudoers template grants authority: %q", sudoers)
	}
}

func TestSudoersTemplateValidatesAsOneExactCommand(t *testing.T) {
	visudo, err := exec.LookPath("visudo")
	if err != nil {
		t.Skip("visudo is not installed")
	}
	path := filepath.Join("..", "..", "packaging", "systemd", "lifepowerctl-sudoers.in")
	if output, err := exec.Command(visudo, "-cf", path).CombinedOutput(); err != nil {
		t.Fatalf("visudo -c: %v\n%s", err, output)
	}
}

func TestInstallLayoutSeparatesRemovedPreservedAndRootMetadata(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "packaging", "systemd", "install-layout.txt.in"))
	if err != nil {
		t.Fatal(err)
	}
	value := string(data)
	for _, required := range []string{
		"file|remove|/usr/lib/lifepowerctl/lifepowerctl|root|root|0755",
		"file|remove|/etc/sudoers.d/lifepowerctl|root|root|0440",
		"file|preserve|/etc/lifepowerctl/config.json|root|lifepowerctl|0640",
		"directory|preserve|/var/lib/lifepowerctl|lifepowerctl|lifepowerctl|0700",
		"directory|preserve|/var/lib/lifepowerctl-jobs|@JOB_USER@|lifepowerctl-jobs|2750",
		"directory|metadata|/var/lib/lifepowerctl-installer|root|root|0700",
	} {
		if !strings.Contains(value, required+"\n") {
			t.Fatalf("install layout missing %q", required)
		}
	}
}
