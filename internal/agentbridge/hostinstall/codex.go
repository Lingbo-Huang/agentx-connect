// Package hostinstall manages the thin AgentX integration installed into AI
// Hosts. It delegates configuration persistence to each Host's supported CLI
// instead of parsing or rewriting private configuration files.
package hostinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	CodexServerName       = "agentx"
	defaultCommandTimeout = 15 * time.Second
	maximumCommandOutput  = 1 << 20
)

type InstallationState string

const (
	InstallationMissing  InstallationState = "MISSING"
	InstallationReady    InstallationState = "READY"
	InstallationBroken   InstallationState = "BROKEN"
	InstallationConflict InstallationState = "CONFLICT"
)

type CodexSpec struct {
	CodexCommand   string
	BridgeCommand  string
	CredentialFile string
	Profile        string
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct {
	Timeout time.Duration
}

func (runner ExecRunner) Run(ctx context.Context, command string, arguments ...string) ([]byte, error) {
	timeout := runner.Timeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var output boundedBuffer
	output.maximum = maximumCommandOutput
	process := exec.CommandContext(commandCtx, command, arguments...)
	process.Stdout = &output
	process.Stderr = &output
	err := process.Run()
	if commandCtx.Err() != nil {
		return nil, errors.New("Host MCP configuration command timed out")
	}
	if output.exceeded {
		return nil, errors.New("Host MCP configuration command exceeded the safe output limit")
	}
	if err != nil {
		return output.Bytes(), &CommandExecutionError{cause: err}
	}
	return output.Bytes(), nil
}

// CommandExecutionError deliberately excludes command output from Error. A
// caller may use the separately returned bounded output for a documented status
// such as "not configured" without leaking it into logs by default.
type CommandExecutionError struct {
	cause error
}

func (err *CommandExecutionError) Error() string {
	return fmt.Sprintf("Host MCP configuration command failed: %v", err.cause)
}
func (err *CommandExecutionError) Unwrap() error { return err.cause }

type CodexManager struct {
	runner CommandRunner
}

func NewCodexManager(runner CommandRunner) (*CodexManager, error) {
	if runner == nil {
		return nil, errors.New("Codex Host installer requires a command runner")
	}
	return &CodexManager{runner: runner}, nil
}

func (manager *CodexManager) Check(ctx context.Context, spec CodexSpec) (InstallationState, error) {
	normalized, err := normalizeCodexSpec(spec)
	if err != nil {
		return "", err
	}
	servers, err := manager.list(ctx, normalized.CodexCommand)
	if err != nil {
		return "", err
	}
	for _, server := range servers {
		if server.Name != CodexServerName {
			continue
		}
		if server.Enabled && server.Transport.Type == "stdio" && server.Transport.Command == normalized.BridgeCommand &&
			matchesCodexArguments(server.Transport.Arguments, normalized) {
			if err := validateCodexSpecFiles(normalized); err != nil {
				return InstallationBroken, nil
			}
			return InstallationReady, nil
		}
		return InstallationConflict, nil
	}
	return InstallationMissing, nil
}

func (manager *CodexManager) Install(ctx context.Context, spec CodexSpec) (InstallationState, error) {
	state, err := manager.Check(ctx, spec)
	if err != nil || state == InstallationReady {
		return state, err
	}
	if state == InstallationConflict {
		return state, errors.New("Codex already has a different MCP server named agentx; review it before installing")
	}
	if state == InstallationBroken {
		return state, errors.New("Codex has an AgentX MCP entry whose executable or credential file is unavailable; uninstall it before reinstalling")
	}
	normalized, err := normalizeCodexSpec(spec)
	if err != nil {
		return "", err
	}
	if err := validateCodexSpecFiles(normalized); err != nil {
		return "", err
	}
	if _, err := manager.runner.Run(ctx, normalized.CodexCommand, "mcp", "add", CodexServerName, "--", normalized.BridgeCommand,
		"--profile", normalized.Profile, "--credential-file", normalized.CredentialFile); err != nil {
		return "", err
	}
	state, err = manager.Check(ctx, normalized)
	if err != nil {
		return "", err
	}
	if state != InstallationReady {
		return state, errors.New("Codex did not retain the expected AgentX MCP configuration")
	}
	return state, nil
}

func (manager *CodexManager) Uninstall(ctx context.Context, spec CodexSpec) (InstallationState, error) {
	state, err := manager.Check(ctx, spec)
	if err != nil || state == InstallationMissing {
		return state, err
	}
	if state == InstallationConflict {
		return state, errors.New("refusing to remove a different Codex MCP server named agentx")
	}
	normalized, err := normalizeCodexSpec(spec)
	if err != nil {
		return "", err
	}
	if _, err := manager.runner.Run(ctx, normalized.CodexCommand, "mcp", "remove", CodexServerName); err != nil {
		return "", err
	}
	state, err = manager.Check(ctx, normalized)
	if err != nil {
		return "", err
	}
	if state != InstallationMissing {
		return state, errors.New("Codex retained the AgentX MCP configuration after removal")
	}
	return state, nil
}

type codexServer struct {
	Name      string         `json:"name"`
	Enabled   bool           `json:"enabled"`
	Transport codexTransport `json:"transport"`
}

type codexTransport struct {
	Type      string   `json:"type"`
	Command   string   `json:"command"`
	Arguments []string `json:"args"`
}

func (manager *CodexManager) list(ctx context.Context, command string) ([]codexServer, error) {
	output, err := manager.runner.Run(ctx, command, "mcp", "list", "--json")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var servers []codexServer
	if err := decoder.Decode(&servers); err != nil {
		return nil, errors.New("Codex returned an invalid MCP configuration list")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("Codex returned more than one MCP configuration value")
	}
	return servers, nil
}

func normalizeCodexSpec(spec CodexSpec) (CodexSpec, error) {
	spec.CodexCommand = strings.TrimSpace(spec.CodexCommand)
	if spec.CodexCommand == "" {
		spec.CodexCommand = "codex"
	}
	spec.Profile = strings.ToLower(strings.TrimSpace(spec.Profile))
	if spec.Profile == "" {
		spec.Profile = ToolProfileFull
	}
	if spec.Profile != ToolProfileFull && spec.Profile != ToolProfileCompact {
		return CodexSpec{}, errors.New("Codex AgentX MCP profile must be full or compact")
	}
	var err error
	if spec.BridgeCommand, err = absolutePath(spec.BridgeCommand, "AgentX MCP command"); err != nil {
		return CodexSpec{}, err
	}
	if spec.CredentialFile, err = absolutePath(spec.CredentialFile, "HostInstallation credential file"); err != nil {
		return CodexSpec{}, err
	}
	return spec, nil
}

func matchesCodexArguments(arguments []string, spec CodexSpec) bool {
	expected := []string{"--profile", spec.Profile, "--credential-file", spec.CredentialFile}
	if equalStrings(arguments, expected) {
		return true
	}
	// Before Tool Profiles were exposed, Codex omitted --profile and the bridge
	// defaulted to Full. Preserve check/uninstall compatibility for that exact
	// legacy shape; Compact can never match it.
	return spec.Profile == ToolProfileFull && equalStrings(arguments, []string{"--credential-file", spec.CredentialFile})
}

func absolutePath(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	return absolute, nil
}

func validateCodexSpecFiles(spec CodexSpec) error {
	if err := validateRegularFile(spec.BridgeCommand, "AgentX MCP command", true); err != nil {
		return err
	}
	return validateRegularFile(spec.CredentialFile, "HostInstallation credential file", false)
}

func validateRegularFile(path, label string, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a regular non-symlink file", label)
	}
	if executable && runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("%s must be executable", label)
	}
	return nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type boundedBuffer struct {
	bytes.Buffer
	maximum  int
	exceeded bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := buffer.maximum - buffer.Len()
	if remaining <= 0 {
		buffer.exceeded = true
		return originalLength, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		buffer.exceeded = true
	}
	_, _ = buffer.Buffer.Write(value)
	return originalLength, nil
}
