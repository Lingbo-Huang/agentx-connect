package hostinstall

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCodexInstallIsIdempotentAndUsesSupportedCLI(t *testing.T) {
	spec := testSpec(t)
	runner := &statefulCodexRunner{}
	manager, err := NewCodexManager(runner)
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Install(context.Background(), spec)
	if err != nil || state != InstallationReady {
		t.Fatalf("Install state=%s err=%v", state, err)
	}
	wantAdd := []string{"mcp", "add", CodexServerName, "--", spec.BridgeCommand, "--profile", ToolProfileFull, "--credential-file", spec.CredentialFile}
	if !containsCall(runner.calls, wantAdd) {
		t.Fatalf("calls=%#v", runner.calls)
	}
	callCount := len(runner.calls)
	state, err = manager.Install(context.Background(), spec)
	if err != nil || state != InstallationReady || len(runner.calls) != callCount+1 {
		t.Fatalf("idempotent install state=%s err=%v calls=%#v", state, err, runner.calls)
	}
}

func TestCodexInstallAndUninstallRefuseConflictingEntry(t *testing.T) {
	spec := testSpec(t)
	runner := &statefulCodexRunner{server: &codexServer{
		Name: CodexServerName, Enabled: true,
		Transport: codexTransport{Type: "stdio", Command: "/different/command", Arguments: []string{"--different"}},
	}}
	manager, _ := NewCodexManager(runner)
	if state, err := manager.Install(context.Background(), spec); state != InstallationConflict || err == nil {
		t.Fatalf("Install state=%s err=%v", state, err)
	}
	if state, err := manager.Uninstall(context.Background(), spec); state != InstallationConflict || err == nil {
		t.Fatalf("Uninstall state=%s err=%v", state, err)
	}
	for _, call := range runner.calls {
		if len(call) >= 2 && (call[1] == "add" || call[1] == "remove") {
			t.Fatalf("conflicting entry was mutated: %#v", runner.calls)
		}
	}
}

func TestCodexUninstallOnlyRemovesMatchingAgentXEntry(t *testing.T) {
	spec := testSpec(t)
	runner := &statefulCodexRunner{}
	manager, _ := NewCodexManager(runner)
	if _, err := manager.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	state, err := manager.Uninstall(context.Background(), spec)
	if err != nil || state != InstallationMissing {
		t.Fatalf("Uninstall state=%s err=%v", state, err)
	}
	if !containsCall(runner.calls, []string{"mcp", "remove", CodexServerName}) {
		t.Fatalf("calls=%#v", runner.calls)
	}
}

func TestCodexUninstallCleansMatchingBrokenEntry(t *testing.T) {
	spec := testSpec(t)
	runner := &statefulCodexRunner{}
	manager, _ := NewCodexManager(runner)
	if _, err := manager.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(spec.CredentialFile); err != nil {
		t.Fatal(err)
	}
	if state, err := manager.Check(context.Background(), spec); err != nil || state != InstallationBroken {
		t.Fatalf("Check state=%s err=%v", state, err)
	}
	if state, err := manager.Uninstall(context.Background(), spec); err != nil || state != InstallationMissing {
		t.Fatalf("Uninstall state=%s err=%v", state, err)
	}
}

func TestCodexRecognizesLegacyImplicitFullButNotCompact(t *testing.T) {
	spec := testSpec(t)
	runner := &statefulCodexRunner{server: &codexServer{
		Name: CodexServerName, Enabled: true,
		Transport: codexTransport{Type: "stdio", Command: spec.BridgeCommand, Arguments: []string{"--credential-file", spec.CredentialFile}},
	}}
	manager, _ := NewCodexManager(runner)
	if state, err := manager.Check(context.Background(), spec); err != nil || state != InstallationReady {
		t.Fatalf("legacy Full Check() state=%s error=%v", state, err)
	}
	spec.Profile = ToolProfileCompact
	if state, err := manager.Check(context.Background(), spec); err != nil || state != InstallationConflict {
		t.Fatalf("legacy Compact Check() state=%s error=%v", state, err)
	}
}

func testSpec(t *testing.T) CodexSpec {
	t.Helper()
	directory := t.TempDir()
	bridge := filepath.Join(directory, "agentx-bridge-mcp")
	credential := filepath.Join(directory, "host-installation.json")
	if err := os.WriteFile(bridge, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	return CodexSpec{CodexCommand: "codex", BridgeCommand: bridge, CredentialFile: credential}
}

type statefulCodexRunner struct {
	server *codexServer
	calls  [][]string
}

func (runner *statefulCodexRunner) Run(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	if reflect.DeepEqual(arguments, []string{"mcp", "list", "--json"}) {
		if runner.server == nil {
			return []byte("[]"), nil
		}
		return json.Marshal([]codexServer{*runner.server})
	}
	if len(arguments) >= 3 && arguments[0] == "mcp" && arguments[1] == "add" {
		runner.server = &codexServer{Name: CodexServerName, Enabled: true, Transport: codexTransport{
			Type: "stdio", Command: arguments[4], Arguments: append([]string(nil), arguments[5:]...),
		}}
		return nil, nil
	}
	if reflect.DeepEqual(arguments, []string{"mcp", "remove", CodexServerName}) {
		runner.server = nil
		return nil, nil
	}
	return nil, nil
}

func containsCall(calls [][]string, expected []string) bool {
	for _, call := range calls {
		if reflect.DeepEqual(call, expected) {
			return true
		}
	}
	return false
}
