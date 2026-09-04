package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testInstaller(t *testing.T) (installer, *[]byte) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := []byte("release one")
	i := installer{root: dir, host: "codex", probe: func(string) error { return nil }}
	i.fetch = func(_ context.Context, url string, _ int64) ([]byte, error) {
		asset := "agentx-bridge-mcp_" + runtime.GOOS + "_" + runtime.GOARCH + suffix()
		if strings.HasSuffix(url, "SHA256SUMS") {
			return []byte(digest(binary) + "  " + asset + "\n"), nil
		}
		return binary, nil
	}
	return i, &binary
}
func TestReleasedInstallerRejectsMixedVersionsBeforeSideEffects(t *testing.T) {
	oldVersion := buildVersion
	buildVersion = "0.2.2"
	t.Cleanup(func() { buildVersion = oldVersion })
	root := filepath.Join(t.TempDir(), "not-created")
	err := run([]string{"install", "--host", "codex", "--version", "v0.2.0", "--root", root, "--no-connect", "--plugin"})
	if err == nil || !strings.Contains(err.Error(), "installer release does not match") {
		t.Fatalf("mixed binary/Skill version accepted: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("version rejection created installation files")
	}
}
func TestInstallUpgradeRollbackAndRemoval(t *testing.T) {
	i, b := testInstaller(t)
	for n := 0; n < 2; n++ {
		if err := i.install(context.Background(), "v1.0.0", "", false); err != nil {
			t.Fatal(err)
		}
	}
	*b = []byte("release two")
	if err := i.install(context.Background(), "v2.0.0", "", false); err == nil {
		t.Fatal("implicit replacement")
	}
	if err := i.install(context.Background(), "v2.0.0", "", true); err != nil {
		t.Fatal(err)
	}
	if err := i.rollback(); err != nil {
		t.Fatal(err)
	}
	s, err := i.load()
	if err != nil || s.Version != "v1.0.0" {
		t.Fatalf("rollback state: %v", err)
	}
	if err := i.verify(s); err != nil {
		t.Fatal(err)
	}
	if err := i.uninstall(); err != nil {
		t.Fatal(err)
	}
	if err := i.uninstall(); err != nil {
		t.Fatalf("repeat removal: %v", err)
	}
	if _, err := os.Stat(i.binary()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("binary retained")
	}
}
func TestWorkBuddyDesktopSkillMigrationAndRollback(t *testing.T) {
	for _, changed := range []string{"none", "old", "destination"} {
		t.Run(changed, func(t *testing.T) {
			i, binary := testInstaller(t)
			i.host = "workbuddy"
			t.Setenv("HOME", i.root)
			t.Setenv("USERPROFILE", i.root)
			old := filepath.Join(i.root, ".codebuddy", "skills", "agentx-delivery-network", "SKILL.md")
			next := skillDestination(i.root, "workbuddy", false)
			if next != filepath.Join(i.root, ".workbuddy", "skills", "agentx-delivery-network", "SKILL.md") {
				t.Fatal("incorrect WorkBuddy Desktop Skill directory")
			}
			if err := i.install(context.Background(), "v0.2.0", old, false); err != nil {
				t.Fatal(err)
			}
			if changed != "none" {
				path := old
				if changed == "destination" {
					path = next
				}
				if err := atomicWrite(path, []byte("user content"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := i.install(context.Background(), "v0.2.1", next, true); err == nil {
					t.Fatal("migration replaced user content")
				}
				body, err := os.ReadFile(path)
				if err != nil || string(body) != "user content" {
					t.Fatal("user content changed")
				}
				return
			}
			*binary = []byte("release two")
			if err := i.install(context.Background(), "v0.2.1", next, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("old Skill retained")
			}
			if err := i.rollback(); err != nil {
				t.Fatal(err)
			}
			s, err := i.load()
			if err != nil || s.Version != "v0.2.0" || s.SkillPath != old {
				t.Fatal("migration rollback manifest mismatch")
			}
			if err := i.verify(s); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(next); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("new Skill retained after rollback")
			}
			if err := i.uninstall(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestFailedDownloadChecksumAndProbePreserveInstallation(t *testing.T) {
	for _, failure := range []string{"network", "hash", "probe"} {
		t.Run(failure, func(t *testing.T) {
			i, _ := testInstaller(t)
			if err := i.install(context.Background(), "v1.0.0", "", false); err != nil {
				t.Fatal(err)
			}
			fetch := i.fetch
			switch failure {
			case "network":
				i.fetch = func(context.Context, string, int64) ([]byte, error) { return nil, errors.New("offline") }
			case "hash":
				i.fetch = func(ctx context.Context, url string, max int64) ([]byte, error) {
					if strings.HasSuffix(url, "SHA256SUMS") {
						return fetch(ctx, url, max)
					}
					return []byte("corrupt"), nil
				}
			case "probe":
				i.probe = func(string) error { return errors.New("cannot execute") }
			}
			if err := i.install(context.Background(), "v2.0.0", "", true); err == nil {
				t.Fatal("expected failure")
			}
			s, err := i.load()
			if err != nil || s.Version != "v1.0.0" {
				t.Fatal("installation changed")
			}
			if err := i.verify(s); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestRecoveryAfterInterruptedWriteAndRemoval(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "remove"}[remove], func(t *testing.T) {
			i, _ := testInstaller(t)
			if err := i.install(context.Background(), "v1.0.0", "", false); err != nil {
				t.Fatal(err)
			}
			old, err := os.ReadFile(i.binary())
			if err != nil {
				t.Fatal(err)
			}
			after := []byte("partial upgrade")
			hash := digest(after)
			if remove {
				hash = "ABSENT"
			}
			journal := transaction{Changes: []change{{Path: i.binary(), Before: old, Existed: true, AfterHash: hash, Mode: 0700}}}
			body, _ := json.Marshal(journal)
			if err := atomicWrite(filepath.Join(i.root, "transaction.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			if remove {
				err = os.Remove(i.binary())
			} else {
				err = atomicWrite(i.binary(), after, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := i.recover(); err != nil {
				t.Fatal(err)
			}
			s, err := i.load()
			if err != nil {
				t.Fatal(err)
			}
			if err := i.verify(s); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestUserChangesAndUnsafeJournalPreserved(t *testing.T) {
	i, _ := testInstaller(t)
	if err := i.install(context.Background(), "v1.0.0", "", false); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(i.binary(), []byte("user modified"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := i.install(context.Background(), "v2.0.0", "", true); err == nil {
		t.Fatal("overwrote user binary")
	}
	if err := i.uninstall(); err == nil {
		t.Fatal("removed user binary")
	}
	outside := filepath.Join(filepath.Dir(i.root), "outside-agentx-test")
	body, _ := json.Marshal(transaction{Changes: []change{{Path: outside, Before: []byte("arbitrary"), Existed: true}}})
	if err := atomicWrite(filepath.Join(i.root, "transaction.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.recover(); err == nil {
		t.Fatal("accepted arbitrary journal path")
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrote outside root")
	}
}
func TestUnownedAndModifiedSkillPreserved(t *testing.T) {
	i, _ := testInstaller(t)
	// Isolated user home on each OS; no real Host configuration is touched.
	t.Setenv("HOME", i.root)
	t.Setenv("USERPROFILE", i.root)
	destination := skillDestination(i.root, "codex", false)
	if err := atomicWrite(destination, skill, 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.install(context.Background(), "v1.0.0", destination, false); err == nil {
		t.Fatal("claimed unowned Skill")
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := i.install(context.Background(), "v1.0.0", destination, false); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(destination, []byte("user changes"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := i.install(context.Background(), "v2.0.0", destination, true); err == nil {
		t.Fatal("overwrote changed Skill")
	}
	if err := i.uninstall(); err == nil {
		t.Fatal("removed changed Skill")
	}
}
func TestChecksumAndOriginValidation(t *testing.T) {
	for _, value := range []string{"http://example.test", "https://u:p@example.test", "https://example.test/path", "https://example.test?q=1", "https://example.test#fragment"} {
		if validOrigin(value) {
			t.Fatalf("accepted %s", value)
		}
	}
	if !validOrigin("https://example.test:8443") {
		t.Fatal("rejected HTTPS origin")
	}
	line := digest([]byte("test")) + "  binary\n"
	for _, sums := range []string{"", line + line, "invalid  binary\n"} {
		if _, err := checksum([]byte(sums), "binary"); err == nil {
			t.Fatal("accepted invalid sums")
		}
	}
}
func TestInstallerLockExcludesConcurrentWriter(t *testing.T) {
	i, _ := testInstaller(t)
	unlock, err := lockRoot(i.root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockRoot(i.root); err == nil {
		second()
		t.Fatal("concurrent writer entered")
	}
	unlock()
	last, err := lockRoot(i.root)
	if err != nil {
		t.Fatal(err)
	}
	last()
}
