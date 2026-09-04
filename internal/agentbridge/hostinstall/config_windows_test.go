package hostinstall

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsNativeExecutableAndCursorLifecycle(t *testing.T) {
	// Windows executable bits are not represented by os.FileMode.
	executable := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	if err := validateRegularFile(executable, "Windows command", true); err != nil {
		t.Fatal(err)
	}
	if _, err := (ExecRunner{}).Run(context.Background(), executable, "/c", "exit", "0"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	credential := filepath.Join(dir, "host.json")
	if err := os.WriteFile(credential, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewCursorManager()
	spec := CursorSpec{ConfigFile: filepath.Join(dir, "mcp.json"), BridgeCommand: executable, CredentialFile: credential, Profile: ToolProfileCompact}
	if state, err := manager.Install(spec); err != nil || state != InstallationReady {
		t.Fatalf("install: %s %v", state, err)
	}
	if state, err := manager.Check(spec); err != nil || state != InstallationReady {
		t.Fatalf("check: %s %v", state, err)
	}
	if state, err := manager.Uninstall(spec); err != nil || state != InstallationMissing {
		t.Fatalf("uninstall: %s %v", state, err)
	}
}
