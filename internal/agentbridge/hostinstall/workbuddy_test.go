package hostinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkBuddyInstallPreservesUnrelatedConfigurationAndUninstallsOwnedEntry(t *testing.T) {
	spec := workBuddyTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(spec.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	initial := `{"theme":"dark","mcpServers":{"existing":{"command":"existing-server","args":["serve"]}}}`
	if err := os.WriteFile(spec.ConfigFile, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewWorkBuddyManager()
	state, err := manager.Install(spec)
	if err != nil || state != InstallationReady {
		t.Fatalf("Install() state=%s error=%v", state, err)
	}
	root, exists, err := loadWorkBuddyConfig(spec.ConfigFile)
	if err != nil || !exists {
		t.Fatalf("load installed config: exists=%t error=%v", exists, err)
	}
	var theme string
	if err := json.Unmarshal(root["theme"], &theme); err != nil || theme != "dark" {
		t.Fatalf("unrelated top-level value was not preserved: %q error=%v", theme, err)
	}
	servers, err := workBuddyServers(root)
	if err != nil || len(servers) != 2 || servers["existing"] == nil || servers[WorkBuddyServerName] == nil {
		t.Fatalf("servers=%#v error=%v", servers, err)
	}
	state, err = manager.Uninstall(spec)
	if err != nil || state != InstallationMissing {
		t.Fatalf("Uninstall() state=%s error=%v", state, err)
	}
	root, _, err = loadWorkBuddyConfig(spec.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	servers, err = workBuddyServers(root)
	if err != nil || len(servers) != 1 || servers["existing"] == nil {
		t.Fatalf("servers after uninstall=%#v error=%v", servers, err)
	}
}

func TestWorkBuddyInstallIsIdempotentAndRefusesConflictingEntry(t *testing.T) {
	spec := workBuddyTestSpec(t)
	manager := NewWorkBuddyManager()
	if state, err := manager.Install(spec); err != nil || state != InstallationReady {
		t.Fatalf("first Install() state=%s error=%v", state, err)
	}
	before, err := os.ReadFile(spec.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Install(spec); err != nil || state != InstallationReady {
		t.Fatalf("second Install() state=%s error=%v", state, err)
	}
	after, err := os.ReadFile(spec.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("idempotent install rewrote the WorkBuddy configuration")
	}

	conflicting := workBuddyTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(conflicting.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflicting.ConfigFile, []byte(`{"mcpServers":{"agentx":{"type":"stdio","command":"different","args":[]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Install(conflicting); state != InstallationConflict || err == nil {
		t.Fatalf("conflicting Install() state=%s error=%v", state, err)
	}
	if state, err := manager.Uninstall(conflicting); state != InstallationConflict || err == nil {
		t.Fatalf("conflicting Uninstall() state=%s error=%v", state, err)
	}
}

func TestWorkBuddyCheckFailsClosedForMalformedAndSymlinkConfiguration(t *testing.T) {
	manager := NewWorkBuddyManager()
	malformed := workBuddyTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(malformed.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(malformed.ConfigFile, []byte(`{"mcpServers":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Check(malformed); state != InstallationBroken || err == nil {
		t.Fatalf("malformed Check() state=%s error=%v", state, err)
	}

	target := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(target, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := workBuddyTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(symlink.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, symlink.ConfigFile); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Check(symlink); state != InstallationBroken || err == nil {
		t.Fatalf("symlink Check() state=%s error=%v", state, err)
	}
}

func TestWorkBuddyCheckReportsBrokenWhenOwnedFilesDisappear(t *testing.T) {
	spec := workBuddyTestSpec(t)
	manager := NewWorkBuddyManager()
	if state, err := manager.Install(spec); err != nil || state != InstallationReady {
		t.Fatalf("Install() state=%s error=%v", state, err)
	}
	if err := os.Remove(spec.BridgeCommand); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Check(spec); err != nil || state != InstallationBroken {
		t.Fatalf("Check() state=%s error=%v", state, err)
	}
}

func workBuddyTestSpec(t *testing.T) WorkBuddySpec {
	t.Helper()
	directory := t.TempDir()
	bridge := filepath.Join(directory, "agentx-bridge-mcp")
	credential := filepath.Join(directory, "host-installation.json")
	if err := os.WriteFile(bridge, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	return WorkBuddySpec{
		ConfigFile:    filepath.Join(directory, ".workbuddy", "mcp.json"),
		BridgeCommand: bridge, CredentialFile: credential,
	}
}
