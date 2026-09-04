package hostinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClaudeInstallIsIdempotentAndUsesOfficialCLI(t *testing.T) {
	spec := claudeTestSpec(t)
	runner := &statefulClaudeRunner{}
	manager, err := NewClaudeManager(runner)
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Install(context.Background(), spec)
	if err != nil || state != InstallationReady {
		t.Fatalf("Install() state=%s error=%v", state, err)
	}
	want := []string{"mcp", "add", "--scope", ClaudeScopeUser, "--transport", "stdio", ClaudeServerName, "--",
		spec.BridgeCommand, "--profile", ToolProfileCompact, "--credential-file", spec.CredentialFile}
	if !containsCall(runner.calls, want) {
		t.Fatalf("calls=%#v", runner.calls)
	}
	callCount := len(runner.calls)
	state, err = manager.Install(context.Background(), spec)
	if err != nil || state != InstallationReady || len(runner.calls) != callCount+1 {
		t.Fatalf("idempotent Install() state=%s error=%v calls=%#v", state, err, runner.calls)
	}
}

func TestClaudeRefusesConflictingScopeOrCommand(t *testing.T) {
	spec := claudeTestSpec(t)
	runner := &statefulClaudeRunner{server: &claudeServer{
		Scope: "Project config", Type: "stdio", Command: spec.BridgeCommand,
		Arguments: strings.Join([]string{"--profile", ToolProfileCompact, "--credential-file", spec.CredentialFile}, " "),
	}}
	manager, _ := NewClaudeManager(runner)
	if state, err := manager.Install(context.Background(), spec); state != InstallationConflict || err == nil {
		t.Fatalf("Install() state=%s error=%v", state, err)
	}
	if state, err := manager.Uninstall(context.Background(), spec); state != InstallationConflict || err == nil {
		t.Fatalf("Uninstall() state=%s error=%v", state, err)
	}
	for _, call := range runner.calls {
		if len(call) > 1 && (call[1] == "add" || call[1] == "remove") {
			t.Fatalf("conflicting entry was mutated: %#v", runner.calls)
		}
	}
}

func TestClaudeUninstallRemovesOnlyMatchingScope(t *testing.T) {
	spec := claudeTestSpec(t)
	runner := &statefulClaudeRunner{}
	manager, _ := NewClaudeManager(runner)
	if _, err := manager.Install(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	state, err := manager.Uninstall(context.Background(), spec)
	if err != nil || state != InstallationMissing {
		t.Fatalf("Uninstall() state=%s error=%v", state, err)
	}
	if !containsCall(runner.calls, []string{"mcp", "remove", ClaudeServerName, "--scope", ClaudeScopeUser}) {
		t.Fatalf("calls=%#v", runner.calls)
	}
}

func TestClaudeOutputParserFailsClosed(t *testing.T) {
	if _, err := parseClaudeServer([]byte("agentx:\n  Type: stdio\n")); err == nil {
		t.Fatal("parseClaudeServer() accepted incomplete output")
	}
	if !claudeReportsMissing([]byte(`No MCP server named "agentx". Run claude mcp add.`)) {
		t.Fatal("missing server output was not recognized")
	}
}

func claudeTestSpec(t *testing.T) ClaudeSpec {
	t.Helper()
	directory := t.TempDir()
	bridge := filepath.Join(directory, "agentx-bridge-mcp")
	credential := filepath.Join(directory, "host-installation-claude.json")
	if err := os.WriteFile(bridge, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credential, []byte("credential"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ClaudeSpec{ClaudeCommand: "claude", BridgeCommand: bridge, CredentialFile: credential, Scope: ClaudeScopeUser}
}

type statefulClaudeRunner struct {
	server *claudeServer
	calls  [][]string
}

func (runner *statefulClaudeRunner) Run(_ context.Context, _ string, arguments ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string(nil), arguments...))
	if reflect.DeepEqual(arguments, []string{"mcp", "get", ClaudeServerName}) {
		if runner.server == nil {
			return []byte(`No MCP server named "agentx". Run claude mcp add.`), errors.New("exit status 1")
		}
		return []byte("agentx:\n  Scope: " + runner.server.Scope + "\n  Status: connected\n  Type: " + runner.server.Type +
			"\n  Command: " + runner.server.Command + "\n  Args: " + runner.server.Arguments + "\n  Environment: " + runner.server.Environment + "\n"), nil
	}
	if len(arguments) >= 8 && arguments[0] == "mcp" && arguments[1] == "add" {
		runner.server = &claudeServer{
			Scope: "User config", Type: "stdio", Command: arguments[8], Arguments: strings.Join(arguments[9:], " "),
		}
		return nil, nil
	}
	if reflect.DeepEqual(arguments, []string{"mcp", "remove", ClaudeServerName, "--scope", ClaudeScopeUser}) {
		runner.server = nil
		return nil, nil
	}
	return nil, nil
}
