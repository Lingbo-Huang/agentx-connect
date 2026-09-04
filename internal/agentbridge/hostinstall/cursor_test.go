package hostinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCursorInstallPreservesConfigurationAndUsesCompactProfile(t *testing.T) {
	spec := cursorTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(spec.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	initial := `{"editor":{"fontSize":14},"mcpServers":{"existing":{"command":"existing-server"}}}`
	if err := os.WriteFile(spec.ConfigFile, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewCursorManager()
	state, err := manager.Install(spec)
	if err != nil || state != InstallationReady {
		t.Fatalf("Install() state=%s error=%v", state, err)
	}
	root, exists, err := loadJSONMCPConfig("Cursor", spec.ConfigFile)
	if err != nil || !exists {
		t.Fatalf("load config exists=%t error=%v", exists, err)
	}
	var editor map[string]int
	if err := json.Unmarshal(root["editor"], &editor); err != nil || editor["fontSize"] != 14 {
		t.Fatalf("unrelated config was not preserved: %#v error=%v", editor, err)
	}
	servers, err := jsonMCPServers("Cursor", root)
	if err != nil || len(servers) != 2 || servers[CursorServerName] == nil {
		t.Fatalf("servers=%#v error=%v", servers, err)
	}
	before, err := os.ReadFile(spec.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Install(spec); err != nil || state != InstallationReady {
		t.Fatalf("idempotent Install() state=%s error=%v", state, err)
	}
	after, err := os.ReadFile(spec.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("idempotent install rewrote Cursor configuration")
	}
}

func TestCursorRefusesConflictAndUninstallsOwnedEntry(t *testing.T) {
	spec := cursorTestSpec(t)
	manager := NewCursorManager()
	if _, err := manager.Install(spec); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Uninstall(spec); err != nil || state != InstallationMissing {
		t.Fatalf("Uninstall() state=%s error=%v", state, err)
	}

	conflict := cursorTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(conflict.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conflict.ConfigFile, []byte(`{"mcpServers":{"agentx":{"command":"different"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Install(conflict); state != InstallationConflict || err == nil {
		t.Fatalf("conflicting Install() state=%s error=%v", state, err)
	}
}

func cursorTestSpec(t *testing.T) CursorSpec {
	t.Helper()
	directory := t.TempDir()
	bridge := filepath.Join(directory, "agentx-bridge-mcp")
	credential := filepath.Join(directory, "host-installation-cursor.json")
	if err := os.WriteFile(bridge, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	return CursorSpec{ConfigFile: filepath.Join(directory, ".cursor", "mcp.json"), BridgeCommand: bridge, CredentialFile: credential}
}
