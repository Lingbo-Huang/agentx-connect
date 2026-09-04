package hostinstall

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
)

const (
	ClaudeServerName   = AgentXServerName
	ClaudeScopeLocal   = "local"
	ClaudeScopeProject = "project"
	ClaudeScopeUser    = "user"
)

type ClaudeSpec struct {
	ClaudeCommand  string
	BridgeCommand  string
	CredentialFile string
	Profile        string
	Scope          string
}

type ClaudeManager struct {
	runner CommandRunner
}

func NewClaudeManager(runner CommandRunner) (*ClaudeManager, error) {
	if runner == nil {
		return nil, errors.New("Claude Code Host installer requires a command runner")
	}
	return &ClaudeManager{runner: runner}, nil
}

func (manager *ClaudeManager) Check(ctx context.Context, spec ClaudeSpec) (InstallationState, error) {
	normalized, err := normalizeClaudeSpec(spec)
	if err != nil {
		return "", err
	}
	output, err := manager.runner.Run(ctx, normalized.ClaudeCommand, "mcp", "get", ClaudeServerName)
	if err != nil {
		if claudeReportsMissing(output) {
			return InstallationMissing, nil
		}
		return "", err
	}
	server, err := parseClaudeServer(output)
	if err != nil {
		return InstallationBroken, err
	}
	if !matchesClaudeServer(server, normalized) {
		return InstallationConflict, nil
	}
	if err := validateClaudeSpecFiles(normalized); err != nil {
		return InstallationBroken, nil
	}
	return InstallationReady, nil
}

func (manager *ClaudeManager) Install(ctx context.Context, spec ClaudeSpec) (InstallationState, error) {
	normalized, err := normalizeClaudeSpec(spec)
	if err != nil {
		return "", err
	}
	state, err := manager.Check(ctx, normalized)
	if err != nil || state == InstallationReady {
		return state, err
	}
	if state == InstallationConflict {
		return state, errors.New("Claude Code already has a different MCP server named agentx; review it before installing")
	}
	if state == InstallationBroken {
		return state, errors.New("Claude Code has an unreadable AgentX MCP entry; repair it before installing")
	}
	if err := validateClaudeSpecFiles(normalized); err != nil {
		return "", err
	}
	arguments := []string{"mcp", "add", "--scope", normalized.Scope, "--transport", "stdio", ClaudeServerName, "--",
		normalized.BridgeCommand, "--profile", normalized.Profile, "--credential-file", normalized.CredentialFile}
	if _, err := manager.runner.Run(ctx, normalized.ClaudeCommand, arguments...); err != nil {
		return "", err
	}
	state, err = manager.Check(ctx, normalized)
	if err != nil {
		return state, err
	}
	if state != InstallationReady {
		return state, errors.New("Claude Code did not retain the expected AgentX MCP configuration")
	}
	return state, nil
}

func (manager *ClaudeManager) Uninstall(ctx context.Context, spec ClaudeSpec) (InstallationState, error) {
	normalized, err := normalizeClaudeSpec(spec)
	if err != nil {
		return "", err
	}
	state, err := manager.Check(ctx, normalized)
	if err != nil || state == InstallationMissing {
		return state, err
	}
	if state == InstallationConflict {
		return state, errors.New("refusing to remove a different Claude Code MCP server named agentx")
	}
	if _, err := manager.runner.Run(ctx, normalized.ClaudeCommand, "mcp", "remove", ClaudeServerName, "--scope", normalized.Scope); err != nil {
		return "", err
	}
	state, err = manager.Check(ctx, normalized)
	if err != nil {
		return state, err
	}
	if state != InstallationMissing {
		return state, errors.New("Claude Code retained the AgentX MCP configuration after removal")
	}
	return state, nil
}

type claudeServer struct {
	Scope       string
	Type        string
	Command     string
	Arguments   string
	Environment string
}

func normalizeClaudeSpec(spec ClaudeSpec) (ClaudeSpec, error) {
	spec.ClaudeCommand = strings.TrimSpace(spec.ClaudeCommand)
	if spec.ClaudeCommand == "" {
		spec.ClaudeCommand = "claude"
	}
	spec.Scope = strings.ToLower(strings.TrimSpace(spec.Scope))
	if spec.Scope == "" {
		spec.Scope = ClaudeScopeUser
	}
	if spec.Scope != ClaudeScopeLocal && spec.Scope != ClaudeScopeProject && spec.Scope != ClaudeScopeUser {
		return ClaudeSpec{}, errors.New("Claude Code MCP scope must be local, project, or user")
	}
	spec.Profile = strings.ToLower(strings.TrimSpace(spec.Profile))
	if spec.Profile == "" {
		spec.Profile = ToolProfileCompact
	}
	if spec.Profile != ToolProfileFull && spec.Profile != ToolProfileCompact {
		return ClaudeSpec{}, errors.New("Claude Code AgentX MCP profile must be full or compact")
	}
	var err error
	if spec.BridgeCommand, err = absolutePath(spec.BridgeCommand, "AgentX MCP command"); err != nil {
		return ClaudeSpec{}, err
	}
	if spec.CredentialFile, err = absolutePath(spec.CredentialFile, "HostInstallation credential file"); err != nil {
		return ClaudeSpec{}, err
	}
	return spec, nil
}

func validateClaudeSpecFiles(spec ClaudeSpec) error {
	if err := validateRegularFile(spec.BridgeCommand, "AgentX MCP command", true); err != nil {
		return err
	}
	return validateRegularFile(spec.CredentialFile, "HostInstallation credential file", false)
}

func claudeReportsMissing(output []byte) bool {
	return strings.Contains(string(output), `No MCP server named "agentx"`)
}

func parseClaudeServer(output []byte) (claudeServer, error) {
	server := claudeServer{}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, exists := strings.Cut(line, ":")
		if !exists {
			continue
		}
		switch strings.TrimSpace(key) {
		case "Scope":
			server.Scope = strings.TrimSpace(value)
		case "Type":
			server.Type = strings.TrimSpace(value)
		case "Command":
			server.Command = strings.TrimSpace(value)
		case "Args":
			server.Arguments = strings.TrimSpace(value)
		case "Environment":
			server.Environment = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return claudeServer{}, errors.New("read Claude Code MCP configuration")
	}
	if server.Scope == "" || server.Type == "" || server.Command == "" {
		return claudeServer{}, errors.New("Claude Code returned an incomplete MCP configuration")
	}
	return server, nil
}

func matchesClaudeServer(server claudeServer, spec ClaudeSpec) bool {
	expectedScope := map[string]string{
		ClaudeScopeLocal: "Local config", ClaudeScopeProject: "Project config", ClaudeScopeUser: "User config",
	}[spec.Scope]
	expectedArguments := strings.Join([]string{"--profile", spec.Profile, "--credential-file", spec.CredentialFile}, " ")
	return server.Scope == expectedScope && server.Type == "stdio" && server.Command == spec.BridgeCommand &&
		server.Arguments == expectedArguments && server.Environment == ""
}
