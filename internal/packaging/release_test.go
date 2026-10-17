package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testReleaseVersion = "v0.1.0-rc.1"
	testReleaseCommit  = "0123456789abcdef0123456789abcdef01234567"
	testReleaseArchive = "lifepowerctl_0.1.0-rc.1_linux_arm64.tar.gz"
	testReleaseBaseURL = "http://127.0.0.1:18080/releases/download/v0.1.0-rc.1"
)

func TestReleaseShellTemplatesRenderAndExposeExactBinding(t *testing.T) {
	root := filepath.Join("..", "..")
	templates := []struct {
		name     string
		required []string
	}{
		{
			name: "install.sh.in",
			required: []string{
				"archive=" + testReleaseArchive,
				"release_base_url=" + testReleaseBaseURL,
				"checksums=checksums.txt",
			},
		},
		{
			name:     "uninstall.sh.in",
			required: []string{"version=" + testReleaseVersion, "commit=" + testReleaseCommit},
		},
	}
	replacer := strings.NewReplacer(
		"@RELEASE_VERSION@", testReleaseVersion,
		"@RELEASE_COMMIT@", testReleaseCommit,
		"@RELEASE_ARCHIVE@", testReleaseArchive,
		"@RELEASE_BASE_URL@", testReleaseBaseURL,
	)
	for _, test := range templates {
		t.Run(test.name, func(t *testing.T) {
			var rendered string
			if test.name == "install.sh.in" {
				path := filepath.Join(t.TempDir(), "install.sh")
				command := exec.Command("/bin/sh", filepath.Join(root, "scripts", "render-installer.sh"), testReleaseVersion, testReleaseCommit, testReleaseArchive, testReleaseBaseURL, path)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("render installer: %v\n%s", err, output)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				rendered = string(data)
			} else {
				source, err := os.ReadFile(filepath.Join(root, "packaging", "release", test.name))
				if err != nil {
					t.Fatal(err)
				}
				rendered = replacer.Replace(string(source))
			}
			if strings.Contains(rendered, "@RELEASE_") {
				t.Fatalf("unresolved release token in %s", test.name)
			}
			if strings.Contains(rendered, "eval ") || strings.Contains(rendered, "| sudo sh") {
				t.Fatalf("unsafe shell construction in %s", test.name)
			}
			path := filepath.Join(t.TempDir(), strings.TrimSuffix(test.name, ".in"))
			if err := os.WriteFile(path, []byte(rendered), 0o700); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("/bin/sh", "-n", path).CombinedOutput(); err != nil {
				t.Fatalf("sh -n %s: %v\n%s", test.name, err, output)
			}
			output, err := exec.Command("/bin/sh", path, "--release-info").CombinedOutput()
			if err != nil {
				t.Fatalf("release info %s: %v\n%s", test.name, err, output)
			}
			value := string(output)
			for _, required := range append([]string{"format=1", "product=lifepowerctl", "repository=sinoia/lifepowerctl"}, test.required...) {
				if !strings.Contains(value, required+"\n") {
					t.Fatalf("%s release info missing %q:\n%s", test.name, required, value)
				}
			}
			if test.name == "uninstall.sh.in" {
				command := exec.Command("/bin/sh", path)
				if output, err := command.CombinedOutput(); err == nil {
					t.Fatalf("incomplete %s mutation path did not fail closed: %v\n%s", test.name, err, output)
				}
			}
		})
	}
}

func TestReleaseRendererKeepsConstrainedExactAssetContract(t *testing.T) {
	root := filepath.Join("..", "..")
	script, err := os.ReadFile(filepath.Join(root, "scripts", "build-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	value := string(script)
	for _, required := range []string{
		"git status --porcelain=v1 --untracked-files=all",
		"REQUIRE_RELEASE_TAG:-0",
		"refs/tags/$release_version^{commit}",
		"CGO_ENABLED=0 GOOS=linux GOARCH=arm64",
		`target_build_info=$("$go_command" version -m "$stage_directory/lifepowerctl")`,
		`host_goarch=$("$go_command" env GOARCH)`,
		`metadata_binary="$work_directory/metadata-verifier"`,
		"GOMAXPROCS=1 GOMEMLIMIT=64MiB GOGC=5",
		"-buildvcs=false -mod=readonly -trimpath -p=1",
		"source_state=clean",
		"tar --sort=name --format=ustar",
		"gzip -n",
		"sha256sum \"$archive_name\" install.sh uninstall.sh",
		"https://github.com/sinoia/lifepowerctl/releases/download/$release_version",
		"scripts/render-installer.sh",
	} {
		if !strings.Contains(value, required) {
			t.Fatalf("release renderer missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"git push",
		"gh release",
		`machine_info=$("$stage_directory/lifepowerctl" version --machine)`,
	} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("release renderer contains forbidden construction %q", forbidden)
		}
	}
	if output, err := exec.Command("/bin/sh", "-n", filepath.Join(root, "scripts", "build-release.sh")).CombinedOutput(); err != nil {
		t.Fatalf("release renderer shell syntax: %v\n%s", err, output)
	}
	if output, err := exec.Command("/bin/sh", "-n", filepath.Join(root, "scripts", "render-installer.sh")).CombinedOutput(); err != nil {
		t.Fatalf("installer renderer shell syntax: %v\n%s", err, output)
	}
}

func TestReleaseWorkflowKeepsProtectedDraftBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	value := string(workflow)
	for _, required := range []string{
		"tags:\n      - 'v*.*.*'",
		"REQUIRE_RELEASE_TAG: '1'",
		"attestations: write",
		"subject-path: dist/${{ env.RELEASE_VERSION }}/*",
		"SBOM.spdx.json",
		"environment: release",
		"contents: write",
		"gh release view",
		"gh release create",
		"--verify-tag",
		"--draft",
		"--latest=false",
	} {
		if !strings.Contains(value, required) {
			t.Fatalf("release workflow missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"pull_request_target",
		"runs-on: self-hosted",
		"secrets.",
		"--clobber",
		"gh release edit",
	} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("release workflow contains forbidden construction %q", forbidden)
		}
	}
}

func TestRenderedInstallerContainsFreshInstallSafetyBoundary(t *testing.T) {
	root := filepath.Join("..", "..")
	path := filepath.Join(t.TempDir(), "install.sh")
	command := exec.Command("/bin/sh", filepath.Join(root, "scripts", "render-installer.sh"), testReleaseVersion, testReleaseCommit, testReleaseArchive, testReleaseBaseURL, path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render installer: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value := string(data)
	for _, required := range []string{
		"Downloading exact ${RELEASE_VERSION} assets before changing the system",
		"checksum manifest must contain exactly one valid entry",
		"archive member list is not the exact release allowlist",
		"dpkg --print-architecture",
		"    check_system_ancestors\n",
		"system destination ancestor must be owned by root:root",
		"lifepo4wered-daemon.service",
		"/dev/i2c-1",
		"systemd-analyze verify",
		"admin ensure-setup",
		"admin status",
		"runuser -u \"$SERVICE_USER\"",
		"Rolling back the incomplete fresh install",
		"cat <<'__LIFEPOWERCTL_EMBEDDED_ASSET__'",
	} {
		if !strings.Contains(value, required) {
			t.Fatalf("rendered installer missing %q", required)
		}
	}
	for _, forbidden := range []string{"eval ", "| sudo sh", "RELEASE_BASE_URL=${"} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("rendered installer contains forbidden construction %q", forbidden)
		}
	}
}

func TestRenderedInstallerAcquiresAndValidatesExactLocalFixture(t *testing.T) {
	fixture := newAcquisitionFixture(t)
	output, err := runAcquisitionHarness(t, fixture)
	if err != nil {
		t.Fatalf("exact local fixture acquisition: %v\n%s", err, output)
	}
}

func TestRenderedInstallerStagesAndValidatesExactAssetDirectory(t *testing.T) {
	fixture := newAcquisitionFixture(t)
	output, err := runAssetDirectoryHarness(t, fixture)
	if err != nil {
		t.Fatalf("exact asset-directory acquisition: %v\n%s", err, output)
	}
}

func TestRenderedInstallerRejectsSymlinkInAssetDirectory(t *testing.T) {
	fixture := newAcquisitionFixture(t)
	uninstaller := filepath.Join(fixture, "uninstall.sh")
	if err := os.Remove(uninstaller); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(fixture, testReleaseArchive), uninstaller); err != nil {
		t.Fatal(err)
	}
	output, err := runAssetDirectoryHarness(t, fixture)
	if err == nil || !strings.Contains(string(output), "local release asset is missing, not regular, or a symlink") {
		t.Fatalf("symlinked local asset did not fail closed: %v\n%s", err, output)
	}
}

func TestRenderedInstallerRejectsUnsafeAssetDirectoryMetadata(t *testing.T) {
	tests := []struct {
		name, owner, directoryMode, fileMode, required string
	}{
		{name: "non-root owner", owner: "tester:tester", directoryMode: "700", fileMode: "600", required: "--asset-dir must be owned by root:root"},
		{name: "writable directory", owner: "root:root", directoryMode: "777", fileMode: "600", required: "--asset-dir must not be group- or other-writable"},
		{name: "writable asset", owner: "root:root", directoryMode: "700", fileMode: "666", required: "local release asset has unsafe permissions"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAcquisitionFixture(t)
			output, err := runAssetDirectoryHarnessWithMetadata(t, fixture, test.owner, test.directoryMode, test.fileMode)
			if err == nil || !strings.Contains(string(output), test.required) {
				t.Fatalf("unsafe local asset metadata did not fail closed with %q: %v\n%s", test.required, err, output)
			}
		})
	}
}

func TestRenderedInstallerRejectsAdversarialAcquisition(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*testing.T, string)
		required string
	}{
		{
			name: "network unavailable",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				if err := os.Remove(filepath.Join(fixture, "checksums.txt")); err != nil {
					t.Fatal(err)
				}
			},
			required: "download failed or exceeded its bound",
		},
		{
			name: "empty download",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(fixture, "checksums.txt"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			required: "download was empty",
		},
		{
			name: "oversized download",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(fixture, "checksums.txt"), bytes.Repeat([]byte{'x'}, 1048577), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			required: "download failed or exceeded its bound",
		},
		{
			name: "error page",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(fixture, "checksums.txt"), []byte("<html>not found</html>\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			required: "checksum manifest must contain exactly one valid entry",
		},
		{
			name: "missing checksum",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				writeChecksums(t, fixture, []string{"uninstall.sh"})
			},
			required: "checksum manifest must contain exactly one valid entry",
		},
		{
			name: "duplicate checksum",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				writeChecksums(t, fixture, []string{testReleaseArchive, testReleaseArchive, "uninstall.sh"})
			},
			required: "checksum manifest must contain exactly one valid entry",
		},
		{
			name: "checksum mismatch",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				manifest := strings.Repeat("0", 64) + "  " + testReleaseArchive + "\n" + fixtureChecksum(t, fixture, "uninstall.sh")
				if err := os.WriteFile(filepath.Join(fixture, "checksums.txt"), []byte(manifest), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			required: "checksum mismatch",
		},
		{
			name: "wrong uninstaller metadata",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				wrong := "#!/bin/sh\nprintf '%s\\n' format=1 product=lifepowerctl repository=sinoia/lifepowerctl version=v9.9.9 commit=" + testReleaseCommit + "\n"
				if err := os.WriteFile(filepath.Join(fixture, "uninstall.sh"), []byte(wrong), 0o755); err != nil {
					t.Fatal(err)
				}
				writeChecksums(t, fixture, []string{testReleaseArchive, "uninstall.sh"})
			},
			required: "uninstaller metadata is not exact",
		},
		{
			name: "incompatible binary metadata",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				binary := fixtureBinary("target_arch=amd64")
				writeInstallerFixtureArchiveWithBinary(t, filepath.Join(fixture, testReleaseArchive), binary)
				writeChecksums(t, fixture, []string{testReleaseArchive, "uninstall.sh"})
			},
			required: "staged executable metadata must contain exactly one line: target_arch=arm64",
		},
		{
			name: "invalid runtime metadata",
			mutate: func(t *testing.T, fixture string) {
				t.Helper()
				binary := strings.Replace(fixtureBinary("target_arch=arm64"), "go=go1.23.2", "go=bad runtime", 1)
				writeInstallerFixtureArchiveWithBinary(t, filepath.Join(fixture, testReleaseArchive), binary)
				writeChecksums(t, fixture, []string{testReleaseArchive, "uninstall.sh"})
			},
			required: "staged executable metadata has an invalid or duplicated Go runtime field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAcquisitionFixture(t)
			test.mutate(t, fixture)
			output, err := runAcquisitionHarness(t, fixture)
			if err == nil {
				t.Fatalf("adversarial fixture unexpectedly passed:\n%s", output)
			}
			if !strings.Contains(string(output), test.required) {
				t.Fatalf("failure missing %q:\n%s", test.required, output)
			}
		})
	}
}

func TestRenderedInstallerRejectsHostileArchivesBeforeExtraction(t *testing.T) {
	tests := []struct {
		name     string
		members  func(string) []fixtureArchiveMember
		corrupt  bool
		required string
	}{
		{
			name: "unexpected member",
			members: func(sentinel string) []fixtureArchiveMember {
				members := validFixtureMembers(fixtureBinary("target_arch=arm64"))
				return append(members, fixtureArchiveMember{name: "lifepowerctl/unexpected", mode: 0o644, body: "no\n"})
			},
			required: "archive member list is not the exact release allowlist",
		},
		{
			name: "duplicate member",
			members: func(sentinel string) []fixtureArchiveMember {
				members := validFixtureMembers(fixtureBinary("target_arch=arm64"))
				return append(members, fixtureArchiveMember{name: "lifepowerctl/README.md", mode: 0o644, body: "duplicate\n"})
			},
			required: "archive member list is not the exact release allowlist",
		},
		{
			name: "absolute path",
			members: func(sentinel string) []fixtureArchiveMember {
				return []fixtureArchiveMember{{name: sentinel, mode: 0o644, body: "escape\n"}}
			},
			required: "archive member list is not the exact release allowlist",
		},
		{
			name: "parent traversal",
			members: func(sentinel string) []fixtureArchiveMember {
				return []fixtureArchiveMember{{name: "../../outside-sentinel", mode: 0o644, body: "escape\n"}}
			},
			required: "archive member list is not the exact release allowlist",
		},
		{
			name: "symbolic link",
			members: func(sentinel string) []fixtureArchiveMember {
				members := validFixtureMembers(fixtureBinary("target_arch=arm64"))
				members[2] = fixtureArchiveMember{name: "lifepowerctl/README.md", mode: 0o777, typeflag: tar.TypeSymlink, linkname: sentinel}
				return members
			},
			required: "archive contains a non-file member",
		},
		{
			name: "hard link",
			members: func(sentinel string) []fixtureArchiveMember {
				members := validFixtureMembers(fixtureBinary("target_arch=arm64"))
				members[2] = fixtureArchiveMember{name: "lifepowerctl/README.md", mode: 0o644, typeflag: tar.TypeLink, linkname: "lifepowerctl/LICENSE"}
				return members
			},
			required: "archive contains a non-file member",
		},
		{
			name: "fifo",
			members: func(sentinel string) []fixtureArchiveMember {
				members := validFixtureMembers(fixtureBinary("target_arch=arm64"))
				members[2] = fixtureArchiveMember{name: "lifepowerctl/README.md", mode: 0o644, typeflag: tar.TypeFifo}
				return members
			},
			required: "archive contains a non-file member",
		},
		{
			name:     "corrupt gzip",
			corrupt:  true,
			required: "archive member listing failed or timed out",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAcquisitionFixture(t)
			outside := filepath.Join(t.TempDir(), "outside-sentinel")
			if test.corrupt {
				if err := os.WriteFile(filepath.Join(fixture, testReleaseArchive), []byte("not a gzip archive\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixtureArchiveMembers(t, filepath.Join(fixture, testReleaseArchive), test.members(outside))
			}
			writeChecksums(t, fixture, []string{testReleaseArchive, "uninstall.sh"})
			output, err := runAcquisitionHarness(t, fixture)
			if err == nil {
				t.Fatalf("hostile archive unexpectedly passed:\n%s", output)
			}
			if !strings.Contains(string(output), test.required) {
				t.Fatalf("failure missing %q:\n%s", test.required, output)
			}
			if _, err := os.Lstat(outside); !os.IsNotExist(err) {
				t.Fatalf("hostile archive created outside path: %v", err)
			}
		})
	}
}

func TestRenderedInstallerRejectsUnsupportedNativeArchitectureBeforeMutation(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tools := t.TempDir()
	commands := map[string]string{
		"id":    "#!/bin/sh\nif [ \"${1-}\" = -u ]; then echo 0; else /usr/bin/id \"$@\"; fi\n",
		"dpkg":  "#!/bin/sh\nprintf '%s\\n' armhf\n",
		"sed":   "#!/bin/sh\ncase \"${2-}\" in 's/^ID=//p') printf '%s\\n' raspbian ;; 's/^VERSION_ID=//p') printf '%s\\n' 13 ;; *) exec /usr/bin/sed \"$@\" ;; esac\n",
		"uname": "#!/bin/sh\nprintf '%s\\n' aarch64\n",
	}
	for name, body := range commands {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	harness := prefix + `
PATH="$TEST_TOOLS:/usr/sbin:/usr/bin:/sbin:/bin"
export PATH
job_user=pi
development_host=1
non_interactive=1
accept_authority=1
preflight
`
	path := filepath.Join(t.TempDir(), "unsupported-architecture.sh")
	if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", path)
	command.Env = append(os.Environ(), "TEST_TOOLS="+tools)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("unsupported architecture unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(string(output), "native user-space architecture is not arm64") {
		t.Fatalf("unexpected architecture failure:\n%s", output)
	}
}

func TestRenderedInstallerClassifiesSemanticVersions(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		target, installed, want string
	}{
		{"v1.0.0", "v1.0.0", "same"},
		{"v1.0.1", "v1.0.0", "upgrade"},
		{"v1.1.0", "v1.0.9", "upgrade"},
		{"v2.0.0", "v1.99.99", "upgrade"},
		{"v1.0.0", "v1.0.1", "downgrade"},
		{"v1.0.0-rc.2", "v1.0.0-rc.1", "upgrade"},
		{"v1.0.0-rc.10", "v1.0.0-rc.2", "upgrade"},
		{"v1.0.0-rc.1", "v1.0.0", "downgrade"},
		{"v1.0.0", "v1.0.0-rc.1", "upgrade"},
		{"v1.0.0-1", "v1.0.0-alpha", "downgrade"},
	}
	var calls strings.Builder
	for _, test := range tests {
		fmt.Fprintf(&calls, "test \"$(compare_release_versions %s %s)\" = %s\n", test.target, test.installed, test.want)
	}
	path := filepath.Join(t.TempDir(), "versions.sh")
	if err := os.WriteFile(path, []byte(prefix+"\n"+calls.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("/bin/sh", path).CombinedOutput(); err != nil {
		t.Fatalf("semantic version classification: %v\n%s", err, output)
	}
}

func TestRenderedInstallerClassifiesInstalledLifecycleAndRequiresExplicitDowngrade(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name, installedVersion, installedCommit, want string
		allowDowngrade                                bool
		wantFailure                                   string
	}{
		{name: "same", installedVersion: testReleaseVersion, installedCommit: testReleaseCommit, want: "same-version"},
		{name: "upgrade", installedVersion: "v0.0.9", installedCommit: strings.Repeat("1", 40), want: "upgrade"},
		{name: "downgrade refused", installedVersion: "v0.2.0", installedCommit: strings.Repeat("2", 40), wantFailure: "requires --allow-downgrade"},
		{name: "downgrade accepted", installedVersion: "v0.2.0", installedCommit: strings.Repeat("2", 40), allowDowngrade: true, want: "downgrade"},
		{name: "immutable version", installedVersion: testReleaseVersion, installedCommit: strings.Repeat("3", 40), wantFailure: "release immutability is violated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			paths := recoveryHarnessPaths(root)
			if err := os.Mkdir(paths.metadata, 0o700); err != nil {
				t.Fatal(err)
			}
			installed := fmt.Sprintf("format=1\nversion=%s\ncommit=%s\njob_user=tester\nweb=on-demand\n", test.installedVersion, test.installedCommit)
			if err := os.WriteFile(filepath.Join(paths.metadata, "installed"), []byte(installed), 0o600); err != nil {
				t.Fatal(err)
			}
			allow := 0
			if test.allowDowngrade {
				allow = 1
			}
			harness := prefix + recoveryHarnessOverrides(paths) + fmt.Sprintf("\nallow_downgrade=%d\nload_installation_record\nprintf '%%s\\n' \"$installation_action\"\n", allow)
			path := filepath.Join(t.TempDir(), "classify.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Env = append(os.Environ(), "TEST_TOOLS=/usr/bin", fmt.Sprintf("TEST_UID=%d", os.Getuid()), fmt.Sprintf("TEST_GID=%d", os.Getgid()))
			output, err := command.CombinedOutput()
			if test.wantFailure != "" {
				if err == nil || !strings.Contains(string(output), test.wantFailure) {
					t.Fatalf("classification did not fail with %q: %v\n%s", test.wantFailure, err, output)
				}
				return
			}
			if err != nil || strings.TrimSpace(string(output)) != test.want {
				t.Fatalf("classification = %q, %v; want %q", output, err, test.want)
			}
		})
	}
}

func TestRenderedInstallerValidatesInstallationPorts(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		port string
		ok   bool
	}{
		{port: "1024", ok: true},
		{port: "8080", ok: true},
		{port: "65535", ok: true},
		{port: ""},
		{port: "0"},
		{port: "80"},
		{port: "01024"},
		{port: "65536"},
		{port: "service"},
	}
	for _, test := range tests {
		t.Run(test.port, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "port.sh")
			if err := os.WriteFile(path, []byte(prefix+"\nvalidate_web_port \"$1\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("/bin/sh", path, test.port).CombinedOutput()
			if test.ok && err != nil {
				t.Fatalf("valid port refused: %v\n%s", err, output)
			}
			if !test.ok && (err == nil || !strings.Contains(string(output), "port must")) {
				t.Fatalf("invalid port accepted or wrong failure: %v\n%s", err, output)
			}
		})
	}
}

func TestRenderedInstallerRendersAndChecksSelectedPort(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	work := t.TempDir()
	logPath := filepath.Join(work, "wget.log")
	script := prefix + `
web_port=18081
job_user=tester
job_uid=1000
job_group=tester
job_home=/home/tester
work_directory="$TEST_WORK"
id() { printf '%s\n' 900; }
visudo() { :; }
prepare_policy
grep -Fqx '  "listen_address": "127.0.0.1:18081",' "$policy/config.json"
grep -Fqx '  "external_origin": "http://127.0.0.1:18081",' "$policy/config.json"
! grep -q '@WEB_PORT@' "$policy/config.json"
have() { [ "$1" = wget ]; }
timeout() { shift; "$@"; }
wget() { printf '%s\n' "$*" >"$TEST_LOG"; }
check_web_health
grep -Fq 'http://127.0.0.1:18081/healthz' "$TEST_LOG"
`
	path := filepath.Join(t.TempDir(), "selected-port.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", path)
	command.Env = append(os.Environ(), "TEST_WORK="+work, "TEST_LOG="+logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("selected port was not rendered and checked: %v\n%s", err, output)
	}
}

func TestRenderedInstallerLoadsRecordedAndLegacyPorts(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	for _, test := range []struct {
		name, record, requested, want string
	}{
		{name: "recorded", record: "format=2\nversion=v0.0.9\ncommit=" + strings.Repeat("1", 40) + "\njob_user=tester\nweb=enabled\nport=18081\n", requested: "8080", want: "18081"},
		{name: "legacy default", record: "format=1\nversion=v0.0.9\ncommit=" + strings.Repeat("1", 40) + "\njob_user=tester\nweb=enabled\n", requested: "8080", want: "8080"},
		{name: "legacy adopted custom", record: "format=1\nversion=v0.0.9\ncommit=" + strings.Repeat("1", 40) + "\njob_user=tester\nweb=enabled\n", requested: "18081", want: "18081"},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := recoveryHarnessPaths(t.TempDir())
			if err := os.Mkdir(paths.metadata, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(paths.metadata, "installed"), []byte(test.record), 0o600); err != nil {
				t.Fatal(err)
			}
			script := prefix + recoveryHarnessOverrides(paths) + "\nweb_port=" + test.requested + "\nallow_downgrade=0\nload_installation_record\nprintf '%s\\n' \"$installed_port\"\n"
			path := filepath.Join(t.TempDir(), "load-port.sh")
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Env = append(os.Environ(), "TEST_TOOLS=/usr/bin", fmt.Sprintf("TEST_UID=%d", os.Getuid()), fmt.Sprintf("TEST_GID=%d", os.Getgid()))
			output, err := command.CombinedOutput()
			if err != nil || strings.TrimSpace(string(output)) != test.want {
				t.Fatalf("loaded port = %q, %v; want %q", output, err, test.want)
			}
		})
	}
}

func TestRenderedInstallerRequiresTwoWayStateSchemaCompatibility(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name                                                                    string
		durable, previousMinimum, previousCurrent, targetMinimum, targetCurrent int
		wantFailure                                                             string
	}{
		{name: "compatible", durable: 1, previousMinimum: 1, previousCurrent: 2, targetMinimum: 1, targetCurrent: 2},
		{name: "empty still needs rollback compatibility", durable: 0, previousMinimum: 1, previousCurrent: 1, targetMinimum: 2, targetCurrent: 2, wantFailure: "cannot roll back"},
		{name: "target cannot read durable", durable: 1, previousMinimum: 1, previousCurrent: 2, targetMinimum: 2, targetCurrent: 2, wantFailure: "cannot read durable schema"},
		{name: "previous cannot roll back target", durable: 1, previousMinimum: 1, previousCurrent: 1, targetMinimum: 1, targetCurrent: 2, wantFailure: "cannot roll back"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := prefix + fmt.Sprintf(`
installation_action=upgrade
current_state_schema=%d
previous_schema_minimum=%d
previous_schema_current=%d
minimum_schema=%d
state_schema=%d
validate_lifecycle_compatibility
`, test.durable, test.previousMinimum, test.previousCurrent, test.targetMinimum, test.targetCurrent)
			path := filepath.Join(t.TempDir(), "schema.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("/bin/sh", path).CombinedOutput()
			if test.wantFailure == "" {
				if err != nil {
					t.Fatalf("compatible schema rejected: %v\n%s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), test.wantFailure) {
				t.Fatalf("schema did not fail with %q: %v\n%s", test.wantFailure, err, output)
			}
		})
	}
}

func TestRenderedInstallerRejectsHostileFreshPathsWithoutChangingThem(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name   string
		create func(*testing.T, string)
	}{
		{
			name: "regular file",
			create: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("preserve\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directory",
			create: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "symbolic link",
			create: func(t *testing.T, path string) {
				if err := os.Symlink("outside", path); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "managed-target")
			test.create(t, target)
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			harness := prefix + "\nrequire_fresh_path \"$TARGET\"\n"
			path := filepath.Join(t.TempDir(), "path-preflight.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Env = append(os.Environ(), "TARGET="+target)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("hostile path unexpectedly passed:\n%s", output)
			}
			after, statErr := os.Lstat(target)
			if statErr != nil {
				t.Fatalf("hostile path was removed: %v", statErr)
			}
			if before.Mode() != after.Mode() || before.Size() != after.Size() {
				t.Fatalf("hostile path changed: before=%v/%d after=%v/%d", before.Mode(), before.Size(), after.Mode(), after.Size())
			}
		})
	}
}

func TestRenderedInstallerRejectsUnsafeSystemDestinationAncestors(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name        string
		prepare     func(*testing.T, string)
		uidOffset   int
		wantFailure string
	}{
		{
			name: "safe directory",
			prepare: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "wrong owner",
			prepare: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
			uidOffset:   1,
			wantFailure: "must be owned by root:root",
		},
		{
			name: "group writable",
			prepare: func(t *testing.T, path string) {
				if err := os.Chmod(path, 0o775); err != nil {
					t.Fatal(err)
				}
			},
			wantFailure: "group- or other-writable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			test.prepare(t, directory)
			expectedUID := os.Getuid() + test.uidOffset
			harness := prefix + fmt.Sprintf("\nROOT_UID=%d\nROOT_GID=%d\nrequire_safe_system_ancestor %q\n", expectedUID, os.Getgid(), directory)
			path := filepath.Join(t.TempDir(), "ancestor.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("/bin/sh", path).CombinedOutput()
			if test.wantFailure == "" {
				if err != nil {
					t.Fatalf("safe ancestor rejected: %v\n%s", err, output)
				}
				return
			}
			if err == nil || !strings.Contains(string(output), test.wantFailure) {
				t.Fatalf("unsafe ancestor did not fail with %q: %v\n%s", test.wantFailure, err, output)
			}
		})
	}
}

func TestRenderedInstallerChecksEveryFreshDestinationAndCollision(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name     string
		target   func(recoveryPaths) string
		kind     string
		getent   string
		portBusy bool
		required string
	}{
		{name: "install directory", target: func(p recoveryPaths) string { return p.install }, kind: "directory", required: "path collision"},
		{name: "config file", target: func(p recoveryPaths) string { return p.config }, kind: "file", required: "path collision"},
		{name: "state symlink", target: func(p recoveryPaths) string { return p.state }, kind: "symlink", required: "managed path is a symlink"},
		{name: "jobs directory", target: func(p recoveryPaths) string { return p.jobs }, kind: "directory", required: "path collision"},
		{name: "metadata directory", target: func(p recoveryPaths) string { return p.metadata }, kind: "directory", required: "path collision"},
		{name: "runtime directory", target: func(p recoveryPaths) string { return p.runtime }, kind: "directory", required: "path collision"},
		{name: "web unit", target: func(p recoveryPaths) string { return filepath.Join(p.systemd, "lifepowerctl.service") }, kind: "file", required: "path collision"},
		{name: "controller unit", target: func(p recoveryPaths) string { return filepath.Join(p.systemd, "lifepowerctl-controller.service") }, kind: "file", required: "path collision"},
		{name: "runner unit", target: func(p recoveryPaths) string { return filepath.Join(p.systemd, "lifepowerctl-job-runner.service") }, kind: "file", required: "path collision"},
		{name: "poweroff unit", target: func(p recoveryPaths) string { return filepath.Join(p.systemd, "lifepowerctl-poweroff.service") }, kind: "file", required: "path collision"},
		{name: "tmpfiles policy", target: func(p recoveryPaths) string { return p.tmpfiles }, kind: "file", required: "path collision"},
		{name: "sudo policy", target: func(p recoveryPaths) string { return p.sudoers }, kind: "file", required: "path collision"},
		{name: "service user", getent: "service-user", required: "account collision"},
		{name: "service group", getent: "service-group", required: "group collision"},
		{name: "jobs group", getent: "jobs-group", required: "group collision"},
		{name: "port", portBusy: true, required: "TCP port 18081 is already listening"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			paths := recoveryHarnessPaths(root)
			var target string
			if test.target != nil {
				target = test.target(paths)
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				switch test.kind {
				case "directory":
					if err := os.Mkdir(target, 0o755); err != nil {
						t.Fatal(err)
					}
				case "file":
					if err := os.WriteFile(target, []byte("preserve\n"), 0o640); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(root, target); err != nil {
						t.Fatal(err)
					}
				}
			}
			tools := writeFreshCollisionTools(t, test.getent, test.portBusy)
			harness := prefix + recoveryHarnessOverrides(paths)
			if test.portBusy {
				harness += "\nweb_port=18081\n"
			}
			harness += "\ncheck_fresh_destinations\n"
			path := filepath.Join(t.TempDir(), "collision.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Env = append(os.Environ(), "TEST_TOOLS="+tools, fmt.Sprintf("TEST_UID=%d", os.Getuid()), fmt.Sprintf("TEST_GID=%d", os.Getgid()))
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.required) {
				t.Fatalf("collision did not fail with %q: %v\n%s", test.required, err, output)
			}
			if target != "" {
				if _, err := os.Lstat(target); err != nil {
					t.Fatalf("preflight changed hostile destination: %v", err)
				}
			}
		})
	}
}

func writeFreshCollisionTools(t *testing.T, collision string, portBusy bool) string {
	t.Helper()
	tools := t.TempDir()
	getent := `#!/bin/sh
case "$TEST_COLLISION:$1:${2-}" in
service-user:passwd:lifepowerctl) printf '%s\n' 'lifepowerctl:x:900:900::/unexpected:/bin/sh'; exit 0 ;;
service-group:group:lifepowerctl) printf '%s\n' 'lifepowerctl:x:900:someone'; exit 0 ;;
jobs-group:group:lifepowerctl-jobs) printf '%s\n' 'lifepowerctl-jobs:x:901:someone'; exit 0 ;;
esac
exit 2
`
	ss := "#!/bin/sh\nexit 0\n"
	if portBusy {
		ss = "#!/bin/sh\nprintf '%s\\n' 'LISTEN 0 128 127.0.0.1:18081 0.0.0.0:*'\n"
	}
	for name, body := range map[string]string{"getent": getent, "ss": ss} {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TEST_COLLISION", collision)
	return tools
}

func TestRenderedInstallerRecoversEveryRecordedMutationBoundary(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	phases := []string{
		"metadata", "service-group", "jobs-group", "service-user", "memberships", "policy",
		"directories", "payload", "units", "setup", "enabled", "started", "healthy",
	}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			work, err := os.MkdirTemp("/tmp", "lifepowerctl-install.")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(work) })
			if err := os.Chmod(work, 0o700); err != nil {
				t.Fatal(err)
			}
			tools := writeRecoveryTools(t)
			paths := recoveryHarnessPaths(root)
			for _, directory := range []string{paths.install, paths.config, paths.state, paths.jobs, paths.runtime, paths.powerRuntime, paths.systemd, filepath.Dir(paths.tmpfiles), filepath.Dir(paths.sudoers)} {
				if err := os.MkdirAll(directory, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{
				filepath.Join(paths.systemd, "lifepowerctl.service"),
				filepath.Join(paths.systemd, "lifepowerctl-controller.service"),
				filepath.Join(paths.systemd, "lifepowerctl-job-runner.service"),
				filepath.Join(paths.systemd, "lifepowerctl-poweroff.service"),
				paths.tmpfiles, paths.sudoers,
			} {
				if err := os.WriteFile(path, []byte("managed\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			sentinel := filepath.Join(root, "unrelated")
			if err := os.WriteFile(sentinel, []byte("preserve\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			interrupted := strings.Replace(prefix, "failure_point() { :; }", `failure_point() { kill -KILL "$$"; }`, 1) + recoveryHarnessOverrides(paths) + `
job_user=tester
web_lifecycle=enabled
work_directory="$TEST_WORK"
mkdir -m 0700 "$METADATA_ROOT"
write_transaction "$TEST_PHASE"
printf '%s\n' unexpected-success
`
			interruptedPath := filepath.Join(t.TempDir(), "interrupt.sh")
			if err := os.WriteFile(interruptedPath, []byte(interrupted), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", interruptedPath)
			command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, tools, phase)...)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("interruption reported success:\n%s", output)
			}
			if strings.Contains(string(output), "unexpected-success") {
				t.Fatalf("interruption crossed the recorded boundary:\n%s", output)
			}
			marker, err := os.ReadFile(filepath.Join(paths.metadata, "transaction"))
			if err != nil {
				t.Fatalf("durable marker missing after interruption: %v", err)
			}
			if !strings.Contains(string(marker), "phase="+phase+"\n") {
				t.Fatalf("durable marker did not record %s:\n%s", phase, marker)
			}

			recovery := prefix + recoveryHarnessOverrides(paths) + "\nrecover_fresh\n"
			recoveryPath := filepath.Join(t.TempDir(), "recover.sh")
			if err := os.WriteFile(recoveryPath, []byte(recovery), 0o700); err != nil {
				t.Fatal(err)
			}
			command = exec.Command("/bin/sh", recoveryPath)
			command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, tools, phase)...)
			output, err = command.CombinedOutput()
			if err != nil {
				t.Fatalf("recovery failed: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "Interrupted fresh install recovered") {
				t.Fatalf("recovery did not report its bounded result:\n%s", output)
			}
			for _, path := range []string{paths.metadata, paths.install, paths.config, paths.state, paths.jobs, paths.runtime, paths.powerRuntime, work} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("recovery left managed path %s: %v", path, err)
				}
			}
			if value, err := os.ReadFile(sentinel); err != nil || string(value) != "preserve\n" {
				t.Fatalf("recovery changed unrelated file: %q, %v", value, err)
			}
		})
	}
}

func TestRenderedInstallerRollsBackCaughtFailureAndSignalAtEveryBoundary(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	phases := []string{
		"metadata", "service-group", "jobs-group", "service-user", "memberships", "policy",
		"directories", "payload", "units", "setup", "enabled", "started", "healthy",
	}
	modes := []struct {
		name string
		hook string
	}{
		{name: "failure", hook: `failure_point() { return 1; }`},
		{name: "signal", hook: `failure_point() { kill -TERM "$$"; }`},
	}
	for _, mode := range modes {
		for _, phase := range phases {
			t.Run(mode.name+"/"+phase, func(t *testing.T) {
				root := t.TempDir()
				work, err := os.MkdirTemp("/tmp", "lifepowerctl-install.")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(work) })
				if err := os.Chmod(work, 0o700); err != nil {
					t.Fatal(err)
				}
				tools := writeRecoveryTools(t)
				paths := recoveryHarnessPaths(root)
				harness := strings.Replace(prefix, "failure_point() { :; }", mode.hook, 1) + recoveryHarnessOverrides(paths) + `
job_user=tester
web_lifecycle=enabled
work_directory="$TEST_WORK"
mkdir -m 0700 "$METADATA_ROOT"
rollback_authorized=1
trap 'cleanup $?' EXIT
trap 'cleanup 1' HUP INT TERM
write_transaction "$TEST_PHASE"
printf '%s\n' unexpected-success
`
				path := filepath.Join(t.TempDir(), "caught.sh")
				if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
					t.Fatal(err)
				}
				command := exec.Command("/bin/sh", path)
				command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, tools, phase)...)
				output, err := command.CombinedOutput()
				if err == nil || strings.Contains(string(output), "unexpected-success") {
					t.Fatalf("%s did not fail closed: %v\n%s", mode.name, err, output)
				}
				if !strings.Contains(string(output), "Rolling back the incomplete fresh install") {
					t.Fatalf("%s did not run rollback:\n%s", mode.name, output)
				}
				for _, path := range []string{paths.metadata, work} {
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("%s left managed path %s: %v", mode.name, path, err)
					}
				}
			})
		}
	}
}

func TestRenderedInstallerRecoveryRefusesTamperedStateWithoutChangingIt(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name     string
		mutate   func(*testing.T, recoveryPaths, string)
		required string
	}{
		{
			name: "marker mode",
			mutate: func(t *testing.T, paths recoveryPaths, work string) {
				if err := os.Chmod(filepath.Join(paths.metadata, "transaction"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			required: "transaction marker ownership or mode is unsafe",
		},
		{
			name: "different release",
			mutate: func(t *testing.T, paths recoveryPaths, work string) {
				path := filepath.Join(paths.metadata, "transaction")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = bytes.Replace(data, []byte(testReleaseCommit), []byte("ffffffffffffffffffffffffffffffffffffffff"), 1)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			required: "transaction marker belongs to a different installer release",
		},
		{
			name: "duplicate field",
			mutate: func(t *testing.T, paths recoveryPaths, work string) {
				path := filepath.Join(paths.metadata, "transaction")
				file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("phase=healthy\n"); err != nil {
					_ = file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			},
			required: "transaction marker has unexpected fields",
		},
		{
			name: "unexpected metadata",
			mutate: func(t *testing.T, paths recoveryPaths, work string) {
				if err := os.WriteFile(filepath.Join(paths.metadata, "foreign"), []byte("preserve\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			required: "transaction metadata directory contains an unexpected entry",
		},
		{
			name: "managed symlink",
			mutate: func(t *testing.T, paths recoveryPaths, work string) {
				if err := os.Symlink(filepath.Dir(paths.install), paths.install); err != nil {
					t.Fatal(err)
				}
			},
			required: "recovery refused a managed symlink",
		},
		{
			name: "completed install",
			mutate: func(t *testing.T, paths recoveryPaths, work string) {
				if err := os.WriteFile(filepath.Join(paths.metadata, "installed"), []byte("format=1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			required: "recovery refused a completed install",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			work, err := os.MkdirTemp("/tmp", "lifepowerctl-install.")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(work) })
			if err := os.Chmod(work, 0o700); err != nil {
				t.Fatal(err)
			}
			tools := writeRecoveryTools(t)
			paths := recoveryHarnessPaths(root)
			create := prefix + recoveryHarnessOverrides(paths) + `
job_user=tester
web_lifecycle=enabled
work_directory="$TEST_WORK"
mkdir -m 0700 "$METADATA_ROOT"
write_transaction metadata
`
			createPath := filepath.Join(t.TempDir(), "create-marker.sh")
			if err := os.WriteFile(createPath, []byte(create), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", createPath)
			command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, tools, "metadata")...)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("create marker: %v\n%s", err, output)
			}
			test.mutate(t, paths, work)

			recoverScript := prefix + recoveryHarnessOverrides(paths) + "\nrecover_fresh\n"
			recoverPath := filepath.Join(t.TempDir(), "recover.sh")
			if err := os.WriteFile(recoverPath, []byte(recoverScript), 0o700); err != nil {
				t.Fatal(err)
			}
			command = exec.Command("/bin/sh", recoverPath)
			command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, tools, "metadata")...)
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.required) {
				t.Fatalf("tampered recovery did not fail with %q: %v\n%s", test.required, err, output)
			}
			if _, err := os.Lstat(filepath.Join(paths.metadata, "transaction")); err != nil {
				t.Fatalf("refused recovery changed the marker: %v", err)
			}
			if _, err := os.Lstat(work); err != nil {
				t.Fatalf("refused recovery changed the work directory: %v", err)
			}
		})
	}
}

func TestRenderedInstallerRecoveryPreservesMarkerUntilRollbackCompletes(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	root := t.TempDir()
	work, err := os.MkdirTemp("/tmp", "lifepowerctl-install.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	if err := os.Chmod(work, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := recoveryHarnessPaths(root)
	if err := os.MkdirAll(paths.systemd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.systemd, "lifepowerctl.service"), []byte("managed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	goodTools := writeRecoveryTools(t)
	create := prefix + recoveryHarnessOverrides(paths) + `
job_user=tester
web_lifecycle=enabled
work_directory="$TEST_WORK"
mkdir -m 0700 "$METADATA_ROOT"
write_transaction units
`
	createPath := filepath.Join(t.TempDir(), "create-marker.sh")
	if err := os.WriteFile(createPath, []byte(create), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", createPath)
	command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, goodTools, "units")...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create marker: %v\n%s", err, output)
	}

	failingTools := writeRecoveryTools(t)
	if err := os.WriteFile(filepath.Join(failingTools, "systemctl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	recoverScript := prefix + recoveryHarnessOverrides(paths) + "\nrecover_fresh\n"
	recoverPath := filepath.Join(t.TempDir(), "recover.sh")
	if err := os.WriteFile(recoverPath, []byte(recoverScript), 0o700); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("/bin/sh", recoverPath)
	command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, failingTools, "units")...)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "rollback was incomplete") {
		t.Fatalf("incomplete rollback did not fail closed: %v\n%s", err, output)
	}
	if _, err := os.Lstat(filepath.Join(paths.metadata, "transaction")); err != nil {
		t.Fatalf("incomplete rollback removed its marker: %v", err)
	}

	command = exec.Command("/bin/sh", recoverPath)
	command.Env = append(os.Environ(), recoveryHarnessEnvironment(root, work, goodTools, "units")...)
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("bounded recovery retry failed: %v\n%s", err, output)
	}
	for _, path := range []string{paths.metadata, work} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("completed retry left %s: %v", path, err)
		}
	}
}

func TestRenderedInstallerLifecycleUpdatePreservesDataAndRestoresFailedUpdate(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name, failurePhase, wantVersion, wantCommit string
		wantFailure                                 bool
		badHealth                                   bool
		legacyPath                                  bool
	}{
		{name: "successful update", wantVersion: testReleaseVersion, wantCommit: testReleaseCommit, legacyPath: true},
		{name: "failed health restores previous", failurePhase: "started", wantVersion: "v0.0.9", wantCommit: strings.Repeat("1", 40), wantFailure: true},
		{name: "unhealthy target restores previous", badHealth: true, wantVersion: "v0.0.9", wantCommit: strings.Repeat("1", 40), wantFailure: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLifecycleFixture(t)
			if test.legacyPath {
				if err := os.Remove(filepath.Join(fixture.paths.systemd, "lifepowerctl-poweroff.path")); err != nil {
					t.Fatal(err)
				}
			}
			if test.badHealth {
				writeLifecycleUnhealthyBinary(t, filepath.Join(fixture.payload, "lifepowerctl"), testReleaseVersion, testReleaseCommit)
			}
			hook := `failure_point() { :; }`
			if test.failurePhase != "" {
				hook = fmt.Sprintf(`failure_point() { [ "$1" != %s ] || return 1; }`, test.failurePhase)
			}
			harness := strings.Replace(prefix, "failure_point() { :; }", hook, 1) + recoveryHarnessOverrides(fixture.paths) + `
check_existing_destinations() { :; }
job_user=tester
job_uid=1000
job_gid=1000
job_group=tester
job_home=/tmp
controller_uid="$TEST_UID"
web_lifecycle=enabled
web_port=18081
installed_port=18081
installed_version=v0.0.9
installed_commit=1111111111111111111111111111111111111111
previous_schema_minimum=1
previous_schema_current=1
minimum_schema=1
state_schema=1
current_state_schema=1
current_web_active=active
work_directory="$TEST_WORK"
payload="$TEST_PAYLOAD"
install_complete=0
rollback_authorized=0
rollback_kind=''
lifecycle_backup_started=0
trap 'cleanup $?' EXIT
trap 'cleanup 1' HUP INT TERM
mutate_lifecycle
`
			path := filepath.Join(t.TempDir(), "lifecycle.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Env = append(os.Environ(), fixture.environment("")...)
			output, err := command.CombinedOutput()
			if test.wantFailure {
				if err == nil || !strings.Contains(string(output), "was restored successfully") {
					t.Fatalf("failed update did not restore: %v\n%s", err, output)
				}
			} else if err != nil {
				t.Fatalf("lifecycle update failed: %v\n%s", err, output)
			}
			assertLifecycleFixture(t, fixture, test.wantVersion, test.wantCommit)
			if !test.wantFailure {
				installed := readTestFile(t, filepath.Join(fixture.paths.metadata, "installed"))
				if !strings.Contains(installed, "format=2\n") || !strings.Contains(installed, "port=18081\n") {
					t.Fatalf("successful legacy upgrade did not record adopted port:\n%s", installed)
				}
			}
		})
	}
}

func TestRenderedInstallerRecoversAbruptLifecycleInterruptionAtEveryBoundary(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	for _, phase := range []string{"prepared", "stopped", "payload", "units", "started", "healthy", "committed"} {
		t.Run(phase, func(t *testing.T) {
			fixture := newLifecycleFixture(t)
			hook := `failure_point() { [ "$1" != "$TEST_PHASE" ] || kill -KILL "$$"; }`
			harness := strings.Replace(prefix, "failure_point() { :; }", hook, 1) + recoveryHarnessOverrides(fixture.paths) + `
check_existing_destinations() { :; }
job_user=tester
job_uid=1000
job_gid=1000
job_group=tester
job_home=/tmp
controller_uid="$TEST_UID"
web_lifecycle=enabled
installed_version=v0.0.9
installed_commit=1111111111111111111111111111111111111111
previous_schema_minimum=1
previous_schema_current=1
minimum_schema=1
state_schema=1
current_state_schema=1
current_web_active=active
work_directory="$TEST_WORK"
payload="$TEST_PAYLOAD"
install_complete=0
rollback_authorized=0
rollback_kind=''
lifecycle_backup_started=0
trap 'cleanup $?' EXIT
trap 'cleanup 1' HUP INT TERM
mutate_lifecycle
printf '%s\n' unexpected-success
`
			path := filepath.Join(t.TempDir(), "interrupt-lifecycle.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Env = append(os.Environ(), fixture.environment(phase)...)
			output, err := command.CombinedOutput()
			if err == nil || strings.Contains(string(output), "unexpected-success") {
				t.Fatalf("abrupt lifecycle interruption did not fail closed: %v\n%s", err, output)
			}
			marker, err := os.ReadFile(filepath.Join(fixture.paths.metadata, "transaction"))
			if err != nil || !strings.Contains(string(marker), "phase="+phase+"\n") {
				t.Fatalf("lifecycle marker after interruption: %v\n%s", err, marker)
			}

			recoverScript := prefix + recoveryHarnessOverrides(fixture.paths) + `
check_existing_destinations() { :; }
recover_install
`
			recoverPath := filepath.Join(t.TempDir(), "recover-lifecycle.sh")
			if err := os.WriteFile(recoverPath, []byte(recoverScript), 0o700); err != nil {
				t.Fatal(err)
			}
			command = exec.Command("/bin/sh", recoverPath)
			command.Env = append(os.Environ(), fixture.environment(phase)...)
			output, err = command.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "Interrupted lifecycle update recovered") {
				t.Fatalf("lifecycle recovery failed: %v\n%s", err, output)
			}
			assertLifecycleFixture(t, fixture, "v0.0.9", strings.Repeat("1", 40))
		})
	}
}

func TestRenderedInstallerLifecycleUpdatePreservesStoppedOnDemandWeb(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	fixture := newLifecycleFixture(t)
	installed := "format=1\nversion=v0.0.9\ncommit=" + strings.Repeat("1", 40) + "\njob_user=tester\nweb=on-demand\n"
	if err := os.WriteFile(filepath.Join(fixture.paths.metadata, "installed"), []byte(installed), 0o600); err != nil {
		t.Fatal(err)
	}
	harness := prefix + recoveryHarnessOverrides(fixture.paths) + `
check_existing_destinations() { :; }
job_user=tester
job_uid=1000
job_gid=1000
job_group=tester
job_home=/tmp
controller_uid="$TEST_UID"
web_lifecycle=on-demand
installed_version=v0.0.9
installed_commit=1111111111111111111111111111111111111111
previous_schema_minimum=1
previous_schema_current=1
minimum_schema=1
state_schema=1
current_state_schema=1
current_web_active=inactive
work_directory="$TEST_WORK"
payload="$TEST_PAYLOAD"
install_complete=0
rollback_authorized=0
rollback_kind=''
lifecycle_backup_started=0
trap 'cleanup $?' EXIT
trap 'cleanup 1' HUP INT TERM
mutate_lifecycle
`
	path := filepath.Join(t.TempDir(), "on-demand.sh")
	if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", path)
	command.Env = append(os.Environ(), fixture.environment("")...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("on-demand lifecycle update: %v\n%s", err, output)
	}
	log, err := os.ReadFile(fixture.systemctlLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "start lifepowerctl.service") {
		t.Fatalf("update started stopped on-demand web:\n%s", log)
	}
	installedAfter, err := os.ReadFile(filepath.Join(fixture.paths.metadata, "installed"))
	if err != nil || !strings.Contains(string(installedAfter), "web=on-demand\n") {
		t.Fatalf("on-demand installed record changed: %v\n%s", err, installedAfter)
	}
	assertLifecycleFixture(t, fixture, testReleaseVersion, testReleaseCommit)
}

func TestRenderedInstallerSameVersionExitsBeforeDownloadOrMutation(t *testing.T) {
	root := filepath.Join("..", "..")
	path := filepath.Join(t.TempDir(), "install.sh")
	command := exec.Command("/bin/sh", filepath.Join(root, "scripts", "render-installer.sh"), testReleaseVersion, testReleaseCommit, testReleaseArchive, testReleaseBaseURL, path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render installer: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, main, found := strings.Cut(string(data), "\nnon_interactive=0;")
	if !found {
		t.Fatal("rendered installer main was not found")
	}
	verification := strings.Index(main, `if [ "$installation_action" = same-version ]`)
	work := strings.Index(main, `work_directory=$(mktemp`)
	acquisition := strings.Index(main, "acquire_and_validate")
	mutation := strings.Index(main, "mutate_lifecycle")
	if verification < 0 || work < 0 || acquisition < 0 || mutation < 0 || !(verification < work && work < acquisition && acquisition < mutation) {
		t.Fatalf("same-version verification is not ordered before staging and mutation")
	}
	verificationBlock := main[verification:work]
	if !strings.Contains(verificationBlock, "no files or services were changed") || !strings.Contains(verificationBlock, "exit 0") {
		t.Fatalf("same-version path does not terminate as a no-change verification:\n%s", verificationBlock)
	}
}

func TestRenderedInstallerRefusesUnsafeLifecycleStatusBeforeMutation(t *testing.T) {
	prefix := renderedInstallerPrefix(t)
	tests := []struct {
		name, old, replacement, required string
	}{
		{name: "enabled automation", old: "automation=disabled\n", replacement: "automation=enabled\n", required: "maintenance=safe"},
		{name: "active lifecycle", old: "lifecycle=inactive\n", replacement: "lifecycle=shutdown-intended\n", required: "maintenance=safe"},
		{name: "active job", old: "jobs=idle\n", replacement: "jobs=active\n", required: "jobs=idle"},
		{name: "uncertain wake", old: "maintenance=safe\n", replacement: "maintenance=unsafe\n", required: "maintenance=safe"},
		{name: "power transition", old: "maintenance=safe\n", replacement: "maintenance=unknown\n", required: "maintenance=safe"},
		{name: "recovered controller", old: "controller=ready\n", replacement: "controller=recovered\n", required: "controller=ready"},
		{name: "unavailable board", old: "board=ready\n", replacement: "board=unavailable\n", required: "board=ready"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newLifecycleFixture(t)
			binaryPath := filepath.Join(fixture.paths.install, "lifepowerctl")
			binary, err := os.ReadFile(binaryPath)
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "enabled automation" || test.name == "active lifecycle" {
				binary = bytes.Replace(binary, []byte("maintenance=safe\n"), []byte("maintenance=unsafe\n"), 1)
			}
			binary = bytes.Replace(binary, []byte(test.old), []byte(test.replacement), 1)
			if err := os.WriteFile(binaryPath, binary, 0o755); err != nil {
				t.Fatal(err)
			}
			harness := prefix + recoveryHarnessOverrides(fixture.paths) + `
installed_version=v0.0.9
installed_commit=1111111111111111111111111111111111111111
installed_web=enabled
job_user=tester
read_current_installation
printf '%s\n' unexpected-success
`
			path := filepath.Join(t.TempDir(), "unsafe-status.sh")
			if err := os.WriteFile(path, []byte(harness), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("/bin/sh", path)
			command.Env = append(os.Environ(), fixture.environment("")...)
			output, err := command.CombinedOutput()
			if err == nil || strings.Contains(string(output), "unexpected-success") || !strings.Contains(string(output), test.required) {
				t.Fatalf("unsafe lifecycle did not fail before mutation with %q: %v\n%s", test.required, err, output)
			}
			if _, err := os.Lstat(filepath.Join(fixture.paths.metadata, "transaction")); !os.IsNotExist(err) {
				t.Fatalf("unsafe preflight created transaction: %v", err)
			}
		})
	}
}

type lifecycleFixture struct {
	root, work, tools, payload, systemctlLog string
	paths                                    recoveryPaths
}

func newLifecycleFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	root := t.TempDir()
	paths := recoveryHarnessPaths(root)
	work, err := os.MkdirTemp("/tmp", "lifepowerctl-install.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	if err := os.Chmod(work, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{paths.install, paths.config, paths.state, filepath.Join(paths.state, "activity"), paths.jobs, filepath.Join(paths.jobs, "output"), paths.metadata, paths.runtime, paths.powerRuntime, paths.systemd, filepath.Dir(paths.tmpfiles), filepath.Dir(paths.sudoers)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(paths.metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	oldCommit := strings.Repeat("1", 40)
	writeLifecycleBinary(t, filepath.Join(paths.install, "lifepowerctl"), "v0.0.9", oldCommit)
	writeLifecycleUninstaller(t, filepath.Join(paths.install, "uninstall.sh"), "v0.0.9", oldCommit)
	for _, path := range []string{filepath.Join(paths.install, "install-manifest"), filepath.Join(paths.systemd, "lifepowerctl-controller.service"), filepath.Join(paths.systemd, "lifepowerctl-job-runner.service"), filepath.Join(paths.systemd, "lifepowerctl-poweroff.service"), filepath.Join(paths.systemd, "lifepowerctl-poweroff.path"), filepath.Join(paths.systemd, "lifepowerctl.service"), paths.tmpfiles} {
		if err := os.WriteFile(path, []byte("previous "+filepath.Base(path)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.sudoers, []byte("previous sudoers\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	installed := "format=1\nversion=v0.0.9\ncommit=" + oldCommit + "\njob_user=tester\nweb=enabled\n"
	if err := os.WriteFile(filepath.Join(paths.metadata, "installed"), []byte(installed), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(paths.config, "config.json"):     "preserved-config\n",
		filepath.Join(paths.state, "controller.json"):  "preserved-state\n",
		filepath.Join(paths.state, "activity", "one"):  "preserved-activity\n",
		filepath.Join(paths.jobs, "jobs.json"):         "preserved-jobs\n",
		filepath.Join(paths.jobs, "output", "run.log"): "preserved-output\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	payload := filepath.Join(root, "payload")
	if err := os.Mkdir(payload, 0o700); err != nil {
		t.Fatal(err)
	}
	writeLifecycleBinary(t, filepath.Join(payload, "lifepowerctl"), testReleaseVersion, testReleaseCommit)
	writeLifecycleUninstaller(t, filepath.Join(work, "uninstall.sh"), testReleaseVersion, testReleaseCommit)
	tools := writeLifecycleTools(t)
	return lifecycleFixture{root: root, work: work, tools: tools, payload: payload, systemctlLog: filepath.Join(root, "systemctl.log"), paths: paths}
}

func (fixture lifecycleFixture) environment(phase string) []string {
	return []string{
		"TEST_ROOT=" + fixture.root,
		"TEST_WORK=" + fixture.work,
		"TEST_TOOLS=" + fixture.tools,
		"TEST_PAYLOAD=" + fixture.payload,
		"TEST_PHASE=" + phase,
		"TEST_SYSTEMCTL_LOG=" + fixture.systemctlLog,
		"TEST_RUNTIME=" + fixture.paths.runtime,
		"TEST_POWER_RUNTIME=" + fixture.paths.powerRuntime,
		fmt.Sprintf("TEST_UID=%d", os.Getuid()),
		fmt.Sprintf("TEST_GID=%d", os.Getgid()),
	}
}

func writeLifecycleBinary(t *testing.T, path, version, commit string) {
	t.Helper()
	body := fmt.Sprintf(`#!/bin/sh
case "${1-}:${2-}" in
version:--machine)
cat <<'EOF'
format=1
product=lifepowerctl
version=%s
commit=%s
source_state=clean
target_os=linux
target_arch=arm64
go=go1.23.2
state_schema_current=1
state_schema_min_readable=1
EOF
;;
admin:status)
cat <<'EOF'
format=1
product=lifepowerctl
controller=ready
running_version=%s
running_commit=%s
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
board_product=LiFePO4wered/Pi+
register_set=7
maintenance=safe
EOF
;;
*) exit 2 ;;
esac
`, version, commit, version, commit)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeLifecycleUnhealthyBinary(t *testing.T, path, version, commit string) {
	t.Helper()
	body := fmt.Sprintf(`#!/bin/sh
if [ "${1-}:${2-}" = version:--machine ]; then
cat <<'EOF'
format=1
product=lifepowerctl
version=%s
commit=%s
source_state=clean
target_os=linux
target_arch=arm64
go=go1.23.2
state_schema_current=1
state_schema_min_readable=1
EOF
exit 0
fi
exit 1
`, version, commit)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeLifecycleUninstaller(t *testing.T, path, version, commit string) {
	t.Helper()
	body := fmt.Sprintf("#!/bin/sh\n[ \"${1-}\" = --release-info ] || exit 2\nprintf '%%s\\n' format=1 product=lifepowerctl repository=sinoia/lifepowerctl version=%s commit=%s\n", version, commit)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeLifecycleTools(t *testing.T) string {
	t.Helper()
	tools := t.TempDir()
	commands := map[string]string{
		"id": `#!/bin/sh
if [ "${1-}" = -u ] && [ "$#" -eq 1 ]; then echo 0; exit 0; fi
if [ "${1-}" = -u ]; then echo "$TEST_UID"; exit 0; fi
/usr/bin/id "$@"
`,
		"install": `#!/bin/sh
set -eu
arguments=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o|-g) shift 2 ;;
        *) arguments="$arguments '$1'"; shift ;;
    esac
done
eval "exec /usr/bin/install $arguments"
`,
		"runuser": `#!/bin/sh
set -eu
[ "$1" = -u ]; shift 2
[ "$1" = -- ]; shift
exec "$@"
`,
		"sleep": "#!/bin/sh\nexit 0\n",
		"systemctl": `#!/bin/sh
[ -z "${TEST_SYSTEMCTL_LOG-}" ] || printf '%s\n' "$*" >>"$TEST_SYSTEMCTL_LOG"
case "${1-}" in
show) echo inactive ;;
is-active) [ "${2-}" = --quiet ] || echo active ;;
is-enabled) echo enabled ;;
esac
exit 0
`,
		"systemd-analyze":  "#!/bin/sh\nexit 0\n",
		"systemd-tmpfiles": "#!/bin/sh\n[ \"${1-}\" != --create ] || mkdir -p \"$TEST_RUNTIME\" \"$TEST_POWER_RUNTIME\"\nexit 0\n",
		"visudo":           "#!/bin/sh\nexit 0\n",
		"wget":             "#!/bin/sh\nexit 0\n",
	}
	for name, body := range commands {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return tools
}

func assertLifecycleFixture(t *testing.T, fixture lifecycleFixture, version, commit string) {
	t.Helper()
	command := exec.Command(filepath.Join(fixture.paths.install, "lifepowerctl"), "version", "--machine")
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "version="+version+"\n") || !strings.Contains(string(output), "commit="+commit+"\n") {
		t.Fatalf("installed payload identity: %v\n%s", err, output)
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
	for _, path := range []string{filepath.Join(fixture.paths.metadata, "transaction"), filepath.Join(fixture.paths.metadata, "previous")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("completed lifecycle left rollback path %s: %v", path, err)
		}
	}
}

type recoveryPaths struct {
	install, config, state, jobs, metadata, runtime, powerRuntime, systemd, tmpfiles, sudoers string
}

func recoveryHarnessPaths(root string) recoveryPaths {
	return recoveryPaths{
		install:      filepath.Join(root, "usr-lib-lifepowerctl"),
		config:       filepath.Join(root, "etc-lifepowerctl"),
		state:        filepath.Join(root, "var-lib-lifepowerctl"),
		jobs:         filepath.Join(root, "var-lib-lifepowerctl-jobs"),
		metadata:     filepath.Join(root, "var-lib-lifepowerctl-installer"),
		runtime:      filepath.Join(root, "run-lifepowerctl"),
		powerRuntime: filepath.Join(root, "run-lifepowerctl-power"),
		systemd:      filepath.Join(root, "systemd"),
		tmpfiles:     filepath.Join(root, "tmpfiles", "lifepowerctl.conf"),
		sudoers:      filepath.Join(root, "sudoers", "lifepowerctl"),
	}
}

func recoveryHarnessOverrides(paths recoveryPaths) string {
	return fmt.Sprintf(`
PATH="$TEST_TOOLS:/usr/bin:/bin"
export PATH
INSTALL_ROOT=%q
CONFIG_ROOT=%q
STATE_ROOT=%q
JOBS_ROOT=%q
METADATA_ROOT=%q
RUNTIME_ROOT=%q
POWER_RUNTIME_ROOT=%q
SYSTEMD_ROOT=%q
TMPFILES_POLICY=%q
SUDOERS_POLICY=%q
TRANSACTION_FILE="${METADATA_ROOT}/transaction"
PREVIOUS_ROOT="${METADATA_ROOT}/previous"
ROOT_UID="$TEST_UID"
ROOT_GID="$TEST_GID"
`, paths.install, paths.config, paths.state, paths.jobs, paths.metadata, paths.runtime, paths.powerRuntime, paths.systemd, paths.tmpfiles, paths.sudoers)
}

func recoveryHarnessEnvironment(root, work, tools, phase string) []string {
	return []string{
		"TEST_ROOT=" + root,
		"TEST_WORK=" + work,
		"TEST_TOOLS=" + tools,
		"TEST_PHASE=" + phase,
		"TEST_POWER_RUNTIME=" + filepath.Join(root, "run-lifepowerctl-power"),
		fmt.Sprintf("TEST_UID=%d", os.Getuid()),
		fmt.Sprintf("TEST_GID=%d", os.Getgid()),
	}
}

func writeRecoveryTools(t *testing.T) string {
	t.Helper()
	tools := t.TempDir()
	commands := map[string]string{
		"id":        "#!/bin/sh\n[ \"${1-}\" = -u ] && { echo 0; exit 0; }\nexit 1\n",
		"getent":    "#!/bin/sh\nexit 2\n",
		"systemctl": "#!/bin/sh\ncase \"${1-}\" in show) echo inactive ;; is-enabled) echo disabled ;; esac\nexit 0\n",
	}
	for name, body := range commands {
		if err := os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return tools
}

func newAcquisitionFixture(t *testing.T) string {
	t.Helper()
	fixture := t.TempDir()
	writeInstallerFixtureArchive(t, filepath.Join(fixture, testReleaseArchive))
	uninstallerSource, err := os.ReadFile(filepath.Join("..", "..", "packaging", "release", "uninstall.sh.in"))
	if err != nil {
		t.Fatal(err)
	}
	uninstaller := strings.NewReplacer(
		"@RELEASE_VERSION@", testReleaseVersion,
		"@RELEASE_COMMIT@", testReleaseCommit,
	).Replace(string(uninstallerSource))
	if err := os.WriteFile(filepath.Join(fixture, "uninstall.sh"), []byte(uninstaller), 0o755); err != nil {
		t.Fatal(err)
	}
	writeChecksums(t, fixture, []string{testReleaseArchive, "uninstall.sh"})
	return fixture
}

func fixtureChecksum(t *testing.T, fixture, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture, name))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name)
}

func writeChecksums(t *testing.T, fixture string, names []string) {
	t.Helper()
	var manifest strings.Builder
	for _, name := range names {
		manifest.WriteString(fixtureChecksum(t, fixture, name))
	}
	if err := os.WriteFile(filepath.Join(fixture, "checksums.txt"), []byte(manifest.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runAcquisitionHarness(t *testing.T, fixture string) ([]byte, error) {
	t.Helper()

	tools := t.TempDir()
	wget := `#!/bin/sh
set -eu
destination=''
url=''
while [ "$#" -gt 0 ]; do
    case "$1" in
        -O) destination=$2; shift ;;
        -*) ;;
        *) url=$1 ;;
    esac
    shift
done
test -n "$destination"
test -n "$url"
/bin/cp "$FIXTURE_ROOT/${url##*/}" "$destination"
`
	if err := os.WriteFile(filepath.Join(tools, "wget"), []byte(wget), 0o755); err != nil {
		t.Fatal(err)
	}
	prefix := renderedInstallerPrefix(t)
	harness := prefix + `
PATH="$TEST_TOOLS:/usr/bin:/bin"
export PATH
asset_directory=''
work_directory=$(mktemp -d /tmp/lifepowerctl-acquire.XXXXXX)
trap 'rm -rf -- "$work_directory"' EXIT HUP INT TERM
acquire_and_validate
test -x "$payload/lifepowerctl"
`
	harnessPath := filepath.Join(t.TempDir(), "acquire.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", harnessPath)
	command.Env = append(os.Environ(), "TEST_TOOLS="+tools, "FIXTURE_ROOT="+fixture)
	return command.CombinedOutput()
}

func runAssetDirectoryHarness(t *testing.T, fixture string) ([]byte, error) {
	t.Helper()
	return runAssetDirectoryHarnessWithMetadata(t, fixture, "root:root", "700", "600")
}

func runAssetDirectoryHarnessWithMetadata(t *testing.T, fixture, owner, directoryMode, fileMode string) ([]byte, error) {
	t.Helper()

	tools := t.TempDir()
	stat := `#!/bin/sh
set -eu
case "$2" in
    %U:%G) printf '%s\n' "$TEST_STAT_OWNER" ;;
    %a) if [ -d "$3" ]; then printf '%s\n' "$TEST_DIRECTORY_MODE"; else printf '%s\n' "$TEST_FILE_MODE"; fi ;;
    *) exec /usr/bin/stat "$@" ;;
esac
`
	if err := os.WriteFile(filepath.Join(tools, "stat"), []byte(stat), 0o755); err != nil {
		t.Fatal(err)
	}
	prefix := renderedInstallerPrefix(t)
	harness := prefix + `
PATH="$TEST_TOOLS:/usr/bin:/bin"
export PATH
asset_directory="$FIXTURE_ROOT"
work_directory=$(mktemp -d /tmp/lifepowerctl-acquire.XXXXXX)
trap 'rm -rf -- "$work_directory"' EXIT HUP INT TERM
acquire_and_validate
test -x "$payload/lifepowerctl"
`
	harnessPath := filepath.Join(t.TempDir(), "asset-directory.sh")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("/bin/sh", harnessPath)
	command.Env = append(os.Environ(), "TEST_TOOLS="+tools, "FIXTURE_ROOT="+fixture,
		"TEST_STAT_OWNER="+owner, "TEST_DIRECTORY_MODE="+directoryMode, "TEST_FILE_MODE="+fileMode)
	return command.CombinedOutput()
}

func renderedInstallerPrefix(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	renderedPath := filepath.Join(t.TempDir(), "install.sh")
	command := exec.Command("/bin/sh", filepath.Join(root, "scripts", "render-installer.sh"), testReleaseVersion, testReleaseCommit, testReleaseArchive, testReleaseBaseURL, renderedPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("render installer: %v\n%s", err, output)
	}
	rendered, err := os.ReadFile(renderedPath)
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, found := strings.Cut(string(rendered), "\nnon_interactive=0;")
	if !found {
		t.Fatal("could not isolate rendered installer functions")
	}
	return prefix + "\nweb_port=8080\ninstalled_port=8080\n"
}

func writeInstallerFixtureArchive(t *testing.T, destination string) {
	t.Helper()
	writeInstallerFixtureArchiveWithBinary(t, destination, fixtureBinary("target_arch=arm64"))
}

func fixtureBinary(targetLine string) string {
	return `#!/bin/sh
cat <<'EOF'
format=1
product=lifepowerctl
version=v0.1.0-rc.1
commit=0123456789abcdef0123456789abcdef01234567
source_state=clean
target_os=linux
` + targetLine + `
go=go1.23.2
state_schema_current=1
state_schema_min_readable=1
EOF
`
}

func writeInstallerFixtureArchiveWithBinary(t *testing.T, destination, binary string) {
	t.Helper()
	writeFixtureArchiveMembers(t, destination, validFixtureMembers(binary))
}

type fixtureArchiveMember struct {
	name     string
	mode     int64
	typeflag byte
	linkname string
	body     string
}

func validFixtureMembers(binary string) []fixtureArchiveMember {
	manifest := `format=1
product=lifepowerctl
version=v0.1.0-rc.1
commit=0123456789abcdef0123456789abcdef01234567
source_state=clean
target_os=linux
target_arch=arm64
state_schema_current=1
state_schema_min_readable=1
`
	return []fixtureArchiveMember{
		{name: "lifepowerctl/", mode: 0o755, typeflag: tar.TypeDir},
		{name: "lifepowerctl/LICENSE", mode: 0o644, body: "fixture license\n"},
		{name: "lifepowerctl/README.md", mode: 0o644, body: "fixture readme\n"},
		{name: "lifepowerctl/lifepowerctl", mode: 0o755, body: binary},
		{name: "lifepowerctl/release-manifest.txt", mode: 0o644, body: manifest},
	}
}

func writeFixtureArchiveMembers(t *testing.T, destination string, members []fixtureArchiveMember) {
	t.Helper()
	file, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, member := range members {
		typeflag := member.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{Name: member.name, Mode: member.mode, Size: int64(len(member.body)), Typeflag: typeflag, Linkname: member.linkname}
		if typeflag != tar.TypeReg {
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if member.body != "" {
			if _, err := tarWriter.Write([]byte(member.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseArchiveContractIsClosedAndBounded(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "packaging", "release", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	value := string(data)
	for _, required := range []string{
		"lifepowerctl/lifepowerctl",
		"lifepowerctl/LICENSE",
		"lifepowerctl/README.md",
		"lifepowerctl/release-manifest.txt",
		"Links, devices, FIFOs, sockets, duplicate names, absolute paths, parent",
		"archive size is 32 MiB",
		"executable is limited to 24 MiB",
	} {
		if !strings.Contains(value, required) {
			t.Fatalf("release contract missing %q", required)
		}
	}
}
