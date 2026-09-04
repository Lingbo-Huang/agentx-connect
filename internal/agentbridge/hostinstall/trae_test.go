package hostinstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTraeInstallPreservesProjectConfiguration(t *testing.T) {
	spec := traeTestSpec(t)
	if err := os.MkdirAll(filepath.Dir(spec.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	initial := `{"project":{"name":"demo"},"mcpServers":{"existing":{"command":"existing-server"}}}`
	if err := os.WriteFile(spec.ConfigFile, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewTraeManager()
	state, err := manager.Install(spec)
	if err != nil || state != InstallationReady {
		t.Fatalf("Install() state=%s error=%v", state, err)
	}
	root, exists, err := loadJSONMCPConfig("Trae", spec.ConfigFile)
	if err != nil || !exists {
		t.Fatalf("load config exists=%t error=%v", exists, err)
	}
	var project map[string]string
	if err := json.Unmarshal(root["project"], &project); err != nil || project["name"] != "demo" {
		t.Fatalf("unrelated config was not preserved: %#v error=%v", project, err)
	}
	servers, err := jsonMCPServers("Trae", root)
	if err != nil || len(servers) != 2 || servers[TraeServerName] == nil {
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
		t.Fatal("idempotent install rewrote Trae configuration")
	}
}

func TestTraeRefusesConflictAndUninstallsOwnedEntry(t *testing.T) {
	spec := traeTestSpec(t)
	manager := NewTraeManager()
	if _, err := manager.Install(spec); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Uninstall(spec); err != nil || state != InstallationMissing {
		t.Fatalf("Uninstall() state=%s error=%v", state, err)
	}

	conflict := traeTestSpec(t)
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

func traeTestSpec(t *testing.T) TraeSpec {
	t.Helper()
	directory := t.TempDir()
	bridge := filepath.Join(directory, "agentx-bridge-mcp")
	credential := filepath.Join(directory, "host-installation-trae.json")
	if err := os.WriteFile(bridge, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	return TraeSpec{ConfigFile: filepath.Join(directory, ".trae", "mcp.json"), BridgeCommand: bridge, CredentialFile: credential}
}
